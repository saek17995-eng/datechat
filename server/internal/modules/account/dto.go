package account

// SendCodeInput 发送验证码入参。
type SendCodeInput struct {
	Phone string
	Scene string // login / register / bind，仅用于审计
	IP    string
}

// SendCodeOutput 发送验证码出参。
type SendCodeOutput struct {
	ExpireIn  int    `json:"expire_in"` // 验证码有效秒数
	ResendIn  int    `json:"resend_in"` // 多少秒后可再次发送
	DebugCode string `json:"debug_code,omitempty"`
}

// LoginInput 手机号 + 验证码登录入参。未注册的手机号会自动注册。
type LoginInput struct {
	Phone      string
	Code       string
	DeviceID   string
	Platform   int
	AppVersion string
	PushToken  string
	IP         string
	UserAgent  string
}

// TokenPair 双令牌。
type TokenPair struct {
	AccessToken     string `json:"access_token"`
	AccessExpireAt  int64  `json:"access_expire_at"`
	RefreshToken    string `json:"refresh_token"`
	RefreshExpireAt int64  `json:"refresh_expire_at"`
}

// IMInfo 下发给客户端的 IM 登录材料。
// Enabled=false 表示服务端未配置 IM 密钥，客户端应跳过 IM 登录（本地联调用）。
type IMInfo struct {
	Enabled  bool   `json:"enabled"`
	SDKAppID int    `json:"sdk_app_id,omitempty"`
	UserID   string `json:"user_id,omitempty"`
	UserSig  string `json:"usersig,omitempty"`
	ExpireAt int64  `json:"usersig_expire_at,omitempty"`
}

// UserBrief 登录/我的页面共用的用户摘要。
type UserBrief struct {
	UID            int64  `json:"uid"`
	Nickname       string `json:"nickname"`
	Avatar         string `json:"avatar"`
	Gender         int    `json:"gender"`
	PhoneMasked    string `json:"phone_masked"`
	RealnameStatus int    `json:"realname_status"`
	RegisteredAt   int64  `json:"registered_at,omitempty"`
}

// LoginOutput 登录结果。
type LoginOutput struct {
	UID       int64     `json:"uid"`
	IsNewUser bool      `json:"is_new_user"`
	Token     TokenPair `json:"token"`
	User      UserBrief `json:"user"`
	IM        IMInfo    `json:"im"`
}
