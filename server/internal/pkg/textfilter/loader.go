package textfilter

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// sensitiveWordRow 映射 sensitive_word 表的最小字段集。
// 只取需要的列：过滤器不关心 id 与时间，少传字段能减少内存与网络开销。
type sensitiveWordRow struct {
	Word     string `gorm:"column:word"`
	Category string `gorm:"column:category"`
	Level    int    `gorm:"column:level"`
}

// LoadFromDB 从数据库加载启用状态的敏感词，返回可直接使用的过滤器。
//
// 启动期调用一次；词库变更后调用 Refresh 热更新。
// 加载失败必须让服务起不来吗？不：敏感词只是内容安全的一层，
// 缺失时降级为空词库（等价于"不过滤"）比整体不可用更糟，所以只返回错误交给调用方决定。
func LoadFromDB(ctx context.Context, db *gorm.DB) (*Filter, error) {
	rows, err := queryWords(ctx, db)
	if err != nil {
		return nil, err
	}
	f := New()
	f.Load(rows)
	return f, nil
}

// Refresh 重新加载词库。运营后台改词后调用，整个过程只在一把写锁内完成。
func Refresh(ctx context.Context, db *gorm.DB, f *Filter) error {
	rows, err := queryWords(ctx, db)
	if err != nil {
		return err
	}
	f.Load(rows)
	return nil
}

// NewRefreshLoop 启动后台定时刷新。
// 运营同学在后台加了新词却要等发版才生效，是最容易被投诉的问题，所以做成定时拉取。
// 返回的 stop 函数用于优雅退出。
func NewRefreshLoop(ctx context.Context, db *gorm.DB, f *Filter, interval time.Duration, logf func(string, ...any)) (stop func()) {
	if db == nil || f == nil || interval <= 0 {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				if err := Refresh(ctx, db, f); err != nil && logf != nil {
					logf("刷新敏感词库失败: %v", err)
				}
			}
		}
	}()
	return func() { close(done) }
}

func queryWords(ctx context.Context, db *gorm.DB) ([]Word, error) {
	if db == nil {
		return nil, fmt.Errorf("textfilter: 数据库未初始化")
	}
	var rows []sensitiveWordRow
	if err := db.WithContext(ctx).
		Table("sensitive_word").
		Select("word", "category", "level").
		Where("status = ?", 1).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("textfilter: 加载敏感词失败: %w", err)
	}
	words := make([]Word, 0, len(rows))
	for _, r := range rows {
		words = append(words, Word{Text: r.Word, Category: r.Category, Level: r.Level})
	}
	return words, nil
}
