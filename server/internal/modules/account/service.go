// Package account 负责账号体系：短信验证码、手机号登录注册、双令牌刷新、IM 账号签发。
//
// 设计约束：
//   - 验证码只存 Redis（5 分钟），不落库；发送频率与日上限也在 Redis 里控，避免打库；
//   - 注册必须在一个事务里完成 user / user_auth / user_profile / wallet 四张表；
//   - refresh token 采用"单设备单会话 + 一次性轮换"，旧 token 再次使用即视为泄露并强制重登；
//   - IM 账号导入失败绝不能阻塞登录（IM 是能力供应商，不是登录的必要条件）。
package account

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/datechat/server/internal/config"
	"github.com/datechat/server/internal/pkg/crypto"
	"github.com/datechat/server/internal/pkg/errs"
	"github.com/datechat/server/internal/pkg/jwt"
	"github.com/datechat/server/internal/pkg/phone"
	"github.com/datechat/server/internal/pkg/sms"
)

// Redis 键前缀。统一收口，避免各方法里散落字符串。
const (
	keySMSCodePrefix  = "sms:code:"
	keySMSCDPrefix    = "sms:cd:"
	keySMSDailyPrefix = "sms:day:"
	imImportTimeout   = 3 * time.Second
	maxDeviceIDLength = 64
	defaultNickPrefix = "聊友"
)

// verifyScript 原子校验验证码。
//
// KEYS[1]=验证码键；ARGV[1]=用户输入；ARGV[2]=最大错误次数。
// 返回值：1 通过、0 不匹配、-1 不存在/已过期、-2 已锁定。
//
// 必须用 Lua 而不是 GET+DEL：并发下两个请求同时 GET 到同一个码，
// 会让"一次性验证码"退化成可重放凭证。
const verifyScript = `
local v = redis.call('GET', KEYS[1])
if not v then return -1 end
local sep = string.find(v, '|', 1, true)
if not sep then return -1 end
local code = string.sub(v, 1, sep - 1)
local attempts = tonumber(string.sub(v, sep + 1)) or 0
local maxAttempts = tonumber(ARGV[2])
if attempts >= maxAttempts then return -2 end
if code == ARGV[1] then
  redis.call('DEL', KEYS[1])
  return 1
end
attempts = attempts + 1
local ttl = redis.call('PTTL', KEYS[1])
if ttl <= 0 then ttl = 300000 end
redis.call('SET', KEYS[1], code .. '|' .. attempts, 'PX', ttl)
if attempts >= maxAttempts then return -2 end
return 0
`

// Service 账号领域服务。
type Service struct {
	cfg    *config.Config
	log    *zap.Logger
	repo   *Repository
	rdb    *redis.Client
	tokens *jwt.Manager
	cipher *crypto.Cipher
	sms    sms.Provider
	store  TokenStore
	im     *IMClient

	now func() time.Time
}

// Deps 是 Service 的依赖集合，避免构造函数参数膨胀。
type Deps struct {
	Config *config.Config
	Log    *zap.Logger
	Repo   *Repository
	Redis  *redis.Client
	Tokens *jwt.Manager
	Cipher *crypto.Cipher
	SMS    sms.Provider
	Store  TokenStore // 为空且 Redis 可用时自动使用 Redis 实现
	IM     *IMClient
}

// NewService 构造服务。
func NewService(d Deps) *Service {
	store := d.Store
	if store == nil && d.Redis != nil {
		store = NewRedisTokenStore(d.Redis)
	}
	log := d.Log
	if log == nil {
		log = zap.NewNop()
	}
	// 没有任何短信通道时退化为"打日志"，保证服务能起、流程能跑通。
	provider := d.SMS
	if provider == nil {
		provider = sms.NewLogProvider(log)
	}
	return &Service{
		cfg:    d.Config,
		log:    log,
		repo:   d.Repo,
		rdb:    d.Redis,
		tokens: d.Tokens,
		cipher: d.Cipher,
		sms:    provider,
		store:  store,
		im:     d.IM,
		now:    time.Now,
	}
}

