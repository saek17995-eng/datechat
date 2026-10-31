// Package config 负责加载与校验服务配置。
//
// 设计约定：
//  1. 配置来源只有 yaml 文件，路径由 -c 指定或按 APP_ENV 推导；
//  2. 文件内支持 ${VAR} 形式的环境变量展开，便于容器化部署注入密钥；
//  3. 所有时长字段使用 Duration 包装类型，支持 "2h" / "200ms" 这种可读写法。
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/datechat/server/internal/pkg/tms"
	"gopkg.in/yaml.v3"
)

// Duration 让 yaml 中的 "10s"、"2h" 能被正确解析成 time.Duration。
// yaml.v3 对 time.Duration 的字符串解析行为在不同版本间不稳定，这里显式实现。
type Duration time.Duration

// D 返回标准库时长。
func (d Duration) D() time.Duration { return time.Duration(d) }

func (d Duration) String() string { return time.Duration(d).String() }

// UnmarshalYAML 支持 "200ms" 这类字符串，也兼容纯数字（按秒处理）。
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.ScalarNode {
		return fmt.Errorf("时长字段必须是标量，实际为 %v", value.Kind)
	}
	if value.Tag == "!!int" {
		var seconds int64
		if err := value.Decode(&seconds); err != nil {
			return fmt.Errorf("解析时长整数失败: %w", err)
		}
		*d = Duration(time.Duration(seconds) * time.Second)
		return nil
	}
	parsed, err := time.ParseDuration(value.Value)
	if err != nil {
		return fmt.Errorf("解析时长 %q 失败: %w", value.Value, err)
	}
	*d = Duration(parsed)
	return nil
}

// Config 是服务全量配置。
type Config struct {
	Source     string           `yaml:"-"` // 实际加载的文件路径，便于启动日志打印
	EnvFile    string           `yaml:"-"` // 密钥文件路径（.env），不存在时为空串
	EnvLoaded  int              `yaml:"-"` // 从 .env 实际注入的变量条数，0 表示完全走进程环境变量
	App        AppConfig        `yaml:"app"`
	Log        LogConfig        `yaml:"log"`
	MySQL      MySQLConfig      `yaml:"mysql"`
	Redis      RedisConfig      `yaml:"redis"`
	JWT        JWTConfig        `yaml:"jwt"`
	RateLimit  RateLimitConfig  `yaml:"rate_limit"`
	CORS       CORSConfig       `yaml:"cors"`
	Security   SecurityConfig   `yaml:"security"`
	SMS        SMSConfig        `yaml:"sms"`
	IM         IMConfig         `yaml:"im"`
	Realname   RealnameConfig   `yaml:"realname"`
	Upload     UploadConfig     `yaml:"upload"`
	Moderation ModerationConfig `yaml:"moderation"`
	Admin      AdminConfig      `yaml:"admin"`
	Pay        PayConfig        `yaml:"pay"`
	TRTC       TRTCConfig       `yaml:"trtc"`
	Call       CallConfig       `yaml:"call"`
	Nearby     NearbyConfig     `yaml:"nearby"`
	Observe    ObserveConfig    `yaml:"observe"`
	Compliance ComplianceConfig `yaml:"compliance"`
}

// ObserveConfig P8 可观测与监控。
type ObserveConfig struct {
	MetricsEnabled bool     `yaml:"metrics_enabled"` // 是否暴露 /metrics
	MetricsToken   string   `yaml:"metrics_token"`   // 非空时 /metrics 需要携带该令牌
	SlowRequest    Duration `yaml:"slow_request"`    // 慢请求阈值，超过则单独打 warn 日志
}

func (c ObserveConfig) Policy() ObserveConfig {
	if c.SlowRequest <= 0 {
		c.SlowRequest = Duration(time.Second)
	}
	return c
}

// ComplianceConfig P8 合规开关。
//
// 全部可配置而不是写死在代码里：合规要求会随监管口径变，
// 改配置能热生效的事，没必要每次都发版。
type ComplianceConfig struct {
	MinAge                  int  `yaml:"min_age"`                    // 最低使用年龄
	RealnameRequiredForCall bool `yaml:"realname_required_for_call"` // 发起通话是否要求实名
	RealnameRequiredForCash bool `yaml:"realname_required_for_cash"` // 提现是否要求实名
	MaxRechargeFenPerDay    int  `yaml:"max_recharge_fen_per_day"`   // 未实名用户单日充值上限（分）
	RetentionDays           int  `yaml:"retention_days"`             // 日志与历史数据保留天数
}

func (c ComplianceConfig) Policy() ComplianceConfig {
	if c.MinAge <= 0 {
		c.MinAge = 18
	}
	if c.RetentionDays <= 0 {
		c.RetentionDays = 180
	}
	return c
}

