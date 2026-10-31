// Package chat 实现腾讯云 IM 单聊的业务网关、本地镜像与审计查询。
package chat

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"go.uber.org/zap"

	"github.com/datechat/server/internal/config"
	"github.com/datechat/server/internal/modules/account"
	"github.com/datechat/server/internal/modules/moderation"
	"github.com/datechat/server/internal/pkg/errs"
	"github.com/datechat/server/internal/pkg/textfilter"
)

// Service 聊天领域服务。
type Service struct {
	cfg    *config.Config
	log    *zap.Logger
	repo   *Repository
	im     *account.IMClient
	filter *textfilter.Filter
	mod    TextModerator
	now    func() time.Time
}

// Deps 构造依赖。
type Deps struct {
	Config *config.Config
	Log    *zap.Logger
	Repo   *Repository
	IM     *account.IMClient
	Filter *textfilter.Filter
	// Mod 统一内容安全服务（P4）。为空时退化成本地 DFA 过滤。
	Mod TextModerator
}

// TextModerator 内容安全能力的最小接口。
// chat 依赖它而不是具体实现，既避免与 moderation 包循环依赖，也便于单测注入。
type TextModerator interface {
	CheckText(ctx context.Context, in moderation.CheckInput) (moderation.Verdict, error)
}

func NewService(d Deps) *Service {
	log := d.Log
	if log == nil {
		log = zap.NewNop()
	}
	filter := d.Filter
	if filter == nil {
		filter = textfilter.New()
	}
	return &Service{cfg: d.Config, log: log, repo: d.Repo, im: d.IM, filter: filter, mod: d.Mod, now: time.Now}
}

// checkText 统一的文本审核入口：优先用 moderation 服务（含云审核与统一留痕），
// 未注入时回退到本地 DFA 并自己落留痕，保证行为一致。
func (s *Service) checkText(ctx context.Context, scene string, bizType int, bizID string, uid int64, nickname, text string) (blocked, review bool, out string, err error) {
	if s.mod != nil {
		v, err := s.mod.CheckText(ctx, moderation.CheckInput{
			Scene: scene, BizType: bizType, BizID: bizID,
			UID: uid, Nickname: nickname, Text: text, Save: true,
		})
		if err != nil {
			return false, false, text, errs.ErrInternal.WithErr(err)
		}
		return v.Blocked, v.Review, v.Text, nil
	}
	got := s.filter.Check(text)
	if got.Blocked {
		s.recordModeration(ctx, bizID, uid, moderation.ResultReject, got.Hits)
		return true, false, text, nil
	}
	if len(got.Hits) > 0 {
		result := moderation.ResultPass
		if got.Review {
			result = moderation.ResultReview
		}
		s.recordModeration(ctx, bizID, uid, result, got.Hits)
	}
	return false, got.Review, got.Text, nil
}

