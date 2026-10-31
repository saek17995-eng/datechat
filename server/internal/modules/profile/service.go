// Package profile 负责用户资料、头像上传与他人主页。
//
// 设计约束：
//  1. 资料编辑是局部更新：用指针区分"没传"和"传了空字符串"（清空签名是合法诉求）；
//  2. 昵称/签名/职业/标签一律过敏感词：拦截级直接拒绝，替换级自动打码，转人工级落审核记录；
//  3. 头像不接受任意外链：必须先在 upload_file 里存在我们签发/落盘的记录，
//     否则任何人都能把自己的头像设成任意 URL（钓鱼、盗图、违规图片挂载）；
//  4. 在线状态只落 Redis（带 TTL 的心跳），不写库——高频写 user.last_active_at 会把主库打满。
package profile

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif" // 注册 GIF 解码器：image.DecodeConfig 依赖匿名导入来识别格式
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/datechat/server/internal/config"
	"github.com/datechat/server/internal/modules/moderation"
	"github.com/datechat/server/internal/pkg/cos"
	"github.com/datechat/server/internal/pkg/crypto"
	"github.com/datechat/server/internal/pkg/errs"
	"github.com/datechat/server/internal/pkg/presence"
	"github.com/datechat/server/internal/pkg/textfilter"
)

// 资料字段长度与取值约束。
// 单独定义成常量而不是散落在校验代码里，方便与客户端表单的 maxLength 对齐。
const (
	maxNicknameLen = 16
	maxBioLen      = 200
	maxJobLen      = 32
	maxTags        = 6
	maxTagLen      = 8
	maxPhotos      = 9
	minHeight      = 80
	maxHeight      = 250
	minAge         = 18
	maxAge         = 100
	maxPricePerMin = 10000
)

// 场景目录与在线状态参数。
//
// 在线键前缀来自 internal/pkg/presence：附近的人模块要读同一批键，
// 两边各写一份常量迟早会漂移，漂移的表现是"所有人永远离线"。
const (
	presenceKeepAlive     = 60 * time.Second
	heartbeatWriteDBEvery = 2 * time.Minute // 心跳写库的最小间隔，避免高频更新主表
)

func (s *Service) presenceTTL() time.Duration {
	if s.cfg == nil || s.cfg.Nearby.OnlineTTLSec <= 0 {
		return time.Duration(presence.DefaultTTLSec) * time.Second
	}
	return time.Duration(s.cfg.Nearby.OnlineTTLSec) * time.Second
}

// Service 资料领域服务。
type Service struct {
	cfg    *config.Config
	log    *zap.Logger
	repo   *Repository
	rdb    *redis.Client
	filter *textfilter.Filter
	mod    TextModerator
	now    func() time.Time
}

// Deps 构造 Service 所需依赖。
type Deps struct {
	Config *config.Config
	Log    *zap.Logger
	Repo   *Repository
	Redis  *redis.Client
	Filter *textfilter.Filter
	// Mod 统一内容安全服务（P4）。为空时退回本地过滤。
	Mod TextModerator
}

// NewService 构造服务。filter 为空时退化为"不过滤"，保证服务可用（词库加载失败不应拖垮资料编辑）。
func NewService(d Deps) *Service {
	log := d.Log
	if log == nil {
		log = zap.NewNop()
	}
	filter := d.Filter
	if filter == nil {
		filter = textfilter.New()
	}
	return &Service{
		cfg:    d.Config,
		log:    log,
		repo:   d.Repo,
		rdb:    d.Redis,
		filter: filter,
		mod:    d.Mod,
		now:    time.Now,
	}
}

// MeProfile 返回我的完整资料。
func (s *Service) MeProfile(ctx context.Context, uid int64) (*ProfileView, error) {
	u, p, err := s.load(ctx, uid)
	if err != nil {
		return nil, err
	}
	return s.toProfileView(ctx, u, p), nil
}

