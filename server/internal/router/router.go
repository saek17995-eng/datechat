// Package router 组装 HTTP 路由。
// 按领域分子路由组，每个模块自己负责注册自己的路由，router 只做装配与中间件编排。
package router

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/datechat/server/internal/config"
	"github.com/datechat/server/internal/middleware"
	"github.com/datechat/server/internal/modules/account"
	"github.com/datechat/server/internal/modules/call"
	"github.com/datechat/server/internal/modules/chat"
	"github.com/datechat/server/internal/modules/health"
	"github.com/datechat/server/internal/modules/moderation"
	"github.com/datechat/server/internal/modules/nearby"
	"github.com/datechat/server/internal/modules/ops"
	"github.com/datechat/server/internal/modules/profile"
	"github.com/datechat/server/internal/modules/realname"
	"github.com/datechat/server/internal/modules/system"
	"github.com/datechat/server/internal/modules/wallet"
	"github.com/datechat/server/internal/pkg/crypto"
	"github.com/datechat/server/internal/pkg/errs"
	"github.com/datechat/server/internal/pkg/jwt"
	"github.com/datechat/server/internal/pkg/resp"
	"github.com/datechat/server/internal/pkg/sms"
	"github.com/datechat/server/internal/pkg/textfilter"
	"github.com/datechat/server/internal/pkg/wxpay"
)

// Deps 是路由层需要的全部依赖。
// 用一个结构体而不是长参数列表，后续加模块不用改函数签名。
type Deps struct {
	Config   *config.Config
	Log      *zap.Logger
	DB       *gorm.DB
	Redis    *redis.Client
	Tokens   *jwt.Manager
	Cipher   *crypto.Cipher
	SMS      sms.Provider
	IM       *account.IMClient
	Realname realname.Provider
	Filter   *textfilter.Filter  // 敏感词过滤器；为空时资料模块自动降级为不过滤
	ModSvc   *moderation.Service // P4 内容安全服务；为空时各模块回退本地过滤
	Wallet   *wallet.Service     // P5 钱包与充值服务
	Call     *call.Service       // P6 通话与计费服务；为空时通话路由不注册
	Nearby   *nearby.Service     // P7 附近的人与在线；为空时不注册相关路由
	Ops      *ops.Service        // P8 运营后台；为空时不注册后台查询路由
	// PayVerify P5 微信回调验签。为空时回调路由直接拒绝，绝不放无签名请求发货。
	PayVerify func(body []byte, header http.Header) (*wxpay.Notify, error)
	// AdminMW 后台鉴权中间件。单独注入是为了让"后台未启用"时能明确跳过而不是静默放行。
}