// SendText 发送文本消息。真实环境调用腾讯云 IM，开发环境可用本地镜像降级。
func (s *Service) SendText(ctx context.Context, uid int64, in SendMessageInput) (*MessageView, error) {
	if uid <= 0 || in.PeerUID <= 0 || in.PeerUID == uid {
		return nil, errs.ErrInvalidParam.WithMsg("聊天对象不合法")
	}
	text := strings.TrimSpace(in.Text)
	if text == "" {
		return nil, errs.ErrInvalidParam.WithMsg("消息不能为空")
	}
	maxChars := 500
	if s.cfg != nil && s.cfg.IM.MessageMaxChars > 0 {
		maxChars = s.cfg.IM.MessageMaxChars
	}
	if utf8.RuneCountInString(text) > maxChars {
		return nil, errs.ErrInvalidParam.WithMsg("消息最多 %d 个字", maxChars)
	}
	clientMsgID := strings.TrimSpace(in.ClientMsgID)
	if clientMsgID == "" {
		clientMsgID = newClientMessageID(uid, s.now())
	}
	if !validClientMessageID(clientMsgID) {
		return nil, errs.ErrInvalidParam.WithMsg("client_msg_id 格式不合法")
	}
	if old, err := s.repo.FindMessageByClient(ctx, uid, clientMsgID); err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	} else if old != nil {
		view := toMessageView(*old, uid)
		return &view, nil
	}

	from, err := s.repo.FindUser(ctx, uid)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	peer, err := s.repo.FindUser(ctx, in.PeerUID)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	if from == nil || from.Status != 1 {
		return nil, errs.ErrUserBanned
	}
	if peer == nil || peer.Status != 1 {
		return nil, errs.ErrUserNotFound
	}
	blocked, err := s.repo.BlockedEitherWay(ctx, uid, in.PeerUID)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	if blocked {
		return nil, errs.ErrBlocked
	}

	// P4：统一走内容安全服务（本地 DFA + 可选的云审核 + 统一留痕）。
	// Mod 为空时退化成本地过滤，保证老调用方与单测不受影响。
	blockedByWord, review, text, err := s.checkText(ctx, "message", moderation.BizMessage, clientMsgID, uid, from.Nickname, text)
	if err != nil {
		return nil, err
	}
	if blockedByWord {
		return nil, errs.ErrSensitiveWord
	}
	if review {
		s.log.Info("消息进入人工复核队列", zap.Int64("uid", uid), zap.String("client_msg_id", clientMsgID))
	}

	now := s.now()
	msg := &Message{
		ClientMsgID: &clientMsgID,
		FromUID:     uid,
		ToUID:       in.PeerUID,
		MsgType:     MessageTypeText,
		Status:      MessageStatusNormal,
		SentAt:      now,
	}
	content, _ := json.Marshal(map[string]any{"text": text})
	msg.Content = content
	remoteSynced := false

	if s.im != nil && s.im.RESTEnabled() {
		seq := randomUint31()
		result, sendErr := s.im.SendText(ctx, account.IMUserIDOf(uid), account.IMUserIDOf(in.PeerUID), text, clientMsgID, seq, randomUint31())
		if sendErr != nil {
			var apiErr *account.APIError
			if errors.As(sendErr, &apiErr) && apiErr.Code == 20006 {
				return nil, errs.ErrSensitiveWord.WithMsg("消息未通过安全校验")
			}
			s.log.Warn("腾讯云 IM 发消息失败", zap.Int64("uid", uid), zap.Int64("peer_uid", in.PeerUID), zap.Error(sendErr))
			return nil, errs.ErrServiceBusy.WithMsg("消息发送失败，请稍后重试").WithErr(sendErr)
		}
		msg.MsgID, msg.MsgKey = result.MsgID, result.MsgKey
		if result.MsgTime > 0 {
			msg.SentAt = time.Unix(result.MsgTime, 0)
		}
		msg.Seq = orderSeq(msg.SentAt, int64(seq))
		remoteSynced = true
	} else {
		if s.cfg == nil || !s.cfg.IM.LocalFallback {
			return nil, errs.ErrServiceBusy.WithMsg("即时通信服务尚未配置")
		}
		token := randomHex(8)
		msg.MsgID = fmt.Sprintf("local-%d-%d-%s", uid, now.UnixMilli(), token)
		msg.MsgKey = fmt.Sprintf("local_%d_%s", now.Unix(), token)
		msg.Seq = orderSeq(now, int64(randomUint31()))
	}

	stored, _, err := s.repo.StoreMessage(ctx, msg, messagePreview(text))
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	view := toMessageView(*stored, uid)
	_ = remoteSynced // 消息模型不暴露通道；会话接口对客户端保持一致。
	return &view, nil
}