// SendCode 发送登录验证码。
func (s *Service) SendCode(ctx context.Context, in SendCodeInput) (*SendCodeOutput, error) {
	phoneNum, err := phone.Normalize(in.Phone)
	if err != nil {
		return nil, errs.ErrInvalidParam.WithMsg("请输入正确的手机号")
	}
	if s.rdb == nil {
		return nil, errs.ErrInternal.WithMsg("服务未就绪，请稍后再试")
	}
	hmac := s.cipher.HMAC(phoneNum)

	// 1) 同一手机号的发送间隔，用 SETNX 保证并发下只有一个请求能通过。
	cdKey := keySMSCDPrefix + hmac
	fresh, err := s.rdb.SetNX(ctx, cdKey, "1", s.cfg.SMS.ResendInterval.D()).Result()
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	if !fresh {
		ttl, _ := s.rdb.TTL(ctx, cdKey).Result()
		sec := int(ttl.Seconds())
		if sec < 1 {
			sec = 1
		}
		return nil, errs.ErrSmsTooFrequent.WithMsg("请 %d 秒后再获取验证码", sec)
	}

	// 2) 单手机号单日上限，按自然日计数。
	dayKey := fmt.Sprintf("%s%s:%s", keySMSDailyPrefix, hmac, s.now().Format("20060102"))
	count, err := s.rdb.Incr(ctx, dayKey).Result()
	if err != nil {
		_, _ = s.rdb.Del(ctx, cdKey).Result()
		return nil, errs.ErrInternal.WithErr(err)
	}
	if count == 1 {
		if err := s.rdb.Expire(ctx, dayKey, 24*time.Hour).Err(); err != nil {
			s.log.Warn("设置验证码日计数过期时间失败", zap.Error(err))
		}
	}
	if int(count) > s.cfg.SMS.DailyLimit {
		return nil, errs.ErrSmsTooFrequent.WithMsg("今日验证码发送次数已达上限，请明天再试")
	}

	// 3) 生成并落库（先落 Redis 再发短信：否则用户收到码却验证不了）。
	code, err := crypto.RandomDigits(s.cfg.SMS.CodeLength)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	codeKey := keySMSCodePrefix + hmac
	if err := s.rdb.Set(ctx, codeKey, code+"|0", s.cfg.SMS.CodeTTL.D()).Err(); err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}

	// 4) 下发。
	requestID, sendErr := s.sms.Send(ctx, sms.Message{
		Phone: phoneNum,
		Code:  code,
		TTL:   s.cfg.SMS.CodeTTL.D(),
	})
	s.writeSendLog(ctx, hmac, phoneNum, in, requestID, sendErr)
	if sendErr != nil {
		// 回滚验证码与冷却：发送失败还让用户等 60 秒是最容易被投诉的体验问题。
		_, _ = s.rdb.Del(ctx, codeKey).Result()
		_, _ = s.rdb.Del(ctx, cdKey).Result()
		return nil, errs.ErrServiceBusy.WithMsg("验证码发送失败，请稍后再试")
	}

	out := &SendCodeOutput{
		ExpireIn: int(s.cfg.SMS.CodeTTL.D().Seconds()),
		ResendIn: int(s.cfg.SMS.ResendInterval.D().Seconds()),
	}
	if s.cfg.CanExposeSMSCode() {
		out.DebugCode = code
	}
	return out, nil
}

