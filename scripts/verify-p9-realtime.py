#!/usr/bin/env python3
"""P9 实时通道端到端验收：两个真账号同时在线，验证消息推送 / 来电推送 / 信令转发。

它验证的是"用户看得见的两件事"能不能真的跑通：
  1. A 发消息，B 不刷新页面就能收到（这条链路断了就是"收得到消息但没通知"）；
  2. A 发起通话 → B 收到来电 → B 接听 → A 收到 talking → 双方交换 WebRTC SDP。

为什么不写在 Go 的单元测试里：单测只能验证 Hub 内部的转发逻辑，验证不了
"HTTP 接口写入 → 推送 → 另一条连接收到"这条完整链路，也验证不了
真实握手（401、hello）的行为。所以只能起真服务、开真连接。

前置：
  - 后端已启动（bash scripts/start-backend.sh），默认 http://127.0.0.1:8080
  - configs/config.dev.yaml 的 sms.expose_code_in_dev=true（否则拿不到 debug_code）

用法：
  python3 scripts/verify-p9-realtime.py [base_url]
  python3 scripts/verify-p9-realtime.py http://192.168.1.102:8080

说明：不依赖任何第三方库，WebSocket 客户端是手写的（只要文本帧，不做分片与压缩）。
"""
import base64
import json
import os
import random
import socket
import struct
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

