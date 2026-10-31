// Package jwt 封装令牌签发与校验。
//
// 双令牌设计：
//   - access  ：2 小时，随请求头 Authorization: Bearer 下发，泄露窗口小；
//   - refresh ：30 天，只能用于换取新 access，可被服务端按设备吊销（后续阶段接入 Redis 黑名单）。
package jwt

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	jwtlib "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/datechat/server/internal/pkg/errs"
)

// Type 令牌类型。
type Type string

const (
	TypeAccess  Type = "access"
	TypeRefresh Type = "refresh"
	TypeAdmin   Type = "admin" // 后台管理令牌，UID 为 admin_user.id
)

// Claims 自定义载荷。字段名尽量短，减少令牌体积。
type Claims struct {
	UID      int64  `json:"uid"`
	DeviceID string `json:"did,omitempty"`
	Type     Type   `json:"typ"`
	// Role 仅在后台令牌中非空（admin/auditor/operator）。
	// 不复用 user 表的身份体系：运营人员不是 App 用户，混用会让
	// "管理员被用户拉黑""管理员出现在附近的人"这类状态成为可能。
	Role string `json:"role,omitempty"`
	jwtlib.RegisteredClaims
}

// IsAdmin 是否为后台令牌。
func (c *Claims) IsAdmin() bool { return c != nil && c.Type == TypeAdmin && c.Role != "" }

// Manager 令牌管理器，并发安全，可作为单例注入。
type Manager struct {
	secret     []byte
	issuer     string
	accessTTL  time.Duration
	refreshTTL time.Duration
}

// NewManager 创建管理器。
func NewManager(secret, issuer string, accessTTL, refreshTTL time.Duration) *Manager {
	return &Manager{
		secret:     []byte(secret),
		issuer:     issuer,
		accessTTL:  accessTTL,
		refreshTTL: refreshTTL,
	}
}

// TTL 返回指定类型的有效期。
func (m *Manager) TTL(t Type) time.Duration {
	if t == TypeRefresh {
		return m.refreshTTL
	}
	return m.accessTTL
}

// Generate 签发令牌，同时返回过期时间供响应体使用。
func (m *Manager) Generate(uid int64, deviceID string, t Type) (string, time.Time, error) {
	return m.GenerateWithID(uid, deviceID, t, uuid.NewString())
}

// GenerateWithID 用指定 jti 签发。
// refresh 令牌需要把 jti 存入 Redis 做"一次性使用 + 重放检测"，所以必须能由调用方掌握 jti。
func (m *Manager) GenerateWithID(uid int64, deviceID string, t Type, id string) (string, time.Time, error) {
	now := time.Now()
	expireAt := now.Add(m.TTL(t))

	claims := Claims{
		UID:      uid,
		DeviceID: deviceID,
		Type:     t,
		RegisteredClaims: jwtlib.RegisteredClaims{
			Issuer:    m.issuer,
			Subject:   strconv.FormatInt(uid, 10),
			ID:        id,
			IssuedAt:  jwtlib.NewNumericDate(now),
			NotBefore: jwtlib.NewNumericDate(now),
			ExpiresAt: jwtlib.NewNumericDate(expireAt),
		},
	}

	signed, err := jwtlib.NewWithClaims(jwtlib.SigningMethodHS256, claims).SignedString(m.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("签发令牌失败: %w", err)
	}
	return signed, expireAt, nil
}

// GenerateAdmin 签发后台管理令牌，载荷带角色，供 AdminAuth 中间件校验。
// 后台令牌不绑定设备（运营会用多台机器登录），有效期取 access TTL。
func (m *Manager) GenerateAdmin(adminID int64, role string) (string, time.Time, error) {
	now := time.Now()
	expireAt := now.Add(m.TTL(TypeAccess))
	claims := Claims{
		UID:  adminID,
		Type: TypeAdmin,
		Role: role,
		RegisteredClaims: jwtlib.RegisteredClaims{
			Issuer:    m.issuer,
			Subject:   strconv.FormatInt(adminID, 10),
			ID:        uuid.NewString(),
			IssuedAt:  jwtlib.NewNumericDate(now),
			NotBefore: jwtlib.NewNumericDate(now),
			ExpiresAt: jwtlib.NewNumericDate(expireAt),
		},
	}
	signed, err := jwtlib.NewWithClaims(jwtlib.SigningMethodHS256, claims).SignedString(m.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("签发后台令牌失败: %w", err)
	}
	return signed, expireAt, nil
}

// Parse 校验并解析令牌。过期与非法分别映射到不同业务码，便于客户端决定是否静默刷新。
func (m *Manager) Parse(tokenStr string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwtlib.ParseWithClaims(
		tokenStr,
		claims,
		func(t *jwtlib.Token) (any, error) {
			if _, ok := t.Method.(*jwtlib.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("非预期的签名算法: %v", t.Header["alg"])
			}
			return m.secret, nil
		},
		jwtlib.WithIssuer(m.issuer),
		jwtlib.WithValidMethods([]string{jwtlib.SigningMethodHS256.Alg()}),
	)
	if err != nil {
		if errors.Is(err, jwtlib.ErrTokenExpired) {
			return nil, errs.ErrTokenExpired.WithErr(err)
		}
		return nil, errs.ErrTokenInvalid.WithErr(err)
	}
	if !token.Valid {
		return nil, errs.ErrTokenInvalid
	}
	return claims, nil
}
