package account

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/datechat/server/internal/config"
	"github.com/datechat/server/internal/pkg/tlsig"
)

// IMClient 封装腾讯云 IM 的服务端 REST 接口。
//
// 账号导入、UserSig、服务端代发文本消息、清理未读和撤回都收口在这里。
// 业务层只依赖方法，不接触 REST 鉴权参数，避免每个模块各实现一遍签名与错误解析。
type IMClient struct {
	cfg config.IMConfig
	gen *tlsig.Generator // 未配置密钥时为 nil
	hc  *http.Client
}

// NewIMClient 构造 IM 客户端。
// 未配置 sdk_app_id / secret_key 时返回“空壳”而不是报错：
// 本地开发可通过 im.local_fallback 使用镜像库完成端到端验收。
func NewIMClient(cfg config.IMConfig, timeout time.Duration) *IMClient {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	c := &IMClient{cfg: cfg, hc: &http.Client{Timeout: timeout}}
	if cfg.SdkAppID > 0 && cfg.SecretKey != "" {
		if gen, err := tlsig.New(cfg.SdkAppID, cfg.SecretKey, cfg.UserSigExpire.D()); err == nil {
			c.gen = gen
		}
	}
	return c
}

// CanSign 表示能否签发 UserSig，也表示服务端 REST 鉴权材料是否齐全。
func (c *IMClient) CanSign() bool { return c != nil && c.gen != nil }

// RESTEnabled 表示可以调用腾讯云 IM 服务端 REST API。
func (c *IMClient) RESTEnabled() bool { return c.CanSign() }

// ImportEnabled 表示登录时是否需要调用账号导入。
func (c *IMClient) ImportEnabled() bool { return c != nil && c.cfg.Enabled() }

// UserSig 为指定用户签发 UserSig。
func (c *IMClient) UserSig(userID string) (string, time.Time, error) {
	if !c.CanSign() {
		return "", time.Time{}, fmt.Errorf("account: 未配置 IM 密钥，无法签发 UserSig")
	}
	return c.gen.Generate(userID)
}

// APIError 是腾讯云 IM 在 HTTP 200 中返回的业务错误。
// 腾讯云 REST 大多数失败仍返回 200，因此不能只看 HTTP 状态码。
type APIError struct {
	Code int
	Info string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("腾讯云 IM 调用失败 code=%d info=%s", e.Code, e.Info)
}

// imResponse 是 IM REST 的通用响应。
type imResponse struct {
	ActionStatus string `json:"ActionStatus"`
	ErrorCode    int    `json:"ErrorCode"`
	ErrorInfo    string `json:"ErrorInfo"`
}

// doREST 统一完成管理员 UserSig、公共查询参数、JSON 请求与业务错误解析。
func (c *IMClient) doREST(ctx context.Context, service string, in, out any) error {
	if !c.RESTEnabled() {
		return fmt.Errorf("account: 未配置 IM 密钥，无法调用 REST API")
	}
	adminSig, _, err := c.UserSig(c.cfg.AdminUserID)
	if err != nil {
		return fmt.Errorf("account: 签发管理员 UserSig 失败: %w", err)
	}

	q := url.Values{}
	q.Set("sdkappid", strconv.Itoa(c.cfg.SdkAppID))
	q.Set("identifier", c.cfg.AdminUserID)
	q.Set("usersig", adminSig)
	q.Set("random", strconv.FormatInt(rand.Int63n(4294967294)+1, 10))
	q.Set("contenttype", "json")
	endpoint := strings.TrimRight(c.cfg.Endpoint, "/") + "/v4/" + strings.TrimLeft(service, "/") + "?" + q.Encode()

	body, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("account: 序列化 IM 请求失败: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("account: 构造 IM 请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("account: 调用 IM 接口失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("account: 读取 IM 响应失败: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("account: IM HTTP 状态异常 status=%d body=%s", resp.StatusCode, string(raw))
	}

	var base imResponse
	if err := json.Unmarshal(raw, &base); err != nil {
		return fmt.Errorf("account: 解析 IM 响应失败: %w", err)
	}
	if base.ErrorCode != 0 {
		return &APIError{Code: base.ErrorCode, Info: base.ErrorInfo}
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("account: 解析 IM 业务响应失败: %w", err)
		}
	}
	return nil
}

