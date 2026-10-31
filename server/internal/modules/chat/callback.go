package chat

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/datechat/server/internal/modules/moderation"
	"github.com/datechat/server/internal/pkg/textfilter"
)

const (
	commandBeforeSend = "C2C.CallbackBeforeSendMsg"
	commandAfterSend  = "C2C.CallbackAfterSendMsg"
)

// CallbackResponse 是腾讯云 IM 要求的固定响应格式，不能复用站内 {code,msg,data}。
type CallbackResponse struct {
	ActionStatus    string       `json:"ActionStatus"`
	ErrorInfo       string       `json:"ErrorInfo"`
	ErrorCode       int          `json:"ErrorCode"`
	MsgBody         []MsgElement `json:"MsgBody,omitempty"`
	CloudCustomData string       `json:"CloudCustomData,omitempty"`
}

// MsgElement 对应腾讯云 TIMMsgElement。
type MsgElement struct {
	MsgType    string         `json:"MsgType"`
	MsgContent map[string]any `json:"MsgContent"`
}

// CallbackPayload 兼容 P3 所需的消息前、消息后、已读和撤回通知。
// 腾讯云可能新增字段；未声明字段由 encoding/json 自动忽略。
type CallbackPayload struct {
	CallbackCommand string       `json:"CallbackCommand"`
	FromAccount     string       `json:"From_Account"`
	ToAccount       string       `json:"To_Account"`
	OperatorAccount string       `json:"Operator_Account"`
	ReportAccount   string       `json:"Report_Account"`
	PeerAccount     string       `json:"Peer_Account"`
	MsgSeq          int64        `json:"MsgSeq"`
	MsgRandom       int64        `json:"MsgRandom"`
	MsgTime         int64        `json:"MsgTime"`
	MsgKey          string       `json:"MsgKey"`
	MsgID           string       `json:"MsgId"`
	MsgBody         []MsgElement `json:"MsgBody"`
	CloudCustomData string       `json:"CloudCustomData"`
	SendMsgResult   int          `json:"SendMsgResult"`
	EventTime       int64        `json:"EventTime"`
	C2CMsgInfo      []struct {
		FromAccount string `json:"From_Account"`
		ToAccount   string `json:"To_Account"`
		MsgKey      string `json:"MsgKey"`
		MsgSeq      int64  `json:"MsgSeq"`
	} `json:"C2CMsgInfo"`
}

// VerifyCallback 校验 SDKAppID 和 sha256(Token+RequestTime) 回调签名。
func (s *Service) VerifyCallback(sdkAppID, sign, requestTime string) error {
	if s.cfg == nil {
		return fmt.Errorf("IM 配置未初始化")
	}
	gotAppID, err := strconv.ParseInt(strings.TrimSpace(sdkAppID), 10, 64)
	if err != nil || gotAppID != int64(s.cfg.IM.SdkAppID) {
		return fmt.Errorf("SDKAppID 不匹配")
	}
	token := s.cfg.IM.CallbackToken
	if token == "" {
		return nil
	}
	ts, err := strconv.ParseInt(strings.TrimSpace(requestTime), 10, 64)
	if err != nil {
		return fmt.Errorf("RequestTime 不合法")
	}
	delta := s.now().Unix() - ts
	if delta < 0 {
		delta = -delta
	}
	if time.Duration(delta)*time.Second > s.cfg.IM.CallbackMaxSkew.D() {
		return fmt.Errorf("回调请求已过期")
	}
	sum := sha256.Sum256([]byte(token + requestTime))
	expected := hex.EncodeToString(sum[:])
	if len(sign) != len(expected) || subtle.ConstantTimeCompare([]byte(strings.ToLower(sign)), []byte(expected)) != 1 {
		return fmt.Errorf("回调签名不匹配")
	}
	return nil
}

// HandleCallback 处理腾讯云 IM 回调并返回官方协议响应。
func (s *Service) HandleCallback(ctx context.Context, sdkAppID int64, command string, raw []byte) CallbackResponse {
	var payload CallbackPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return callbackFail("JSON 格式错误")
	}
	if payload.CallbackCommand != "" {
		if command != "" && payload.CallbackCommand != command {
			return callbackFail("CallbackCommand 不一致")
		}
		command = payload.CallbackCommand
	}
	if command == "" {
		return callbackFail("缺少 CallbackCommand")
	}

	kind := callbackKind(command)
	h := sha256.Sum256(append([]byte(command+"\n"), raw...))
	logID, duplicate, err := s.repo.BeginCallback(ctx, sdkAppID, command, kind, hex.EncodeToString(h[:]), string(raw))
	if err != nil {
		return callbackFail("回调日志写入失败")
	}
	// 事件后回调可直接按幂等日志短路；事件前回调要重新计算修改后的 MsgBody，不能只返回通用成功。
	if duplicate && command != commandBeforeSend {
		return callbackOK()
	}

	var response CallbackResponse
	switch command {
	case commandBeforeSend:
		response, err = s.beforeSend(ctx, &payload)
	case commandAfterSend:
		err = s.afterSend(ctx, &payload)
		response = callbackOK()
	default:
		if strings.Contains(command, "MsgRead") {
			err = s.afterRead(ctx, &payload)
			response = callbackOK()
		} else if strings.Contains(strings.ToLower(command), "withdraw") || strings.Contains(strings.ToLower(command), "recall") {
			err = s.afterRecall(ctx, &payload)
			response = callbackOK()
		} else {
			// 未订阅/未来新增命令按成功忽略，避免腾讯持续重试拖垮回调入口。
			response = callbackOK()
		}
	}
	if finishErr := s.repo.FinishCallback(ctx, logID, err); finishErr != nil && err == nil {
		err = finishErr
	}
	if err != nil {
		s.log.Warn("处理腾讯云 IM 回调失败", zap.String("command", command), zap.Error(err))
		return callbackFail("业务处理失败")
	}
	return response
}