// Conversations 返回我的会话列表。
func (s *Service) Conversations(ctx context.Context, uid int64, page, size int) (*ConversationList, error) {
	if page <= 0 {
		page = 1
	}
	if size <= 0 {
		size = 20
	}
	if size > 100 {
		size = 100
	}
	rows, total, unread, err := s.repo.ListConversations(ctx, uid, page, size)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	list := make([]ConversationItem, 0, len(rows))
	for _, row := range rows {
		item := ConversationItem{
			ConvID:    row.ConvID,
			Peer:      PeerBrief{UID: row.PeerUID, Nickname: row.PeerNickname, Avatar: row.PeerAvatar},
			LastMsgID: row.LastMsgID, LastType: row.LastMsgType, LastPreview: row.LastMsgPreview,
			UnreadCount: row.UnreadCount, Pinned: row.IsPinned == 1, Muted: row.IsMuted == 1,
		}
		if row.LastMsgAt != nil {
			item.LastAt = row.LastMsgAt.UnixMilli()
		}
		list = append(list, item)
	}
	return &ConversationList{List: list, Page: page, Size: size, Total: total, UnreadTotal: unread}, nil
}

// Messages 返回和指定用户的本地镜像消息。腾讯 IM 仍是投递主链路，本库用于审计与兜底展示。
func (s *Service) Messages(ctx context.Context, uid, peerUID, beforeID int64, limit int) (*MessageList, error) {
	if peerUID <= 0 || peerUID == uid {
		return nil, errs.ErrInvalidParam.WithMsg("聊天对象不合法")
	}
	if limit <= 0 {
		limit = 30
	}
	if limit > 100 {
		limit = 100
	}
	peer, err := s.repo.FindUser(ctx, peerUID)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	if peer == nil {
		return nil, errs.ErrUserNotFound
	}
	out := &MessageList{Peer: PeerBrief{UID: peer.ID, Nickname: peer.Nickname, Avatar: peer.Avatar}, List: []MessageView{}}
	conv, err := s.repo.FindConversation(ctx, uid, peerUID)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	if conv == nil {
		return out, nil
	}
	messages, hasMore, err := s.repo.ListMessages(ctx, conv.ID, beforeID, limit)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	out.List = make([]MessageView, 0, len(messages))
	for _, m := range messages {
		out.List = append(out.List, toMessageView(m, uid))
	}
	out.HasMore = hasMore
	if hasMore && len(messages) > 0 {
		out.NextCursor = messages[0].ID
	}
	return out, nil
}

// MarkRead 同步腾讯云未读计数并更新本地镜像。
func (s *Service) MarkRead(ctx context.Context, uid, peerUID int64) (*ReadResult, error) {
	if peerUID <= 0 || peerUID == uid {
		return nil, errs.ErrInvalidParam.WithMsg("聊天对象不合法")
	}
	remote := false
	if s.im != nil && s.im.RESTEnabled() {
		if err := s.im.MarkC2CRead(ctx, account.IMUserIDOf(uid), account.IMUserIDOf(peerUID), s.now()); err != nil {
			s.log.Warn("同步 IM 已读失败", zap.Int64("uid", uid), zap.Int64("peer_uid", peerUID), zap.Error(err))
			return nil, errs.ErrServiceBusy.WithMsg("已读状态同步失败，请稍后重试").WithErr(err)
		}
		remote = true
	} else if s.cfg == nil || !s.cfg.IM.LocalFallback {
		return nil, errs.ErrServiceBusy.WithMsg("即时通信服务尚未配置")
	}
	seq, err := s.repo.MarkRead(ctx, uid, peerUID)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	return &ReadResult{PeerUID: peerUID, LastReadSeq: seq, UnreadCount: 0, RemoteSynced: remote}, nil
}