// imAccount 是 account_import 的请求体。
type imAccount struct {
	UserID  string `json:"UserID"`
	Nick    string `json:"Nick,omitempty"`
	FaceURL string `json:"FaceUrl,omitempty"`
}

// ImportAccount 把用户导入 IM。重复导入视为成功。
func (c *IMClient) ImportAccount(ctx context.Context, userID, nickname, faceURL string) error {
	if !c.ImportEnabled() {
		return nil
	}
	err := c.doREST(ctx, "im_open_login_svc/account_import", imAccount{
		UserID: userID, Nick: nickname, FaceURL: faceURL,
	}, nil)
	if apiErr, ok := err.(*APIError); ok && (apiErr.Code == 71000 || apiErr.Code == 71001) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("account: IM 导入账号失败: %w", err)
	}
	return nil
}

// SendTextResult 是单发单聊消息返回的关键字段。
type SendTextResult struct {
	MsgTime int64  `json:"MsgTime"`
	MsgKey  string `json:"MsgKey"`
	MsgID   string `json:"MsgId"`
}

// SendText 通过 openim/sendmsg 以业务用户身份发送文本消息。
func (c *IMClient) SendText(ctx context.Context, fromUserID, toUserID, text, clientMsgID string, msgSeq, msgRandom uint32) (*SendTextResult, error) {
	if msgSeq == 0 {
		msgSeq = uint32(rand.Int31n(2147483646) + 1)
	}
	if msgRandom == 0 {
		msgRandom = uint32(rand.Int31n(2147483646) + 1)
	}
	custom, _ := json.Marshal(map[string]string{"client_msg_id": clientMsgID})
	in := struct {
		SyncOtherMachine int              `json:"SyncOtherMachine"`
		FromAccount      string           `json:"From_Account"`
		ToAccount        string           `json:"To_Account"`
		MsgSeq           uint32           `json:"MsgSeq"`
		MsgRandom        uint32           `json:"MsgRandom"`
		MsgBody          []map[string]any `json:"MsgBody"`
		CloudCustomData  string           `json:"CloudCustomData,omitempty"`
	}{
		SyncOtherMachine: 1,
		FromAccount:      fromUserID,
		ToAccount:        toUserID,
		MsgSeq:           msgSeq,
		MsgRandom:        msgRandom,
		MsgBody: []map[string]any{{
			"MsgType":    "TIMTextElem",
			"MsgContent": map[string]any{"Text": text},
		}},
		CloudCustomData: string(custom),
	}
	var out SendTextResult
	if err := c.doREST(ctx, "openim/sendmsg", in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// MarkC2CRead 清理指定用户与对端会话的未读计数。
func (c *IMClient) MarkC2CRead(ctx context.Context, reportUserID, peerUserID string, readAt time.Time) error {
	in := struct {
		ReportAccount string `json:"Report_Account"`
		PeerAccount   string `json:"Peer_Account"`
		MsgReadTime   int64  `json:"MsgReadTime,omitempty"`
	}{ReportAccount: reportUserID, PeerAccount: peerUserID}
	if !readAt.IsZero() {
		in.MsgReadTime = readAt.Unix()
	}
	return c.doREST(ctx, "openim/admin_set_msg_read", in, nil)
}

// RecallC2C 撤回一条单聊消息。
func (c *IMClient) RecallC2C(ctx context.Context, fromUserID, toUserID, msgKey, reason string) error {
	in := struct {
		FromAccount string `json:"From_Account"`
		ToAccount   string `json:"To_Account"`
		MsgKey      string `json:"MsgKey"`
		Reason      string `json:"Reason,omitempty"`
	}{FromAccount: fromUserID, ToAccount: toUserID, MsgKey: msgKey, Reason: reason}
	return c.doREST(ctx, "openim/admin_msgwithdraw", in, nil)
}