// NearbyConfig 附近的人与在线策略。
type NearbyConfig struct {
	DefaultRadiusKM int  `yaml:"default_radius_km"` // 默认搜索半径
	MaxRadiusKM     int  `yaml:"max_radius_km"`     // 最大搜索半径，防止一次扫全量
	PageSize        int  `yaml:"page_size"`
	DistanceFuzzy   bool `yaml:"distance_fuzzy"` // 对外展示距离是否按区间模糊
	OnlineTTLSec    int  `yaml:"online_ttl_sec"` // 在线状态 TTL
	MinReportSec    int  `yaml:"min_report_interval_sec"`
}

// NearbyPolicy 归一化后的策略。
type NearbyPolicy struct {
	DefaultRadiusKM int
	MaxRadiusKM     int
	PageSize        int
	DistanceFuzzy   bool
	OnlineTTLSec    int
	MinReportSec    int
}

// Policy 返回补齐默认值的附近策略。
func (c NearbyConfig) Policy() NearbyPolicy {
	p := NearbyPolicy{
		DefaultRadiusKM: c.DefaultRadiusKM,
		MaxRadiusKM:     c.MaxRadiusKM,
		PageSize:        c.PageSize,
		DistanceFuzzy:   c.DistanceFuzzy,
		OnlineTTLSec:    c.OnlineTTLSec,
		MinReportSec:    c.MinReportSec,
	}
	if p.DefaultRadiusKM <= 0 {
		p.DefaultRadiusKM = 50
	}
	if p.MaxRadiusKM <= 0 {
		p.MaxRadiusKM = 200
	}
	if p.MaxRadiusKM < p.DefaultRadiusKM {
		p.MaxRadiusKM = p.DefaultRadiusKM
	}
	if p.PageSize <= 0 || p.PageSize > 100 {
		p.PageSize = 20
	}
	if p.OnlineTTLSec <= 0 {
		p.OnlineTTLSec = 120
	}
	if p.MinReportSec < 0 {
		p.MinReportSec = 0
	}
	return p
}

// TRTCConfig 腾讯云 TRTC 实时音视频配置。
//
// TRTC 与 IM 用的是同一套腾讯云 TLS 票据（UserSig 算法完全一致），
// 在同一个应用下通常也共用 SDKAppID，所以未单独配置时直接回落到 im 的凭据，
// 避免同一个应用把同一份密钥配两遍、两处还容易写不一致。
type TRTCConfig struct {
	SdkAppID      int      `yaml:"sdk_app_id"`
	SecretKey     string   `yaml:"secret_key"`
	UserSigExpire Duration `yaml:"usersig_expire"`
	// MockEnabled 无密钥时是否允许下发"本地模拟房间"。
	// 开了之后端到端流程（接通/心跳/挂断/计费）都能跑通，只是房间里没有真实音视频流。
	// 生产环境该开关会被 validate 硬拦。
	MockEnabled bool `yaml:"mock_enabled"`
}

// CallConfig 通话与计费策略。
//
// 时间类参数全部服务端下发，客户端上报的时长只用于展示，绝不参与结算——
// 一旦允许客户端报时长，改包就能白嫖通话。
type CallConfig struct {
	RingTimeoutSec       int `yaml:"ring_timeout_sec"`       // 振铃无应答自动超时
	HeartbeatIntervalSec int `yaml:"heartbeat_interval_sec"` // 建议的客户端心跳间隔
	HeartbeatTimeoutSec  int `yaml:"heartbeat_timeout_sec"`  // 心跳超时判定掉线
	MaxDurationSec       int `yaml:"max_duration_sec"`       // 单通最长时长，兜底
	SweepIntervalSec     int `yaml:"sweep_interval_sec"`     // 后台巡检间隔
	MinCoinsToCall       int `yaml:"min_coins_to_call"`      // 低于该余额不允许发起
}

// CallPolicy 返回补齐默认值后的通话策略（避免各处直接读 0 值）。
type CallPolicy struct {
	RingTimeoutSec       int
	HeartbeatIntervalSec int
	HeartbeatTimeoutSec  int
	MaxDurationSec       int
	SweepIntervalSec     int
	MinCoinsToCall       int
}

// Policy 把 CallConfig 归一化成可直接使用的策略。
func (c CallConfig) Policy() CallPolicy {
	p := CallPolicy{
		RingTimeoutSec:       c.RingTimeoutSec,
		HeartbeatIntervalSec: c.HeartbeatIntervalSec,
		HeartbeatTimeoutSec:  c.HeartbeatTimeoutSec,
		MaxDurationSec:       c.MaxDurationSec,
		SweepIntervalSec:     c.SweepIntervalSec,
		MinCoinsToCall:       c.MinCoinsToCall,
	}
	if p.RingTimeoutSec <= 0 {
		p.RingTimeoutSec = 45
	}
	if p.HeartbeatIntervalSec <= 0 {
		p.HeartbeatIntervalSec = 10
	}
	if p.HeartbeatTimeoutSec <= 0 {
		p.HeartbeatTimeoutSec = 30
	}
	if p.MaxDurationSec <= 0 {
		p.MaxDurationSec = 3600
	}
	if p.SweepIntervalSec <= 0 {
		p.SweepIntervalSec = 15
	}
	if p.MinCoinsToCall < 0 {
		p.MinCoinsToCall = 0
	}
	return p
}

