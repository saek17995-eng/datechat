package moderation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"

	"github.com/datechat/server/internal/config"
	"github.com/datechat/server/internal/pkg/errs"
	"github.com/datechat/server/internal/pkg/jwt"
	"github.com/datechat/server/internal/pkg/textfilter"
)

// CheckInput 一次文本审核的输入。
type CheckInput struct {
	Scene    string // 业务场景：message / nickname / signature
	BizType  int    // BizMessage / BizProfile …
	BizID    string // 业务ID，用于留痕回溯
	UID      int64
	Nickname string
	Text     string
	// Save 是否落审核留痕。资料类必落；消息类在批量场景可由调用方决定。
	Save bool
}

// Deps 依赖。Filter/Cloud 用接口而非具体类型，方便测试注入假实现。
type Deps struct {
	Config *config.Config
	Log    *zap.Logger
	Repo   *Repository
	Filter TextFilter
	Cloud  CloudChecker
	Tokens *jwt.Manager
}

// Service 内容安全与关系处置服务。
type Service struct {
	cfg    *config.Config
	log    *zap.Logger
	repo   *Repository
	filter TextFilter
	cloud  CloudChecker
	tokens *jwt.Manager
	now    func() time.Time
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
	return &Service{
		cfg: d.Config, log: log, repo: d.Repo,
		filter: filter, cloud: d.Cloud, tokens: d.Tokens,
		now: time.Now,
	}
}

// 允许的举报理由。白名单而不是自由文本，便于后台统计与自动化处置。
var reportReasons = map[string]struct{}{
	"spam": {}, "porn": {}, "abuse": {}, "fraud": {}, "minor": {}, "other": {},
}

// CheckText 统一文本审核入口。
//
// 两道防线的组合策略：
//  1. 本地 DFA 先跑——快、免费、可解释，命中拦截级就直接结束，不再花云端的钱和时间；
//  2. 只有"DFA 没拦住、且配置了云审核"时才送云，云端负责识别变体与语义风险；
//  3. 云端不可用（未配置/超时/报错）时按 CloudFailOpen 决定放行还是转人工，
//     默认放行——云抖动不该让用户发不出消息，但要留下警告日志。
func (s *Service) CheckText(ctx context.Context, in CheckInput) (Verdict, error) {
	v := Verdict{Text: in.Text, Provider: ProviderDFA}

	if s.cfg != nil && s.cfg.Moderation.DFAEnabled {
		got := s.filter.Check(in.Text)
		v.Hits = got.Hits
		v.Text = got.Text
		if got.Blocked {
			v.Blocked = true
			v.Label = firstLabel(got.Hits)
			v.Score = 1
			s.save(ctx, in, &v, ResultReject)
			return v, nil
		}
		if got.Review {
			v.Review = true
			v.Label = firstLabel(got.Hits)
		}
	}

	if s.cloud != nil && s.cloud.Enabled() && s.cfg != nil && s.cfg.Moderation.CloudEnabled {
		// DFA 已判定拦截就没必要送云；DFA 未命中时，是否送云由 CloudOnUnknown 决定。
		if !v.Blocked && (v.Review || len(v.Hits) > 0 || s.cfg.Moderation.CloudOnUnknown) {
			cloud, err := s.cloud.Moderate(ctx, in.Text, in.BizID, in.UID, in.Nickname)
			if err != nil {
				s.log.Warn("云审核调用失败", zap.String("scene", in.Scene), zap.Error(err))
				if !s.cfg.Moderation.CloudFailOpen {
					// 保守策略：云不可用时转人工，不放行。
					v.Review = true
					v.Label = "cloud_unavailable"
					s.save(ctx, in, &v, ResultReview)
					return v, nil
				}
			} else if cloud != nil {
				v.Provider = ProviderTMS
				v.Label = cloud.Label
				v.Score = cloud.Score01()
				for _, k := range cloud.Keywords {
					v.Hits = append(v.Hits, textfilter.Hit{Word: k, Category: cloud.Label, Level: 0})
				}
				switch {
				case cloud.Blocked():
					v.Blocked = true
				case cloud.NeedReview():
					v.Review = true
				}
			}
		}
	}

	result := ResultPass
	switch {
	case v.Blocked:
		result = ResultReject
	case v.Review:
		result = ResultReview
	}
	s.save(ctx, in, &v, result)
	return v, nil
}