// UpdateProfile 局部更新我的资料。
//
// 返回更新后的完整视图：客户端不必再拉一次，也避免"保存成功但界面还是旧值"的错觉。
func (s *Service) UpdateProfile(ctx context.Context, uid int64, in ProfileInput) (*ProfileView, error) {
	u, p, err := s.load(ctx, uid)
	if err != nil {
		return nil, err
	}

	dirty := false
	if in.Nickname != nil {
		nick := strings.TrimSpace(*in.Nickname)
		if nick == "" {
			return nil, errs.ErrInvalidParam.WithMsg("昵称不能为空")
		}
		if utf8.RuneCountInString(nick) > maxNicknameLen {
			return nil, errs.ErrInvalidParam.WithMsg("昵称最多 %d 个字", maxNicknameLen)
		}
		blocked, review, nick2 := s.checkText(ctx, "nickname", uid, nick)
		if blocked {
			return nil, errs.ErrSensitiveWord.WithMsg("昵称包含违规内容，请修改后重试")
		}
		s.recordReview(ctx, uid, "nickname", review, nil)
		u.Nickname = nick2
		dirty = true
	}
	if in.Gender != nil {
		if *in.Gender != GenderMale && *in.Gender != GenderFemale {
			return nil, errs.ErrInvalidParam.WithMsg("性别取值不合法")
		}
		u.Gender = *in.Gender
		dirty = true
	}
	if in.Birthday != nil {
		birth := strings.TrimSpace(*in.Birthday)
		if birth != "" {
			t, perr := time.ParseInLocation("2006-01-02", birth, time.Local)
			if perr != nil {
				return nil, errs.ErrInvalidParam.WithMsg("生日格式应为 YYYY-MM-DD")
			}
			age := calcAge(t, s.now())
			if age < minAge || age > maxAge {
				return nil, errs.ErrInvalidParam.WithMsg("年龄需在 %d~%d 岁之间", minAge, maxAge)
			}
			u.Birthday = &t
			dirty = true
		}
	}
	if in.CityCode != nil {
		code := strings.TrimSpace(*in.CityCode)
		if code != "" {
			city, cerr := s.repo.FindCity(ctx, code)
			if cerr != nil {
				return nil, errs.ErrInternal.WithErr(cerr)
			}
			if city == nil {
				return nil, errs.ErrInvalidParam.WithMsg("城市不存在或已下线")
			}
		}
		u.CityCode = code
		dirty = true
	}
	if in.Height != nil {
		if *in.Height != 0 && (*in.Height < minHeight || *in.Height > maxHeight) {
			return nil, errs.ErrInvalidParam.WithMsg("身高需在 %d~%d cm 之间", minHeight, maxHeight)
		}
		p.Height = *in.Height
		dirty = true
	}
	if in.Job != nil {
		job := strings.TrimSpace(*in.Job)
		if utf8.RuneCountInString(job) > maxJobLen {
			return nil, errs.ErrInvalidParam.WithMsg("职业最多 %d 个字", maxJobLen)
		}
		blocked, review, job2 := s.checkText(ctx, "job", uid, job)
		if blocked {
			return nil, errs.ErrSensitiveWord.WithMsg("职业包含违规内容，请修改后重试")
		}
		s.recordReview(ctx, uid, "job", review, nil)
		p.Job = job2
		dirty = true
	}
	if in.Bio != nil {
		bio := strings.TrimSpace(*in.Bio)
		if utf8.RuneCountInString(bio) > maxBioLen {
			return nil, errs.ErrInvalidParam.WithMsg("签名最多 %d 个字", maxBioLen)
		}
		blocked, review, bio2 := s.checkText(ctx, "bio", uid, bio)
		if blocked {
			return nil, errs.ErrSensitiveWord.WithMsg("签名包含违规内容，请修改后重试")
		}
		s.recordReview(ctx, uid, "bio", review, nil)
		p.Bio = bio2
		dirty = true
	}
	if in.Tags != nil {
		tags, terr := s.sanitizeTags(*in.Tags)
		if terr != nil {
			return nil, terr
		}
		p.Tags = tags
		dirty = true
	}
	if in.Photos != nil {
		photos, perr := s.sanitizePhotos(ctx, uid, *in.Photos)
		if perr != nil {
			return nil, perr
		}
		p.Photos = photos
		dirty = true
	}
	if in.VoiceIntro != nil {
		voice := strings.TrimSpace(*in.VoiceIntro)
		if voice != "" && !s.allowedFileURL(voice) {
			return nil, errs.ErrInvalidParam.WithMsg("语音地址不合法")
		}
		p.VoiceIntro = voice
		dirty = true
	}
	if in.PriceAudio != nil {
		if *in.PriceAudio < 0 || *in.PriceAudio > maxPricePerMin {
			return nil, errs.ErrInvalidParam.WithMsg("语音定价需在 0~%d 金币/分钟之间", maxPricePerMin)
		}
		p.PriceAudio = *in.PriceAudio
		dirty = true
	}
	if in.PriceVideo != nil {
		if *in.PriceVideo < 0 || *in.PriceVideo > maxPricePerMin {
			return nil, errs.ErrInvalidParam.WithMsg("视频定价需在 0~%d 金币/分钟之间", maxPricePerMin)
		}
		p.PriceVideo = *in.PriceVideo
		dirty = true
	}
	if in.PriceEnabled != nil {
		if *in.PriceEnabled != 0 && *in.PriceEnabled != 1 {
			return nil, errs.ErrInvalidParam.WithMsg("自定价开关取值不合法")
		}
		p.PriceEnabled = *in.PriceEnabled
		dirty = true
	}

	if !dirty {
		return s.toProfileView(ctx, u, p), nil
	}
	if err := s.repo.SaveUserProfile(ctx, u, p); err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	return s.toProfileView(ctx, u, p), nil
}

