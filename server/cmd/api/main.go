// Command api 是 HTTP 服务入口。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/datechat/server/internal/bootstrap"
	"github.com/datechat/server/internal/config"
	"github.com/datechat/server/internal/modules/account"
	"github.com/datechat/server/internal/modules/call"
	"github.com/datechat/server/internal/modules/moderation"
	"github.com/datechat/server/internal/modules/nearby"
	"github.com/datechat/server/internal/modules/ops"
	"github.com/datechat/server/internal/modules/realtime"
	"github.com/datechat/server/internal/modules/wallet"
	"github.com/datechat/server/internal/pkg/crypto"
	"github.com/datechat/server/internal/pkg/jwt"
	"github.com/datechat/server/internal/pkg/textfilter"
	"github.com/datechat/server/internal/pkg/tms"
	"github.com/datechat/server/internal/pkg/trtc"
	"github.com/datechat/server/internal/pkg/wxpay"
	"github.com/datechat/server/internal/router"
)

func main() {
	configPath := flag.String("c", "", "配置文件路径；留空则按 APP_ENV 读取 configs/config.{env}.yaml")
	// -check-config 只做「加载 + 校验 + 脱敏打印」然后退出，不连库、不监听端口。
	// 用途：填充真实密钥后，在 CI 或上线前先证明配置本身是合法的。
	checkOnly := flag.Bool("check-config", false, "仅校验配置并打印脱敏结果后退出")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "配置校验失败: %v\n", err)
		os.Exit(2)
	}
	if *checkOnly {
		printConfigCheck(cfg)
		return
	}

	log := bootstrap.NewLogger(cfg.Log)
	defer func() { _ = log.Sync() }()

	log.Info("服务启动中",
		zap.String("name", cfg.App.Name),
		zap.String("version", cfg.App.Version),
		zap.String("env", cfg.App.Env),
		zap.String("config", cfg.Source),
	)

	db, err := bootstrap.NewMySQL(cfg.MySQL, log)
	if err != nil {
		log.Fatal("初始化 MySQL 失败", zap.Error(err))
	}
	rdb, err := bootstrap.NewRedis(cfg.Redis, log)
	if err != nil {
		log.Fatal("初始化 Redis 失败", zap.Error(err))
	}
	defer func() { _ = rdb.Close() }()

	// P1 起服务需要：敏感数据加解密、双令牌签发、短信通道、IM 客户端、实名通道。
	// 全部在启动期构造，任何一项配置错误都直接 Fail Fast。
	cipher, err := crypto.NewCipher(cfg.Security.DataKey, cfg.Security.HMACKey)
	if err != nil {
		log.Fatal("初始化数据加密组件失败", zap.Error(err))
	}
	tokens := jwt.NewManager(cfg.JWT.Secret, cfg.JWT.Issuer, cfg.JWT.AccessTTL.D(), cfg.JWT.RefreshTTL.D())

	smsProvider, err := bootstrap.NewSMSProvider(cfg, log)
	if err != nil {
		log.Fatal("初始化短信通道失败", zap.Error(err))
	}
	realnameProvider, err := bootstrap.NewRealnameProvider(cfg, log)
	if err != nil {
		log.Fatal("初始化实名通道失败", zap.Error(err))
	}

	imClient := account.NewIMClient(cfg.IM, 5*time.Second)
	// P2：敏感词库与本地上传目录。
	// 后台任务统一挂在这个上下文上，退出时一起取消。
	bgCtx, stopBg := context.WithCancel(context.Background())
	defer stopBg()

	// 词库加载失败不阻断启动（降级为不过滤），但必须打日志：
	// 静默跳过会让"资料里出现违规昵称"变成一个没人知道原因的线上问题。
	filter, err := textfilter.LoadFromDB(context.Background(), db)
	if err != nil {
		log.Warn("加载敏感词库失败，资料编辑将不做内容过滤", zap.Error(err))
		filter = textfilter.New()
	} else {
		log.Info("敏感词库已加载", zap.Int("words", filter.Size()))
	}
	stopRefresh := textfilter.NewRefreshLoop(bgCtx, db, filter, 5*time.Minute, func(msg string, args ...any) {
		log.Warn(msg, zap.Any("args", args))
	})
	defer stopRefresh()

	if cfg.Upload.Provider == "local" {
		if err := os.MkdirAll(cfg.Upload.Local.Dir, 0o755); err != nil {
			log.Fatal("创建本地上传目录失败", zap.String("dir", cfg.Upload.Local.Dir), zap.Error(err))
		}
	}

	// P4：统一内容安全服务 = 本地 DFA + 可选云审核 + 统一留痕。
	// 云审核未配密钥时 Enabled() 为 false，自动退化为纯本地过滤。
	cloud := tms.New(cfg.Moderation.Tencent.TMSConfig(cfg.Moderation.CloudEnabled))
	if cfg.Moderation.CloudEnabled && !cloud.Enabled() {
		log.Warn("已开启云审核但缺少腾讯云密钥，将退化为本地 DFA 审核")
	}
	modSvc := moderation.NewService(moderation.Deps{
		Config: cfg,
		Log:    log,
		Repo:   moderation.NewRepository(db),
		Filter: filter,
		Cloud:  cloud,
		Tokens: tokens,
	})
	// P5：微信支付客户端。配置不全时 Enabled() 为 false，下单会走本地模拟通道。
	payClient, err := wxpay.New(wxpay.Config{
		Enabled:    cfg.Pay.Wechat.Enabled,
		AppID:      cfg.Pay.Wechat.AppID,
		MchID:      cfg.Pay.Wechat.MchID,
		SerialNo:   cfg.Pay.Wechat.SerialNo,
		PrivateKey: cfg.Pay.Wechat.PrivateKey,
		APIV3Key:   cfg.Pay.Wechat.APIV3Key,
		NotifyURL:  cfg.Pay.Wechat.NotifyURL,
		PayScene:   cfg.Pay.Wechat.PayScene,
	})
	if err != nil {
		log.Fatal("初始化微信支付失败", zap.Error(err))
	}
	if !payClient.Enabled() {
		log.Warn("未配置微信支付商户密钥，充值将走本地模拟通道（仅 dev 可用）")
	}
	walletSvc := wallet.NewService(cfg, log, wallet.NewRepository(db), payClient)
	// P6：TRTC 进房凭据与通话计费。TRTC 未配密钥时走本地模拟房间（仅非生产），
	// 通话业务链路（接通/心跳/挂断/结算）依旧能完整验收。
	trtcClient := trtc.New(trtc.Config{
		Enabled:   true,
		SdkAppID:  cfg.TRTC.SdkAppID,
		SecretKey: cfg.TRTC.SecretKey,
		Expire:    cfg.TRTC.UserSigExpire.D(),
		Mock:      cfg.TRTC.MockEnabled,
	})
	if !trtcClient.Enabled() {
		log.Warn("未配置 TRTC 密钥，通话将下发本地模拟房间（无真实音视频流）",
			zap.Bool("mock", trtcClient.MockEnabled()))
	}
	// P9：WebSocket 实时通道。消息推送与通话信令都走它；
	// 进程退出前统一关闭，避免客户端要等 90s 读超时才发现断线。
	hub := realtime.NewHub(log)
	defer hub.Close()

	// P7：附近的人与在线。位置检索走 Redis GEO，在线状态复用心跳写入的 presence 键。
	callSvc := call.NewService(call.Deps{
		Config: cfg,
		Log:    log,
		Repo:   call.NewRepository(db),
		Room:   trtcClient,
		Wallet: walletSvc,
		Notify: hub.Push,
	})
	nearbySvc := nearby.NewService(nearby.Deps{
		Config: cfg,
		Log:    log,
		Repo:   nearby.NewRepository(db, rdb),
		Rdb:    rdb,
	})
	// P8：运营后台。注入 cipher 才能按手机号检索并展示脱敏号。
	opsSvc := ops.NewService(ops.Deps{
		Config: cfg,
		Log:    log,
		Repo:   ops.NewRepository(db),
		Rdb:    rdb,
		Cipher: cipher,
	})
	// 回调验签：只有真实支付客户端可用时才允许回调发货。
	var payVerify func(body []byte, header http.Header) (*wxpay.Notify, error)
	if payClient.Enabled() {
		payVerify = func(body []byte, header http.Header) (*wxpay.Notify, error) {
			return payClient.VerifyNotify(body,
				header.Get("Wechatpay-Signature"), header.Get("Wechatpay-Timestamp"),
				header.Get("Wechatpay-Nonce"), header.Get("Wechatpay-Serial"))
		}
	}

	// 首次部署自动建超管；已有账号时该函数直接返回，不会覆盖或提权。
	if err := modSvc.EnsureBootstrapAdmin(bgCtx, cfg.Admin.BootstrapUser, cfg.Admin.BootstrapPwd, "系统初始化"); err != nil {
		log.Warn("初始化后台账号失败（可忽略，若已存在管理员）", zap.Error(err))
	}

	// P6：通话巡检。客户端闪退/断网时没人来发心跳，
	// 必须靠服务端定时把"超时未接、心跳超时、余额耗尽"的通话收尾，否则会一直计费。
	stopCallSweep := startCallSweeper(bgCtx, log, callSvc, cfg.Call.Policy().SweepIntervalSec)
	defer stopCallSweep()

	if !imClient.CanSign() {
		log.Warn("未配置腾讯云 IM 密钥，登录接口不会下发 UserSig（客户端无法登录 IM）")
	}

	srv := &http.Server{
		Addr: cfg.App.Addr,
		Handler: router.New(router.Deps{
			Config:    cfg,
			Log:       log,
			DB:        db,
			Redis:     rdb,
			Tokens:    tokens,
			Cipher:    cipher,
			SMS:       smsProvider,
			IM:        imClient,
			Realname:  realnameProvider,
			Filter:    filter,
			ModSvc:    modSvc,
			Wallet:    walletSvc,
			PayVerify: payVerify,
			Call:      callSvc,
			Nearby:    nearbySvc,
			Ops:       opsSvc,
			Realtime:  hub,
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       cfg.App.ReadTimeout.D(),
		WriteTimeout:      cfg.App.WriteTimeout.D(),
		IdleTimeout:       cfg.App.IdleTimeout.D(),
	}

	listenErr := make(chan error, 1)
	go func() {
		log.Info("HTTP 服务已监听", zap.String("addr", cfg.App.Addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			listenErr <- err
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-listenErr:
		log.Fatal("HTTP 服务异常退出", zap.Error(err))
	case <-ctx.Done():
		log.Info("收到退出信号，开始优雅关闭")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.App.ShutdownTimeout.D())
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("优雅关闭未在超时内完成", zap.Error(err), zap.Duration("timeout", cfg.App.ShutdownTimeout.D()))
	}
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
	log.Info("服务已退出")
}

// startCallSweeper 启动通话巡检协程，返回停止函数。
// 巡检的每次失败只记日志不中断：一次数据库抖动不该让整个巡检线程死掉。
func startCallSweeper(ctx context.Context, log *zap.Logger, svc *call.Service, intervalSec int) func() {
	if intervalSec <= 0 {
		intervalSec = 15
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				sweepCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				rep, err := svc.Sweep(sweepCtx, 200)
				cancel()
				if err != nil {
					log.Error("通话巡检失败", zap.Error(err))
					continue
				}
				if rep.RingTimeout+rep.BeatTimeout+rep.NoBalance > 0 {
					log.Info("通话巡检已收尾异常通话",
						zap.Int("ring_timeout", rep.RingTimeout),
						zap.Int("beat_timeout", rep.BeatTimeout),
						zap.Int("no_balance", rep.NoBalance),
					)
				}
			}
		}
	}()
	return func() {
		close(stop)
		<-done
	}
}