// save 落审核留痕。失败只记日志：审核记录写不进去不该阻塞用户发消息。
func (s *Service) save(ctx context.Context, in CheckInput, v *Verdict, result int) {
	if !in.Save || s.repo == nil {
		return
	}
	raw := ""
	if len(v.Hits) > 0 {
		if b, err := json.Marshal(v.Hits); err == nil {
			raw = string(b)
		}
	}
	rec := &ModerationRecord{
		BizType:  in.BizType,
		BizID:    in.BizID,
		UserID:   in.UID,
		Provider: v.Provider,
		Scene:    in.Scene,
		Label:    v.Label,
		Score:    v.Score,
		Result:   result,
		Raw:      raw,
	}
	if err := s.repo.InsertModeration(ctx, rec); err != nil {
		s.log.Warn("写审核留痕失败", zap.String("scene", in.Scene), zap.Error(err))
	}
}

func firstLabel(hits []textfilter.Hit) string {
	if len(hits) == 0 {
		return ""
	}
	return hits[0].Category
}

// ReportInput 举报提交参数。
type ReportInput struct {
	TargetType int
	TargetID   string
	TargetUID  int64
	Reason     string
	Detail     string
	Evidence   []string
}

// SubmitReport 提交举报。
// 限频与去重都在服务端：客户端的按钮置灰只是体验优化，不能作为风控依据。
func (s *Service) SubmitReport(ctx context.Context, uid int64, in ReportInput) (*Report, error) {
	if _, ok := reportReasons[in.Reason]; !ok {
		return nil, errs.BadRequest("举报理由不合法")
	}
	if in.TargetID == "" {
		return nil, errs.BadRequest("缺少举报对象")
	}
	if in.TargetType == TargetUser && in.TargetUID == 0 {
		in.TargetUID = parseID(in.TargetID)
	}
	if in.TargetUID == uid {
		return nil, errs.BadRequest("不能举报自己")
	}
	if s.cfg != nil && s.cfg.Moderation.Report.ReasonRequired && strings.TrimSpace(in.Detail) == "" {
		return nil, errs.BadRequest("请补充举报描述")
	}
	if len(in.Detail) > 500 {
		return nil, errs.BadRequest("举报描述过长")
	}

	limit := 20
	if s.cfg != nil && s.cfg.Moderation.Report.DailyLimit > 0 {
		limit = s.cfg.Moderation.Report.DailyLimit
	}
	n, err := s.repo.CountReportToday(ctx, uid, startOfDay(s.now()))
	if err != nil {
		return nil, err
	}
	if int(n) >= limit {
		return nil, errs.TooManyRequests("今日举报次数已达上限")
	}

	rep := &Report{
		ReporterID: uid,
		TargetType: in.TargetType,
		TargetID:   in.TargetID,
		TargetUID:  in.TargetUID,
		Reason:     in.Reason,
		Detail:     in.Detail,
		Status:     ReportPending,
	}
	if len(in.Evidence) > 0 {
		if b, e := json.Marshal(in.Evidence); e == nil {
			v := string(b)
			rep.Evidence = &v
		}
	}
	if err := s.repo.InsertReport(ctx, rep); err != nil {
		if IsDuplicate(err) {
			return nil, errs.Conflict("你已经举报过该内容，我们会尽快处理")
		}
		return nil, err
	}
	return rep, nil
}

// AdminLogin 后台账号登录。
// 失败统一返回"用户名或密码错误"，不区分账号是否存在，避免账号枚举。
func (s *Service) AdminLogin(ctx context.Context, username, password, ip string) (string, time.Time, *AdminUser, error) {
	if s.tokens == nil {
		return "", time.Time{}, nil, errors.New("后台登录未启用")
	}
	admin, err := s.repo.FindAdminByUsername(ctx, username)
	if err != nil {
		s.log.Info("后台登录失败：账号不存在", zap.String("username", username), zap.String("ip", ip))
		return "", time.Time{}, nil, errs.Unauthorized("用户名或密码错误")
	}
	if admin.Status != AdminStatusEnabled {
		return "", time.Time{}, nil, errs.Unauthorized("账号已停用")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(password)); err != nil {
		s.log.Info("后台登录失败：口令错误", zap.String("username", username), zap.String("ip", ip))
		return "", time.Time{}, nil, errs.Unauthorized("用户名或密码错误")
	}
	token, expireAt, err := s.tokens.GenerateAdmin(admin.ID, RoleName(admin.Role))
	if err != nil {
		return "", time.Time{}, nil, err
	}
	if err := s.repo.TouchAdminLogin(ctx, admin.ID, ip); err != nil {
		s.log.Warn("更新后台登录信息失败", zap.Error(err))
	}
	return token, expireAt, admin, nil
}