// New 构建 gin 引擎。
func New(d Deps) *gin.Engine {
	cfg := d.Config
	if cfg.IsProd() {
		gin.SetMode(gin.ReleaseMode)
	} else {
		gin.SetMode(gin.DebugMode)
	}

	engine := gin.New()
	engine.HandleMethodNotAllowed = true
	engine.RedirectTrailingSlash = false

	// 顺序有意义：Trace 必须最先（后续日志都要拿 TraceID），Recover 要能兜住后面所有中间件。
	engine.Use(
		middleware.Trace(),
		middleware.AccessLog(d.Log),
		middleware.Recover(d.Log),
		middleware.Metrics(),
		middleware.SlowRequest(d.Log, cfg.Observe.Policy().SlowRequest.D()),
		middleware.CORS(cfg.CORS),
	)

	engine.NoRoute(func(c *gin.Context) {
		resp.Fail(c, errs.ErrNotFound.WithMsg("接口不存在: %s %s", c.Request.Method, c.Request.URL.Path))
	})
	engine.NoMethod(func(c *gin.Context) {
		resp.Fail(c, errs.ErrMethodNotAllowed)
	})

	// P8 /metrics：不挂 JWT（Prometheus 抓取器不会带令牌），
	// 但生产必须配 observe.metrics_token，否则等于把内部指标公开到公网。
	if cfg.Observe.MetricsEnabled {
		engine.GET("/metrics", health.Metrics(cfg.Observe.MetricsToken))
	}

	// 探针不挂限流，否则被探测平台高频调用会误伤。
	engine.GET("/healthz", health.Liveness)
	engine.GET("/readyz", health.Readiness(d.DB, d.Redis))

	authMW := middleware.Auth(d.Tokens)

	// P3 IM 回调不能挂 JWT，也不能经过通用限流：腾讯云事件前回调只有 2 秒超时。
	chatSvc := chat.NewService(chat.Deps{
		Config: cfg,
		Log:    d.Log,
		Repo:   chat.NewRepository(d.DB),
		IM:     d.IM,
		Filter: d.Filter,
		Mod:    d.ModSvc,
	})
	chatHandler := chat.NewHandler(chatSvc)
	chatHandler.RegisterCallback(engine)

	// P5 支付回调同样不挂 JWT：来源由回调验签保证。
	if d.Wallet != nil {
		wallet.NewHandler(d.Wallet, d.PayVerify).RegisterCallback(engine)
	}

	v1 := engine.Group("/v1")
	if cfg.RateLimit.Enabled {
		v1.Use(middleware.RateLimit(cfg.RateLimit.RPS, cfg.RateLimit.Burst, cfg.RateLimit.TTL.D()))
	}
	{
		v1.GET("/ping", health.Ping(cfg))

		// P8：协议与最低年龄。不挂鉴权——未登录也要能弹协议。
		system.NewHandler(cfg, d.Log, d.DB).Register(v1)

		// 账号：发码 / 登录 / 刷新 / 退出 / 我的 / UserSig
		accountSvc := account.NewService(account.Deps{
			Config: cfg,
			Log:    d.Log,
			Repo:   account.NewRepository(d.DB),
			Redis:  d.Redis,
			Tokens: d.Tokens,
			Cipher: d.Cipher,
			SMS:    d.SMS,
			IM:     d.IM,
		})
		account.NewHandler(accountSvc).Register(v1, authMW)

		// 单聊：会话列表 / 消息历史 / 发消息 / 已读 / 撤回。
		chatHandler.Register(v1, authMW)

		// 实名：提交 / 查状态 / 回查结果
		realnameSvc := realname.NewService(cfg, d.Log, realname.NewRepository(d.DB), d.Cipher, d.Realname)
		realname.NewHandler(realnameSvc).Register(v1, authMW)

		// 资料：我的资料 / 头像上传 / 他人主页
		profileSvc := profile.NewService(profile.Deps{
			Config: cfg,
			Log:    d.Log,
			Repo:   profile.NewRepository(d.DB),
			Redis:  d.Redis,
			Filter: d.Filter,
			Mod:    d.ModSvc,
		})
		profile.NewHandler(profileSvc).Register(v1, authMW)

		// P5：钱包、充值档位与订单
		if d.Wallet != nil {
			wallet.NewHandler(d.Wallet, nil).Register(v1, authMW)
		}

		// P7：附近的人、在线状态与"谁看过我"。
		if d.Nearby != nil {
			nearby.NewHandler(d.Nearby).Register(v1, authMW)
		}

		// P6：通话（发起/接听/挂断/心跳/记录）与后台巡检。
		// 依赖 TRTC 与钱包两个外部能力，任一缺失就不注册路由，避免"能下单但打不通"。
		if d.Call != nil {
			call.NewHandler(d.Call).Register(v1, authMW)
			call.NewHandler(d.Call).RegisterAdmin(v1, middleware.AdminAuth(d.Tokens))
		}

		// P8：运营后台（概览/用户/订单/通话/操作日志）。
		if d.Ops != nil {
			ops.NewHandler(d.Ops).RegisterAdmin(v1, middleware.AdminAuth(d.Tokens))
		}

		// P4：举报与后台处置。后台路由单独挂 AdminAuth，用户令牌不能越权。
		if d.ModSvc != nil {
			moderation.NewHandler(d.ModSvc).Register(v1, authMW)
			moderation.NewHandler(d.ModSvc).RegisterAdmin(v1, middleware.AdminAuth(d.Tokens))
		}

		// 本地上传通道：把落盘目录挂成静态目录。
		// 生产环境走 COS 时这里不会注册，静态资源全部由对象存储/CDN 承担。
		if cfg.Upload.Provider == "local" {
			prefix := strings.TrimSuffix(cfg.Upload.Local.URLPrefix, "/")
			if prefix != "" {
				engine.StaticFS(prefix, gin.Dir(cfg.Upload.Local.Dir, false))
				d.Log.Info("已挂载本地上传静态目录",
					zap.String("prefix", prefix),
					zap.String("dir", cfg.Upload.Local.Dir),
				)
			}
		}
	}

	return engine
}