// printConfigCheck 打印脱敏后的配置体检结果，供 -check-config 使用。
// 只报告"有没有配 / 多少字节"，绝不打印密钥原文——这份输出会进 CI 日志。
func printConfigCheck(c *config.Config) {
	mask := func(v string) string {
		if v == "" {
			return "未配置"
		}
		return fmt.Sprintf("已配置(%d 字节)", len(v))
	}
	fmt.Println("配置校验通过")
	fmt.Printf("  环境       : %s\n", c.App.Env)
	fmt.Printf("  配置来源   : %s\n", c.Source)
	if c.EnvLoaded > 0 {
		fmt.Printf("  密钥文件   : %s（注入 %d 项）\n", c.EnvFile, c.EnvLoaded)
	} else {
		fmt.Printf("  密钥文件   : 未使用（%s 不存在或变量已由进程注入）\n", c.EnvFile)
	}
	fmt.Printf("  MySQL      : %s\n", c.MySQL.MaskedDSN())
	fmt.Printf("  Redis      : %v\n", c.Redis.Addrs)
	fmt.Printf("  JWT        : %s\n", mask(c.JWT.Secret))
	fmt.Printf("  数据加密   : data_key=%s hmac_key=%s\n", mask(c.Security.DataKey), mask(c.Security.HMACKey))
	fmt.Printf("  短信       : provider=%s secret_id=%s\n", c.SMS.Provider, mask(c.SMS.Tencent.SecretID))
	fmt.Printf("  实名       : provider=%s enabled=%v secret_id=%s\n",
		c.Realname.Provider, c.Realname.Enabled, mask(c.Realname.Tencent.SecretID))
	fmt.Printf("  IM         : sdk_app_id=%d enabled=%v local_fallback=%v secret=%s callback_token=%s\n",
		c.IM.SdkAppID, c.IM.Enabled(), c.IM.LocalFallback, mask(c.IM.SecretKey), mask(c.IM.CallbackToken))
	fmt.Printf("  上传       : provider=%s\n", c.Upload.Provider)
	fmt.Printf("  TRTC       : sdk_app_id=%d secret=%s mock=%v\n",
		c.TRTC.SdkAppID, mask(c.TRTC.SecretKey), c.TRTC.MockEnabled)
	pol := c.Call.Policy()
	fmt.Printf("  通话       : 振铃超时=%ds 心跳=%ds/%ds 单通上限=%ds 巡检=%ds\n",
		pol.RingTimeoutSec, pol.HeartbeatIntervalSec, pol.HeartbeatTimeoutSec, pol.MaxDurationSec, pol.SweepIntervalSec)
	if c.Upload.Provider == "cos" {
		fmt.Printf("               bucket=%s host=%s\n", c.Upload.COS.Bucket, c.Upload.COS.Host)
	}

	// 给几条"能跑但不该上线"的提醒，这类问题不会触发校验失败。
	if !c.IsProd() && c.IM.LocalFallback {
		fmt.Println("  提醒       : dev 下 im.local_fallback=true，消息只写本地镜像，未真正发往腾讯云")
	}
	if c.IsProd() && c.Upload.Provider == "local" {
		fmt.Println("  提醒       : 生产使用本地盘上传，多副本部署会导致文件读不到，建议切 cos")
	}
	if c.CanExposeSMSCode() {
		fmt.Println("  提醒       : 验证码会回带在响应体中，仅限本地联调")
	}
}