// SetAvatar 设置头像。
//
// 校验链路：URL 合法性 → 必须是本站签发/落盘的文件 → 属于当前用户 → 未被审核拒绝。
// 少了任何一环，头像都会变成"任意外链挂载点"（钓鱼图片、违规图片、盗链）。
func (s *Service) SetAvatar(ctx context.Context, uid int64, avatarURL string) (*ProfileView, error) {
	raw := strings.TrimSpace(avatarURL)
	if raw == "" {
		return nil, errs.ErrInvalidParam.WithMsg("头像地址不能为空")
	}
	if !s.allowedFileURL(raw) {
		return nil, errs.ErrInvalidParam.WithMsg("头像地址不合法，请先通过上传接口获取")
	}
	key := s.objectKeyOf(raw)
	file, err := s.repo.FindUploadByKey(ctx, key)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	if file == nil {
		return nil, errs.ErrInvalidParam.WithMsg("头像文件不存在，请重新上传")
	}
	if file.UserID != uid {
		// 别人的文件不能拿来当自己的头像：既防越权，也避免一个违规文件被多个账号复用。
		s.log.Warn("跨用户引用上传文件", zap.Int64("uid", uid), zap.String("key", key))
		return nil, errs.ErrForbidden.WithMsg("该文件不属于当前账号")
	}
	if file.Status == FileStatusRejected {
		return nil, errs.ErrSensitiveWord.WithMsg("该图片未通过审核，请更换后重试")
	}
	if file.Scene != SceneAvatar && file.Scene != ScenePhoto {
		return nil, errs.ErrInvalidParam.WithMsg("该文件不能用作头像")
	}

	status := FileStatusPassed
	if s.cfg.Upload.AvatarReview {
		status = FileStatusPending
	}
	if err := s.repo.MarkUploadUsed(ctx, file.ID, status); err != nil {
		s.log.Warn("标记头像文件已使用失败", zap.Error(err))
	}
	if err := s.repo.UpdateAvatar(ctx, uid, raw); err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	s.recordReview(ctx, uid, "avatar", status == FileStatusPending, nil)

	u, p, err := s.load(ctx, uid)
	if err != nil {
		return nil, err
	}
	return s.toProfileView(ctx, u, p), nil
}

// PublicProfile 他人主页。
func (s *Service) PublicProfile(ctx context.Context, viewerID, uid int64) (*PublicProfileView, error) {
	u, p, err := s.loadForView(ctx, uid)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, errs.ErrUserNotFound
	}

	view := &PublicProfileView{
		UID:            u.ID,
		Nickname:       u.Nickname,
		Avatar:         u.Avatar,
		Gender:         u.Gender,
		GenderText:     genderText(u.Gender),
		CityCode:       u.CityCode,
		Height:         p.Height,
		Job:            p.Job,
		Bio:            p.Bio,
		Tags:           p.Tags,
		Photos:         p.Photos,
		VoiceIntro:     p.VoiceIntro,
		RealnameStatus: p.RealnameStatus,
		RegisteredAt:   u.CreatedAt.Unix(),
	}
	if u.Birthday != nil {
		view.Age = calcAge(*u.Birthday, s.now())
	}
	if u.CityCode != "" {
		if city, cerr := s.repo.FindCity(ctx, u.CityCode); cerr == nil && city != nil {
			view.CityName = city.Name
		} else if cerr != nil {
			s.log.Warn("查询城市失败", zap.String("code", u.CityCode), zap.Error(cerr))
		}
	}
	if gp, gerr := s.repo.FindGlobalPrice(ctx); gerr == nil && gp != nil {
		view.PriceAudio, view.PriceVideo = effectivePrice(p, gp)
	} else if gerr != nil {
		s.log.Warn("读取全局定价失败", zap.Error(gerr))
	} else {
		view.PriceAudio, view.PriceVideo = p.PriceAudio, p.PriceVideo
	}
	if u.LastActiveAt != nil {
		view.LastActiveAt = u.LastActiveAt.Unix()
	}
	view.Online = s.online(ctx, uid)
	view.LastActiveText = lastActiveText(view.Online, u.LastActiveAt, s.now())

	view.Relation.IsSelf = viewerID == uid
	if !view.Relation.IsSelf && viewerID > 0 {
		blocked, blockedBy, rerr := s.repo.BlockRelation(ctx, viewerID, uid)
		if rerr != nil {
			s.log.Warn("查询拉黑关系失败", zap.Error(rerr))
		} else {
			view.Relation.Blocked = blocked
			view.Relation.BlockedBy = blockedBy
		}
		// 看别人主页才记访问；自己看自己不算，被对方拉黑也不写（避免骚扰痕迹）。
		if !blockedBy {
			if verr := s.repo.UpsertVisit(ctx, viewerID, uid, s.now()); verr != nil {
				s.log.Warn("记录访问失败", zap.Int64("viewer", viewerID), zap.Int64("target", uid), zap.Error(verr))
			}
		}
	}
	return view, nil
}