// Login 手机号 + 验证码登录，未注册则自动注册。
func (s *Service) Login(ctx context.Context, in LoginInput) (*LoginOutput, error) {
	phoneNum, err := phone.Normalize(in.Phone)
	if err != nil {
		return nil, errs.ErrInvalidParam.WithMsg("请输入正确的手机号")
	}
	if in.DeviceID == "" || len(in.DeviceID) > maxDeviceIDLength {
		return nil, errs.ErrInvalidParam.WithMsg("缺少设备标识")
	}
	hmac := s.cipher.HMAC(phoneNum)

	if err := s.verifyCode(ctx, hmac, in.Code); err != nil {
		s.writeLoginLog(ctx, 0, hmac, in, LoginResultFailed, "验证码校验失败")
		return nil, err
	}

	u, isNew, err := s.findOrRegister(ctx, phoneNum, hmac, in)
	if err != nil {
		s.writeLoginLog(ctx, 0, hmac, in, LoginResultFailed, "账号不可用")
		return nil, err
	}

	pair, err := s.issueTokenPair(ctx, u.ID, in.DeviceID)
	if err != nil {
		return nil, err
	}

	// 设备登记与活跃时间：失败不影响登录，只记日志。
	now := s.now()
	if err := s.repo.UpsertDevice(ctx, &UserDevice{
		UserID:       u.ID,
		DeviceID:     in.DeviceID,
		Platform:     in.Platform,
		PushToken:    in.PushToken,
		AppVersion:   in.AppVersion,
		LastIP:       in.IP,
		LastActiveAt: &now,
	}); err != nil {
		s.log.Warn("登记设备失败", zap.Int64("uid", u.ID), zap.Error(err))
	}
	if err := s.repo.TouchActive(ctx, u.ID, now); err != nil {
		s.log.Warn("更新活跃时间失败", zap.Int64("uid", u.ID), zap.Error(err))
	}
	s.writeLoginLog(ctx, u.ID, hmac, in, LoginResultSuccess, "")

	// IM 账号导入：best-effort，异步执行，失败只告警。
	s.importIMAccountAsync(u)

	return &LoginOutput{
		UID:       u.ID,
		IsNewUser: isNew,
		Token:     *pair,
		User:      s.userBrief(ctx, u),
		IM:        s.buildIMInfo(u),
	}, nil
}

// Refresh 用 refresh token 换取新的双令牌。
// 旧 refresh 立即失效（一次性），重复使用会被判定为重放。
func (s *Service) Refresh(ctx context.Context, refreshToken string) (*TokenPair, error) {
	claims, err := s.tokens.Parse(refreshToken)
	if err != nil {
		return nil, err
	}
	if claims.Type != jwt.TypeRefresh {
		return nil, errs.ErrTokenInvalid.WithMsg("令牌类型不正确")
	}
	if claims.DeviceID == "" {
		return nil, errs.ErrTokenInvalid.WithMsg("令牌缺少设备信息，请重新登录")
	}

	newJTI, err := crypto.RandomToken(16)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	ok, err := s.store.RotateRefresh(ctx, claims.UID, claims.DeviceID, claims.ID, newJTI, s.tokens.TTL(jwt.TypeRefresh))
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	if !ok {
		// 这里同时覆盖三种情况：已退出登录、被其他设备顶下线、旧令牌被重放。
		// 对客户端来说处理方式一致：清理本地凭证，重新登录。
		s.log.Warn("刷新令牌校验失败（可能被重放）",
			zap.Int64("uid", claims.UID),
			zap.String("device_id", claims.DeviceID),
		)
		return nil, errs.ErrTokenInvalid.WithMsg("登录状态已失效，请重新登录")
	}

	accessJTI, err := crypto.RandomToken(16)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	access, accessExp, err := s.tokens.GenerateWithID(claims.UID, claims.DeviceID, jwt.TypeAccess, accessJTI)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	refresh, refreshExp, err := s.tokens.GenerateWithID(claims.UID, claims.DeviceID, jwt.TypeRefresh, newJTI)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	return &TokenPair{
		AccessToken:     access,
		AccessExpireAt:  accessExp.Unix(),
		RefreshToken:    refresh,
		RefreshExpireAt: refreshExp.Unix(),
	}, nil
}

// Logout 退出登录，吊销该设备的刷新态。
func (s *Service) Logout(ctx context.Context, uid int64, deviceID string) error {
	if deviceID == "" {
		return nil
	}
	if err := s.store.RevokeRefresh(ctx, uid, deviceID); err != nil {
		return errs.ErrInternal.WithErr(err)
	}
	return nil
}

// Me 返回当前登录用户摘要。
func (s *Service) Me(ctx context.Context, uid int64) (*UserBrief, error) {
	u, err := s.repo.FindByID(ctx, uid)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	if u == nil {
		return nil, errs.ErrUserNotFound
	}
	brief := s.userBrief(ctx, u)
	brief.RegisteredAt = u.CreatedAt.Unix()
	return &brief, nil
}