// Recall 撤回本人在时间窗内发送的消息。
func (s *Service) Recall(ctx context.Context, uid int64, msgID string) (*RecallResult, error) {
	msgID = strings.TrimSpace(msgID)
	if msgID == "" {
		return nil, errs.ErrInvalidParam.WithMsg("消息 ID 不能为空")
	}
	m, err := s.repo.FindMessageForRecall(ctx, uid, msgID)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	if m == nil {
		return nil, errs.ErrMessageNotFound
	}
	if m.Status == MessageStatusRecalled {
		when := int64(0)
		if m.RecalledAt != nil {
			when = m.RecalledAt.UnixMilli()
		}
		return &RecallResult{MsgID: msgID, Status: MessageStatusRecalled, RecalledAt: when}, nil
	}
	window := 2 * time.Minute
	if s.cfg != nil && s.cfg.IM.RecallWindow.D() > 0 {
		window = s.cfg.IM.RecallWindow.D()
	}
	if s.now().Sub(m.SentAt) > window {
		return nil, errs.ErrMessageRecallExpired
	}
	remote := false
	if s.im != nil && s.im.RESTEnabled() {
		if m.MsgKey == "" {
			return nil, errs.ErrServiceBusy.WithMsg("消息撤回标识缺失，请稍后重试")
		}
		err := s.im.RecallC2C(ctx, account.IMUserIDOf(m.FromUID), account.IMUserIDOf(m.ToUID), m.MsgKey, "用户主动撤回")
		if err != nil {
			var apiErr *account.APIError
			if !errors.As(err, &apiErr) || apiErr.Code != 20023 {
				s.log.Warn("腾讯云 IM 撤回失败", zap.String("msg_id", msgID), zap.Error(err))
				return nil, errs.ErrServiceBusy.WithMsg("撤回失败，请稍后重试").WithErr(err)
			}
		}
		remote = true
	} else if s.cfg == nil || !s.cfg.IM.LocalFallback {
		return nil, errs.ErrServiceBusy.WithMsg("即时通信服务尚未配置")
	}
	at := s.now()
	if err := s.repo.RecallMessage(ctx, msgID, uid, at); err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	return &RecallResult{MsgID: msgID, Status: MessageStatusRecalled, RecalledAt: at.UnixMilli(), RemoteSynced: remote}, nil
}

func (s *Service) recordModeration(ctx context.Context, bizID string, uid int64, result int, hits []textfilter.Hit) {
	raw, _ := json.Marshal(map[string]any{"hits": hits})
	label := ""
	if len(hits) > 0 {
		label = hits[0].Category
	}
	if err := s.repo.InsertModeration(ctx, bizID, uid, result, label, string(raw)); err != nil {
		s.log.Warn("写消息审核记录失败", zap.Int64("uid", uid), zap.Error(err))
	}
}

func toMessageView(m Message, viewer int64) MessageView {
	view := MessageView{
		ID: m.ID, MsgID: m.MsgID, ConvID: m.ConvID, FromUID: m.FromUID, ToUID: m.ToUID,
		Type: m.MsgType, Status: m.Status, SentAt: m.SentAt.UnixMilli(), Mine: m.FromUID == viewer,
	}
	if m.ClientMsgID != nil {
		view.ClientMsgID = *m.ClientMsgID
	}
	if m.RecalledAt != nil {
		view.RecalledAt = m.RecalledAt.UnixMilli()
	}
	if m.Status != MessageStatusRecalled {
		var content struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(m.Content, &content)
		view.Text = content.Text
	}
	return view
}

func messagePreview(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) > 30 {
		return string(runes[:30]) + "…"
	}
	return text
}

func orderSeq(at time.Time, tie int64) int64 {
	if tie < 0 {
		tie = -tie
	}
	return at.UnixMilli()*1000 + tie%1000
}

func validClientMessageID(s string) bool {
	if len(s) < 8 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("-_.:", r) {
			continue
		}
		return false
	}
	return true
}

func newClientMessageID(uid int64, at time.Time) string {
	return fmt.Sprintf("srv-%d-%d-%s", uid, at.UnixMilli(), randomHex(4))
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := crand.Read(buf); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func randomUint31() uint32 {
	buf := make([]byte, 4)
	if _, err := crand.Read(buf); err != nil {
		return uint32(time.Now().UnixNano()&0x7fffffff) + 1
	}
	n := uint32(buf[0])<<24 | uint32(buf[1])<<16 | uint32(buf[2])<<8 | uint32(buf[3])
	n &= 0x7fffffff
	if n == 0 {
		n = 1
	}
	return n
}