// Heartbeat 上报在线心跳。
// 只写 Redis：在线状态是高频、可丢失的数据，落到主库等于给 MySQL 加压。
func (s *Service) Heartbeat(ctx context.Context, uid int64) (*HeartbeatResult, error) {
	if s.rdb == nil {
		return nil, errs.ErrInternal.WithMsg("服务未就绪，请稍后再试")
	}
	key := presence.Key(uid)
	if err := s.rdb.Set(ctx, key, s.now().Unix(), s.presenceTTL()).Err(); err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}

	// 主表活跃时间按最小间隔刷新：列表排序要用，但不需要每次心跳都写。
	coolKey := key + ":db"
	if fresh, ferr := s.rdb.SetNX(ctx, coolKey, "1", heartbeatWriteDBEvery).Result(); ferr == nil && fresh {
		if derr := s.repo.TouchActive(ctx, uid, s.now()); derr != nil {
			s.log.Warn("刷新活跃时间失败", zap.Int64("uid", uid), zap.Error(derr))
		}
	}
	return &HeartbeatResult{Online: true, KeepAlive: int64(presenceKeepAlive.Seconds())}, nil
}

// Cities 返回城市字典。
func (s *Service) Cities(ctx context.Context) ([]CityItem, error) {
	list, err := s.repo.ListCities(ctx, 0)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	out := make([]CityItem, 0, len(list))
	for _, c := range list {
		out = append(out, CityItem{Code: c.Code, Province: c.Province, Name: c.Name})
	}
	return out, nil
}

// UploadLocal 接收文件并落到本地磁盘（仅 local 通道）。
//
// 文件内容类型以 magic bytes 嗅探为准，不信客户端的 Content-Type——
// 客户端传什么都行，服务端必须自己判断这是不是一张图片。
func (s *Service) UploadLocal(ctx context.Context, uid int64, scene int, file *multipart.FileHeader) (*UploadedFile, error) {
	if s.cfg.Upload.Provider != "local" {
		return nil, errs.ErrInvalidParam.WithMsg("当前未启用本地上传通道")
	}
	if file == nil {
		return nil, errs.ErrInvalidParam.WithMsg("缺少上传文件")
	}
	if file.Size > s.cfg.Upload.MaxImageBytes() {
		return nil, errs.ErrInvalidParam.WithMsg("文件超过 %d MB 上限", s.cfg.Upload.MaxImageMB)
	}
	if file.Size <= 0 {
		return nil, errs.ErrInvalidParam.WithMsg("文件内容为空")
	}

	src, err := file.Open()
	if err != nil {
		return nil, errs.ErrInvalidParam.WithMsg("读取上传文件失败")
	}
	defer func() { _ = src.Close() }()

	data, err := io.ReadAll(io.LimitReader(src, s.cfg.Upload.MaxImageBytes()+1))
	if err != nil {
		return nil, errs.ErrInvalidParam.WithMsg("读取上传文件失败")
	}
	if int64(len(data)) > s.cfg.Upload.MaxImageBytes() {
		return nil, errs.ErrInvalidParam.WithMsg("文件超过 %d MB 上限", s.cfg.Upload.MaxImageMB)
	}

	mime := http.DetectContentType(data)
	if !strings.HasPrefix(mime, "image/") {
		return nil, errs.ErrInvalidParam.WithMsg("只支持上传图片")
	}

	ext := extOfMime(mime)
	key, err := s.newObjectKey(scene, uid, ext)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	absPath := filepath.Join(s.cfg.Upload.Local.Dir, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return nil, errs.ErrInternal.WithErr(fmt.Errorf("profile: 创建上传目录失败: %w", err))
	}
	if err := os.WriteFile(absPath, data, 0o644); err != nil {
		return nil, errs.ErrInternal.WithErr(fmt.Errorf("profile: 写入上传文件失败: %w", err))
	}

	width, height := imageSize(data)
	sum := sha256.Sum256(data)
	record := &UploadFile{
		UserID:    uid,
		Scene:     scene,
		Storage:   StorageLocal,
		URL:       s.cfg.Upload.PublicBaseURL() + "/" + key,
		ObjectKey: key,
		FileSize:  int64(len(data)),
		MimeType:  mime,
		Width:     width,
		Height:    height,
		Checksum:  hex.EncodeToString(sum[:]),
		Status:    FileStatusPassed,
	}
	if err := s.repo.InsertUploadFile(ctx, record); err != nil {
		// 落盘成功但台账失败：留下孤儿文件比留下"能用但没记录的文件"更安全，
		// 因为后者会让头像校验（必须存在台账）直接失效。
		_ = os.Remove(absPath)
		return nil, errs.ErrInternal.WithErr(err)
	}
	return &UploadedFile{
		URL:     record.URL,
		Key:     record.ObjectKey,
		Size:    record.FileSize,
		Mime:    record.MimeType,
		Status:  record.Status,
		Scene:   record.Scene,
		Storage: record.Storage,
	}, nil
}

