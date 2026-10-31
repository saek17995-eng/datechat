package bootstrap

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/datechat/server/internal/config"
)

// zapWriter 把 GORM 的日志转给 zap，避免出现两套日志格式。
type zapWriter struct{ log *zap.Logger }

func (w zapWriter) Printf(format string, args ...any) {
	w.log.Sugar().Infof(format, args...)
}

// NewMySQL 建立连接池并等待可用。
func NewMySQL(cfg config.MySQLConfig, log *zap.Logger) (*gorm.DB, error) {
	var db *gorm.DB

	err := waitFor("mysql", 15, 2*time.Second, log, func() error {
		gdb, err := gorm.Open(mysql.Open(cfg.DSN()), &gorm.Config{
			Logger: gormlogger.New(zapWriter{log: log}, gormlogger.Config{
				SlowThreshold:             cfg.SlowThreshold.D(),
				LogLevel:                  gormlogger.Warn,
				IgnoreRecordNotFoundError: true,
				Colorful:                  false,
			}),
			// 单条写入由业务自己控制事务边界，关闭 GORM 的默认事务可减少一次 BEGIN/COMMIT。
			SkipDefaultTransaction: true,
			NowFunc:                func() time.Time { return time.Now().Local() },
		})
		if err != nil {
			return err
		}

		sqlDB, err := gdb.DB()
		if err != nil {
			return err
		}
		sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
		sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
		sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime.D())
		sqlDB.SetConnMaxIdleTime(cfg.ConnMaxIdleTime.D())

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := sqlDB.PingContext(ctx); err != nil {
			return err
		}
		db = gdb
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("初始化 MySQL 失败: %w", err)
	}

	log.Info("MySQL 已连接", zap.String("dsn", cfg.MaskedDSN()))
	return db, nil
}