BASE = (sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:8080").rstrip("/")
URL = urllib.parse.urlparse(BASE)
HOST, PORT = URL.hostname, (URL.port or (443 if URL.scheme == "https" else 80))

PASS = FAIL = 0


def check(name, cond, extra=""):
    global PASS, FAIL
    if cond:
        PASS += 1
        print(f"  \033[32m✓\033[0m {name} {extra}")
    else:
        FAIL += 1
        print(f"  \033[31m✗\033[0m {name} {extra}")


def brief(value, limit=100):
    return json.dumps(value, ensure_ascii=False)[:limit]


def http(method, path, body=None, token=None):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(BASE + path, method=method, data=data,
                                 headers={"Content-Type": "application/json"})
    if token:
        req.add_header("Authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(req, timeout=15) as r:
            return json.load(r)
    except urllib.error.HTTPError as e:
        try:
            return json.load(e)
        except Exception:
            return {"code": -1, "msg": f"HTTP {e.code}"}


class WS:
    """最小 WebSocket 客户端：握手 + 收发文本帧。"""

    def __init__(self, token):
        self.sock = socket.create_connection((HOST, PORT), timeout=10)
        key = base64.b64encode(os.urandom(16)).decode()
        req = (f"GET /v1/ws HTTP/1.1\r\nHost: {HOST}:{PORT}\r\nUpgrade: websocket\r\n"
               f"Connection: Upgrade\r\nSec-WebSocket-Key: {key}\r\nSec-WebSocket-Version: 13\r\n"
               f"Authorization: Bearer {token}\r\n\r\n")
        self.sock.sendall(req.encode())
        buf = b""
        while b"\r\n\r\n" not in buf:
            chunk = self.sock.recv(4096)
            if not chunk:
                raise RuntimeError("握手时连接被关闭")
            buf += chunk
        self.status = buf.split(b"\r\n")[0].decode()
        self.buf = buf.split(b"\r\n\r\n", 1)[1]
        # 令牌非法时服务端在 Upgrade 之前就返回 401，这里必须是非 101 才算失败。
        if "101" not in self.status:
            raise RuntimeError(f"握手失败: {self.status}")

    def send(self, obj):
        payload = json.dumps(obj).encode()
        mask = os.urandom(4)
        n = len(payload)
        header = bytearray([0x81])
        if n < 126:
            header.append(0x80 | n)
        elif n < 65536:
            header.append(0x80 | 126)
            header += struct.pack(">H", n)
        else:
            header.append(0x80 | 127)
            header += struct.pack(">Q", n)
        # 客户端发出的帧必须掩码，否则服务端按协议直接关连接。
        masked = bytes(b ^ mask[i % 4] for i, b in enumerate(payload))
        self.sock.sendall(bytes(header) + mask + masked)

    def _recv(self, n):
        while len(self.buf) < n:
            chunk = self.sock.recv(65536)
            if not chunk:
                raise EOFError
            self.buf += chunk
        out, self.buf = self.buf[:n], self.buf[n:]
        return out

    def poll(self, timeout=0.3):
        self.sock.settimeout(timeout)
        try:
            _b0, b1 = self._recv(2)
        except (socket.timeout, EOFError):
            return None
        n = b1 & 0x7F
        if n == 126:
            n = struct.unpack(">H", self._recv(2))[0]
        elif n == 127:
            n = struct.unpack(">Q", self._recv(8))[0]
        raw = self._recv(n) if n else b""
        try:
            return json.loads(raw.decode())
        except Exception:
            return None

    def wait(self, typ, timeout=6):
        deadline = time.time() + timeout
        while time.time() < deadline:
            frame = self.poll(0.3)
            if frame and frame.get("type") == typ:
                return frame
        return None

    def close(self):
        try:
            self.sock.close()
        except Exception:
            pass


def login(phone):
    sent = http("POST", "/v1/auth/sms/code", {"phone": phone, "scene": "login"})
    code = (sent.get("data") or {}).get("debug_code")
    if not code:
        raise SystemExit(f"拿不到验证码（确认 sms.expose_code_in_dev=true）：{brief(sent, 200)}")
    ok = http("POST", "/v1/auth/login",
              {"phone": phone, "code": code, "device_id": "verify-p9",
               "platform": 1, "app_version": "0.1.0"})
    d = ok.get("data") or {}
    return d["uid"], d["token"]["access_token"]


def idcard(seq):
    """生成校验位合法的身份证号（GB11643）。唯一索引 uk_id_card 会拒重复。"""
    body = "11010119900307%03d" % (seq % 1000)
    weights = (7, 9, 10, 5, 8, 4, 2, 1, 6, 3, 7, 9, 10, 5, 8, 4, 2)
    codes = "10X98765432"
    return body + codes[sum(int(body[i]) * weights[i] for i in range(17)) % 11]


def realname(token, seq):
    """身份证号全库唯一，撞号就换一个再试。"""
    last = None
    for candidate in [seq] + [random.randrange(1000) for _ in range(9)]:
        last = http("POST", "/v1/realname/submit",
                    {"real_name": "测试用户", "id_card": idcard(candidate)}, token)
        if last.get("code") == 0:
            return last
    return last


def recharge(token):
    """通话要求余额 >=120 金币，用 mock 支付把钱包充上。"""
    pkgs = http("GET", "/v1/wallet/packages", None, token)
    pkg_id = 0
    for p in (pkgs.get("data") or {}).get("list") or []:
        if (p.get("coins") or 0) >= 200:
            pkg_id = p.get("id") or 0
            break
    if not pkg_id:
        return {"code": -1, "msg": "取不到充值套餐"}
    order = http("POST", "/v1/wallet/recharge", {"package_id": pkg_id}, token)
    order_no = (order.get("data") or {}).get("order_no")
    if not order_no:
        return order
    return http("POST", f"/v1/wallet/recharge/{order_no}/mock-paid", {}, token)


def as_int(value):
    try:
        return int(value)
    except (TypeError, ValueError):
        return 0


def main():
    seed = int(time.time()) % 100000000
    uid_a, tok_a = login(f"138{seed:08d}"[:11])
    uid_b, tok_b = login(f"139{seed:08d}"[:11])
    print(f"A uid={uid_a}  B uid={uid_b}  ({BASE})")

    print("\n1. 连接与鉴权")
    try:
        WS("bad.token")
        check("无令牌/非法令牌握手被拒", False, "居然握上了")
    except Exception as e:
        check("无令牌/非法令牌握手被拒", True, f"（{type(e).__name__}）")

    wsa, wsb = WS(tok_a), WS(tok_b)
    hello_a, hello_b = wsa.wait("hello"), wsb.wait("hello")
    check("A 收到 hello 帧", hello_a is not None, f"uid={(hello_a or {}).get('data', {}).get('uid')}")
    check("B 收到 hello 帧", hello_b is not None, f"uid={(hello_b or {}).get('data', {}).get('uid')}")

    print("\n2. 新消息推送（对应「收得到消息但没通知」这个现象）")
    msg = http("POST", "/v1/im/messages", {"peer_uid": uid_b, "text": "端到端验证：你好"}, tok_a)
    check("A 发消息成功", msg.get("code") == 0, brief(msg.get("data", {}), 80))
    push = wsb.wait("new_message")
    check("B 未刷新页面就收到 new_message", push is not None)
    if push:
        d = push.get("data", {})
        check("推送带发送者与正文（够弹通知）",
              bool(d.get("text")) and d.get("from_uid") == uid_a,
              f"from={d.get('from_uid')} text={d.get('text')} nick={d.get('nickname')}")

    print("\n3. 来电推送")
    check("A 实名认证通过", realname(tok_a, seed).get("code") == 0)
    check("B 实名认证通过", realname(tok_b, seed + 1).get("code") == 0)
    check("A 充值成功（通话需 >=120 金币）", recharge(tok_a).get("code") == 0)
    call = http("POST", "/v1/calls", {"callee_id": uid_b, "media_type": 2}, tok_a)
    check("通话创建成功", call.get("code") == 0, brief(call.get("data", {}), 120))
    # 创建响应是 {"data": {"session": {...}}}，call_no 嵌在 session 里。
    call_no = ((call.get("data") or {}).get("session") or {}).get("call_no")
    invite = wsb.wait("call_invite")
    check("B 收到 call_invite", invite is not None)
    if invite:
        d = invite.get("data", {})
        check("邀请带 call_no 与 media_type",
              d.get("call_no") == call_no and d.get("media_type") == 2,
              f"call_no={d.get('call_no')} media={d.get('media_type')}")

    print("\n4. 接听与状态回传")
    accepted = http("POST", f"/v1/calls/{call_no}/accept", {}, tok_b)
    check("B 接听成功", accepted.get("code") == 0, brief(accepted, 120))
    upd = wsa.wait("call_update")
    check("A 收到 call_update", upd is not None)
    if upd:
        d = upd.get("data", {})
        # 两种写法都下发：statusText 是早期约定，status_text 与其余接口保持一致。
        check("状态为 talking", d.get("status_text") == "talking" and d.get("statusText") == "talking",
              brief(d, 120))

    print("\n5. WebRTC 信令转发（offer / answer / ICE 都走这条）")
    wsa.send({"type": "signal", "to": uid_b, "call_no": call_no,
              "kind": "offer", "payload": {"sdp": "v=0\r\no=- 1 2 IN IP4 127.0.0.1\r\n", "type": "offer"}})
    sig = wsb.wait("signal")
    check("B 收到 signal", sig is not None)
    if sig:
        check("kind / payload / call_no 原样透传，from 由服务端补",
              sig.get("kind") == "offer"
              and (sig.get("payload") or {}).get("type") == "offer"
              and sig.get("call_no") == call_no
              and as_int((sig.get("data") or {}).get("from")) == uid_a,
              f"kind={sig.get('kind')} from={(sig.get('data') or {}).get('from')}")

    print("\n6. 挂断通知")
    hung = http("POST", f"/v1/calls/{call_no}/hangup", {}, tok_b)
    check("B 挂断成功", hung.get("code") == 0, brief(hung, 120))
    fin = wsa.wait("call_update")
    check("A 收到结束通知", fin is not None, brief((fin or {}).get("data", {}), 120))

    wsa.close()
    wsb.close()
    print(f"\n结果：{PASS} 通过 / {FAIL} 失败")
    return 1 if FAIL else 0


if __name__ == "__main__":
    raise SystemExit(main())