// UserSig 为当前用户签发 IM 登录凭证（客户端 UserSig 过期后调用）。
func (s *Service) UserSig(ctx context.Context, uid int64) (*IMInfo, error) {
	if s.im == nil || !s.im.CanSign() {
		return nil, errs.ErrServiceBusy.WithMsg("IM 服务未配置，请联系管理员")
	}
	u, err := s.repo.FindByID(ctx, uid)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	if u == nil {
		return nil, errs.ErrUserNotFound
	}
	info := s.buildIMInfo(u)
	if info.UserSig == "" {
		return nil, errs.ErrServiceBusy.WithMsg("IM 凭证签发失败，请稍后再试")
	}
	return &info, nil
}

// —— 内部实现 ——

// findOrRegister 查找或注册用户，并校验账号状态。
func (s *Service) findOrRegister(ctx context.Context, phoneNum, hmac string, in LoginInput) (*User, bool, error) {
	u, err := s.repo.FindByPhoneHMAC(ctx, hmac)
	if err != nil {
		return nil, false, errs.ErrInternal.WithErr(err)
	}

	isNew := false
	if u == nil {
		cipherPhone, encErr := s.cipher.Encrypt(phoneNum)
		if encErr != nil {
			return nil, false, errs.ErrInternal.WithErr(encErr)
		}
		u = &User{
			PhoneHMAC:   hmac,
			PhoneCipher: cipherPhone,
			Nickname:    defaultNickname(phoneNum),
			Status:      UserStatusNormal,
			RegisterIP:  in.IP,
		}
		if err := s.repo.CreateUser(ctx, u); err != nil {
			if !IsDuplicateKey(err) {
				return nil, false, errs.ErrInternal.WithErr(err)
			}
			// 并发注册：另一个请求刚插入成功，回读即可。
			u, err = s.repo.FindByPhoneHMAC(ctx, hmac)
			if err != nil || u == nil {
				return nil, false, errs.ErrInternal.WithErr(err)
			}
		} else {
			isNew = true
		}
	}

	switch u.Status {
	case UserStatusNormal:
	case UserStatusBanned, UserStatusDeleted:
		return nil, false, errs.ErrUserBanned
	case UserStatusFrozen:
		return nil, false, errs.ErrUserBanned.WithMsg("账号已被临时冻结，请联系客服解冻")
	default:
		return nil, false, errs.ErrUserBanned.WithMsg("账号状态异常，请联系客服")
	}
	return u, isNew, nil
}

// verifyCode 校验验证码，原子消费。
func (s *Service) verifyCode(ctx context.Context, hmac, code string) error {
	if code == "" {
		return errs.ErrSmsCodeInvalid.WithMsg("请输入验证码")
	}
	res, err := s.rdb.Eval(ctx, verifyScript,
		[]string{keySMSCodePrefix + hmac},
		code, s.cfg.SMS.MaxVerifyAttempts,
	).Int64()
	if err != nil {
		return errs.ErrInternal.WithErr(err)
	}
	switch res {
	case 1:
		return nil
	case -2:
		return errs.ErrSmsCodeInvalid.WithMsg("验证码错误次数过多，请重新获取")
	case -1:
		return errs.ErrSmsCodeInvalid.WithMsg("验证码已过期，请重新获取")
	default:
		return errs.ErrSmsCodeInvalid
	}
}

// issueTokenPair 签发双令牌，并把 refresh 的 jti 写入服务端状态。
func (s *Service) issueTokenPair(ctx context.Context, uid int64, deviceID string) (*TokenPair, error) {
	if s.store == nil {
		return nil, errs.ErrInternal.WithMsg("服务未就绪，请稍后再试")
	}
	refreshJTI, err := crypto.RandomToken(16)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}

	accessJTI, err := crypto.RandomToken(16)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}

	access, accessExp, err := s.tokens.GenerateWithID(uid, deviceID, jwt.TypeAccess, accessJTI)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	refresh, refreshExp, err := s.tokens.GenerateWithID(uid, deviceID, jwt.TypeRefresh, refreshJTI)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	if err := s.store.SaveRefresh(ctx, uid, deviceID, refreshJTI, s.tokens.TTL(jwt.TypeRefresh)); err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	return &TokenPair{
		AccessToken:     access,
		AccessExpireAt:  accessExp.Unix(),
		RefreshToken:    refresh,
		RefreshExpireAt: refreshExp.Unix(),
	}, nil
}