// EnsureBootstrapAdmin 在没有任何后台账号时创建超管。
// 只在首次部署生效；口令来自配置，初始化后应从配置中移除。
func (s *Service) EnsureBootstrapAdmin(ctx context.Context, username, password, realName string) error {
	if username == "" || password == "" || s.repo == nil {
		return nil
	}
	n, err := s.repo.CountAdmin(ctx)
	if err != nil || n > 0 {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if err := s.repo.InsertAdmin(ctx, &AdminUser{
		Username: username, PasswordHash: string(hash), RealName: realName,
		Role: RoleSuperAdmin, Status: AdminStatusEnabled,
	}); err != nil {
		return err
	}
	s.log.Info("已初始化后台超管账号", zap.String("username", username))
	return nil
}

// HandleReport 处置一条举报。
// 处置动作会同时写审计日志：运营的每一次封禁都要能追责。
func (s *Service) HandleReport(ctx context.Context, adminID int64, adminName, ip string, reportID int64, action, note string) error {
	rep, err := s.repo.FindReport(ctx, reportID)
	if err != nil {
		return errs.NotFound("举报不存在")
	}
	if rep.Status != ReportPending {
		return errs.Conflict("该举报已处理")
	}

	status := ReportHandled
	if action == ActionIgnore {
		status = ReportRejected
	}
	if err := s.repo.UpdateReportHandle(ctx, reportID, adminID, status, action, note); err != nil {
		return err
	}

	// 处置到"人"的动作才改用户状态；warn/ignore 只留记录。
	if rep.TargetUID > 0 && (action == ActionFreeze || action == ActionBan) {
		target := UserStatusFrozen
		if action == ActionBan {
			target = UserStatusBanned
		}
		before, _ := s.repo.UserStatus(ctx, rep.TargetUID)
		if err := s.repo.UpdateUserStatus(ctx, rep.TargetUID, target); err != nil {
			return err
		}
		if err := s.repo.InsertOpLog(ctx, adminID, adminName, "user", action, "user", fmt.Sprint(rep.TargetUID),
			statusJSON(before), statusJSON(target), note, ip); err != nil {
			s.log.Warn("写审计日志失败", zap.Error(err))
		}
	}
	if err := s.repo.InsertOpLog(ctx, adminID, adminName, "report", "handle_report", "report", fmt.Sprint(reportID),
		statusJSON(rep.Status), statusJSON(status), note, ip); err != nil {
		s.log.Warn("写审计日志失败", zap.Error(err))
	}
	return nil
}

// SetUserStatus 直接处置用户（冻结/封禁/解封），不经过举报。
func (s *Service) SetUserStatus(ctx context.Context, adminID int64, adminName, ip string, uid int64, action, note string) error {
	var target int
	switch action {
	case ActionFreeze:
		target = UserStatusFrozen
	case ActionBan:
		target = UserStatusBanned
	case ActionUnban:
		target = UserStatusNormal
	default:
		return errs.BadRequest("不支持的处置动作")
	}
	before, err := s.repo.UserStatus(ctx, uid)
	if err != nil {
		return errs.NotFound("用户不存在")
	}
	if err := s.repo.UpdateUserStatus(ctx, uid, target); err != nil {
		return err
	}
	if err := s.repo.InsertOpLog(ctx, adminID, adminName, "user", action, "user", fmt.Sprint(uid),
		statusJSON(before), statusJSON(target), note, ip); err != nil {
		s.log.Warn("写审计日志失败", zap.Error(err))
	}
	return nil
}

// ReviewModeration 人工复核审核记录。pass=true 判定通过，false 判定违规。
func (s *Service) ReviewModeration(ctx context.Context, adminID int64, id int64, pass bool, note string) error {
	result := ResultReject
	if pass {
		result = ResultPass
	}
	return s.repo.UpdateModerationHandle(ctx, id, adminID, result, note)
}

// ListReports 后台举报列表。
func (s *Service) ListReports(ctx context.Context, status, page, size int) ([]Report, int64, error) {
	p, n := normalizePage(page, size)
	return s.repo.ListReports(ctx, status, p, n)
}

// ListModeration 后台审核列表。
func (s *Service) ListModeration(ctx context.Context, result, page, size int) ([]ModerationRecord, int64, error) {
	p, n := normalizePage(page, size)
	return s.repo.ListModeration(ctx, result, p, n)
}

func normalizePage(page, size int) (int, int) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 20
	}
	if size > 100 {
		size = 100
	}
	return page, size
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func parseID(s string) int64 {
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int64(c-'0')
	}
	return n
}

func statusJSON(v int) string {
	b, err := json.Marshal(map[string]int{"status": v})
	if err != nil {
		return ""
	}
	return string(b)
}
