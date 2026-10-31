// Package cos 实现腾讯云对象存储（COS）的直传签名。
//
// 为什么服务端只签名、不中转文件：
//   - 一张头像 3~5MB，若穿过应用服务器，带宽与内存都会被大图上传打满；
//   - 直传后客户端拿到的 URL 仍需回调业务接口提交，服务端在提交时校验
//     "这个 object_key 是否由我们签发过"，避免伪造外链。
//
// 签名算法（POST Object 表单上传，与 COS 官方一致）：
//
//	SignKey      = HMAC-SHA1(SecretKey, KeyTime)          // 十六进制小写
//	StringToSign = SHA1(policy 明文)                       // 十六进制小写
//	Signature    = HMAC-SHA1(SignKey, StringToSign)        // 十六进制小写
//
// 直传时必须把 q-* 签名字段放在 file 字段之前，否则 COS 会拒绝。
package cos

import (
	"crypto/hmac"
	"crypto/sha1" // #nosec G505 -- COS 官方签名算法固定为 SHA1/HMAC-SHA1
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Credential COS 访问密钥。
type Credential struct {
	SecretID  string
	SecretKey string
}

// Valid 判断密钥是否已配置。未配置时签名接口不可用，上传通道应回落或直接报错。
func (c Credential) Valid() bool { return c.SecretID != "" && c.SecretKey != "" }

// PostObjectInput 直传签名入参。
type PostObjectInput struct {
	Bucket        string        // 形如 datechat-1250000000
	Region        string        // 形如 ap-guangzhou
	Key           string        // 对象键，如 upload/1/avatar/2026/1730000000.jpg
	ContentType   string        // 允许的 Content-Type 前缀，如 image/（必须带斜杠）
	MaxBytes      int64         // 大小上限
	ACL           string        // 一般留空；需要时传 private / public-read
	Expire        time.Duration // 凭证有效期
	SuccessStatus string        // 上传成功返回码，默认 200
}

// PostObjectOutput 下发给客户端的直传凭证。
// 客户端按 Fields 的顺序构造 multipart/form-data：签名相关字段必须排在 file 之前。
type PostObjectOutput struct {
	Host         string            `json:"host"`          // 直传域名，不含路径
	Key          string            `json:"key"`           // 对象键
	URL          string            `json:"url"`           // 上传成功后的访问地址
	ExpireAt     int64             `json:"expire_at"`     // 凭证过期时间戳
	FieldOrder   []string          `json:"field_order"`   // 表单字段顺序（file 放最后）
	Fields       map[string]string `json:"fields"`        // 除 file 外的表单字段
	Policy       string            `json:"policy"`        // base64 策略，便于客户端调试
	MaxBytes     int64             `json:"max_bytes"`     // 大小上限，客户端可先本地校验
	AllowedTypes []string          `json:"allowed_types"` // 允许的 MIME 前缀
}

// SignPostObject 计算 POST Object 直传签名。
func SignPostObject(cred Credential, now time.Time, in PostObjectInput) (*PostObjectOutput, error) {
	if !cred.Valid() {
		return nil, fmt.Errorf("cos: 未配置 SecretID / SecretKey")
	}
	if in.Bucket == "" || in.Region == "" || in.Key == "" {
		return nil, fmt.Errorf("cos: bucket / region / key 不能为空")
	}
	key := strings.TrimLeft(in.Key, "/")
	expire := in.Expire
	if expire <= 0 {
		expire = 30 * time.Minute
	}
	maxBytes := in.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 5 << 20
	}
	contentType := in.ContentType
	if contentType == "" {
		contentType = "image/"
	}
	if !strings.HasSuffix(contentType, "/") {
		return nil, fmt.Errorf("cos: ContentType 需为前缀形式（如 image/），实际 %q", contentType)
	}
	status := in.SuccessStatus
	if status == "" {
		status = "200"
	}

	start := now.Unix()
	end := now.Add(expire).Unix()
	keyTime := fmt.Sprintf("%d;%d", start, end)

	conditions := []any{
		map[string]string{"bucket": in.Bucket},
		[]any{"starts-with", "$key", key},
		[]any{"starts-with", "$Content-Type", contentType},
		[]any{"content-length-range", 1, maxBytes},
		map[string]string{"q-sign-algorithm": "sha1"},
		map[string]string{"q-ak": cred.SecretID},
		map[string]string{"q-key-time": keyTime},
		map[string]string{"q-sign-time": keyTime},
	}
	if in.ACL != "" {
		conditions = append(conditions, map[string]string{"acl": in.ACL})
	}
	policy := Policy{
		Expiration: now.Add(expire).UTC().Format("2006-01-02T15:04:05.000Z"),
		Conditions: conditions,
	}

	policyText, err := policy.JSON()
	if err != nil {
		return nil, fmt.Errorf("cos: 序列化 policy 失败: %w", err)
	}

	signKey := hmacSHA1Hex(cred.SecretKey, keyTime)
	stringToSign := sha1Hex(policyText)
	signature := hmacSHA1Hex(signKey, stringToSign)

	host := fmt.Sprintf("https://%s.cos.%s.myqcloud.com", in.Bucket, in.Region)
	fields := map[string]string{
		"key":                   key,
		"q-sign-algorithm":      "sha1",
		"q-ak":                  cred.SecretID,
		"q-key-time":            keyTime,
		"q-sign-time":           keyTime,
		"policy":                base64.StdEncoding.EncodeToString([]byte(policyText)),
		"q-signature":           signature,
		"success_action_status": status,
	}
	order := []string{"key", "q-sign-algorithm", "q-ak", "q-key-time", "q-sign-time", "policy", "q-signature", "success_action_status"}
	if in.ACL != "" {
		fields["acl"] = in.ACL
		order = append([]string{"key", "acl"}, order[1:]...)
	}

	return &PostObjectOutput{
		Host:         host,
		Key:          key,
		URL:          host + "/" + escapePath(key),
		ExpireAt:     end,
		FieldOrder:   append(order, "file"),
		Fields:       fields,
		Policy:       policyText,
		MaxBytes:     maxBytes,
		AllowedTypes: []string{contentType + "*"},
	}, nil
}

// PublicURL 生成对象的公网访问地址（带 CDN 域名时优先用 CDN）。
func PublicURL(cdnDomain, host, key string) string {
	key = strings.TrimLeft(key, "/")
	if cdnDomain != "" {
		return strings.TrimRight(cdnDomain, "/") + "/" + escapePath(key)
	}
	if host == "" {
		return "/" + escapePath(key)
	}
	return strings.TrimRight(host, "/") + "/" + escapePath(key)
}

// escapePath 对对象键做路径转义：中文与空格必须编码，但保留目录分隔符。
func escapePath(key string) string {
	parts := strings.Split(key, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

func hmacSHA1Hex(key, data string) string {
	m := hmac.New(sha1.New, []byte(key))
	_, _ = m.Write([]byte(data))
	return hex.EncodeToString(m.Sum(nil))
}

func sha1Hex(data string) string {
	s := sha1.New() // #nosec G401 -- COS 签名规范固定使用 SHA1
	_, _ = s.Write([]byte(data))
	return hex.EncodeToString(s.Sum(nil))
}
