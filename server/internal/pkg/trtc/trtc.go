// Package trtc 封装腾讯云 TRTC 的进房凭据与房间号生成。
//
// TRTC 本身是端侧 SDK 直连的：服务端只负责"签一张能进这个房间的票"，
// 不中转任何音视频流量。所以这个包很薄——但它必须做对三件事：
//
//  1. UserSig 的算法必须与 IM 完全一致（腾讯云 TLS 票据），否则客户端报鉴权失败；
//  2. 房间号必须全局唯一且不可猜，否则陌生人能进别人的房间；
//  3. 没有密钥时不能静默返回空串，要么明确报错，要么明确走 mock（且仅非生产）。
package trtc

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/datechat/server/internal/pkg/tlsig"
)

// Config 运行期参数。Enabled=false 时签发一律失败，由上层决定是否走 mock。
type Config struct {
	Enabled   bool
	SdkAppID  int
	SecretKey string
	Expire    time.Duration
	// Mock 允许在无密钥时下发模拟凭据。仅非生产可开，生产由配置校验硬拦。
	Mock bool
}

// Client 进房凭据签发器。
type Client struct {
	cfg    Config
	gen    *tlsig.Generator // 无密钥时为 nil
	roomID func() string    // 便于测试注入固定房间号
}

// New 构造客户端。密钥缺失或非法时不返回错误，而是得到一个 Enabled()=false 的客户端：
// 通话"能不能打通"不该影响服务启动，缺密钥应该在接口层给出明确提示。
func New(cfg Config) *Client {
	c := &Client{cfg: cfg}
	if cfg.Expire <= 0 {
		c.cfg.Expire = 24 * time.Hour
	}
	if cfg.SdkAppID > 0 && cfg.SecretKey != "" {
		if gen, err := tlsig.New(cfg.SdkAppID, cfg.SecretKey, c.cfg.Expire); err == nil {
			c.gen = gen
		}
	}
	c.roomID = randomRoomID
	return c
}

// Enabled 表示能否签发真实凭据。
func (c *Client) Enabled() bool { return c != nil && c.gen != nil }

// MockEnabled 表示允许下发模拟凭据（仅非生产）。
func (c *Client) MockEnabled() bool { return c != nil && c.cfg.Mock }

// SdkAppID 返回应用标识，客户端进房需要。
func (c *Client) SdkAppID() int {
	if c == nil {
		return 0
	}
	return c.cfg.SdkAppID
}

// Credential 进房凭据。
type Credential struct {
	SdkAppID int    `json:"sdk_app_id"`
	RoomID   string `json:"room_id"`
	UserID   string `json:"user_id"`
	UserSig  string `json:"user_sig"`
	// Mock 为 true 表示这是本地模拟凭据：客户端应跳过真实进房，只跑业务与计费流程。
	Mock          bool   `json:"mock"`
	Expire        int64  `json:"expire_at"`
	PrivateMapKey string `json:"private_map_key,omitempty"` // TRTC 房间密钥，当前版本留空即可
}

// UserSig 签发指定用户的 UserSig。
func (c *Client) UserSig(userID string) (string, time.Time, error) {
	if !c.Enabled() {
		return "", time.Time{}, fmt.Errorf("trtc: 未配置 TRTC 密钥，无法签发 UserSig")
	}
	return c.gen.Generate(userID)
}

// Issue 生成一次完整的进房凭据。
// mock 优先于报错的顺序很重要：明确开了 mock 就稳定走 mock，
// 不要因为"某天配了半套密钥（有 appid 没 key）"而在真实与模拟之间来回跳。
func (c *Client) Issue(userID, roomID string) (*Credential, error) {
	cred := &Credential{SdkAppID: c.SdkAppID(), RoomID: roomID, UserID: userID}
	if c.Enabled() {
		sig, expireAt, err := c.UserSig(userID)
		if err != nil {
			return nil, err
		}
		cred.UserSig = sig
		cred.Expire = expireAt.Unix()
		return cred, nil
	}
	if c.MockEnabled() {
		cred.Mock = true
		cred.UserSig = "MOCK_USER_SIG_" + userID
		cred.Expire = time.Now().Add(c.cfg.Expire).Unix()
		return cred, nil
	}
	return nil, fmt.Errorf("trtc: TRTC 未启用且未开启 mock，无法进房（请配置 trtc.sdk_app_id/secret_key）")
}

// NewRoomID 生成房间号。
//
// 用随机 uint32 而不是自增：房间号会出现在客户端进房参数与日志里，
// 自增 ID 会让"猜房间号蹭进别人通话"变成可能。
func (c *Client) NewRoomID() string {
	if c == nil || c.roomID == nil {
		return randomRoomID()
	}
	return c.roomID()
}

// WithRoomIDFunc 替换房间号生成器，仅用于测试。
func (c *Client) WithRoomIDFunc(f func() string) *Client {
	if c != nil {
		c.roomID = f
	}
	return c
}

func randomRoomID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", 1000000000+time.Now().UnixNano()%3000000000)
	}
	v := binary.BigEndian.Uint32(b[:])
	// 落在 [1e9, 4e9) 区间：保证是正数、位数固定，也避开 TRTC 保留的小房间号。
	return fmt.Sprintf("%d", 1000000000+v%3000000000)
}