func (s *Service) beforeSend(ctx context.Context, payload *CallbackPayload) (CallbackResponse, error) {
	fromUID, okFrom := parseIMUserID(payload.FromAccount)
	toUID, okTo := parseIMUserID(payload.ToAccount)
	if !okFrom || !okTo || fromUID == toUID {
		return callbackReject(120003, "用户标识不合法"), nil
	}
	blocked, err := s.repo.BlockedEitherWay(ctx, fromUID, toUID)
	if err != nil {
		return CallbackResponse{}, err
	}
	if blocked {
		return callbackReject(120001, "由于隐私设置，消息无法发送"), nil
	}

	changed := false
	var allHits []textfilter.Hit
	review := false
	for i := range payload.MsgBody {
		if payload.MsgBody[i].MsgType != "TIMTextElem" {
			continue
		}
		rawText, _ := payload.MsgBody[i].MsgContent["Text"].(string)
		hitBlocked, hitReview, outText := false, false, rawText
		if s.mod != nil {
			v, err := s.mod.CheckText(ctx, moderation.CheckInput{
				Scene: "callback", BizType: moderation.BizMessage, BizID: callbackBizID(payload),
				UID: fromUID, Text: rawText, Save: true,
			})
			if err != nil {
				// 审核服务异常时放行：宁可漏审，也不能让回调失败导致消息发不出去。
				s.log.Warn("回调审核失败，按放行处理", zap.Error(err))
			} else {
				hitBlocked, hitReview, outText = v.Blocked, v.Review, v.Text
				for _, h := range v.Hits {
					allHits = append(allHits, textfilter.Hit{Word: h.Word, Category: h.Category, Level: h.Level})
				}
			}
		} else {
			checked := s.filter.Check(rawText)
			allHits = append(allHits, checked.Hits...)
			hitBlocked, hitReview, outText = checked.Blocked, checked.Review, checked.Text
			if checked.Blocked {
				s.recordModeration(ctx, callbackBizID(payload), fromUID, moderation.ResultReject, checked.Hits)
				return callbackReject(120002, "内容包含违规信息"), nil
			}
		}
		if hitBlocked {
			return callbackReject(120002, "内容包含违规信息"), nil
		}
		if hitReview {
			review = true
		}
		if outText != rawText {
			payload.MsgBody[i].MsgContent["Text"] = outText
			changed = true
		}
	}
	if len(allHits) > 0 && s.mod == nil {
		result := moderation.ResultPass
		if review {
			result = moderation.ResultReview
		}
		s.recordModeration(ctx, callbackBizID(payload), fromUID, result, allHits)
	}
	out := callbackOK()
	if changed {
		out.MsgBody = payload.MsgBody
		out.CloudCustomData = payload.CloudCustomData
	}
	return out, nil
}

func (s *Service) afterSend(ctx context.Context, payload *CallbackPayload) error {
	if payload.SendMsgResult != 0 {
		return nil
	}
	fromUID, okFrom := parseIMUserID(payload.FromAccount)
	toUID, okTo := parseIMUserID(payload.ToAccount)
	if !okFrom || !okTo || fromUID == toUID {
		return fmt.Errorf("chat: 回调用户标识不合法")
	}
	msgID := strings.TrimSpace(payload.MsgID)
	if msgID == "" {
		msgID = "im-" + strings.TrimSpace(payload.MsgKey)
	}
	if msgID == "im-" {
		return fmt.Errorf("chat: 回调缺少 MsgId/MsgKey")
	}
	sentAt := s.now()
	if payload.EventTime > 0 {
		sentAt = time.UnixMilli(payload.EventTime)
	} else if payload.MsgTime > 0 {
		sentAt = time.Unix(payload.MsgTime, 0)
	}
	msgType, text := callbackContent(payload.MsgBody)
	var content []byte
	if msgType == MessageTypeText {
		content, _ = json.Marshal(map[string]any{"text": text})
	} else {
		content, _ = json.Marshal(map[string]any{"elements": payload.MsgBody})
	}
	var clientID *string
	if payload.CloudCustomData != "" {
		var custom struct {
			ClientMsgID string `json:"client_msg_id"`
		}
		if json.Unmarshal([]byte(payload.CloudCustomData), &custom) == nil && validClientMessageID(custom.ClientMsgID) {
			clientID = &custom.ClientMsgID
		}
	}
	msg := &Message{
		MsgID: msgID, MsgKey: payload.MsgKey, ClientMsgID: clientID,
		FromUID: fromUID, ToUID: toUID, MsgType: msgType, Content: content,
		Seq: orderSeq(sentAt, payload.MsgSeq), Status: MessageStatusNormal, SentAt: sentAt,
	}
	preview := text
	if msgType != MessageTypeText {
		preview = callbackTypePreview(msgType)
	}
	_, _, err := s.repo.StoreMessage(ctx, msg, messagePreview(preview))
	return err
}