// PayConfig 支付配置。
type PayConfig struct {
	OrderExpireMin int  `yaml:"order_expire_min"` // 订单未支付自动关闭时长
	MaxNotifyRetry int  `yaml:"max_notify_retry"` // 单订单最大回调次数
	ReconcileHours int  `yaml:"reconcile_hours"`  // 对账回看小时数
	MockEnabled    bool `yaml:"mock_enabled"`     // 是否允许本地模拟支付（仅非生产）
	Wechat         struct {
		Enabled    bool   `yaml:"enabled"`
		AppID      string `yaml:"app_id"`
		MchID      string `yaml:"mch_id"`
		SerialNo   string `yaml:"serial_no"`
		PrivateKey string `yaml:"private_key"` // PEM 内容或文件路径
		APIV3Key   string `yaml:"api_v3_key"`
		NotifyURL  string `yaml:"notify_url"`
		PayScene   string `yaml:"pay_scene"` // app / jsapi
	} `yaml:"wechat"`
}

// AdminConfig 后台管理配置。
type AdminConfig struct {
	Enabled       bool     `yaml:"enabled"`        // 未初始化管理员账号时无法登录
	TokenTTL      Duration `yaml:"token_ttl"`      // 后台令牌有效期，默认 8h
	BootstrapUser string   `yaml:"bootstrap_user"` // 首次初始化超管账号名
	BootstrapPwd  string   `yaml:"bootstrap_pwd"`  // 首次初始化超管口令，仅首次生效
}

// UploadConfig 图片/语音上传策略。
//
// provider=local 时文件落到本地磁盘并由本服务提供静态访问，仅用于自测与内网联调；
// provider=cos 时服务端只签直传凭证（客户端直传对象存储），业务流量不穿过应用服务器。
type UploadConfig struct {
	Provider     string          `yaml:"provider"` // local | cos
	MaxImageMB   int             `yaml:"max_image_mb"`
	AvatarReview bool            `yaml:"avatar_review"` // 头像是否需要人工/机审通过后才生效
	Local        LocalUploadCfg  `yaml:"local"`
	COS          COSUploadConfig `yaml:"cos"`
}

// LocalUploadCfg 本地盘上传。
type LocalUploadCfg struct {
	Dir       string `yaml:"dir"`        // 落盘目录
	URLPrefix string `yaml:"url_prefix"` // 静态访问前缀，如 /static/uploads
}

// COSUploadConfig 腾讯云对象存储直传参数。
// 服务端只做签名，不中转文件；客户端拿到凭证后直传 COS，再回调提交 URL。
type COSUploadConfig struct {
	SecretID   string   `yaml:"secret_id"`
	SecretKey  string   `yaml:"secret_key"`
	Region     string   `yaml:"region"`
	Bucket     string   `yaml:"bucket"`      // 形如 datechat-1250000000
	Prefix     string   `yaml:"prefix"`      // 对象键前缀，如 avatar/
	Host       string   `yaml:"host"`        // 直传域名 https://{bucket}.cos.{region}.myqcloud.com
	CDNDomain  string   `yaml:"cdn_domain"`  // 访问用 CDN 域名，为空则回落到 Host
	SignExpire Duration `yaml:"sign_expire"` // 直传凭证有效期
}

// MaxImageBytes 返回单张图片的大小上限（字节）。
func (u UploadConfig) MaxImageBytes() int64 {
	mb := u.MaxImageMB
	if mb <= 0 {
		mb = 5
	}
	return int64(mb) * 1024 * 1024
}

// PublicBaseURL 返回上传文件的对外访问基址，用于校验客户端提交的 URL 是否属于本站。
func (u UploadConfig) PublicBaseURL() string {
	if u.Provider == "cos" {
		if u.COS.CDNDomain != "" {
			return strings.TrimRight(u.COS.CDNDomain, "/")
		}
		return strings.TrimRight(u.COS.Host, "/")
	}
	return strings.TrimRight(u.Local.URLPrefix, "/")
}

type AppConfig struct {
	Name            string   `yaml:"name"`
	Env             string   `yaml:"env"`
	Addr            string   `yaml:"addr"`
	Version         string   `yaml:"version"`
	ShutdownTimeout Duration `yaml:"shutdown_timeout"`
	ReadTimeout     Duration `yaml:"read_timeout"`
	WriteTimeout    Duration `yaml:"write_timeout"`
	IdleTimeout     Duration `yaml:"idle_timeout"`
}

type LogConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