// COSSign 签发对象存储直传凭证，并预登记文件台账。
// 客户端直传完成后，用同一个 URL 调 SetAvatar 提交。
func (s *Service) COSSign(ctx context.Context, uid int64, scene int, ext string) (*UploadSign, error) {
	if s.cfg.Upload.Provider != "cos" {
		return nil, errs.ErrInvalidParam.WithMsg("当前上传通道不是对象存储")
	}
	cred := cos.Credential{SecretID: s.cfg.Upload.COS.SecretID, SecretKey: s.cfg.Upload.COS.SecretKey}
	if !cred.Valid() {
		return nil, errs.ErrServiceBusy.WithMsg("对象存储未配置，请联系管理员")
	}
	if ext == "" {
		ext = "jpg"
	}
	key, err := s.newObjectKey(scene, uid, ext)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	now := s.now()
	signed, err := cos.SignPostObject(cred, now, cos.PostObjectInput{
		Bucket:        s.cfg.Upload.COS.Bucket,
		Region:        s.cfg.Upload.COS.Region,
		Key:           key,
		ContentType:   "image/",
		MaxBytes:      s.cfg.Upload.MaxImageBytes(),
		Expire:        s.cfg.Upload.COS.SignExpire.D(),
		SuccessStatus: "200",
	})
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	publicURL := cos.PublicURL(s.cfg.Upload.COS.CDNDomain, s.cfg.Upload.COS.Host, key)

	record := &UploadFile{
		UserID:    uid,
		Scene:     scene,
		Storage:   StorageCOS,
		URL:       publicURL,
		ObjectKey: key,
		Status:    FileStatusPassed,
	}
	if s.cfg.Upload.AvatarReview {
		record.Status = FileStatusPending
	}
	if err := s.repo.InsertUploadFile(ctx, record); err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	return &UploadSign{
		Provider:     "cos",
		Host:         signed.Host,
		Key:          signed.Key,
		URL:          publicURL,
		ExpireAt:     signed.ExpireAt,
		Fields:       signed.Fields,
		FieldOrder:   signed.FieldOrder,
		MaxBytes:     signed.MaxBytes,
		AllowedTypes: signed.AllowedTypes,
	}, nil
}

// UploadSign 返回当前通道的上传方式说明。
// local 通道直接返回上传接口路径，客户端不用区分配置就能走通流程。
func (s *Service) UploadSign(ctx context.Context, uid int64, scene int, ext string) (*UploadSign, error) {
	if s.cfg.Upload.Provider == "cos" {
		return s.COSSign(ctx, uid, scene, ext)
	}
	if ext == "" {
		ext = "jpg"
	}
	key, err := s.newObjectKey(scene, uid, ext)
	if err != nil {
		return nil, errs.ErrInternal.WithErr(err)
	}
	return &UploadSign{
		Provider:     "local",
		Key:          key,
		URL:          s.cfg.Upload.PublicBaseURL() + "/" + key,
		UploadPath:   "/v1/uploads/local",
		MaxBytes:     s.cfg.Upload.MaxImageBytes(),
		AllowedTypes: []string{"image/jpeg", "image/png", "image/webp"},
	}, nil
}

// —— 内部实现 ——

