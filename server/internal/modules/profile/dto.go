package profile

// ProfileInput 资料编辑入参。
//
// 全部用指针表达"客户端是否传了这个字段"：
// 资料编辑是局部更新，用零值判断会把"把签名清空"和"不改签名"混为一谈。
type ProfileInput struct {
	Nickname     *string   `json:"nickname"`
	Gender       *int      `json:"gender"`
	Birthday     *string   `json:"birthday"` // YYYY-MM-DD
	CityCode     *string   `json:"city_code"`
	Height       *int      `json:"height"`
	Job          *string   `json:"job"`
	Bio          *string   `json:"bio"`
	Tags         *[]string `json:"tags"`
	Photos       *[]string `json:"photos"`
	VoiceIntro   *string   `json:"voice_intro"`
	PriceAudio   *int      `json:"price_audio"`
	PriceVideo   *int      `json:"price_video"`
	PriceEnabled *int      `json:"price_enabled"`
}

// ProfileView 我的资料（含可编辑字段与生效定价）。
type ProfileView struct {
	UID            int64    `json:"uid"`
	Nickname       string   `json:"nickname"`
	Avatar         string   `json:"avatar"`
	Gender         int      `json:"gender"`
	GenderText     string   `json:"gender_text"`
	Birthday       string   `json:"birthday,omitempty"` // YYYY-MM-DD
	Age            int      `json:"age"`
	CityCode       string   `json:"city_code"`
	CityName       string   `json:"city_name"`
	Height         int      `json:"height"`
	Job            string   `json:"job"`
	Bio            string   `json:"bio"`
	Tags           []string `json:"tags"`
	Photos         []string `json:"photos"`
	VoiceIntro     string   `json:"voice_intro"`
	PriceEnabled   int      `json:"price_enabled"`
	PriceAudio     int      `json:"price_audio"` // 生效价：自定价优先，否则全局价
	PriceVideo     int      `json:"price_video"`
	PriceSelfAudio int      `json:"price_self_audio"` // 自定价原值，0 表示跟随全局
	PriceSelfVideo int      `json:"price_self_video"`
	RealnameStatus int      `json:"realname_status"`
	RegisteredAt   int64    `json:"registered_at"`
	UpdatedAt      int64    `json:"updated_at"`
}

// Relation 访问者与被访问者之间的关系。
// 拉黑关系返回给客户端而不是直接 403：主页仍然可看，
// 由客户端禁用"打招呼/通话"按钮，比整页报错的体验好。
type Relation struct {
	IsSelf    bool `json:"is_self"`
	Blocked   bool `json:"blocked"`    // 我拉黑了对方
	BlockedBy bool `json:"blocked_by"` // 对方拉黑了我
}

// PublicProfileView 他人主页视图。
type PublicProfileView struct {
	UID            int64    `json:"uid"`
	Nickname       string   `json:"nickname"`
	Avatar         string   `json:"avatar"`
	Gender         int      `json:"gender"`
	GenderText     string   `json:"gender_text"`
	Age            int      `json:"age"`
	CityCode       string   `json:"city_code"`
	CityName       string   `json:"city_name"`
	Height         int      `json:"height"`
	Job            string   `json:"job"`
	Bio            string   `json:"bio"`
	Tags           []string `json:"tags"`
	Photos         []string `json:"photos"`
	VoiceIntro     string   `json:"voice_intro"`
	PriceAudio     int      `json:"price_audio"`
	PriceVideo     int      `json:"price_video"`
	Online         bool     `json:"online"`
	LastActiveAt   int64    `json:"last_active_at"`
	LastActiveText string   `json:"last_active_text"`
	RealnameStatus int      `json:"realname_status"`
	RegisteredAt   int64    `json:"registered_at"`
	Relation       Relation `json:"relation"`
}

// CityItem 城市字典项（客户端选择城市用）。
type CityItem struct {
	Code     string `json:"code"`
	Province string `json:"province"`
	Name     string `json:"name"`
}

// UploadSign 直传凭证。
// Provider=cos 时客户端按 FieldOrder 组装 multipart 直传对象存储；
// Provider=local 时改为调用 /v1/uploads/local 上传文件本体。
type UploadSign struct {
	Provider     string            `json:"provider"`
	Host         string            `json:"host,omitempty"`
	Key          string            `json:"key"`
	URL          string            `json:"url"`
	ExpireAt     int64             `json:"expire_at,omitempty"`
	Fields       map[string]string `json:"fields,omitempty"`
	FieldOrder   []string          `json:"field_order,omitempty"`
	MaxBytes     int64             `json:"max_bytes"`
	AllowedTypes []string          `json:"allowed_types"`
	UploadPath   string            `json:"upload_path,omitempty"` // local 模式下的上传接口
}

// UploadedFile 上传完成后的文件信息。
type UploadedFile struct {
	URL     string `json:"url"`
	Key     string `json:"key"`
	Size    int64  `json:"size"`
	Mime    string `json:"mime"`
	Status  int    `json:"status"`
	Scene   int    `json:"scene"`
	Storage int    `json:"storage"`
}

// HeartbeatResult 心跳结果。
type HeartbeatResult struct {
	Online    bool  `json:"online"`
	KeepAlive int64 `json:"keep_alive"` // 在线状态保留秒数
}
