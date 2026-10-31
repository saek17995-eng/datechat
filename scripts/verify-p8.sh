#!/usr/bin/env bash
# P8 端到端验收：运营后台、监控、合规与压测
# 覆盖：后台登录与概览、用户检索/详情/封禁/解禁、订单与通话查询、操作日志留痕、
#       /metrics 抓取与鉴权、协议接口、实名门槛、单日充值额度、留存清理 dry-run、压测结论。
set -uo pipefail

BASE="${BASE:-http://127.0.0.1:8080}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PWD="${ADMIN_PWD:-dev-admin-pwd}"
SERVER_DIR="${SERVER_DIR:-$(cd "$(dirname "$0")/../server" && pwd)}"

PASS=0; FAIL=0
ok()   { printf '  \033[32m✓\033[0m %s\n' "$*"; PASS=$((PASS+1)); }
bad()  { printf '  \033[31m✗\033[0m %s\n' "$*"; FAIL=$((FAIL+1)); }
step() { printf '\n\033[1m%s\033[0m\n' "$*"; }
info() { printf '  %s\n' "$*"; }

api() {
  local method="$1" path="$2" token="${3:-}" data="${4:-}"
  local args=(-sS -X "$method" "$BASE$path" -H 'Content-Type: application/json')
  [ -n "$token" ] && args+=(-H "Authorization: Bearer $token")
  [ -n "$data" ] && args+=(--data "$data")
  curl "${args[@]}"
}
code() { printf '%s' "${1:-}" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("code","?"))' 2>/dev/null || echo '?'; }
field() { printf '%s' "${1:-}" | python3 -c "
import sys,json
d=json.load(sys.stdin).get('data') or {}
for k in '${2:-}'.split('.'):
    if k=='': break
    d=(d or {}).get(k) if isinstance(d,dict) else None
if isinstance(d,(list,dict)):
    print(json.dumps(d,ensure_ascii=False))
elif d is None:
    print('')
else:
    print(d)" 2>/dev/null; }
sql() { mysql -h 127.0.0.1 -u datechat -pdatechat_pass datechat -N -e "$1" 2>/dev/null; }
redis() {
  python3 -c "
import socket, sys, time
args = sys.argv[1:]
s = socket.create_connection(('127.0.0.1', 6379), 3)
buf = ('*%d\r\n' % len(args)).encode()
for a in args:
    b = a.encode()
    buf += b'\$%d\r\n' % len(b) + b + b'\r\n'
s.sendall(buf)
time.sleep(0.2)
out = s.recv(65536).decode('utf-8', 'replace').strip()
print(out[1:].split('\r\n')[0] if out[:1] in (':', '+', '-') else out.split('\r\n')[0])
" "$@"
}

TS="$(date +%s)"
PHONE="170${TS:2:7}8"

login_user() {
  local phone="$1"
  local cv_json cv
  cv_json="$(api POST /v1/auth/sms/code "" "{\"phone\":\"$phone\",\"scene\":\"login\"}")"
  cv="$(field "$cv_json" debug_code)"
  api POST /v1/auth/login "" "{\"phone\":\"$phone\",\"code\":\"$cv\",\"device_id\":\"p8-$phone\",\"platform\":1,\"app_version\":\"0.8.0\"}"
}

step "0. 后台登录 + 普通用户登录"
ATOK_JSON="$(api POST /v1/admin/login "" "{\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PWD\"}")"
ATOK="$(field "$ATOK_JSON" token)"
[ -n "${ATOK:-}" ] && ok "后台登录成功" || { bad "后台登录失败：${ATOK_JSON:0:200}"; exit 1; }

U="$(login_user "$PHONE")"; UID_U="$(field "$U" uid)"; TOK_U="$(field "$U" token.access_token)"
[ -n "${UID_U:-}" ] && ok "普通用户登录成功 uid=$UID_U" || { bad "普通用户登录失败"; exit 1; }

step "1. 运营概览"
r="$(api GET /v1/admin/overview "$ATOK")"
if [ "$(code "$r")" = "0" ]; then
  ok "概览返回成功"
  info "用户总数 $(field "$r" users_total)，今日新增 $(field "$r" users_new_today)，今日通话 $(field "$r" calls_today)"
else
  bad "概览失败：$(code "$r") ${r:0:160}"
fi

step "2. 用户检索：UID / 昵称 / 手机号"
r="$(api GET "/v1/admin/users?keyword=$UID_U" "$ATOK")"
[ "$(field "$r" total)" -ge 1 ] && ok "按 UID 能检索到该用户" || bad "按 UID 检索失败：${r:0:160}"
r="$(api GET "/v1/admin/users?keyword=$PHONE" "$ATOK")"
[ "$(field "$r" total)" -ge 1 ] && ok "按手机号能检索到（手机号走 HMAC 精确命中）" || bad "按手机号检索失败：${r:0:160}"
PHONE_SHOWN="$(printf '%s' "$r" | python3 -c "
import sys,json
d=json.load(sys.stdin).get('data') or {}
for x in (d.get('list') or []):
    if str(x.get('uid'))=='$UID_U': print(x.get('phone'))" 2>/dev/null)"
case "${PHONE_SHOWN:-}" in
  *"****"*) ok "手机号后台也只给脱敏值（${PHONE_SHOWN}）" ;;
  *) bad "后台手机号未脱敏：$PHONE_SHOWN" ;;