// load 取用户与资料，用户不存在返回 ErrUserNotFound。
func (s *Service) load(ctx context.Context, uid int64) (*User, *UserProfile, error) {
	u, err := s.repo.FindUser(ctx, uid)
	if err != nil {
		return nil, nil, errs.ErrInternal.WithErr(err)
	}
	if u == nil {
		return nil, nil, errs.ErrUserNotFound
	}
	if u.Status != StatusNormal {
		return nil, nil, errs.ErrUserBanned
	}
	p, err := s.repo.FindProfile(ctx, uid)
	if err != nil {
		return nil, nil, errs.ErrInternal.WithErr(err)
	}
	if p == nil {
		// 理论上注册时就建好了；真缺失时补一行，避免整个资料接口 500。
		p = &UserProfile{UserID: uid}
	}
	return u, p, nil
}

// loadForView 与他人主页共用，但把"封禁用户"也按不存在处理（(nil, nil)），
// 让调用方自行决定文案，避免对外暴露账号状态。
func (s *Service) loadForView(ctx context.Context, uid int64) (*User, *UserProfile, error) {
	u, err := s.repo.FindUser(ctx, uid)
	if err != nil {
		return nil, nil, errs.ErrInternal.WithErr(err)
	}
	if u == nil || u.Status != StatusNormal {
		return nil, nil, nil
	}
	p, err := s.repo.FindProfile(ctx, uid)
	if err != nil {
		return nil, nil, errs.ErrInternal.WithErr(err)
	}
	if p == nil {
		p = &UserProfile{UserID: uid}
	}
	return u, p, nil
}

// toProfileView 组装我的资料视图。
func (s *Service) toProfileView(ctx context.Context, u *User, p *UserProfile) *ProfileView {
	view := &ProfileView{
		UID:            u.ID,
		Nickname:       u.Nickname,
		Avatar:         u.Avatar,
		Gender:         u.Gender,
		GenderText:     genderText(u.Gender),
		CityCode:       u.CityCode,
		Height:         p.Height,
		Job:            p.Job,
		Bio:            p.Bio,
		Tags:           p.Tags,
		Photos:         p.Photos,
		VoiceIntro:     p.VoiceIntro,
		PriceEnabled:   p.PriceEnabled,
		PriceSelfAudio: p.PriceAudio,
		PriceSelfVideo: p.PriceVideo,
		PriceAudio:     p.PriceAudio,
		PriceVideo:     p.PriceVideo,
		RealnameStatus: p.RealnameStatus,
		RegisteredAt:   u.CreatedAt.Unix(),
		UpdatedAt:      p.UpdatedAt.Unix(),
	}
	if u.Birthday != nil {
		view.Birthday = u.Birthday.Format("2006-01-02")
		view.Age = calcAge(*u.Birthday, s.now())
	}
	if u.CityCode != "" {
		if city, err := s.repo.FindCity(ctx, u.CityCode); err == nil && city != nil {
			view.CityName = city.Name
		}
	}
	if gp, err := s.repo.FindGlobalPrice(ctx); err == nil && gp != nil {
		view.PriceAudio, view.PriceVideo = effectivePrice(p, gp)
	}
	return view
}

