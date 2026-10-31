// Package nearby 提供附近的人、在线状态与"谁看过我"。
//
// 三条设计原则：
//
//  1. 位置检索走 Redis GEO，不走 MySQL：附近的人是一个"每秒都在变"的查询，
//     用 SQL 算距离会直接把数据库压垮；MySQL 只存最后上报的坐标，用于兜底与审计。
//  2. 对外距离永远模糊：精确距离等于把"你家在哪"告诉陌生人，
//     所以排序用真实距离，展示只给区间。
//  3. 在线状态是"可丢失"的数据：只写 Redis（TTL），绝不写主库。
package nearby

import (
	"context"
	"math"
	"time"
)

// 距离模糊区间（公里）。上界 inclusive。
var distanceBuckets = []struct {
	max  float64
	text string
}{
	{1, "1km 以内"},
	{3, "3km 以内"},
	{10, "10km 以内"},
	{30, "30km 以内"},
	{100, "100km 以内"},
}

// FuzzyDistance 把真实距离映射成对外展示的区间文本。
// 关闭模糊（distance_fuzzy=false）时返回一位小数的精确值，仅用于内网联调。
func FuzzyDistance(km float64, enabled bool) string {
	if !enabled {
		return formatKM(km)
	}
	for _, b := range distanceBuckets {
		if km <= b.max {
			return b.text
		}
	}
	return "100km 以外"
}

// formatKM 保留一位小数。附近的人不需要米级精度，少一位也少暴露一点位置信息。
func formatKM(km float64) string {
	if km < 0 {
		km = 0
	}
	scaled := int(math.Round(km * 10))
	return itoa(scaled/10) + "." + itoa(scaled%10) + "km"
}

func itoa(v int) string {
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

// Item 附近的人列表项。
type Item struct {
	UID            int64   `json:"uid"`
	Nickname       string  `json:"nickname"`
	Avatar         string  `json:"avatar"`
	Gender         int     `json:"gender"`
	Age            int     `json:"age"`
	CityName       string  `json:"city_name"`
	DistanceText   string  `json:"distance_text"`
	DistanceKM     float64 `json:"distance_km,omitempty"` // 仅关闭模糊时下发，联调用
	Online         bool    `json:"online"`
	LastActiveText string  `json:"last_active_text"`
	PriceAudio     int     `json:"price_audio"`
	PriceVideo     int     `json:"price_video"`
	Realname       bool    `json:"realname"`
}

// Filter 附近的人筛选条件。
type Filter struct {
	Lng      float64
	Lat      float64
	RadiusKM float64
	Gender   int // 0 不限 1 男 2 女
	AgeMin   int
	AgeMax   int
	Page     int
	Size     int
}

// VisitItem 谁看过我。
// Birthday / ReadAt 不对外下发：年龄与已读状态在 service 里换算成 age / unread，
// 避免把用户的精确生日和"我什么时候看的"泄露出去。
type VisitItem struct {
	VisitorID int64      `json:"visitor_id"`
	Nickname  string     `json:"nickname"`
	Avatar    string     `json:"avatar"`
	Gender    int        `json:"gender"`
	Age       int        `json:"age"`
	CityName  string     `json:"city_name"`
	VisitedAt time.Time  `json:"visited_at"`
	Unread    bool       `json:"unread"`
	Online    bool       `json:"online"`
	Birthday  *time.Time `json:"-"`
	ReadAt    *time.Time `json:"-"`
}

// Location 位置上报结果。
type Location struct {
	UID        int64     `json:"uid"`
	Lng        float64   `json:"lng"`
	Lat        float64   `json:"lat"`
	Throttled  bool      `json:"throttled"` // 命中最小上报间隔，本次未落库
	ReportedAt time.Time `json:"reported_at"`
}

// Presence 在线状态查询器。由 service 实现，便于测试替换。
type Presence interface {
	Online(ctx context.Context, ids []int64) (map[int64]bool, error)
}

// ValidateCoord 校验经纬度是否合法。
// 客户端传什么都敢信的话，GEOADD 会把 (999,999) 这种脏数据写进索引，
// 之后每个用户的附近列表都会飘。
func ValidateCoord(lng, lat float64) bool {
	return lng >= -180 && lng <= 180 && lat >= -90 && lat <= 90 && (lng != 0 || lat != 0)
}