type MySQLConfig struct {
	Host            string   `yaml:"host"`
	Port            int      `yaml:"port"`
	User            string   `yaml:"user"`
	Password        string   `yaml:"password"`
	Database        string   `yaml:"database"`
	Charset         string   `yaml:"charset"`
	ParseTime       bool     `yaml:"parse_time"`
	MaxOpenConns    int      `yaml:"max_open_conns"`
	MaxIdleConns    int      `yaml:"max_idle_conns"`
	ConnMaxLifetime Duration `yaml:"conn_max_lifetime"`
	ConnMaxIdleTime Duration `yaml:"conn_max_idle_time"`
	SlowThreshold   Duration `yaml:"slow_threshold"`
}

// DSN 生成 go-sql-driver/mysql 连接串。
// timeout/readTimeout/writeTimeout 必须设置，否则网络异常时连接会长时间挂起。
func (m MySQLConfig) DSN() string {
	return fmt.Sprintf(
		"%s:%s@tcp(%s:%d)/%s?charset=%s&parseTime=%t&loc=Local&timeout=5s&readTimeout=10s&writeTimeout=10s",
		m.User, m.Password, m.Host, m.Port, m.Database, m.Charset, m.ParseTime,
	)
}

// MaskedDSN 用于日志输出，隐藏口令。
func (m MySQLConfig) MaskedDSN() string {
	return fmt.Sprintf("%s:***@tcp(%s:%d)/%s", m.User, m.Host, m.Port, m.Database)
}

type RedisConfig struct {
	Addrs        []string `yaml:"addrs"`
	Password     string   `yaml:"password"`
	DB           int      `yaml:"db"`
	PoolSize     int      `yaml:"pool_size"`
	DialTimeout  Duration `yaml:"dial_timeout"`
	ReadTimeout  Duration `yaml:"read_timeout"`
	WriteTimeout Duration `yaml:"write_timeout"`
}

type JWTConfig struct {
	Secret     string   `yaml:"secret"`
	Issuer     string   `yaml:"issuer"`
	AccessTTL  Duration `yaml:"access_ttl"`
	RefreshTTL Duration `yaml:"refresh_ttl"`
}

type RateLimitConfig struct {
	Enabled bool     `yaml:"enabled"`
	RPS     float64  `yaml:"rps"`
	Burst   int      `yaml:"burst"`
	TTL     Duration `yaml:"ttl"` // 单个 IP 限流器的空闲回收时间
}

type CORSConfig struct {
	AllowOrigins  []string `yaml:"allow_origins"`
	AllowMethods  []string `yaml:"allow_methods"`
	AllowHeaders  []string `yaml:"allow_headers"`
	ExposeHeaders []string `yaml:"expose_headers"`
	MaxAge        Duration `yaml:"max_age"`
}

// SecurityConfig 保存数据加密所需的两把密钥。
//
//   - DataKey：AES-256-GCM 主密钥，加密手机号、姓名、身份证号。必须是 32 字节，
//     或用任意长度口令（内部按 SHA-256 派生），生产环境应从 KMS / 密钥管理服务注入。
//   - HMACKey：确定性哈希密钥，用于生成 phone_hmac / id_card_hmac 这类需要唯一索引的列。
//     与 DataKey 必须不同：一旦混用，密文泄露会连带影响索引列的不可逆性。
type SecurityConfig struct {
	DataKey string `yaml:"data_key"`
	HMACKey string `yaml:"hmac_key"`
}

// SMSConfig 短信验证码策略。
type SMSConfig struct {
	Provider          string           `yaml:"provider"` // mock | tencent
	CodeLength        int              `yaml:"code_length"`
	CodeTTL           Duration         `yaml:"code_ttl"`
	ResendInterval    Duration         `yaml:"resend_interval"`
	DailyLimit        int              `yaml:"daily_limit"`
	MaxVerifyAttempts int              `yaml:"max_verify_attempts"`
	ExposeCodeInDev   bool             `yaml:"expose_code_in_dev"` // 仅 dev 生效：响应体回带验证码，方便联调
	Tencent           TencentSMSConfig `yaml:"tencent"`
}

// TencentSMSConfig 腾讯云短信。
type TencentSMSConfig struct {
	SecretID   string `yaml:"secret_id"`
	SecretKey  string `yaml:"secret_key"`
	SdkAppID   string `yaml:"sdk_app_id"`
	SignName   string `yaml:"sign_name"`
	TemplateID string `yaml:"template_id"`
	Region     string `yaml:"region"`
}