// sanitizeTags 清洗标签：去空白、去重、限长、限数量，并过敏感词。
func (s *Service) sanitizeTags(in []string) (StringList, error) {
	tags := make(StringList, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, raw := range in {
		tag := strings.TrimSpace(raw)
		if tag == "" {
			continue
		}
		if utf8.RuneCountInString(tag) > maxTagLen {
			return nil, errs.ErrInvalidParam.WithMsg("单个标签最多 %d 个字", maxTagLen)
		}
		// 标签走本地 DFA：sanitizeTags 是纯函数（便于单测），不引入 ctx/IO 依赖。
		checked := s.filter.Check(tag)
		if checked.Blocked {
			return nil, errs.ErrSensitiveWord.WithMsg("标签包含违规内容，请修改后重试")
		}
		tag = checked.Text
		if _, dup := seen[tag]; dup {
			continue
		}
		seen[tag] = struct{}{}
		tags = append(tags, tag)
		if len(tags) >= maxTags {
			break
		}
	}
	if len(tags) > maxTags {
		tags = tags[:maxTags]
	}
	return tags, nil
}

// sanitizePhotos 校验相册：数量上限 + 地址必须来自本站上传记录。
func (s *Service) sanitizePhotos(ctx context.Context, uid int64, in []string) (StringList, error) {
	if len(in) > maxPhotos {
		return nil, errs.ErrInvalidParam.WithMsg("相册最多 %d 张", maxPhotos)
	}
	photos := make(StringList, 0, len(in))
	for _, raw := range in {
		u := strings.TrimSpace(raw)
		if u == "" {
			continue
		}
		if !s.allowedFileURL(u) {
			return nil, errs.ErrInvalidParam.WithMsg("相册图片地址不合法，请先上传")
		}
		file, err := s.repo.FindUploadByKey(ctx, s.objectKeyOf(u))
		if err != nil {
			return nil, errs.ErrInternal.WithErr(err)
		}
		if file == nil || file.UserID != uid {
			return nil, errs.ErrInvalidParam.WithMsg("相册图片不存在，请重新上传")
		}
		if file.Status == FileStatusRejected {
			return nil, errs.ErrSensitiveWord.WithMsg("图片未通过审核，请更换后重试")
		}
		photos = append(photos, u)
		if len(photos) >= maxPhotos {
			break
		}
	}
	return photos, nil
}

// TextModerator 内容安全能力的最小接口（与 chat 模块保持同一契约）。
type TextModerator interface {
	CheckText(ctx context.Context, in moderation.CheckInput) (moderation.Verdict, error)
}

// checkText 统一审核入口：优先用 moderation 服务，未注入时回退本地 DFA。
// 资料类字段（昵称/签名/职业/标签）都走这里，保证审核口径一致。
func (s *Service) checkText(ctx context.Context, scene string, uid int64, text string) (blocked, review bool, out string) {
	if s.mod != nil {
		v, err := s.mod.CheckText(ctx, moderation.CheckInput{
			Scene: scene, BizType: moderation.BizProfile, BizID: fmt.Sprint(uid),
			UID: uid, Text: text, Save: true,
		})
		if err != nil {
			// 审核服务异常时按本地结果兜底，不阻塞用户改资料。
			s.log.Warn("资料审核失败，回退本地过滤", zap.String("scene", scene), zap.Error(err))
		} else {
			return v.Blocked, v.Review, v.Text
		}
	}
	got := s.filter.Check(text)
	return got.Blocked, got.Review, got.Text
}

// recordReview 落一条内容审核记录。
//
// 文本与头像的每次变更都留痕：运营后台要能回答"这个昵称是谁在什么时候改的、当时审核结论是什么"，
// 只记录命中的话，出事后就无从追溯。P4 接入腾讯云审核后，这里改为写第三方结论。
func (s *Service) recordReview(ctx context.Context, uid int64, scene string, review bool, hits []textfilter.Hit) {
	result := 1 // 通过
	label := ""
	var raw *string
	if review {
		result = 3 // 转人工
		label = "review"
	}
	if len(hits) > 0 {
		if b, err := json.Marshal(hits); err == nil {
			v := string(b)
			raw = &v
		}
	}
	if err := s.repo.InsertModeration(ctx, 2, fmt.Sprint(uid), uid, scene, result, label, raw); err != nil {
		s.log.Warn("写审核记录失败", zap.Int64("uid", uid), zap.String("scene", scene), zap.Error(err))
	}
}

// allowedFileURL 判断地址是否属于本站允许的文件域。
func (s *Service) allowedFileURL(raw string) bool {
	if raw == "" || strings.Contains(raw, "..") {
		return false
	}
	base := s.cfg.Upload.PublicBaseURL()
	if strings.HasPrefix(raw, "/") {
		return strings.HasPrefix(raw, base+"/") || strings.HasPrefix(raw, "/static/")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	if parsed.Host == "" {
		return false
	}
	// 本地模式下客户端可能回传完整地址（带 host），只要路径属于静态前缀即可。
	if strings.HasPrefix(parsed.Path, base) || strings.HasPrefix(parsed.Path, "/static/") {
		return true
	}
	if base != "" && strings.HasPrefix(base, "http") {
		if host, herr := url.Parse(base); herr == nil && host.Host == parsed.Host {
			return true
		}
	}
	return false
}

// objectKeyOf 从访问地址反解对象键，用于查上传台账。
func (s *Service) objectKeyOf(raw string) string {
	key := raw
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		if parsed, err := url.Parse(raw); err == nil {
			key = parsed.Path
		}
	}
	key = strings.TrimPrefix(key, "/")
	prefix := strings.Trim(s.cfg.Upload.Local.URLPrefix, "/")
	if prefix != "" && strings.HasPrefix(key, prefix+"/") {
		key = strings.TrimPrefix(key, prefix+"/")
	}
	if decoded, err := url.PathUnescape(key); err == nil {
		key = decoded
	}
	return path.Clean(key)
}

// newObjectKey 生成对象键：{场景}/{用户}/{日期}/{随机}.{扩展名}。
// 带上用户与日期是为了在对象存储里按人/按天排查，也便于生命周期规则回收。
func (s *Service) newObjectKey(scene int, uid int64, ext string) (string, error) {
	dir := "avatar"
	switch scene {
	case ScenePhoto:
		dir = "photo"
	case SceneVoice:
		dir = "voice"
	case SceneEvidence:
		dir = "evidence"
	}
	token, err := crypto.RandomToken(8)
	if err != nil {
		return "", err
	}
	prefix := strings.Trim(s.cfg.Upload.COS.Prefix, "/")
	key := fmt.Sprintf("%s/%d/%s/%s.%s", dir, uid, s.now().Format("20060102"), token, ext)
	if prefix != "" {
		key = prefix + "/" + key
	}
	return key, nil
}

// online 判断用户是否在线（只看 Redis 心跳）。Redis 不可用时降级为不在线。
func (s *Service) online(ctx context.Context, uid int64) bool {
	if s.rdb == nil {
		return false
	}
	ok, err := s.rdb.Exists(ctx, presence.Key(uid)).Result()
	if err != nil {
		s.log.Warn("查询在线状态失败", zap.Int64("uid", uid), zap.Error(err))
		return false
	}
	return ok > 0
}

// calcAge 按生日计算周岁。
//
// 不能用 YearDay 比较：生日在闰年时会多一天（2 月 29 日之后出生的人），
// 直接比 YearDay 会算出"差一天不满周岁"的错误结果。
// 用 AddDate 让标准库处理月份与闰年，再判断是否已过今年的生日。
func calcAge(birthday, now time.Time) int {
	age := now.Year() - birthday.Year()
	if now.Before(birthday.AddDate(age, 0, 0)) {
		age--
	}
	if age < 0 {
		age = 0
	}
	return age
}

// genderText 性别文案。
func genderText(gender int) string {
	switch gender {
	case GenderMale:
		return "男"
	case GenderFemale:
		return "女"
	default:
		return "保密"
	}
}

// lastActiveText 活跃文案：在线优先，否则按时间梯度给人类可读描述。
func lastActiveText(online bool, last *time.Time, now time.Time) string {
	if online {
		return "在线"
	}
	if last == nil || last.IsZero() {
		return "离线"
	}
	d := now.Sub(*last)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前活跃", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时前活跃", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%d 天前活跃", int(d.Hours()/24))
	default:
		return "长期未活跃"
	}
}

