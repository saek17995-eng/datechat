package call

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/datechat/server/internal/config"
	"github.com/datechat/server/internal/pkg/errs"
)

// Deps 构造依赖。
// 依赖全部是接口：RoomProvider（进房凭据）与 Charger（计费）都可以被替换成假实现，
// 这样"计费账本是否算对"能被单测覆盖，而不必真的打通一路音视频。
type Deps struct {
	Config *config.Config
	Log    *zap.Logger
	Repo   *Repository
	Room   RoomProvider
	Wallet Charger
	Now    func() time.Time
}

// Service 通话服务。
type Service struct {
	cfg    *config.Config
	log    *zap.Logger
	repo   *Repository
	room   RoomProvider
	wallet Charger
	now    func() time.Time
}

// NewService 构造服务。
func NewService(d Deps) *Service {
	if d.Log == nil {
		d.Log = zap.NewNop()
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{cfg: d.Config, log: d.Log, repo: d.Repo, room: d.Room, wallet: d.Wallet, now: d.Now}
}

func (s *Service) policy() config.CallPolicy {
	if s.cfg == nil {
		return config.CallConfig{}.Policy()
	}
	return s.cfg.Call.Policy()
}

// CreateResult 发起通话的结果。
type CreateResult struct {
	Session              *Session    `json:"session"`
	Ticket               *RoomTicket `json:"ticket"`
	PricePerMin          int64       `json:"price_per_min"`
	HeartbeatIntervalSec int         `json:"heartbeat_interval_sec"`
	Balance              int64       `json:"balance"`
}

// Create 发起通话。
//
// 前置校验的顺序是刻意安排的：先查"能不能打"（用户状态/拉黑/并发），
// 再查"打得起吗"（余额），最后才建单——避免给一个打不起的通话留下僵尸记录。
func (s *Service) Create(ctx context.Context, callerID, calleeID int64, mediaType int) (*CreateResult, error) {
	if callerID <= 0 || calleeID <= 0 {
		return nil, errs.ErrInvalidParam.WithMsg("通话双方不能为空")
	}
	if callerID == calleeID {
		return nil, errs.ErrInvalidParam.WithMsg("不能呼叫自己")
	}
	if mediaType != MediaAudio && mediaType != MediaVideo {
		return nil, errs.ErrInvalidParam.WithMsg("媒体类型不合法")
	}
	if s.room == nil {
		return nil, errs.ErrServiceBusy.WithMsg("音视频服务未就绪，请稍后再试")
	}
	if s.wallet == nil {
		return nil, errs.ErrServiceBusy.WithMsg("计费服务未就绪，请稍后再试")
	}

	// P8 合规：主叫必须已实名才能发起付费通话。
	// 这是陌生人社交的硬要求——付费聊天如果允许匿名，等于给违规内容开了口子。
	if s.cfg != nil && s.cfg.Compliance.Policy().RealnameRequiredForCall {
		verified, err := s.repo.RealnameVerified(ctx, callerID)
		if err != nil {
			return nil, errs.ErrInternal.WithErr(err)
		}
		if !verified {
			return nil, errs.ErrForbidden.WithMsg("请先完成实名认证再发起通话")
		}
	}

	status, err := s.repo.UserStatus(ctx, calleeID)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	if status == 0 {
		return nil, errs.ErrUserNotFound
	}
	if status != 1 {
		return nil, errs.ErrUserBanned
	}
	blocked, err := s.repo.BlockedEitherWay(ctx, callerID, calleeID)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	if blocked {
		return nil, errs.ErrBlocked
	}
	for _, uid := range []int64{callerID, calleeID} {
		active, err := s.repo.ActiveSession(ctx, uid)
		if err != nil {
			return nil, errs.ErrInternal.WithErr(err)
		}
		if active != nil {
			return nil, errs.ErrCallBusy
		}
	}

	price, err := s.repo.FindPrice(ctx, calleeID, mediaType)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	if price.PerMin <= 0 {
		return nil, errs.ErrServiceBusy.WithMsg("该通话类型暂未定价")
	}
	balance, err := s.wallet.Balance(ctx, callerID)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	need := price.PerMin
	if min := int64(s.policy().MinCoinsToCall); min > need {
		need = min
	}
	if balance < need {
		return nil, errs.ErrBalanceNotEnough.WithMsg("金币不足，至少需要 %d 金币才能发起通话", need)
	}

	now := s.now()
	callNo := NewCallNo(now, randomUint32())
	roomID := s.room.NewRoomID()
	st := &Session{
		CallNo:       callNo,
		CallerID:     callerID,
		CalleeID:     calleeID,
		MediaType:    mediaType,
		PriceMode:    price.Mode,
		PricePerMin:  int(price.PerMin),
		PlatformRate: price.PlatformRate,
		PayerID:      callerID,
		Status:       StatusInit,
		RoomID:       roomID,
	}
	if err := s.repo.InsertSession(ctx, st); err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	_ = s.repo.InsertEvent(ctx, st.ID, "create", StatusInit, StatusInit, callerID,
		payload(map[string]any{"media_type": mediaType, "price_per_min": price.PerMin}))

	ok, err := s.repo.UpdateState(ctx, st.ID, []int{StatusInit}, StatusRinging, nil)
	if err != nil || !ok {
		return nil, errs.ErrCallStateIllegal.WithMsg("通话状态已变更，请重试")
	}
	st.Status = StatusRinging
	_ = s.repo.InsertEvent(ctx, st.ID, "ring", StatusInit, StatusRinging, callerID, "")

	ticket, err := s.room.Issue(trtcUserID(callerID), roomID)
	if err != nil {
		// 建单成功但签票失败：立刻把单撤掉，否则会留一个永远进不去房间的振铃。
		_, _ = s.repo.UpdateState(ctx, st.ID, []int{StatusRinging}, StatusCanceled, nil)
		s.log.Error("签发进房凭据失败", zap.String("call_no", callNo), zap.Error(err))
		return nil, errs.ErrServiceBusy.WithMsg("音视频服务暂不可用，请稍后再试")
	}
	return &CreateResult{
		Session:              st,
		Ticket:               ticket,
		PricePerMin:          price.PerMin,
		HeartbeatIntervalSec: s.policy().HeartbeatIntervalSec,
		Balance:              balance,
	}, nil
}

// JoinResult 被叫接听的结果。
type JoinResult struct {
	Session     *Session    `json:"session"`
	Ticket      *RoomTicket `json:"ticket"`
	PricePerMin int64       `json:"price_per_min"`
	Balance     int64       `json:"balance"`
}

// Accept 被叫接听。
//
// 计费从接通这一刻开始：先扣第 1 分钟，扣不到就拒绝接通，
// 绝不进"先聊再算钱"的状态——那样会留下无法追回的欠费。
func (s *Service) Accept(ctx context.Context, uid int64, callNo string) (*JoinResult, error) {
	st, err := s.repo.FindByCallNo(ctx, callNo)
	if err != nil {
		return nil, errs.ErrCallNotFound
	}
	if st.CalleeID != uid {
		return nil, errs.ErrForbidden.WithMsg("只有被叫可以接听")
	}
	if st.Status != StatusRinging {
		return nil, errs.ErrCallStateIllegal.WithMsg("该通话已不在振铃中")
	}
	now := s.now()
	ok, err := s.repo.UpdateState(ctx, st.ID, []int{StatusRinging}, StatusTalking, map[string]any{
		"started_at":   now,
		"last_beat_at": now,
	})
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	if !ok {
		return nil, errs.ErrCallStateIllegal.WithMsg("该通话已被处理")
	}
	st.Status, st.StartedAt, st.LastBeatAt = StatusTalking, &now, &now
	_ = s.repo.InsertEvent(ctx, st.ID, "accept", StatusRinging, StatusTalking, uid, "")

	price := st.price()
	balance, err := s.wallet.Charge(ctx, st.PayerID, SegmentBizID(st.CallNo, 1), price.PerMin, "通话扣费 第1分钟")
	if err != nil {
		if errs.Is(err, errs.CodeBalanceNotEnough) {
			// 接通瞬间发现付不起：立刻结算挂断，不留"通话中但没扣到钱"的窗口。
			_ = s.finish(ctx, st, 0, now, StatusEnded, EndNoBalance, "settle")
			return nil, errs.ErrBalanceNotEnough
		}
		return nil, errs.ErrInternal.WithErr(err)
	}
	if _, err := s.repo.FreezeSegment(ctx, &Segment{
		CallSessionID: st.ID, SeqNo: 1, StartAt: now, Coins: price.PerMin, Status: SegmentFrozen,
	}); err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	st.ChargedMinutes = 1
	if _, err := s.repo.UpdateState(ctx, st.ID, []int{StatusTalking}, StatusTalking, map[string]any{
		"charged_minutes": 1,
	}); err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	_ = s.repo.InsertEvent(ctx, st.ID, "charge", StatusTalking, StatusTalking, 0,
		payload(map[string]any{"seq": 1, "coins": price.PerMin}))

	ticket, err := s.room.Issue(trtcUserID(st.CalleeID), st.RoomID)
	if err != nil {
		s.log.Error("被叫签票失败", zap.String("call_no", callNo), zap.Error(err))
		return nil, errs.ErrServiceBusy.WithMsg("音视频服务暂不可用，请稍后再试")
	}
	return &JoinResult{Session: st, Ticket: ticket, PricePerMin: price.PerMin, Balance: balance}, nil
}

// Reject 被叫拒接。
func (s *Service) Reject(ctx context.Context, uid int64, callNo string) (*Session, error) {
	st, err := s.repo.FindByCallNo(ctx, callNo)
	if err != nil {
		return nil, errs.ErrCallNotFound
	}
	if st.CalleeID != uid {
		return nil, errs.ErrForbidden.WithMsg("只有被叫可以拒接")
	}
	if err := s.finish(ctx, st, uid, s.now(), StatusRejected, EndReject, "reject"); err != nil {
		return nil, err
	}
	return s.repo.FindByID(ctx, st.ID)
}

// Cancel 主叫取消（振铃阶段）。
func (s *Service) Cancel(ctx context.Context, uid int64, callNo string) (*Session, error) {
	st, err := s.repo.FindByCallNo(ctx, callNo)
	if err != nil {
		return nil, errs.ErrCallNotFound
	}
	if st.CallerID != uid {
		return nil, errs.ErrForbidden.WithMsg("只有主叫可以取消")
	}
	if err := s.finish(ctx, st, uid, s.now(), StatusCanceled, EndNormal, "cancel"); err != nil {
		return nil, err
	}
	return s.repo.FindByID(ctx, st.ID)
}

// Hangup 挂断（通话中，任一方）。
func (s *Service) Hangup(ctx context.Context, uid int64, callNo string) (*Session, error) {
	st, err := s.repo.FindByCallNo(ctx, callNo)
	if err != nil {
		return nil, errs.ErrCallNotFound
	}
	if !st.IsParticipant(uid) {
		return nil, errs.ErrForbidden.WithMsg("不是该通话的参与方")
	}
	if st.Status == StatusRinging {
		// 振铃阶段的"挂断"语义取决于身份：主叫是取消，被叫是拒接。
		if st.CallerID == uid {
			return s.Cancel(ctx, uid, callNo)
		}
		return s.Reject(ctx, uid, callNo)
	}
	if st.Status != StatusTalking {
		return nil, errs.ErrCallStateIllegal.WithMsg("该通话已结束")
	}
	if err := s.finish(ctx, st, uid, s.now(), StatusEnded, EndNormal, "hangup"); err != nil {
		return nil, err
	}
	return s.repo.FindByID(ctx, st.ID)
}

// BeatResult 心跳响应。客户端据此决定是否该退出通话页。
type BeatResult struct {
	CallNo         string `json:"call_no"`
	Status         int    `json:"status"`
	Ended          bool   `json:"ended"`
	EndReason      int    `json:"end_reason"`
	ElapsedSec     int    `json:"elapsed_sec"`
	ChargedMinutes int    `json:"charged_minutes"`
	TotalCoins     int64  `json:"total_coins"`
	Balance        int64  `json:"balance"`
	ServerTime     int64  `json:"server_time"`
}

// Heartbeat 通话心跳。
//
// 心跳做三件事：刷新存活时间、按服务端时钟续扣分钟、把"余额耗尽"的结果回给客户端。
// 时长永远由服务端算：客户端上报的 elapsed 只写进事件流水用于排查，不参与计费。
func (s *Service) Heartbeat(ctx context.Context, uid int64, callNo string, clientElapsed int) (*BeatResult, error) {
	st, err := s.repo.FindByCallNo(ctx, callNo)
	if err != nil {
		return nil, errs.ErrCallNotFound
	}
	if !st.IsParticipant(uid) {
		return nil, errs.ErrForbidden.WithMsg("不是该通话的参与方")
	}
	if st.Status != StatusTalking {
		return &BeatResult{
			CallNo: st.CallNo, Status: st.Status, Ended: isFinal(st.Status),
			EndReason: st.EndReason, ChargedMinutes: st.ChargedMinutes,
			TotalCoins: st.TotalCoins, ServerTime: s.now().Unix(),
		}, nil
	}
	now := s.now()
	if _, err := s.repo.UpdateState(ctx, st.ID, []int{StatusTalking}, StatusTalking, map[string]any{
		"last_beat_at": now,
	}); err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	st.LastBeatAt = &now

	elapsed := 0
	if st.StartedAt != nil {
		elapsed = int(now.Sub(*st.StartedAt).Seconds())
	}
	_ = s.repo.InsertEvent(ctx, st.ID, "beat", StatusTalking, StatusTalking, uid,
		payload(map[string]any{"client_elapsed": clientElapsed, "server_elapsed": elapsed}))

	insufficient := false
	reason, ended := 0, false
	if max := s.policy().MaxDurationSec; max > 0 && elapsed >= max {
		// 达到单通上限：按正常挂断结算，避免用户被"无限计费"。
		if err := s.finish(ctx, st, 0, now, StatusEnded, EndMaxDuration, "max_duration"); err != nil {
			return nil, err
		}
		ended, reason = true, EndMaxDuration
	} else {
		insufficient, err = s.chargeDue(ctx, st, now)
		if err != nil {
			return nil, err
		}
		if insufficient {
			if err := s.finish(ctx, st, 0, now, StatusEnded, EndNoBalance, "no_balance"); err != nil {
				return nil, err
			}
			ended, reason = true, EndNoBalance
		}
	}

	fresh, err := s.repo.FindByID(ctx, st.ID)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	balance, _ := s.wallet.Balance(ctx, st.PayerID)
	return &BeatResult{
		CallNo:         fresh.CallNo,
		Status:         fresh.Status,
		Ended:          ended || isFinal(fresh.Status),
		EndReason:      reasonOr(fresh.EndReason, reason),
		ElapsedSec:     elapsed,
		ChargedMinutes: fresh.ChargedMinutes,
		TotalCoins:     fresh.TotalCoins,
		Balance:        balance,
		ServerTime:     now.Unix(),
	}, nil
}

// Detail 通话详情：会话 + 分钟切片 + 事件流水 + 账单一致性结论。
// 一致性结论直接服务于"账单金额与秒数是否一致"的验收，不必人工对数。
type Detail struct {
	Session     *Session     `json:"session"`
	Segments    []Segment    `json:"segments"`
	Events      []Event      `json:"events"`
	Consistency *Consistency `json:"consistency"`
}

// Consistency 账单自检。三件事必须同时成立，任一为 false 都说明计费有 bug 或有人改了库。
type Consistency struct {
	DurationSec     int   `json:"duration_sec"`
	BillableMinutes int   `json:"billable_minutes"`
	PricePerMin     int64 `json:"price_per_min"`
	TotalCoins      int64 `json:"total_coins"`
	SegmentSum      int64 `json:"segment_sum"`
	AmountMatches   bool  `json:"amount_matches"`    // 总额 == 计费分钟 × 单价
	MinutesMatchSec bool  `json:"minutes_match_sec"` // 计费分钟 == ceil(时长/60)
	SegmentMatches  bool  `json:"segment_matches"`   // 切片合计 == 总额
}

// Detail 查询单通详情。
func (s *Service) Detail(ctx context.Context, uid int64, callNo string) (*Detail, error) {
	st, err := s.repo.FindByCallNo(ctx, callNo)
	if err != nil {
		return nil, errs.ErrCallNotFound
	}
	if !st.IsParticipant(uid) {
		return nil, errs.ErrForbidden.WithMsg("不是该通话的参与方")
	}
	segs, err := s.repo.ListSegments(ctx, st.ID)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	events, err := s.repo.ListEvents(ctx, st.ID, 100)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	var segSum int64
	for _, sg := range segs {
		segSum += sg.Coins
	}
	perMin := st.price().PerMin
	cons := &Consistency{
		DurationSec:     st.DurationSec,
		BillableMinutes: st.BillableMinutes,
		PricePerMin:     perMin,
		TotalCoins:      st.TotalCoins,
		SegmentSum:      segSum,
		AmountMatches:   st.TotalCoins == int64(st.BillableMinutes)*perMin,
		MinutesMatchSec: st.BillableMinutes == BillableMinutes(st.DurationSec),
		SegmentMatches:  segSum == st.TotalCoins,
	}
	return &Detail{Session: st, Segments: segs, Events: events, Consistency: cons}, nil
}

// List 通话记录。
func (s *Service) List(ctx context.Context, uid int64, page, size int) ([]Session, int64, error) {
	return s.repo.ListSessions(ctx, uid, page, size)
}

// Price 查询与某用户的通话定价（发起前展示，避免"接通才发现贵"）。
func (s *Service) Price(ctx context.Context, calleeID int64, mediaType int) (*Price, error) {
	if mediaType != MediaAudio && mediaType != MediaVideo {
		mediaType = MediaAudio
	}
	return s.repo.FindPrice(ctx, calleeID, mediaType)
}

// SweepReport 巡检结果。
type SweepReport struct {
	RingTimeout int `json:"ring_timeout"`
	BeatTimeout int `json:"beat_timeout"`
	NoBalance   int `json:"no_balance"`
	Charged     int `json:"charged"`
	Scanned     int `json:"scanned"`
}

// Sweep 后台巡检：处理"没人来触发"的异常通话。
//
// 三类必须靠服务端兜底，因为客户端可能已经闪退、断网或被强杀：
//  1. 振铃超时未接；
//  2. 通话中心跳超时（掉线）；
//  3. 通话中余额耗尽（客户端不发心跳也要能自动挂断）。
func (s *Service) Sweep(ctx context.Context, limit int) (*SweepReport, error) {
	if limit <= 0 {
		limit = 100
	}
	now := s.now()
	pol := s.policy()
	rep := &SweepReport{}

	rings, err := s.repo.ListRingTimeout(ctx, now.Add(-time.Duration(pol.RingTimeoutSec)*time.Second), limit)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	for _, st := range rings {
		if err := s.finish(ctx, &st, 0, now, StatusTimeout, EndNoAnswer, "ring_timeout"); err != nil {
			s.log.Error("振铃超时处理失败", zap.String("call_no", st.CallNo), zap.Error(err))
			continue
		}
		rep.RingTimeout++
	}

	beats, err := s.repo.ListBeatTimeout(ctx, now.Add(-time.Duration(pol.HeartbeatTimeoutSec)*time.Second), limit)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	for _, st := range beats {
		if err := s.finish(ctx, &st, 0, now, StatusEnded, EndHeartbeat, "beat_timeout"); err != nil {
			s.log.Error("心跳超时处理失败", zap.String("call_no", st.CallNo), zap.Error(err))
			continue
		}
		rep.BeatTimeout++
	}

	talking, err := s.repo.ListTalking(ctx, limit)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	rep.Scanned = len(talking)
	for _, st := range talking {
		item := st
		if max := pol.MaxDurationSec; max > 0 && item.StartedAt != nil &&
			int(now.Sub(*item.StartedAt).Seconds()) >= max {
			if err := s.finish(ctx, &item, 0, now, StatusEnded, EndMaxDuration, "max_duration"); err != nil {
				s.log.Error("超时强挂失败", zap.String("call_no", item.CallNo), zap.Error(err))
			}
			continue
		}
		insufficient, err := s.chargeDue(ctx, &item, now)
		if err != nil {
			s.log.Error("巡检续扣失败", zap.String("call_no", item.CallNo), zap.Error(err))
			continue
		}
		if insufficient {
			if err := s.finish(ctx, &item, 0, now, StatusEnded, EndNoBalance, "no_balance"); err != nil {
				s.log.Error("余额耗尽挂断失败", zap.String("call_no", item.CallNo), zap.Error(err))
				continue
			}
			rep.NoBalance++
		}
	}
	return rep, nil
}

// chargeDue 续扣到"当前时刻应扣的分钟数"。
// 返回 insufficient=true 表示余额已经付不起下一分钟，由调用方决定挂断。
func (s *Service) chargeDue(ctx context.Context, st *Session, now time.Time) (bool, error) {
	if st.StartedAt == nil || st.Status != StatusTalking {
		return false, nil
	}
	price := st.price()
	elapsed := int(now.Sub(*st.StartedAt).Seconds())
	if elapsed < 0 {
		elapsed = 0
	}
	due := BillableMinutes(elapsed)
	for st.ChargedMinutes < due {
		seq := st.ChargedMinutes + 1
		_, err := s.wallet.Charge(ctx, st.PayerID, SegmentBizID(st.CallNo, seq), price.PerMin,
			fmt.Sprintf("通话扣费 第%d分钟", seq))
		if err != nil {
			if errs.Is(err, errs.CodeBalanceNotEnough) {
				s.log.Info("余额不足，停止续扣",
					zap.String("call_no", st.CallNo), zap.Int64("payer", st.PayerID), zap.Int("seq", seq))
				return true, nil
			}
			return false, errs.ErrInternal.WithErr(err)
		}
		if _, err := s.repo.FreezeSegment(ctx, &Segment{
			CallSessionID: st.ID,
			SeqNo:         seq,
			StartAt:       st.StartedAt.Add(time.Duration(seq-1) * time.Minute),
			Coins:         price.PerMin,
			Status:        SegmentFrozen,
		}); err != nil {
			return false, errs.ErrInternal.WithErr(err)
		}
		st.ChargedMinutes = seq
		if _, err := s.repo.UpdateState(ctx, st.ID, []int{StatusTalking}, StatusTalking, map[string]any{
			"charged_minutes": seq,
		}); err != nil {
			return false, errs.ErrInternal.WithErr(err)
		}
		_ = s.repo.InsertEvent(ctx, st.ID, "charge", StatusTalking, StatusTalking, 0,
			payload(map[string]any{"seq": seq, "coins": price.PerMin}))
	}
	return false, nil
}

// finish 结束通话：补扣最后一分钟 → 结算账单 → 切片归档 → 落事件。
//
// 关键不变量：billable_minutes == ceil(duration_sec / 60) 且 total_coins == billable_minutes × 单价。
// 余额不足时把结束时间回拨到"已付费的最后一分钟末尾"，
// 这样时长与账单永远自洽，不会出现"聊了 95 秒只算了 1 分钟"的对不上账。
func (s *Service) finish(ctx context.Context, st *Session, operator int64, now time.Time, status, reason int, event string) error {
	price := st.price()
	from := st.Status
	duration := 0
	endedAt := now

	if st.Status == StatusTalking && st.StartedAt != nil {
		insufficient, err := s.chargeDue(ctx, st, now)
		if err != nil {
			return err
		}
		if insufficient {
			reason = EndNoBalance
		}
		duration = int(now.Sub(*st.StartedAt).Seconds())
		if duration < 0 {
			duration = 0
		}
		if max := s.policy().MaxDurationSec; max > 0 && duration > max {
			duration = max
			endedAt = st.StartedAt.Add(time.Duration(max) * time.Second)
			if status == StatusEnded {
				reason = EndMaxDuration
			}
		}
		// 只结算"已付费"的时长：不足一分钟的部分按一分钟算，没付费的部分不计入。
		if paid := st.ChargedMinutes * 60; insufficient && duration > paid {
			duration = paid
			endedAt = st.StartedAt.Add(time.Duration(paid) * time.Second)
		}
		// 接通并计费过的通话，时长至少记 1 秒。
		// 否则"接通后 0.3 秒就挂断"会记成 duration=0 却扣了 1 分钟的钱，
		// 账单看起来像凭空扣费，也破坏了「分钟 = ceil(秒数/60)」的对账不变量。
		if st.ChargedMinutes > 0 && duration < 1 {
			duration = 1
			endedAt = st.StartedAt.Add(time.Second)
		}
		if err := s.repo.SettleSegments(ctx, st.ID, *st.StartedAt, endedAt, price.PerMin); err != nil {
			return errs.ErrInternal.WithErr(err)
		}
	}

	minutes := st.ChargedMinutes
	if st.Status == StatusTalking && minutes < BillableMinutes(duration) {
		minutes = BillableMinutes(duration)
	}
	total := int64(minutes) * price.PerMin
	income, fee := price.Split(total)

	if income > 0 && st.Status == StatusTalking {
		if err := s.wallet.Income(ctx, st.CalleeID, st.CallNo, income,
			fmt.Sprintf("通话收益 %s", st.CallNo)); err != nil {
			// 收益入账失败不能吞掉：宁可让本次结束报错重试，也不能让用户白聊。
			return errs.ErrInternal.WithErr(err)
		}
	} else {
		income, fee = 0, 0
		total = 0
		minutes = 0
	}

	updates := map[string]any{
		"ended_at":         endedAt,
		"duration_sec":     duration,
		"billable_minutes": minutes,
		"total_coins":      total,
		"payee_income":     income,
		"platform_fee":     fee,
		"end_reason":       reason,
	}
	ok, err := s.repo.UpdateState(ctx, st.ID, []int{StatusRinging, StatusTalking, StatusInit}, status, updates)
	if err != nil {
		return errs.ErrInternal.WithErr(err)
	}
	if !ok {
		// 已被别人结束：这不是错误，幂等返回即可（双端同时挂断很常见）。
		return nil
	}
	st.Status, st.EndedAt = status, &endedAt
	_ = s.repo.InsertEvent(ctx, st.ID, event, from, status, operator, payload(map[string]any{
		"reason": reason, "duration": duration, "minutes": minutes, "total": total,
	}))
	s.log.Info("通话结束", zap.String("call_no", st.CallNo), zap.Int("status", status),
		zap.Int("reason", reason), zap.Int("minutes", minutes), zap.Int64("total", total))
	return nil
}

// price 从会话快照还原定价。
// 用快照而不是实时查表：通话中途运营改价，不该让正在通话的用户账单突变。
func (s *Session) price() Price {
	if s == nil {
		return Price{}
	}
	return Price{PerMin: int64(s.PricePerMin), PlatformRate: s.PlatformRate, Mode: s.PriceMode}
}

// trtcUserID TRTC/IM 的用户标识，与 IM 导入账号保持同一规则 u_{uid}。
func trtcUserID(uid int64) string { return fmt.Sprintf("u_%d", uid) }

func payload(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

func reasonOr(current, fallback int) int {
	if current != 0 {
		return current
	}
	return fallback
}

var _ = errors.New
