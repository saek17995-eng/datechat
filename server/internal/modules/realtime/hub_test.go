package realtime

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"github.com/datechat/server/internal/pkg/jwt"
)

const testSecret = "0123456789abcdef0123456789abcdef"

// newTestServer 起一个只挂了实时路由的 HTTP 服务。
func newTestServer(t *testing.T) (*Hub, *httptest.Server, *jwt.Manager) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	hub := NewHub(zap.NewNop())
	tokens := jwt.NewManager(testSecret, "datechat", time.Hour, 24*time.Hour)

	engine := gin.New()
	v1 := engine.Group("/v1")
	NewHandler(hub, tokens).Register(v1)

	srv := httptest.NewServer(engine)
	t.Cleanup(srv.Close)
	t.Cleanup(hub.Close)
	return hub, srv, tokens
}

// dial 用 access token 建立连接，并把 ws:// 换成服务实际地址。
func dial(t *testing.T, srv *httptest.Server, token string, header http.Header) (*websocket.Conn, *http.Response) {
	t.Helper()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/ws"
	conn, resp, err := websocket.DefaultDialer.Dial(url, header)
	if err != nil {
		t.Fatalf("WebSocket 握手失败: %v", err)
	}
	return conn, resp
}

func tokenOf(t *testing.T, m *jwt.Manager, uid int64) string {
	t.Helper()
	tk, _, err := m.Generate(uid, "device-test", jwt.TypeAccess)
	if err != nil {
		t.Fatalf("签发令牌失败: %v", err)
	}
	return tk
}

func readFrame(t *testing.T, conn *websocket.Conn) Frame {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var frame Frame
	if err := conn.ReadJSON(&frame); err != nil {
		t.Fatalf("读帧失败: %v", err)
	}
	return frame
}

func TestHandshakeRejectsAnonymous(t *testing.T) {
	_, srv, _ := newTestServer(t)

	// 无令牌：必须 401，而不是先升级再关（否则客户端只能靠关闭帧猜原因）。
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/ws"
	_, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err == nil {
		t.Fatal("匿名握手竟然成功了")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("期望 401，实际 %v", resp)
	}
}

func TestHelloOnConnect(t *testing.T) {
	_, srv, tokens := newTestServer(t)
	conn, _ := dial(t, srv, tokenOf(t, tokens, 1001), http.Header{
		"Authorization": []string{"Bearer " + tokenOf(t, tokens, 1001)},
	})
	defer conn.Close()

	frame := readFrame(t, conn)
	if frame.Type != TypeHello {
		t.Fatalf("期望 hello，实际 %s", frame.Type)
	}
	data, _ := frame.Data.(map[string]any)
	if int64(data["uid"].(float64)) != 1001 {
		t.Fatalf("hello 里的 uid 不对: %v", data["uid"])
	}
}

func TestPushToUser(t *testing.T) {
	hub, srv, tokens := newTestServer(t)
	conn, _ := dial(t, srv, "", http.Header{
		"Authorization": []string{"Bearer " + tokenOf(t, tokens, 1002)},
	})
	defer conn.Close()

	readFrame(t, conn) // hello

	hub.Push(1002, TypeNewMessage, map[string]any{"text": "你好"})
	frame := readFrame(t, conn)
	if frame.Type != TypeNewMessage {
		t.Fatalf("期望 new_message，实际 %s", frame.Type)
	}
	data, _ := frame.Data.(map[string]any)
	if data["text"] != "你好" {
		t.Fatalf("载荷不对: %v", data)
	}
	if frame.Seq == 0 {
		t.Fatal("seq 应为正数，客户端要靠它判断漏帧")
	}
}

// TestSignalForward 验证 A 的 signal 帧会被原样转给 B——
// WebRTC 的 offer/answer/ICE 全部依赖这条转发。
func TestSignalForward(t *testing.T) {
	_, srv, tokens := newTestServer(t)

	connA, _ := dial(t, srv, "", http.Header{"Authorization": []string{"Bearer " + tokenOf(t, tokens, 2001)}})
	defer connA.Close()
	connB, _ := dial(t, srv, "", http.Header{"Authorization": []string{"Bearer " + tokenOf(t, tokens, 2002)}})
	defer connB.Close()

	readFrame(t, connA)
	readFrame(t, connB)

	out, _ := json.Marshal(Frame{
		Type:    TypeSignal,
		To:      2002,
		CallNo:  "C202611010000001234",
		Kind:    "offer",
		Payload: map[string]any{"sdp": "v=0..."},
	})
	if err := connA.WriteMessage(websocket.TextMessage, out); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	frame := readFrame(t, connB)
	if frame.Type != TypeSignal || frame.Kind != "offer" {
		t.Fatalf("B 收到的帧不对: %+v", frame)
	}
	if frame.CallNo != "C202611010000001234" {
		t.Fatalf("call_no 没带上: %+v", frame)
	}
	data, _ := frame.Data.(map[string]any)
	if int64(data["from"].(float64)) != 2001 {
		t.Fatalf("from 应为 2001，实际 %v", data["from"])
	}
	payload, _ := frame.Payload["sdp"].(string)
	if payload != "v=0..." {
		t.Fatalf("sdp 丢失: %v", frame.Payload)
	}
}

// TestMultipleDevices 验证一人多设备都能收到，且离线时不报错。
func TestMultipleDevices(t *testing.T) {
	hub, srv, tokens := newTestServer(t)
	tk := tokenOf(t, tokens, 3001)

	c1, _ := dial(t, srv, "", http.Header{"Authorization": []string{"Bearer " + tk}})
	defer c1.Close()
	c2, _ := dial(t, srv, "", http.Header{"Authorization": []string{"Bearer " + tk}})
	defer c2.Close()
	readFrame(t, c1)
	readFrame(t, c2)

	hub.Push(3001, TypeUnread, map[string]any{"total": 2})
	readFrame(t, c1)
	readFrame(t, c2)

	// 离线用户：不该 panic，也不该阻塞。
	hub.Push(999999, TypeNewMessage, map[string]any{"text": "没人收"})

	if users, conns := hub.Connections(); users != 1 || conns != 2 {
		t.Fatalf("期望 1 个用户 2 条连接，实际 %d/%d", users, conns)
	}
	if !hub.Online(3001) {
		t.Fatal("3001 应在线")
	}
}
