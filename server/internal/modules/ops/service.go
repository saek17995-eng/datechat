package ops

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/datechat/server/internal/config"
	"github.com/datechat/server/internal/pkg/crypto"
	"github.com/datechat/server/internal/pkg/errs"
	"github.com/datechat/server/internal/pkg/phone"
	"github.com/datechat/server/internal/pkg/presence"
)

// Deps 构造依赖。
type Deps struct {
	Config *config.Config
	Log    *zap.Logger
	Repo   *Repository
	Rdb    *redis.Client
	Cipher *crypto.Cipher // 用于展示脱敏手机号与按手机号检索
	Now    func() time.Time
}

// Service 运营后台服务。
type Service struct {
	cfg    *config.Config
	log    *zap.Logger
	repo   *Repository
	rdb    *redis.Client
	cipher *crypto.Cipher
	now    func() time.Time
}

func NewService(d Deps) *Service {
	if d.Log == nil {
		d.Log = zap.NewNop()
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{
		cfg: d.Config, log: d.Log, repo: d.Repo,
		rdb: d.Rdb, cipher: d.Cipher, now: d.Now,
	}
}

// Overview 运营概览。since 由调用方给（一般是今天零点），便于测试与按天补查。
func (s *Service) Overview(ctx context.Context, since time.Time) (*Overview, error) {
	out, err := s.repo.Overview(ctx, since)
	if err != nil {
		s.log.Error("查询运营概览失败", zap.Error(err))
		return nil, errs.ErrInternal.WithErr(err)
	}
	return out, nil
}

// ListUsers 用户列表。
//
// 手机号一律脱敏后再出接口，即使后台令牌也不给明文：
// 后台账号往往多人共用，明文手机号一旦被导出就是一次数据事故。
func (s *Service) ListUsers(ctx context.Context, q UserQuery) ([]UserRow, int64, error) {
	hmac := ""
	// 关键词是合法手机号时走 HMAC 精确检索：手机号是密文存储，
	// 用 LIKE 既查不到也没法走索引。
	if q.Keyword != "" {
		if norm, nerr := phone.Normalize(q.Keyword); nerr == nil && s.cipher != nil {
			hmac = s.cipher.HMAC(norm)
		}
	}
	rows, total, err := s.repo.ListUsers(ctx, q, hmac)
	if err != nil {
		s.log.Error("查询用户列表失败", zap.Error(err))
		return nil, 0, errs.ErrInternal.WithErr(err)
	}
	s.decorate(rows)
	return rows, total, nil
}

// GetUser 用户详情。
func (s *Service) GetUser(ctx context.Context, uid int64) (*UserDetail, error) {
	row, err := s.repo.GetUser(ctx, uid)
	if err != nil {
		s.log.Error("查询用户详情失败", zap.Int64("uid", uid), zap.Error(err))
		return nil, errs.ErrInternal.WithErr(err)
	}
	if row == nil {
		return nil, errs.ErrUserNotFound
	}
	s.decorate([]UserRow{*row})

	blocked, blockedBy, calls, seconds, income, err := s.repo.UserStats(ctx, uid)
	if err != nil {
		s.log.Warn("查询用户统计失败", zap.Int64("uid", uid), zap.Error(err))
	}
	reports, err := s.repo.RecentReports(ctx, uid, 5)
	if err != nil {
		s.log.Warn("查询用户举报记录失败", zap.Int64("uid", uid), zap.Error(err))
	}
	orders, _, err := s.repo.ListOrders(ctx, OrderQuery{UserID: uid, Status: -1, Page: 1, Size: 5})
	if err != nil {
		s.log.Warn("查询用户订单失败", zap.Int64("uid", uid), zap.Error(err))
	}

	out := &UserDetail{
		UserRow:        *row,
		BlockedCount:   blocked,
		BlockedByCount: blockedBy,
		CallCount:      calls,
		CallSeconds:    seconds,
		IncomeCoins:    income,
		RecentReports:  reports,
		RecentOrders:   orders,
	}
	if s.rdb != nil {
		if n, e := s.rdb.Exists(ctx, presence.Key(uid)).Result(); e == nil {
			out.Online = n > 0
		}
	}
	return out, nil
}

// SetUserStatus 设置用户状态。
//
// 三件事必须一起做，顺序不能变：
//  1. 校验状态合法（不允许后台一键"注销"）；
//  2. 改库；
//  3. 写审计日志（before/after 都记），失败要能查得到——
//     只改状态不记日志，出问题时没人知道是谁干的。
func (s *Service) SetUserStatus(ctx context.Context, adminID int64, adminName string,
	uid int64, status int, banDays int, reason, ip string) error {
	if !ValidStatus(status) {
		return errs.ErrInvalidParam.WithMsg("状态不合法（1 正常 / 2 冻结 / 3 封禁）")
	}
	if status != StatusNormal && reason == "" {
		return errs.ErrInvalidParam.WithMsg("封禁/冻结必须填写原因")
	}

	before, err := s.repo.GetUser(ctx, uid)
	if err != nil {
		return errs.ErrInternal.WithErr(err)
	}
	if before == nil {
		return errs.ErrUserNotFound
	}

	var banUntil *time.Time
	if status == StatusBanned && banDays > 0 {
		t := s.now().AddDate(0, 0, banDays)
		banUntil = &t
	}
	if status == StatusNormal {
		reason = ""
	}
	if err := s.repo.SetUserStatus(ctx, uid, status, banUntil, reason); err != nil {
		s.log.Error("修改用户状态失败", zap.Int64("uid", uid), zap.Error(err))
		return errs.ErrInternal.WithErr(err)
	}

	beforeJSON := mustJSON(map[string]any{
		"status": before.Status, "ban_until": before.BanUntil, "ban_reason": before.BanReason,
	})
	afterJSON := mustJSON(map[string]any{
		"status": status, "ban_until": banUntil, "ban_reason": reason,
	})
	if logErr := s.repo.InsertOpLog(ctx, adminID, adminName, "user", "set_status",
		"user", itoa64(uid), beforeJSON, afterJSON, reason, ip); logErr != nil {
		// 状态已经改了，日志没写上去也要让调用方知道：否则这次操作等于没有留痕。
		s.log.Error("写操作日志失败", zap.Int64("uid", uid), zap.Error(logErr))
		return errs.ErrInternal.WithMsg("状态已更新，但操作日志写入失败，请人工补记")
	}
	return nil
}

// ListOrders 充值订单。
func (s *Service) ListOrders(ctx context.Context, q OrderQuery) ([]OrderRow, int64, error) {
	rows, total, err := s.repo.ListOrders(ctx, q)
	if err != nil {
		return nil, 0, errs.ErrInternal.WithErr(err)
	}
	return rows, total, nil
}

// ListCalls 通话记录。
func (s *Service) ListCalls(ctx context.Context, q CallQuery) ([]CallRow, int64, error) {
	rows, total, err := s.repo.ListCalls(ctx, q)
	if err != nil {
		return nil, 0, errs.ErrInternal.WithErr(err)
	}
	return rows, total, nil
}

// ListOpLogs 操作日志。
func (s *Service) ListOpLogs(ctx context.Context, q OpLogQuery) ([]OpLogRow, int64, error) {
	rows, total, err := s.repo.ListOpLogs(ctx, q)
	if err != nil {
		return nil, 0, errs.ErrInternal.WithErr(err)
	}
	return rows, total, nil
}

// decorate 补上展示字段：状态文案、年龄、脱敏手机号。
func (s *Service) decorate(rows []UserRow) {
	now := s.now()
	for i := range rows {
		rows[i].StatusText = statusText(rows[i].Status)
		if s.cipher != nil && len(rows[i].PhoneCipher) > 0 {
			if plain, err := s.cipher.Decrypt(rows[i].PhoneCipher); err == nil && plain != "" {
				rows[i].Phone = phone.Mask(plain)
			}
		}
		if rows[i].Birthday != nil {
			rows[i].Age = ageOf(*rows[i].Birthday, now)
		}
	}
}

// ageOf 由生日算周岁；没有生日返回 0，列表里显示为未知。
func ageOf(birthday, now time.Time) int {
	age := now.Year() - birthday.Year()
	if now.YearDay() < birthday.YearDay() {
		age--
	}
	if age < 0 {
		return 0
	}
	return age
}

// mustJSON 审计日志用的 before/after 快照；序列化失败不能阻断主流程。
func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func itoa64(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [24]byte
	pos := len(buf)
	for v > 0 {
		pos--
		buf[pos] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
