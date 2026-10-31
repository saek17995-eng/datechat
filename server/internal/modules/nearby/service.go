package nearby

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/datechat/server/internal/config"
	"github.com/datechat/server/internal/pkg/errs"
	"github.com/datechat/server/internal/pkg/presence"
)

// Deps 构造依赖。
type Deps struct {
	Config *config.Config
	Log    *zap.Logger
	Repo   *Repository
	Rdb    *redis.Client
	Now    func() time.Time
}

// Service 附近的人与在线服务。
type Service struct {
	cfg  *config.Config
	log  *zap.Logger
	repo *Repository
	rdb  *redis.Client
	now  func() time.Time
}

func NewService(d Deps) *Service {
	if d.Log == nil {
		d.Log = zap.NewNop()
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{cfg: d.Config, log: d.Log, repo: d.Repo, rdb: d.Rdb, now: d.Now}
}

func (s *Service) policy() config.NearbyPolicy {
	if s.cfg == nil {
		return config.NearbyConfig{}.Policy()
	}
	return s.cfg.Nearby.Policy()
}

// 在线键的写入方是资料模块的心跳接口，读取方在这里。
// 共用 presence.Key 保证两边不会各写一套前缀（写错了现场表现是"所有人永远离线"）。
func (s *Service) MarkOnline(ctx context.Context, uid int64) error {
	if s.rdb == nil {
		return nil
	}
	return s.rdb.Set(ctx, presence.Key(uid), s.now().Unix(),
		time.Duration(s.policy().OnlineTTLSec)*time.Second).Err()
}

// UpdateLocation 上报位置。
//
// 校验放在最前面：脏坐标（0,0 或 999,999）一旦进 GEO 索引，
// 会让附近列表整体飘位，而且很难从数据上反推是谁干的。
func (s *Service) UpdateLocation(ctx context.Context, uid int64, lng, lat float64) (*Location, error) {
	if !ValidateCoord(lng, lat) {
		return nil, errs.ErrInvalidParam.WithMsg("经纬度不合法")
	}
	now := s.now()
	pol := s.policy()

	// GEO 每次都更新：它是检索的唯一依据，且写入成本极低。
	if err := s.repo.SaveLocation(ctx, uid, lng, lat, now); err != nil {
		s.log.Error("保存位置失败", zap.Int64("uid", uid), zap.Error(err))
		return nil, errs.ErrInternal.WithErr(err)
	}

	// MySQL 写按最小间隔节流：位置是高频上报，没必要每次都落主库。
	throttled := false
	if s.rdb != nil && pol.MinReportSec > 0 {
		key := fmt.Sprintf("nearby:report:%d", uid)
		ok, err := s.rdb.SetNX(ctx, key, "1", time.Duration(pol.MinReportSec)*time.Second).Result()
		if err == nil && !ok {
			throttled = true
		}
	}
	return &Location{UID: uid, Lng: lng, Lat: lat, Throttled: throttled, ReportedAt: now}, nil
}

// Online 批量查询在线状态。
// 用 EXISTS 管道而不是逐个 GET：列表一次 20+ 人，逐个往返会把接口拖到几百毫秒。
func (s *Service) Online(ctx context.Context, ids []int64) (map[int64]bool, error) {
	out := make(map[int64]bool, len(ids))
	if s.rdb == nil || len(ids) == 0 {
		return out, nil
	}
	pipe := s.rdb.Pipeline()
	cmds := make([]*redis.IntCmd, len(ids))
	for i, id := range ids {
		cmds[i] = pipe.Exists(ctx, presence.Key(id))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	for i, id := range ids {
		out[id] = cmds[i].Val() > 0
	}
	return out, nil
}

// Nearby 附近的人。
//
// 流程：Redis GEO 取候选 → 去掉自己与拉黑关系 → 批量取资料 → 性别/年龄筛选 →
// 距离模糊 + 在线状态 → 内存分页。
// 之所以在内存里筛选分页：Redis GEO 只能"取最近 N 个"，不支持 WHERE，
// 而性别/年龄这些条件每页都会变，交给 SQL 又会把距离计算带回数据库。
func (s *Service) Nearby(ctx context.Context, uid int64, f Filter) ([]Item, int64, error) {
	pol := s.policy()
	if !ValidateCoord(f.Lng, f.Lat) {
		return nil, 0, errs.ErrInvalidParam.WithMsg("经纬度不合法")
	}
	if f.RadiusKM <= 0 {
		f.RadiusKM = float64(pol.DefaultRadiusKM)
	}
	if f.RadiusKM > float64(pol.MaxRadiusKM) {
		f.RadiusKM = float64(pol.MaxRadiusKM)
	}
	if f.Page < 1 {
		f.Page = 1
	}
	if f.Size <= 0 || f.Size > 100 {
		f.Size = pol.PageSize
	}

	// 多取一些候选：要被筛掉的可能是拉黑、性别、年龄，取满一页需要冗余。
	want := f.Page * f.Size * 3
	if want < 100 {
		want = 100
	}
	hits, err := s.repo.SearchGeo(ctx, f.Lng, f.Lat, f.RadiusKM, want)
	if err != nil {
		s.log.Warn("GEO 检索失败，回退 MySQL 兜底", zap.Error(err))
		return nil, 0, errs.ErrInternal.WithMsg("附近的人暂不可用，请稍后再试")
	}

	// 去掉自己，按距离排序后收集候选 ID。
	ids := make([]int64, 0, len(hits))
	dist := make(map[int64]float64, len(hits))
	for _, h := range hits {
		if h.UID == uid {
			continue
		}
		if _, ok := dist[h.UID]; ok {
			continue
		}
		ids = append(ids, h.UID)
		dist[h.UID] = h.DistKM
	}
	if len(ids) == 0 {
		return []Item{}, 0, nil
	}

	blocked, err := s.repo.BlockedPairs(ctx, uid, ids)
	if err != nil {
		s.log.Warn("查询拉黑关系失败", zap.Error(err))
	}
	rows, err := s.repo.ListProfiles(ctx, ids)
	if err != nil {
		return nil, 0, errs.ErrInternal.WithErr(err)
	}

	now := s.now()
	items := make([]Item, 0, len(rows))
	keep := make([]int64, 0, len(rows))
	for _, r := range rows {
		if r.Status != 1 || blocked[r.UID] {
			continue
		}
		age := ageOf(r.Birthday, now)
		if f.Gender != 0 && r.Gender != f.Gender {
			continue
		}
		if f.AgeMin > 0 && age < f.AgeMin {
			continue
		}
		if f.AgeMax > 0 && age > f.AgeMax {
			continue
		}
		keep = append(keep, r.UID)
		item := Item{
			UID:          r.UID,
			Nickname:     r.Nickname,
			Avatar:       r.Avatar,
			Gender:       r.Gender,
			Age:          age,
			CityName:     r.CityName,
			DistanceText: FuzzyDistance(dist[r.UID], pol.DistanceFuzzy),
			PriceAudio:   r.PriceAudio,
			PriceVideo:   r.PriceVideo,
			Realname:     r.Realname == 2,
		}
		if !pol.DistanceFuzzy {
			item.DistanceKM = dist[r.UID]
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return dist[items[i].UID] < dist[items[j].UID] })

	online, err := s.Online(ctx, keep)
	if err != nil {
		s.log.Warn("查询在线状态失败", zap.Error(err))
	}
	for i := range items {
		items[i].Online = online[items[i].UID]
		if !items[i].Online {
			items[i].LastActiveText = "离线"
		} else {
			items[i].LastActiveText = "在线"
		}
	}

	total := int64(len(items))
	start := (f.Page - 1) * f.Size
	if start >= len(items) {
		return []Item{}, total, nil
	}
	end := start + f.Size
	if end > len(items) {
		end = len(items)
	}
	return items[start:end], total, nil
}

// Visits 谁看过我：列表 + 未读数。
func (s *Service) Visits(ctx context.Context, uid int64, page, size int) ([]VisitItem, int64, int64, error) {
	list, total, err := s.repo.ListVisits(ctx, uid, page, size)
	if err != nil {
		return nil, 0, 0, errs.ErrInternal.WithErr(err)
	}
	unread, err := s.repo.CountUnreadVisits(ctx, uid)
	if err != nil {
		s.log.Warn("统计未读访客失败", zap.Error(err))
	}
	ids := make([]int64, 0, len(list))
	for _, v := range list {
		ids = append(ids, v.VisitorID)
	}
	online, err := s.Online(ctx, ids)
	if err != nil {
		s.log.Warn("查询访客在线状态失败", zap.Error(err))
	}
	now := s.now()
	for i := range list {
		list[i].Unread = list[i].ReadAt == nil
		list[i].Online = online[list[i].VisitorID]
		list[i].Age = ageOf(list[i].Birthday, now)
	}
	return list, total, unread, nil
}

// MarkVisitsRead 标记全部已读。
func (s *Service) MarkVisitsRead(ctx context.Context, uid int64) (int64, error) {
	n, err := s.repo.MarkVisitsRead(ctx, uid, s.now())
	if err != nil {
		return 0, errs.ErrInternal.WithErr(err)
	}
	return n, nil
}

// ageOf 由生日算年龄；没有生日返回 0（列表里显示为未知）。
func ageOf(birthday *time.Time, now time.Time) int {
	if birthday == nil {
		return 0
	}
	age := now.Year() - birthday.Year()
	if now.YearDay() < birthday.YearDay() {
		age--
	}
	if age < 0 {
		return 0
	}
	return age
}
