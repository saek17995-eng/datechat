// Package realname 实现实名认证。
//
// 状态机只有四态：未认证 → 审核中 → 通过 / 拒绝。
// 通过之后不允许再次提交（换身份证需要走人工申诉，避免账号买卖）。
//
// 敏感数据（姓名、身份证号）一律 AES-GCM 加密落库，另存 HMAC 做"一号一人"查重。
package realname

import (
	"context"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"go.uber.org/zap"

	"github.com/datechat/server/internal/config"
	"github.com/datechat/server/internal/pkg/crypto"
	"github.com/datechat/server/internal/pkg/errs"
	"github.com/datechat/server/internal/pkg/idcard"
)

// 姓名允许：汉字、英文字母、间隔号（维吾尔族姓名里的 ·）、空格。
var namePattern = regexp.MustCompile(`^[\p{Han}A-Za-z·•\s]{2,20}$`)

// Service 实名领域服务。
type Service struct {
	cfg      *config.Config
	log      *zap.Logger
	repo     *Repository
	cipher   *crypto.Cipher
	provider Provider
	now      func() time.Time
}

// NewService 构造服务。provider 为空时退化为本地模拟通道，保证流程可跑通。
func NewService(cfg *config.Config, log *zap.Logger, repo *Repository, c *crypto.Cipher, provider Provider) *Service {
	if log == nil {
		log = zap.NewNop()
	}
	if provider == nil {
		provider = MockProvider{}
	}
	return &Service{
		cfg:      cfg,
		log:      log,
		repo:     repo,
		cipher:   c,
		provider: provider,
		now:      time.Now,
	}
}

// SubmitInput 提交实名。
type SubmitInput struct {
	RealName string
	IDCard   string
}

// StatusOutput 实名状态。
type StatusOutput struct {
	Status           int    `json:"status"`
	StatusText       string `json:"status_text"`
	Channel          string `json:"channel,omitempty"`
	RealNameMasked   string `json:"real_name_masked,omitempty"`
	IDCardMasked     string `json:"id_card_masked,omitempty"`
	FailReason       string `json:"fail_reason,omitempty"`
	VerifiedAt       int64  `json:"verified_at,omitempty"`
	NeedClientAction bool   `json:"need_client_action,omitempty"`
	H5URL            string `json:"h5_url,omitempty"`
	Trace            string `json:"trace,omitempty"`
}

// Submit 提交实名信息并触发核身。
func (s *Service) Submit(ctx context.Context, uid int64, in SubmitInput) (*StatusOutput, error) {
	if !s.cfg.Realname.Enabled {
		return nil, errs.ErrServiceBusy.WithMsg("实名认证暂未开放")
	}
	name := strings.TrimSpace(in.RealName)
	if !namePattern.MatchString(name) || utf8.RuneCountInString(name) < 2 {
		return nil, errs.ErrInvalidParam.WithMsg("请输入真实姓名（2~20 个汉字）")
	}
	idNo, err := idcard.Normalize(in.IDCard)
	if err != nil {
		return nil, errs.ErrInvalidParam.WithMsg("身份证号格式不正确")
	}
	if err := idcard.Validate(idNo); err != nil {
		return nil, errs.ErrInvalidParam.WithMsg(err.Error())
	}

	existing, err := s.repo.FindByUserID(ctx, uid)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	if existing != nil && existing.Status == StatusPassed {
		return nil, errs.ErrInvalidParam.WithMsg("你已完成实名认证")
	}

	hmac := s.cipher.HMAC(idNo)
	owner, err := s.repo.FindByIDCardHMAC(ctx, hmac)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	if owner != nil && owner.UserID != uid {
		return nil, errs.ErrInvalidParam.WithMsg("该身份证已被其他账号绑定")
	}

	nameCipher, err := s.cipher.Encrypt(name)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	idCipher, err := s.cipher.Encrypt(idNo)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}

	start, err := s.provider.Start(ctx, StartInput{UserID: uid, RealName: name, IDCard: idNo})
	if err != nil {
		s.log.Error("实名通道发起核身失败", zap.Int64("uid", uid), zap.String("provider", s.provider.Name()), zap.Error(err))
		return nil, errs.ErrServiceBusy.WithMsg("实名认证服务暂时不可用，请稍后再试")
	}

	rec := &Record{
		UserID:         uid,
		RealNameCipher: nameCipher,
		IDCardHMAC:     hmac,
		IDCardCipher:   idCipher,
		Status:         StatusProcessing,
		Channel:        channelOf(s.provider.Name()),
		ProviderTrace:  start.Trace,
	}
	if start.Result != nil {
		s.applyResult(rec, start.Result)
	}

	if err := s.repo.Save(ctx, rec); err != nil {
		if IsDuplicateKey(err) {
			return nil, errs.ErrInvalidParam.WithMsg("该身份证已被其他账号绑定")
		}
		return nil, errs.ErrInternal.WithErr(err)
	}
	if err := s.repo.UpdateProfileStatus(ctx, uid, rec.Status); err != nil {
		// 冗余字段同步失败不影响主流程，下次状态变更会再写一次。
		s.log.Warn("同步实名冗余状态失败", zap.Int64("uid", uid), zap.Error(err))
	}

	out := s.buildStatus(rec)
	if rec.Status == StatusProcessing {
		out.NeedClientAction = start.H5URL != ""
		out.H5URL = start.H5URL
	}
	return out, nil
}

