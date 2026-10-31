// Package page 提供列表接口的通用分页参数与返回结构。
package page

// 默认与上限，防止客户端传入 size=100000 打挂数据库。
const (
	DefaultSize = 20
	MaxSize     = 100
)

// Query 是所有列表接口共用的分页入参。
type Query struct {
	Page int `form:"page" json:"page"`
	Size int `form:"size" json:"size"`
}

// Normalize 修正非法值，必须在使用前调用。
func (q *Query) Normalize() {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.Size < 1 {
		q.Size = DefaultSize
	}
	if q.Size > MaxSize {
		q.Size = MaxSize
	}
}

// Offset 计算 SQL offset。
func (q Query) Offset() int {
	return (q.Page - 1) * q.Size
}

// Limit 返回 SQL limit。
func (q Query) Limit() int {
	if q.Size < 1 {
		return DefaultSize
	}
	return q.Size
}

// Result 列表响应体。
type Result struct {
	List    any   `json:"list"`
	Total   int64 `json:"total"`
	Page    int   `json:"page"`
	Size    int   `json:"size"`
	HasMore bool  `json:"has_more"`
}

// NewResult 组装列表响应。list 为空时应传空切片而非 nil，避免客户端拿到 null。
func NewResult(list any, total int64, q Query) Result {
	return Result{
		List:    list,
		Total:   total,
		Page:    q.Page,
		Size:    q.Size,
		HasMore: int64(q.Offset()+q.Limit()) < total,
	}
}