// IMConfig 腾讯云 IM（即时通讯）配置。
// UserID 生成规则固定为 u_{uid}，与用户主键同构，避免额外映射表。
type IMConfig struct {
	SdkAppID        int      `yaml:"sdk_app_id"`
	SecretKey       string   `yaml:"secret_key"`
	AdminUserID     string   `yaml:"admin_user_id"`
	UserSigExpire   Duration `yaml:"usersig_expire"`
	ImportEnabled   bool     `yaml:"import_enabled"` // 登录时调用 account_import
	LocalFallback   bool     `yaml:"local_fallback"` // 无 IM 密钥时允许本地镜像发送，仅限开发/验收
	Endpoint        string   `yaml:"endpoint"`
	CallbackToken   string   `yaml:"callback_token"`    // 控制台回调鉴权 Token；非空即强制验签
	CallbackMaxSkew Duration `yaml:"callback_max_skew"` // RequestTime 最大时间偏差
	MessageMaxChars int      `yaml:"message_max_chars"`
	RecallWindow    Duration `yaml:"recall_window"`
}

// Enabled 判断 IM 能力是否可用（缺密钥时自动降级为不导入，但本地签发 UserSig 仍可跑）。
func (c IMConfig) Enabled() bool {
	return c.ImportEnabled && c.SdkAppID > 0 && c.SecretKey != ""
}

// RealnameConfig 实名认证策略。
// Provider=mock 时走本地模拟通道（可直接通过），provider=tencent 时接腾讯云人脸核身。
type RealnameConfig struct {
	Provider string                `yaml:"provider"` // mock | tencent
	Enabled  bool                  `yaml:"enabled"`
	Tencent  TencentRealnameConfig `yaml:"tencent"`
}

// TencentRealnameConfig 腾讯云人脸核身（FaceID）。
type TencentRealnameConfig struct {
	SecretID    string `yaml:"secret_id"`
	SecretKey   string `yaml:"secret_key"`
	Region      string `yaml:"region"`
	RuleID      string `yaml:"rule_id"`
	Endpoint    string `yaml:"endpoint"`
	RedirectURL string `yaml:"redirect_url"`
}

// ModerationConfig 内容安全策略。
// 本地 DFA 与云审核是两道独立防线：DFA 快且可解释，云审核能识别变体与语义风险。
type ModerationConfig struct {
	DFAEnabled     bool `yaml:"dfa_enabled"`      // 是否启用本地 DFA
	CloudEnabled   bool `yaml:"cloud_enabled"`    // 是否启用腾讯云文本审核
	CloudOnUnknown bool `yaml:"cloud_on_unknown"` // 本地未命中时是否仍送云（更准，但增加延迟与成本）
	// CloudFailOpen 云审核调用失败时是否放行。
	// 默认 true：云抖动不该让用户发不出消息；但对高危场景（如昵称/头像）建议 false。
	CloudFailOpen bool         `yaml:"cloud_fail_open"`
	Tencent       TencentTMS   `yaml:"tencent"`
	Report        ReportConfig `yaml:"report"`
}

// TencentTMS 云审核凭据。与 tms.Config 分开定义，是为了让 yaml 标签和
// Duration 解析留在配置层，tms 包只关心运行期参数。
type TencentTMS struct {
	SecretID  string   `yaml:"secret_id"`
	SecretKey string   `yaml:"secret_key"`
	Region    string   `yaml:"region"`
	BizType   string   `yaml:"biz_type"` // 控制台审核策略，留空用默认
	Endpoint  string   `yaml:"endpoint"`
	Timeout   Duration `yaml:"timeout"`
}

// TMSConfig 转换成 tms 客户端配置。
func (t TencentTMS) TMSConfig(enabled bool) tms.Config {
	return tms.Config{
		Enabled:   enabled,
		SecretID:  t.SecretID,
		SecretKey: t.SecretKey,
		Region:    t.Region,
		BizType:   t.BizType,
		Endpoint:  t.Endpoint,
		Timeout:   t.Timeout.D(),
	}
}

// ReportConfig 举报限频与必填策略。
type ReportConfig struct {
	DailyLimit     int  `yaml:"daily_limit"`     // 单用户单日举报上限
	ReasonRequired bool `yaml:"reason_required"` // 是否强制填写补充描述
}

// IsProd 判断是否为生产环境。
func (c *Config) IsProd() bool { return c.App.Env == "prod" }

// Load 读取并校验配置。path 为空时按 APP_ENV（默认 dev）推导。
func Load(path string) (*Config, error) {
	if path == "" {
		env := os.Getenv("APP_ENV")
		if env == "" {
			env = "dev"
		}
		path = filepath.Join("configs", fmt.Sprintf("config.%s.yaml", env))
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件 %s 失败: %w", path, err)
	}

	// 密钥注入：先加载 .env（进程环境变量优先），再做占位符展开。
	// 这样配置文件里只需要写 ${VAR}，真实值留在仓库外。
	envPath := EnvFilePath(path)
	loaded, err := LoadEnvFile(envPath)
	if err != nil {
		return nil, err
	}

	// 先做环境变量展开，再解析 yaml。注意：口令中若含 $ 需转义为 $$。
	expanded := os.ExpandEnv(string(raw))

	var cfg Config
	cfg.EnvFile, cfg.EnvLoaded = envPath, loaded
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件 %s 失败: %w", path, err)
	}
	cfg.Source = path
	cfg.applyDefaults()

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("配置校验失败: %w", err)
	}
	return &cfg, nil
}