esac

step "3. 用户详情"
r="$(api GET "/v1/admin/users/$UID_U" "$ATOK")"
[ "$(code "$r")" = "0" ] && ok "详情返回成功（余额 $(field "$r" coin_balance)，通话 $(field "$r" call_count) 次）" || bad "详情失败：${r:0:160}"

step "4. 封禁与解禁（含原因与审计）"
r="$(api POST "/v1/admin/users/$UID_U/ban" "$ATOK" '{"days":7,"reason":"P8 验收：测试封禁"}')"
[ "$(code "$r")" = "0" ] && ok "封禁成功（7 天）" || bad "封禁失败：${r:0:160}"
ST="$(sql "SELECT status FROM user WHERE id=$UID_U;")"
[ "${ST:-0}" = "3" ] && ok "数据库状态已变为封禁（3）" || bad "状态未生效：$ST"
BU="$(sql "SELECT COUNT(1) FROM user WHERE id=$UID_U AND ban_until IS NOT NULL;")"
[ "${BU:-0}" = "1" ] && ok "封禁到期时间已写入（到期自动恢复的前提）" || bad "ban_until 未写入"
LOGN="$(sql "SELECT COUNT(1) FROM admin_op_log WHERE module='user' AND action='set_status' AND target_id='$UID_U';")"
[ "${LOGN:-0}" -ge 1 ] && ok "审计日志已记录 ${LOGN} 条" || bad "审计日志缺失"

r="$(api POST "/v1/admin/users/$UID_U/unban" "$ATOK" '{"note":"P8 验收：解除"}')"
[ "$(code "$r")" = "0" ] && ok "解禁成功" || bad "解禁失败：${r:0:160}"
ST="$(sql "SELECT status FROM user WHERE id=$UID_U;")"
[ "${ST:-0}" = "1" ] && ok "状态已恢复为正常（1）" || bad "状态未恢复：$ST"

step "5. 封禁必须填原因"
r="$(api POST "/v1/admin/users/$UID_U/ban" "$ATOK" '{"days":1,"reason":""}')"
[ "$(code "$r")" != "0" ] && ok "空原因的封禁被拒绝" || bad "空原因竟然允许封禁"

step "6. 订单与通话查询"
r="$(api GET "/v1/admin/orders?size=5" "$ATOK")"
[ "$(code "$r")" = "0" ] && ok "订单列表可查（total=$(field "$r" total)）" || bad "订单列表失败：${r:0:160}"
r="$(api GET "/v1/admin/calls?size=5" "$ATOK")"
[ "$(code "$r")" = "0" ] && ok "通话列表可查（total=$(field "$r" total)）" || bad "通话列表失败：${r:0:160}"
r="$(api GET "/v1/admin/oplogs?size=5" "$ATOK")"
[ "$(code "$r")" = "0" ] && ok "操作日志可查（total=$(field "$r" total)）" || bad "操作日志失败：${r:0:160}"

step "7. 后台接口不能被用户令牌访问"
r="$(api GET /v1/admin/overview "$TOK_U")"
[ "$(code "$r")" != "0" ] && ok "普通用户令牌访问后台被拒" || bad "用户令牌竟然能进后台"

step "8. 监控：/metrics"
r="$(curl -sS "$BASE/metrics")"
if printf '%s' "$r" | grep -q "http_requests_total"; then
  ok "/metrics 已暴露请求计数"
else
  bad "/metrics 未包含指标：${r:0:160}"
fi
if printf '%s' "$r" | grep -q 'http_request_duration_seconds_bucket'; then
  ok "/metrics 含延迟直方图分桶"
else
  bad "/metrics 缺少延迟直方图"
fi
if printf '%s' "$r" | grep -q 'route="/v1/admin/overview"'; then
  ok "指标按路由模板打标签（不会因 UID 造成基数爆炸）"
