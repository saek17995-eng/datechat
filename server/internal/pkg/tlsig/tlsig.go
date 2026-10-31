// Package tlsig 生成腾讯云 IM 的 UserSig（TLSSigAPIv2 算法）。
//
// 为什么自己实现而不是引 SDK：
//   - 算法本身只有三步（拼签名串 → HMAC-SHA256 → zlib 压缩后 base64），
//     引入 tencentcloud-sdk-go 会为一个函数拖进上百 MB 依赖；
//   - 自己实现便于注入时钟做确定性测试。
//
// 算法（与官方 Java/Go 示例一致）：
//
//	content = "TLS.identifier:<uid>\nTLS.sdkappid:<appid>\nTLS.time:<ts>\nTLS.expire:<expire>\n"
//	sig     = base64(HMAC-SHA256(key, content))
//	payload = {"TLS.ver":"2.0","TLS.identifier":uid,"TLS.sdkappid":appid,"TLS.time":ts,"TLS.expire":expire,"TLS.sig":sig}
//	UserSig = base64(zlib(payload))
//
// 注意：UserSig 会超时，客户端不应长期缓存；服务端每次登录/续期重新签发。
package tlsig

import (
	"bytes"
	"compress/zlib"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Doc 是压缩前的签名文档，字段名带 TLS. 前缀是与 IM 服务端的约定，不能改名。
type Doc struct {
	Ver        string `json:"TLS.ver"`
	Identifier string `json:"TLS.identifier"`
	SdkAppID   int    `json:"TLS.sdkappid"`
	Time       int64  `json:"TLS.time"`
	Expire     int64  `json:"TLS.expire"`
	Sig        string `json:"TLS.sig"`
}

// Generator 是 UserSig 生成器，并发安全。
type Generator struct {
	sdkAppID int
	key      string
	expire   time.Duration
}

// New 构造生成器。expire 建议 7 天（IM 官方推荐上限为 180 天）。
func New(sdkAppID int, key string, expire time.Duration) (*Generator, error) {
	if sdkAppID <= 0 {
		return nil, errors.New("tlsig: sdkAppID 必须大于 0")
	}
	if key == "" {
		return nil, errors.New("tlsig: 密钥不能为空")
	}
	if expire <= 0 {
		expire = 7 * 24 * time.Hour
	}
	return &Generator{sdkAppID: sdkAppID, key: key, expire: expire}, nil
}

// Expire 返回配置的有效期。
func (g *Generator) Expire() time.Duration { return g.expire }

// Generate 用当前时间签发。
func (g *Generator) Generate(userID string) (string, time.Time, error) {
	return g.GenerateAt(userID, time.Now())
}

// GenerateAt 用指定时间签发。抽出来是为了让单测可以不依赖时间。
func (g *Generator) GenerateAt(userID string, now time.Time) (string, time.Time, error) {
	if userID == "" {
		return "", time.Time{}, errors.New("tlsig: userID 不能为空")
	}
	expireSec := int64(g.expire.Seconds())
	ts := now.Unix()
	expireAt := now.Add(g.expire)

	doc := Doc{
		Ver:        "2.0",
		Identifier: userID,
		SdkAppID:   g.sdkAppID,
		Time:       ts,
		Expire:     expireSec,
		Sig:        sign(g.key, userID, g.sdkAppID, ts, expireSec),
	}

	raw, err := json.Marshal(doc)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("tlsig: 序列化签名文档失败: %w", err)
	}

	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		return "", time.Time{}, fmt.Errorf("tlsig: 压缩签名文档失败: %w", err)
	}
	if err := zw.Close(); err != nil {
		return "", time.Time{}, fmt.Errorf("tlsig: 关闭压缩流失败: %w", err)
	}

	return base64.StdEncoding.EncodeToString(buf.Bytes()), expireAt, nil
}

// SignContent 拼出被签名的原文。导出是为了让单测与排查工具能复用同一份实现。
func SignContent(userID string, sdkAppID int, ts, expireSec int64) string {
	return fmt.Sprintf(
		"TLS.identifier:%s\nTLS.sdkappid:%d\nTLS.time:%d\nTLS.expire:%d\n",
		userID, sdkAppID, ts, expireSec,
	)
}

func sign(key, userID string, sdkAppID int, ts, expireSec int64) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(SignContent(userID, sdkAppID, ts, expireSec)))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// Verify 校验一个 UserSig 是否是本密钥签发且未过期。
// 生产上 IM 服务端会做同样的事，这里主要给单测与排障工具用。
func (g *Generator) Verify(userSig string) (*Doc, error) {
	compressed, err := base64.StdEncoding.DecodeString(userSig)
	if err != nil {
		return nil, fmt.Errorf("tlsig: UserSig 不是合法 base64: %w", err)
	}
	zr, err := zlib.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, fmt.Errorf("tlsig: UserSig 解压失败: %w", err)
	}
	defer func() { _ = zr.Close() }()

	var doc Doc
	dec := json.NewDecoder(zr)
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("tlsig: UserSig 内容不是合法 JSON: %w", err)
	}
	if doc.SdkAppID != g.sdkAppID {
		return nil, fmt.Errorf("tlsig: sdkAppID 不匹配，期望 %d 实际 %d", g.sdkAppID, doc.SdkAppID)
	}
	want := sign(g.key, doc.Identifier, doc.SdkAppID, doc.Time, doc.Expire)
	if !hmac.Equal([]byte(want), []byte(doc.Sig)) {
		return nil, errors.New("tlsig: 签名校验失败")
	}
	if time.Now().Unix() > doc.Time+doc.Expire {
		return nil, errors.New("tlsig: UserSig 已过期")
	}
	return &doc, nil
}