// MustLoad 供 main 使用，失败直接 panic（启动期 Fail Fast 优于带病运行）。
func MustLoad(path string) *Config {
	cfg, err := Load(path)
	if err != nil {
		panic(err)
	}
	return cfg
}

func (c *Config) applyDefaults() {
	if c.App.Addr == "" {
		c.App.Addr = ":8080"
	}
	if c.App.Env == "" {
		c.App.Env = "dev"
	}
	if c.App.ShutdownTimeout == 0 {
		c.App.ShutdownTimeout = Duration(10 * time.Second)
	}
	if c.App.ReadTimeout == 0 {
		c.App.ReadTimeout = Duration(15 * time.Second)
	}
	if c.App.WriteTimeout == 0 {
		c.App.WriteTimeout = Duration(30 * time.Second)
	}
	if c.App.IdleTimeout == 0 {
		c.App.IdleTimeout = Duration(60 * time.Second)
	}
	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
	if c.Log.Format == "" {
		c.Log.Format = "console"
	}
	if c.MySQL.Port == 0 {
		c.MySQL.Port = 3306
	}
	if c.MySQL.Charset == "" {
		c.MySQL.Charset = "utf8mb4"
	}
	if c.MySQL.MaxOpenConns == 0 {
		c.MySQL.MaxOpenConns = 100
	}
	if c.MySQL.MaxIdleConns == 0 {
		c.MySQL.MaxIdleConns = 20
	}
	if c.MySQL.ConnMaxLifetime == 0 {
		c.MySQL.ConnMaxLifetime = Duration(time.Hour)
	}
	if c.MySQL.SlowThreshold == 0 {
		c.MySQL.SlowThreshold = Duration(200 * time.Millisecond)
	}
	if c.Redis.PoolSize == 0 {
		c.Redis.PoolSize = 20
	}
	if c.Redis.DialTimeout == 0 {
		c.Redis.DialTimeout = Duration(3 * time.Second)
	}
	if c.Redis.ReadTimeout == 0 {
		c.Redis.ReadTimeout = Duration(3 * time.Second)
	}
	if c.Redis.WriteTimeout == 0 {
		c.Redis.WriteTimeout = Duration(3 * time.Second)
	}
	if c.JWT.Issuer == "" {
		c.JWT.Issuer = c.App.Name
	}
	if c.JWT.AccessTTL == 0 {
		c.JWT.AccessTTL = Duration(2 * time.Hour)
	}
	if c.JWT.RefreshTTL == 0 {
		c.JWT.RefreshTTL = Duration(30 * 24 * time.Hour)
	}
	if c.RateLimit.RPS <= 0 {
		c.RateLimit.RPS = 50
	}
	if c.RateLimit.Burst <= 0 {
		c.RateLimit.Burst = 100
	}
	if c.RateLimit.TTL == 0 {
		c.RateLimit.TTL = Duration(10 * time.Minute)
	}
	// —— P1：短信 / IM / 实名 ——
	if c.SMS.Provider == "" {
		c.SMS.Provider = "mock"
	}
	if c.SMS.CodeLength == 0 {
		c.SMS.CodeLength = 6
	}
	if c.SMS.CodeTTL == 0 {
		c.SMS.CodeTTL = Duration(5 * time.Minute)
	}
	if c.SMS.ResendInterval == 0 {
		c.SMS.ResendInterval = Duration(60 * time.Second)
	}
	if c.SMS.DailyLimit == 0 {
		c.SMS.DailyLimit = 10
	}
	if c.SMS.MaxVerifyAttempts == 0 {
		c.SMS.MaxVerifyAttempts = 5
	}
	if c.SMS.Tencent.Region == "" {
		c.SMS.Tencent.Region = "ap-guangzhou"
	}
	if c.IM.AdminUserID == "" {
		c.IM.AdminUserID = "administrator"
	}
	if c.IM.UserSigExpire == 0 {
		c.IM.UserSigExpire = Duration(7 * 24 * time.Hour)
	}
	if c.IM.Endpoint == "" {
		c.IM.Endpoint = "https://console.tim.qq.com"
	}
	if c.IM.CallbackMaxSkew == 0 {
		c.IM.CallbackMaxSkew = Duration(time.Minute)
	}
	if c.IM.MessageMaxChars <= 0 {
		c.IM.MessageMaxChars = 500
	}
	if c.IM.RecallWindow == 0 {
		c.IM.RecallWindow = Duration(2 * time.Minute)
	}
	if c.Realname.Provider == "" {
		c.Realname.Provider = "mock"
	}
	if !c.Realname.Enabled && c.Realname.Provider == "mock" && c.Realname.Tencent.SecretID == "" {
		// 配置里完全没提 realname 时默认开启（mock 通道），显式配 tencent 则尊重用户开关。
		c.Realname.Enabled = true
	}
	if c.Realname.Tencent.Endpoint == "" {
		c.Realname.Tencent.Endpoint = "https://faceid.tencentcloudapi.com"
	}
	// —— P2：上传 ——
	if c.Upload.Provider == "" {
		c.Upload.Provider = "local"
	}
	if c.Upload.MaxImageMB <= 0 {
		c.Upload.MaxImageMB = 5
	}
	if c.Upload.Local.Dir == "" {
		c.Upload.Local.Dir = "./.local/uploads"
	}
	if c.Upload.Local.URLPrefix == "" {
		c.Upload.Local.URLPrefix = "/static/uploads"
	}
	if c.Upload.COS.Prefix == "" {
		c.Upload.COS.Prefix = "upload/"
	}
	if c.Upload.COS.SignExpire == 0 {
		c.Upload.COS.SignExpire = Duration(30 * time.Minute)
	}
	if c.Upload.COS.Region == "" {
		c.Upload.COS.Region = "ap-guangzhou"
	}
	// —— P4：内容安全 ——
	// DFA 默认开启：它是唯一不依赖外部服务、也不产生费用的防线。
	// 云审核默认关闭：需要密钥，且默认关闭可避免"配了开关没配密钥却以为在审核"。
	if !c.Moderation.DFAEnabled && !c.Moderation.CloudEnabled {
		c.Moderation.DFAEnabled = true
	}
	if c.Moderation.Report.DailyLimit <= 0 {
		c.Moderation.Report.DailyLimit = 20
	}
	if c.Admin.TokenTTL == 0 {
		c.Admin.TokenTTL = Duration(8 * time.Hour)
	}
	// —— P6：TRTC 与通话 ——
	// 未单独配置 trtc 时共用 IM 的 SDKAppID 与密钥（同一套 TLS 票据体系）。
	if c.TRTC.SdkAppID <= 0 {
		c.TRTC.SdkAppID = c.IM.SdkAppID
	}
	if c.TRTC.SecretKey == "" {
		c.TRTC.SecretKey = c.IM.SecretKey
	}
	if c.TRTC.UserSigExpire == 0 {
		// 比 IM 登录票据短：通话凭据只在单次通话前后短暂使用，泄露影响面更小。
		c.TRTC.UserSigExpire = Duration(24 * time.Hour)
	}
}