else
  bad "指标标签异常"
fi

step "9. 协议接口（未登录可访问）"
r="$(api GET /v1/app/agreements "")"
[ "$(code "$r")" = "0" ] && ok "协议接口可访问（min_age=$(field "$r" min_age)）" || bad "协议接口失败：${r:0:160}"

step "10. 合规：未实名不能发起通话"
P2="170${TS:2:7}9"
V="$(login_user "$P2")"; UID_V="$(field "$V" uid)"; TOK_V="$(field "$V" token.access_token)"
r="$(api POST /v1/calls "$TOK_V" "{\"callee_id\":$UID_U,\"media_type\":1}")"
if [ "$(code "$r")" != "0" ]; then
  ok "未实名用户发起通话被拒（code=$(code "$r")）"
else
  bad "未实名竟然能发起通话：${r:0:160}"
fi
# 置为已实名后应放行（余额不足属于另一类错误，只要不再是"请实名"即可）
# 实名没有"直接置为通过"的接口（那等于绕过实名），只能改库。
sql "UPDATE user_profile SET realname_status=2 WHERE user_id=$UID_V;" >/dev/null
# 余额走真实充值链路，不直接 UPDATE wallet：
# 直接改余额会让"钱包余额 = 流水累计"这条不变量失真，P5 的对账项会误报。
r="$(api GET /v1/wallet/packages "$TOK_V")"
PKG="$(printf '%s' "$r" | python3 -c '
import sys,json
d=json.load(sys.stdin).get("data") or {}
lst=d.get("list") or []
print(lst[0].get("id","") if lst else "")' 2>/dev/null)"
if [ -n "${PKG:-}" ]; then
  r="$(api POST /v1/wallet/recharge "$TOK_V" "{\"package_id\":$PKG}")"
  ORD="$(field "$r" order_no)"
  if [ -n "${ORD:-}" ]; then
    api POST "/v1/wallet/recharge/$ORD/mock-paid" "$TOK_V" >/dev/null
  fi
fi
r="$(api POST /v1/calls "$TOK_V" "{\"callee_id\":$UID_U,\"media_type\":1}")"
if printf '%s' "$r" | grep -q "实名"; then
  bad "已实名后仍提示实名：${r:0:160}"
else
  ok "已实名用户不再被实名门槛拦截（code=$(code "$r")）"
fi

step "11. 留存清理：默认 dry-run 不删数据"
OUT="$(cd "$SERVER_DIR" && source ../scripts/go-env.sh 2>/dev/null; go run ./cmd/cleanup -c configs/config.dev.yaml 2>&1 | tail -12)"
info "${OUT//$'\n'/ | }"
printf '%s' "$OUT" | grep -q "dry-run" && ok "默认是 dry-run（不会误删）" || bad "未处于 dry-run 模式"
BEFORE="$(sql "SELECT COUNT(1) FROM sms_send_log;")"
OUT2="$(cd "$SERVER_DIR" && source ../scripts/go-env.sh 2>/dev/null; go run ./cmd/cleanup -c configs/config.dev.yaml -days 0 2>&1 | tail -5)"
AFTER="$(sql "SELECT COUNT(1) FROM sms_send_log;")"
[ "${BEFORE:-0}" = "${AFTER:-0}" ] && ok "dry-run 后数据行数不变（${BEFORE}）" || bad "dry-run 竟然删了数据：$BEFORE → $AFTER"

step "12. 压测"
BENCH="$(cd "$SERVER_DIR" && source ../scripts/go-env.sh 2>/dev/null; go run ./cmd/bench -c configs/config.dev.yaml -users 20 -dur 8s -think 20ms -rps 40 2>&1 | tail -20)"
info "${BENCH//$'\n'/ | }"
printf '%s' "$BENCH" | grep -q "QPS" && ok "压测完成并输出 QPS/分位延迟" || bad "压测输出异常"
printf '%s' "$BENCH" | grep -q "P95" && ok "压测输出 P95 延迟" || bad "压测缺少 P95"

step "13. 慢请求阈值已生效（配置层面）"
grep -q "slow_request" "$SERVER_DIR/configs/config.dev.yaml" && ok "已配置慢请求阈值" || bad "未配置慢请求阈值"
grep -q "metrics_enabled" "$SERVER_DIR/configs/config.dev.yaml" && ok "已配置 /metrics 开关" || bad "未配置 metrics 开关"

printf '\n\033[1m结果：\033[32m%s 通过\033[0m，\033[31m%s 失败\033[0m\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ] || exit 1