// effectivePrice 计算生效价：开启自定价且自定价 > 0 时用自定价，否则用全局价。
func effectivePrice(p *UserProfile, gp *GlobalPrice) (int, int) {
	audio, video := gp.AudioPerMin, gp.VideoPerMin
	if p.PriceEnabled == 1 {
		if p.PriceAudio > 0 {
			audio = p.PriceAudio
		}
		if p.PriceVideo > 0 {
			video = p.PriceVideo
		}
	}
	return audio, video
}

// extOfMime 把嗅探到的 MIME 映射为扩展名。
func extOfMime(mime string) string {
	switch mime {
	case "image/png":
		return "png"
	case "image/gif":
		return "gif"
	case "image/webp":
		return "webp"
	default:
		return "jpg"
	}
}

// imageSize 读取图片宽高。webp 等标准库不支持的格式会返回 0，不影响流程。
func imageSize(data []byte) (int, int) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

// BlockUser 拉黑某人。重复拉黑是幂等的（ON DUPLICATE 恢复软删记录）。
func (s *Service) BlockUser(ctx context.Context, uid, target int64) error {
	// 用查用户本身代替专门的 exists 接口：顺带确认对方未被注销。
	if _, err := s.repo.FindUser(ctx, target); err != nil {
		return errs.ErrNotFound.WithMsg("用户不存在")
	}
	if err := s.repo.BlockUser(ctx, uid, target); err != nil {
		s.log.Warn("拉黑失败", zap.Int64("uid", uid), zap.Int64("target", target), zap.Error(err))
		return err
	}
	return nil
}

// UnblockUser 取消拉黑。本来就没拉黑也返回成功，客户端不必区分。
func (s *Service) UnblockUser(ctx context.Context, uid, target int64) error {
	return s.repo.UnblockUser(ctx, uid, target)
}

// ListBlocked 我的黑名单。
func (s *Service) ListBlocked(ctx context.Context, uid int64, page, size int) ([]BlockedUser, int64, error) {
	return s.repo.ListBlocked(ctx, uid, page, size)
}