// Status 查询实名状态。
func (s *Service) Status(ctx context.Context, uid int64) (*StatusOutput, error) {
	rec, err := s.repo.FindByUserID(ctx, uid)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	if rec == nil {
		return &StatusOutput{Status: StatusUnverified, StatusText: statusText(StatusUnverified)}, nil
	}
	return s.buildStatus(rec), nil
}

// Query 用核身流水号回查结果并落库。客户端完成活体检测后调用。
func (s *Service) Query(ctx context.Context, uid int64, trace string) (*StatusOutput, error) {
	rec, err := s.repo.FindByUserID(ctx, uid)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	if rec == nil {
		return nil, errs.ErrNotFound.WithMsg("还没有提交实名认证")
	}
	if rec.Status != StatusProcessing {
		return s.buildStatus(rec), nil
	}
	if trace == "" {
		trace = rec.ProviderTrace
	}
	if trace == "" || trace != rec.ProviderTrace {
		return nil, errs.ErrInvalidParam.WithMsg("核身流水号不匹配")
	}

	res, err := s.provider.Query(ctx, trace)
	if err != nil {
		s.log.Error("查询实名核身结果失败", zap.Int64("uid", uid), zap.Error(err))
		return nil, errs.ErrServiceBusy.WithMsg("查询实名结果失败，请稍后再试")
	}
	s.applyResult(rec, res)
	if err := s.repo.Save(ctx, rec); err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	if err := s.repo.UpdateProfileStatus(ctx, uid, rec.Status); err != nil {
		s.log.Warn("同步实名冗余状态失败", zap.Int64("uid", uid), zap.Error(err))
	}
	return s.buildStatus(rec), nil
}

// applyResult 把通道结论写进记录。通过时记录时间戳，拒绝时保留原因便于申诉。
func (s *Service) applyResult(rec *Record, res *Result) {
	if res.Trace != "" {
		rec.ProviderTrace = res.Trace
	}
	if res.Passed {
		now := s.now()
		rec.Status = StatusPassed
		rec.VerifiedAt = &now
		rec.FailReason = ""
		return
	}
	rec.Status = StatusRejected
	rec.VerifiedAt = nil
	reason := res.Reason
	if reason == "" {
		reason = "核身未通过"
	}
	if utf8.RuneCountInString(reason) > 120 {
		reason = string([]rune(reason)[:120])
	}
	rec.FailReason = reason
}

// buildStatus 组装返回体：姓名与身份证只给脱敏形式。
func (s *Service) buildStatus(rec *Record) *StatusOutput {
	out := &StatusOutput{
		Status:     rec.Status,
		StatusText: statusText(rec.Status),
		Channel:    channelName(rec.Channel),
		FailReason: rec.FailReason,
	}
	if rec.VerifiedAt != nil {
		out.VerifiedAt = rec.VerifiedAt.Unix()
	}
	if len(rec.RealNameCipher) > 0 {
		if name, err := s.cipher.Decrypt(rec.RealNameCipher); err == nil {
			out.RealNameMasked = maskName(name)
		}
	}
	if len(rec.IDCardCipher) > 0 {
		if idNo, err := s.cipher.Decrypt(rec.IDCardCipher); err == nil {
			out.IDCardMasked = idcard.Mask(idNo)
		}
	}
	return out
}

// maskName 只露首字：张三 → 张**。
func maskName(name string) string {
	runes := []rune(name)
	if len(runes) == 0 {
		return ""
	}
	if len(runes) == 1 {
		return string(runes[0])
	}
	return string(runes[0]) + strings.Repeat("*", len(runes)-1)
}

func statusText(status int) string {
	switch status {
	case StatusProcessing:
		return "审核中"
	case StatusPassed:
		return "已认证"
	case StatusRejected:
		return "认证未通过"
	default:
		return "未认证"
	}
}

func channelOf(providerName string) int {
	switch providerName {
	case "tencent":
		return ChannelTencent
	case "aliyun":
		return ChannelAliyun
	case "manual":
		return ChannelManual
	default:
		return ChannelMock
	}
}

func channelName(channel int) string {
	switch channel {
	case ChannelTencent:
		return "tencent"
	case ChannelAliyun:
		return "aliyun"
	case ChannelManual:
		return "manual"
	default:
		return "mock"
	}
}
