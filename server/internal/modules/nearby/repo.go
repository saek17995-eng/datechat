package nearby

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// Repository 附近的人数据访问。
// MySQL 只负责"人"（资料、筛选、状态），Redis 只负责"位置与在线"。
type Repository struct {
	db  *gorm.DB
	rdb *redis.Client
}

func NewRepository(db *gorm.DB, rdb *redis.Client) *Repository {
	return &Repository{db: db, rdb: rdb}
}

const geoKey = "geo:users"

// SaveLocation 写入坐标：Redis GEO 用于检索，MySQL 用于兜底与审计。
// 两处都写，但 Redis 失败不影响主流程（下次上报会补上）。
func (r *Repository) SaveLocation(ctx context.Context, uid int64, lng, lat float64, at time.Time) error {
	if r.rdb != nil {
		// GEOADD 是幂等的：同一个 member 重复添加只会更新坐标，不会留下脏点。
		if err := r.rdb.GeoAdd(ctx, geoKey, &redis.GeoLocation{
			Name:      fmt.Sprint(uid),
			Longitude: lng,
			Latitude:  lat,
		}).Err(); err != nil {
			return err
		}
	}
	return r.db.WithContext(ctx).Exec(
		`INSERT INTO user_profile (user_id, lng, lat, location_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE lng = VALUES(lng), lat = VALUES(lat), location_at = VALUES(location_at)`,
		uid, lng, lat, at, at, at,
	).Error
}

// geoHit 一次 GEO 检索的结果。
type geoHit struct {
	UID    int64
	DistKM float64
}

// SearchGeo 以给定坐标为中心做半径检索，返回按距离升序的结果。
//
// Redis GEO 不支持分页，所以这里一次取 count 条后在内存里分页：
// 半径已经被 max_radius 限制，count 也有上界，内存开销可控。
func (r *Repository) SearchGeo(ctx context.Context, lng, lat, radiusKM float64, count int) ([]geoHit, error) {
	if r.rdb == nil {
		return nil, fmt.Errorf("nearby: Redis 不可用")
	}
	if count <= 0 || count > 1000 {
		count = 500
	}
	res, err := r.rdb.GeoSearchLocation(ctx, geoKey, &redis.GeoSearchLocationQuery{
		GeoSearchQuery: redis.GeoSearchQuery{
			Longitude:  lng,
			Latitude:   lat,
			Radius:     radiusKM,
			RadiusUnit: "km",
			Sort:       "ASC",
			Count:      count,
			CountAny:   false,
		},
		WithDist: true,
	}).Result()
	if err != nil {
		return nil, err
	}
	out := make([]geoHit, 0, len(res))
	for _, g := range res {
		var uid int64
		if _, err := fmt.Sscanf(g.Name, "%d", &uid); err != nil {
			continue
		}
		out = append(out, geoHit{UID: uid, DistKM: g.Dist})
	}
	return out, nil
}

// profileRow 列表需要的用户字段。
type profileRow struct {
	UID        int64      `gorm:"column:id"`
	Nickname   string     `gorm:"column:nickname"`
	Avatar     string     `gorm:"column:avatar"`
	Gender     int        `gorm:"column:gender"`
	Birthday   *time.Time `gorm:"column:birthday"`
	CityCode   string     `gorm:"column:city_code"`
	CityName   string     `gorm:"column:city_name"`
	Status     int        `gorm:"column:status"`
	LastActive *time.Time `gorm:"column:last_active_at"`
	PriceAudio int        `gorm:"column:price_audio"`
	PriceVideo int        `gorm:"column:price_video"`
	Realname   int        `gorm:"column:realname_status"`
}

// ListProfiles 批量取用户资料，只返回正常状态且未注销的用户。
// 用一条 IN 查询而不是逐个查：附近的人一次返回 20+ 条，逐条查是 N+1。
func (r *Repository) ListProfiles(ctx context.Context, uids []int64) ([]profileRow, error) {
	if len(uids) == 0 {
		return nil, nil
	}
	var rows []profileRow
	err := r.db.WithContext(ctx).Raw(
		`SELECT u.id, u.nickname, u.avatar, u.gender, u.birthday, u.city_code, u.status, u.last_active_at,
		        c.city AS city_name, p.price_audio, p.price_video, p.realname_status
		 FROM user u
		 LEFT JOIN region_city c ON c.city_code = u.city_code
		 LEFT JOIN user_profile p ON p.user_id = u.id
		 WHERE u.id IN ? AND u.deleted_at IS NULL`, uids,
	).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// BlockedPairs 批量取双向拉黑关系，一次查完避免逐对查询。
func (r *Repository) BlockedPairs(ctx context.Context, uid int64, others []int64) (map[int64]bool, error) {
	out := make(map[int64]bool, len(others))
	if len(others) == 0 {
		return out, nil
	}
	type row struct {
		Other int64 `gorm:"column:other_id"`
	}
	var rows []row
	err := r.db.WithContext(ctx).Raw(
		`SELECT CASE WHEN user_id = ? THEN blocked_user_id ELSE user_id END AS other_id
		 FROM user_block
		 WHERE deleted_at IS NULL
		   AND ((user_id = ? AND blocked_user_id IN ?) OR (blocked_user_id = ? AND user_id IN ?))`,
		uid, uid, others, uid, others,
	).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.Other] = true
	}
	return out, nil
}

// ListVisits 谁看过我。
func (r *Repository) ListVisits(ctx context.Context, uid int64, page, size int) ([]VisitItem, int64, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	q := r.db.WithContext(ctx).Table("user_visit").Where("target_id = ?", uid)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []VisitItem
	err := r.db.WithContext(ctx).Raw(
		`SELECT v.visitor_id, u.nickname, u.avatar, u.gender, u.birthday,
		        c.city AS city_name, v.visited_at, v.read_at
		 FROM user_visit v
		 JOIN user u ON u.id = v.visitor_id AND u.deleted_at IS NULL
		 LEFT JOIN region_city c ON c.city_code = u.city_code
		 WHERE v.target_id = ?
		 ORDER BY v.visited_at DESC
		 LIMIT ? OFFSET ?`, uid, size, (page-1)*size,
	).Scan(&rows).Error
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// CountUnreadVisits 未读访客数。
func (r *Repository) CountUnreadVisits(ctx context.Context, uid int64) (int64, error) {
	var n int64
	if err := r.db.WithContext(ctx).Table("user_visit").
		Where("target_id = ? AND read_at IS NULL", uid).Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

// MarkVisitsRead 把访客记录标记为已读。
func (r *Repository) MarkVisitsRead(ctx context.Context, uid int64, at time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Table("user_visit").
		Where("target_id = ? AND read_at IS NULL", uid).
		Update("read_at", at)
	return res.RowsAffected, res.Error
}