// CanExposeSMSCode 判断是否允许在响应体里回带验证码：只有非生产环境且显式开启才生效。
// 这个开关是为了本地/联调用，绝不能在生产打开。
func (c *Config) CanExposeSMSCode() bool {
	return !c.IsProd() && c.SMS.ExposeCodeInDev
}

// rejectDevSecret 拦住"把 dev 示例密钥带进生产"。
// 判据是前缀而不是等值：改成 dev-xxx-2 这种一眼假的值也应该被拦下。
func rejectDevSecret(name, value string) error {
	if strings.HasPrefix(value, "dev") || strings.HasPrefix(value, "change-me") ||
		strings.HasPrefix(value, "your-") || value == "" {
		return fmt.Errorf("生产环境 %s 仍是占位/示例值，请通过环境变量或 .env 注入真实密钥", name)
	}
	return nil
}

func (c *Config) validate() error {
	if c.MySQL.Host == "" || c.MySQL.Database == "" || c.MySQL.User == "" {
		return fmt.Errorf("mysql.host / mysql.database / mysql.user 不能为空")
	}
	if len(c.Redis.Addrs) == 0 {
		return fmt.Errorf("redis.addrs 不能为空")
	}
	if len(c.JWT.Secret) < 32 {
		return fmt.Errorf("jwt.secret 长度需不小于 32 字节，当前 %d 字节", len(c.JWT.Secret))
	}
	if c.RateLimit.Enabled && c.RateLimit.Burst < 1 {
		return fmt.Errorf("rate_limit.burst 必须为正整数")
	}
	// 敏感数据加密密钥：缺失即拒启动，避免带着空密钥把明文写进库。
	if len(c.Security.DataKey) < 16 {
		return fmt.Errorf("security.data_key 长度需不小于 16 字节，当前 %d 字节", len(c.Security.DataKey))
	}
	if len(c.Security.HMACKey) < 16 {
		return fmt.Errorf("security.hmac_key 长度需不小于 16 字节，当前 %d 字节", len(c.Security.HMACKey))
	}
	if c.Security.DataKey == c.Security.HMACKey {
		return fmt.Errorf("security.data_key 与 security.hmac_key 必须不同")
	}
	if c.SMS.CodeLength < 4 || c.SMS.CodeLength > 8 {
		return fmt.Errorf("sms.code_length 需在 4~8 之间，当前 %d", c.SMS.CodeLength)
	}
	if c.Realname.Enabled && c.Realname.Provider == "tencent" && c.Realname.Tencent.SecretID == "" {
		return fmt.Errorf("实名通道为 tencent 时 realname.tencent.secret_id/secret_key 不能为空")
	}
	if c.SMS.Provider == "tencent" && c.SMS.Tencent.SecretID == "" {
		return fmt.Errorf("短信通道为 tencent 时 sms.tencent.secret_id/secret_key 不能为空")
	}
	if c.IM.MessageMaxChars < 1 || c.IM.MessageMaxChars > 2000 {
		return fmt.Errorf("im.message_max_chars 需在 1~2000 之间，当前 %d", c.IM.MessageMaxChars)
	}
	if c.IM.CallbackMaxSkew.D() < 10*time.Second || c.IM.CallbackMaxSkew.D() > 10*time.Minute {
		return fmt.Errorf("im.callback_max_skew 需在 10s~10m 之间")
	}
	if c.IM.RecallWindow.D() <= 0 || c.IM.RecallWindow.D() > 24*time.Hour {
		return fmt.Errorf("im.recall_window 需在 0~24h 之间")
	}
	// 生产严禁沿用 dev 配置里的示例密钥。这类错误不会报错、只会安静地"能跑"，
	// 直到数据泄密才被发现，所以必须在启动期硬拦。
	if c.IsProd() {
		if err := rejectDevSecret("jwt.secret", c.JWT.Secret); err != nil {
			return err
		}
		if err := rejectDevSecret("security.data_key", c.Security.DataKey); err != nil {
			return err
		}
		if err := rejectDevSecret("security.hmac_key", c.Security.HMACKey); err != nil {
			return err
		}
		// 生产已禁用 local_fallback，IM 必须是真实可用的：缺密钥等于消息全丢。
		if c.IM.SecretKey == "" {
			return fmt.Errorf("生产环境 im.secret_key 不能为空")
		}
		if c.IM.SdkAppID <= 0 {
			return fmt.Errorf("生产环境 im.sdk_app_id 必须为正整数")
		}
		if !c.IM.ImportEnabled {
			return fmt.Errorf("生产环境需开启 im.import_enabled，否则用户无法被导入 IM")
		}
	}
	if c.IsProd() && c.IM.LocalFallback {
		return fmt.Errorf("生产环境禁止开启 im.local_fallback")
	}
	if c.IsProd() && c.IM.CallbackToken == "" {
		return fmt.Errorf("生产环境 im.callback_token 不能为空")
	}
	if c.IM.CallbackToken != "" && c.IM.SdkAppID <= 0 {
		return fmt.Errorf("配置 im.callback_token 时 sdk_app_id 必须为正整数")
	}
	// —— P6：通话 ——
	pol := c.Call.Policy()
	if c.Call.RingTimeoutSec < 0 || c.Call.HeartbeatIntervalSec < 0 ||
		c.Call.HeartbeatTimeoutSec < 0 || c.Call.MaxDurationSec < 0 {
		return fmt.Errorf("call 下的时长参数不能为负数")
	}
	if pol.HeartbeatTimeoutSec <= pol.HeartbeatIntervalSec {
		return fmt.Errorf("call.heartbeat_timeout_sec 必须大于 call.heartbeat_interval_sec，否则正常心跳也会被判掉线")
	}
	if pol.MaxDurationSec < 60 {
		return fmt.Errorf("call.max_duration_sec 不能小于 60 秒")
	}
	// —— P7：附近的人 ——
	np := c.Nearby.Policy()
	if np.DefaultRadiusKM > np.MaxRadiusKM {
		return fmt.Errorf("nearby.default_radius_km 不能大于 max_radius_km")
	}
	if np.OnlineTTLSec < 30 {
		return fmt.Errorf("nearby.online_ttl_sec 不能小于 30 秒，否则正常心跳也会被判离线")
	}
	if c.IsProd() && c.TRTC.MockEnabled {
		return fmt.Errorf("生产环境禁止开启 trtc.mock_enabled")
	}
	if c.IsProd() {
		if err := rejectDevSecret("trtc.secret_key", c.TRTC.SecretKey); err != nil {
			return err
		}
		if c.TRTC.SdkAppID <= 0 {
			return fmt.Errorf("生产环境 trtc.sdk_app_id 必须为正整数")
		}
	}
	switch c.Upload.Provider {
	case "local":
		if c.Upload.Local.Dir == "" || c.Upload.Local.URLPrefix == "" {
			return fmt.Errorf("upload.provider=local 时 upload.local.dir 与 url_prefix 不能为空")
		}
	case "cos":
		if c.Upload.COS.SecretID == "" || c.Upload.COS.SecretKey == "" ||
			c.Upload.COS.Bucket == "" || c.Upload.COS.Host == "" {
			return fmt.Errorf("upload.provider=cos 时 upload.cos 的 secret_id/secret_key/bucket/host 不能为空")
		}
	default:
		return fmt.Errorf("未知的上传通道: %q（可选 local / cos）", c.Upload.Provider)
	}
	return nil
}