func (s *Service) afterRead(ctx context.Context, payload *CallbackPayload) error {
	pairs := make([][2]string, 0, len(payload.C2CMsgInfo)+1)
	reader := payload.OperatorAccount
	if reader == "" {
		reader = payload.ReportAccount
	}
	if reader != "" && payload.PeerAccount != "" {
		pairs = append(pairs, [2]string{reader, payload.PeerAccount})
	}
	for _, info := range payload.C2CMsgInfo {
		pairs = append(pairs, [2]string{info.ToAccount, info.FromAccount})
	}
	if len(pairs) == 0 && payload.ToAccount != "" && payload.FromAccount != "" {
		pairs = append(pairs, [2]string{payload.ToAccount, payload.FromAccount})
	}
	for _, p := range pairs {
		readerUID, okReader := parseIMUserID(p[0])
		peerUID, okPeer := parseIMUserID(p[1])
		if !okReader || !okPeer {
			continue
		}
		if _, err := s.repo.MarkRead(ctx, readerUID, peerUID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) afterRecall(ctx context.Context, payload *CallbackPayload) error {
	m, err := s.repo.FindMessageByReference(ctx, payload.MsgID, payload.MsgKey)
	if err != nil || m == nil {
		return err
	}
	operator, _ := parseIMUserID(payload.OperatorAccount)
	if operator == 0 {
		operator = m.FromUID
	}
	return s.repo.RecallMessage(ctx, m.MsgID, operator, s.now())
}

func callbackContent(body []MsgElement) (int, string) {
	for _, elem := range body {
		switch elem.MsgType {
		case "TIMTextElem":
			text, _ := elem.MsgContent["Text"].(string)
			return MessageTypeText, text
		case "TIMImageElem":
			return MessageTypeImage, "[图片]"
		case "TIMSoundElem":
			return MessageTypeAudio, "[语音]"
		case "TIMVideoFileElem":
			return MessageTypeVideo, "[视频]"
		case "TIMCustomElem":
			return MessageTypeSystem, "[自定义消息]"
		}
	}
	return MessageTypeSystem, "[消息]"
}

func callbackTypePreview(msgType int) string {
	switch msgType {
	case MessageTypeImage:
		return "[图片]"
	case MessageTypeAudio:
		return "[语音]"
	case MessageTypeVideo:
		return "[视频]"
	default:
		return "[消息]"
	}
}

func callbackBizID(p *CallbackPayload) string {
	if p.MsgID != "" {
		return p.MsgID
	}
	if p.MsgKey != "" {
		return p.MsgKey
	}
	return fmt.Sprintf("pre-%s-%s-%d-%d", p.FromAccount, p.ToAccount, p.MsgSeq, p.MsgRandom)
}

func parseIMUserID(raw string) (int64, bool) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "u_") {
		return 0, false
	}
	uid, err := strconv.ParseInt(strings.TrimPrefix(raw, "u_"), 10, 64)
	return uid, err == nil && uid > 0
}

func callbackKind(command string) string {
	switch command {
	case commandBeforeSend:
		return "before_send"
	case commandAfterSend:
		return "after_send"
	}
	lower := strings.ToLower(command)
	if strings.Contains(lower, "read") {
		return "read"
	}
	if strings.Contains(lower, "withdraw") || strings.Contains(lower, "recall") {
		return "recall"
	}
	return "unknown"
}

func callbackOK() CallbackResponse {
	return CallbackResponse{ActionStatus: "OK", ErrorInfo: "", ErrorCode: 0}
}

func callbackReject(code int, info string) CallbackResponse {
	return CallbackResponse{ActionStatus: "OK", ErrorInfo: info, ErrorCode: code}
}

func callbackFail(info string) CallbackResponse {
	return CallbackResponse{ActionStatus: "FAIL", ErrorInfo: info, ErrorCode: 1}
}
