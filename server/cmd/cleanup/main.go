// Command cleanup 按合规留存期清理过期数据。
//
// 为什么要有这个命令而不是写个 SQL 定时跑：
//  1. 默认 dry-run，必须先看清会删多少再真删——删库是一瞬间的事，恢复不是；
//  2. 每张表的留存期不一样（短信日志 30 天、操作审计 2 年），写在一起才对得上口径；
//  3. 分批删除，避免一条大 DELETE 把主库锁死几分钟。
//
// 用法：
//
//	go run ./cmd/cleanup -c configs/config.dev.yaml            # 只看会删多少
//	go run ./cmd/cleanup -c configs/config.dev.yaml -apply     # 真删
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"go.uber.org/zap"

	"github.com/datechat/server/internal/bootstrap"
	"github.com/datechat/server/internal/config"
)

// rule 一张表的留存规则。
type rule struct {
	table  string
	column string
	days   int
	note   string
}

// 留存期按"监管要求 + 业务需要"定：
//   - 短信/登录日志：含手机号哈希与 IP，没必要长期留，30 天足够排查问题；
//   - 支付回调与通话事件：要对账，留 180 天（覆盖半年）；
//   - 后台操作审计：合规要求可追溯，留 730 天；
//   - 聊天消息与订单：属于用户资产，不在自动清理范围内（删了就是事故）。
var rules = []rule{
	{"sms_send_log", "created_at", 30, "含手机号哈希与 IP，够排查即可"},
	{"user_login_log", "created_at", 90, "登录审计，90 天足够追溯异常登录"},
	{"im_callback_log", "created_at", 180, "IM 回调留痕，用于对账与申诉"},
	{"call_event_log", "created_at", 180, "通话状态流水，与账单对账相关"},
	{"pay_notify_log", "created_at", 180, "支付回调留痕，用于对账"},
	{"admin_op_log", "created_at", 730, "后台操作审计，合规要求可追溯"},
}

// batchSize 单批删除行数。一次删太多会长时间持锁，把线上请求堵住。
const batchSize = 2000

func main() {
	configPath := flag.String("c", "", "配置文件路径")
	apply := flag.Bool("apply", false, "真正执行删除；默认只预告不删")
	days := flag.Int("days", 0, "覆盖所有规则的留存天数（0 表示用规则自带值）")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "配置加载失败: %v\n", err)
		os.Exit(2)
	}
	log := bootstrap.NewLogger(cfg.Log)
	defer func() { _ = log.Sync() }()

	retention := cfg.Compliance.Policy().RetentionDays

	db, err := bootstrap.NewMySQL(cfg.MySQL, log)
	if err != nil {
		log.Fatal("连接数据库失败", zap.Error(err))
	}

	mode := "dry-run（加 -apply 才会真删）"
	if *apply {
		mode = "apply（真实删除）"
	}
	fmt.Printf("留存清理：%s，默认留存 %d 天\n\n", mode, retention)

	total := int64(0)
	for _, r := range rules {
		keep := r.days
		if *days > 0 {
			keep = *days
		}
		deadline := time.Now().AddDate(0, 0, -keep)

		var count int64
		if err := db.Table(r.table).
			Where(fmt.Sprintf("%s < ?", r.column), deadline).
			Count(&count).Error; err != nil {
			fmt.Printf("  %-18s 统计失败: %v\n", r.table, err)
			continue
		}
		if count == 0 {
			fmt.Printf("  %-18s %d 天前无数据（%s）\n", r.table, keep, r.note)
			continue
		}

		if !*apply {
			fmt.Printf("  %-18s 将删除 %d 行（早于 %s，%s）\n",
				r.table, count, deadline.Format("2006-01-02"), r.note)
			total += count
			continue
		}

		deleted := int64(0)
		for {
			res := db.Exec(
				fmt.Sprintf("DELETE FROM `%s` WHERE `%s` < ? LIMIT %d", r.table, r.column, batchSize),
				deadline,
			)
			if res.Error != nil {
				fmt.Printf("  %-18s 删除失败: %v\n", r.table, res.Error)
				break
			}
			deleted += res.RowsAffected
			if res.RowsAffected < batchSize {
				break
			}
			// 批次之间让出一点时间，避免连续大事务把主从延迟顶高。
			time.Sleep(50 * time.Millisecond)
		}
		fmt.Printf("  %-18s 已删除 %d 行（预期 %d，%s）\n", r.table, deleted, count, r.note)
		total += deleted
	}

	// 已注销用户的手机号密文：账号注销后手机号就没必要继续留着，
	// 保留 user 行是为了让通话/订单等关联数据还能对得上账。
	if *apply {
		res := db.Exec(
			`UPDATE user SET phone_cipher = '', ban_reason = ''
			 WHERE deleted_at IS NOT NULL AND deleted_at < ? AND phone_cipher <> ''`,
			time.Now().AddDate(0, 0, -retention),
		)
		fmt.Printf("  %-18s 已清空 %d 个已注销账号的手机号密文\n", "user(注销)", res.RowsAffected)
	}

	fmt.Printf("\n合计 %d 行%s\n", total, map[bool]string{true: "已删除", false: "待删除"}[*apply])
}