// buildIMInfo 组装 IM 登录材料；未配置密钥时返回 Enabled=false，客户端跳过 IM 登录。
func (s *Service) buildIMInfo(u *User) IMInfo {
	userID := IMUserIDOf(u.ID)
	if u.IMUserID != nil && *u.IMUserID != "" {
		userID = *u.IMUserID
	}
	if s.im == nil || !s.im.CanSign() {
		return IMInfo{UserID: userID}
	}
	sig, expireAt, err := s.im.UserSig(userID)
	if err != nil {
		s.log.Warn("签发 UserSig 失败", zap.Int64("uid", u.ID), zap.Error(err))
		return IMInfo{UserID: userID}
	}
	return IMInfo{
		Enabled:  true,
		SDKAppID: s.cfg.IM.SdkAppID,
		UserID:   userID,
		UserSig:  sig,
		ExpireAt: expireAt.Unix(),
	}
}

// importIMAccountAsync 异步导入 IM 账号。
// 用独立 context：请求结束后 ctx 会被取消，导入还没发出去就废了。
func (s *Service) importIMAccountAsync(u *User) {
	if s.im == nil || !s.im.ImportEnabled() {
		return
	}
	go func(uid int64, nickname string) {
		ctx, cancel := context.WithTimeout(context.Background(), imImportTimeout)
		defer cancel()
		if err := s.im.ImportAccount(ctx, IMUserIDOf(uid), nickname, ""); err != nil {
			s.log.Warn("IM 账号导入失败（不影响登录）", zap.Int64("uid", uid), zap.Error(err))
		}
	}(u.ID, u.Nickname)
}

// userBrief 组装用户摘要，手机号只回脱敏后的形式。
func (s *Service) userBrief(ctx context.Context, u *User) UserBrief {
	brief := UserBrief{
		UID:      u.ID,
		Nickname: u.Nickname,
		Avatar:   u.Avatar,
		Gender:   u.Gender,
	}
	if plain, err := s.cipher.Decrypt(u.PhoneCipher); err == nil {
		brief.PhoneMasked = phone.Mask(plain)
	}
	if prof, err := s.repo.FindProfile(ctx, u.ID); err == nil && prof != nil {
		brief.RealnameStatus = prof.RealnameStatus
	}
	return brief
}

// writeLoginLog 写登录审计日志，失败只告警。
func (s *Service) writeLoginLog(ctx context.Context, uid int64, hmac string, in LoginInput, result int, reason string) {
	entry := &UserLoginLog{
		UserID:    uid,
		PhoneHMAC: hmac,
		DeviceID:  in.DeviceID,
		IP:        in.IP,
		UserAgent: truncate(in.UserAgent, 255),
		Result:    result,
		Reason:    reason,
	}
	if err := s.repo.InsertLoginLog(ctx, entry); err != nil {
		s.log.Warn("写登录日志失败", zap.Error(err))
	}
}

// writeSendLog 写验证码发送审计（风控与短信成本核算都要用），失败只告警。
func (s *Service) writeSendLog(ctx context.Context, hmac, phoneNum string, in SendCodeInput, requestID string, sendErr error) {
	result := 1
	errMsg := ""
	if sendErr != nil {
		result = 2
		errMsg = truncate(sendErr.Error(), 255)
		s.log.Error("验证码发送失败", zap.String("phone", phone.Mask(phoneNum)), zap.Error(sendErr))
	}
	entry := &SMSSendLog{
		PhoneHMAC: hmac,
		PhoneMask: phone.Mask(phoneNum),
		Scene:     truncate(in.Scene, 16),
		Provider:  truncate(s.sms.Name(), 16),
		RequestID: truncate(requestID, 64),
		IP:        truncate(in.IP, 45),
		Result:    result,
		ErrorMsg:  errMsg,
	}
	if err := s.repo.InsertSMSSendLog(ctx, entry); err != nil {
		s.log.Warn("写短信发送日志失败", zap.Error(err))
	}
}

// defaultNickname 用手机号后 4 位生成默认昵称，避免所有新用户都叫"用户"。
func defaultNickname(phoneNum string) string {
	if len(phoneNum) != 11 {
		return defaultNickPrefix
	}
	return defaultNickPrefix + phoneNum[7:]
}

// truncate 按字节截断，避免超长输入撑爆列宽。
func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit]
}
