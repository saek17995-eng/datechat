#!/usr/bin/env bash
# P4 端到端验收：关系与内容安全
# 覆盖：拉黑拦截（接口 + IM 回调）、拉黑解除、黑名单列表、举报提交/去重/限频、
#       敏感词拦截与审核留痕、后台登录、越权拦截、后台处置与审计。
set -uo pipefail

BASE="${BASE:-http://127.0.0.1:8080}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PWD="${ADMIN_PWD:-dev-admin-pwd}"

PASS=0
FAIL=0
ok()   { printf '  \033[32m✓\033[0m %s\n' "$*"; PASS=$((PASS+1)); }
bad()  { printf '  \033[31m✗\033[0m %s\n' "$*"; FAIL=$((FAIL+1)); }
step() { printf '\n\033[1m%s\033[0m\n' "$*"; }
info() { printf '  %s\n' "$*"; }

# api METHOD PATH [TOKEN] [DATA]
api() {
  local method="$1" path="$2" token="${3:-}" data="${4:-}"
  local args=(-sS -X "$method" "$BASE$path" -H 'Content-Type: application/json')
  [ -n "$token" ] && args+=(-H "Authorization: Bearer $token")
  [ -n "$data" ] && args+=(--data "$data")
  curl "${args[@]}"
}

code()  { printf '%s' "${1:-}" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("code","?"))' 2>/dev/null || echo '?'; }
field() { printf '%s' "${1:-}" | python3 -c "
import sys,json
d=json.load(sys.stdin).get('data') or {}
for k in '${2:-}'.split('.'):
    d = (d or {}).get(k) if isinstance(d,dict) else None
print('' if d is None else d)" 2>/dev/null; }

TS="$(date +%s)"
# 手机号本机号段，避免撞历史数据
BASE_NUM="${TS:2:7}"   # 7 位，拼上 170 前缀与尾号刚好 11 位
phoneA="170${BASE_NUM}1"
phoneB="170${BASE_NUM}2"

login() { # login PHONE -> "uid token"
  local p="$1"
  local sent c r
  sent="$(api POST /v1/auth/sms/code "" "{\"phone\":\"$p\",\"scene\":\"login\"}")"
  c="$(field "$sent" debug_code)"
  if [ -z "$c" ]; then printf '\n'; return 1; fi
  r="$(api POST /v1/auth/login "" "{\"phone\":\"$p\",\"code\":\"$c\",\"device_id\":\"p4-$p\",\"platform\":1,\"app_version\":\"0.4.0\"}")"
  printf '%s %s' "$(field "$r" uid)" "$(field "$r" token.access_token)"
}

step "0. 准备两个测试用户"
read -r AUID ATOK <<<"$(login "$phoneA")"
read -r BUID BTOK <<<"$(login "$phoneB")"
if [ -n "${AUID:-}" ] && [ -n "${BUID:-}" ]; then
  ok "用户登录成功 A=$AUID B=$BUID"
else
  bad "登录失败，无法继续"; exit 1
fi

step "1. 拉黑后无法发消息"
api POST "/v1/users/$BUID/block" "$ATOK" >/dev/null
r="$(api POST /v1/im/messages "$ATOK" "{\"peer_uid\":$BUID,\"text\":\"hello\",\"client_msg_id\":\"p4-1-$TS\"}")"
[ "$(code "$r")" = "30001" ] && ok "拉黑后发消息被拒（30001）" || bad "期望 30001，实际 $(code "$r")"
r="$(api POST /v1/im/messages "$BTOK" "{\"peer_uid\":$AUID,\"text\":\"hello\",\"client_msg_id\":\"p4-2-$TS\"}")"
[ "$(code "$r")" = "30001" ] && ok "被拉黑方发消息同样被拒（双向）" || bad "期望 30001，实际 $(code "$r")"

step "2. IM 发消息前回调拦截拉黑关系"
cb="$(api POST '/v1/im/callback?SdkAppid=1400000000&CallbackCommand=C2C.CallbackBeforeSendMsg' '' \
  "{\"CallbackCommand\":\"C2C.CallbackBeforeSendMsg\",\"From_Account\":\"u_$AUID\",\"To_Account\":\"u_$BUID\",\"MsgSeq\":1,\"MsgRandom\":1,\"MsgTime\":$TS,\"MsgKey\":\"p4-$TS\",\"MsgId\":\"p4-cb-1\",\"MsgBody\":[{\"MsgType\":\"TIMTextElem\",\"MsgContent\":{\"Text\":\"正常内容\"}}],\"EventTime\":${TS}000}")"
echo "$cb" | grep -q '"ErrorCode":120001' && ok "回调拦截拉黑（120001）" || bad "回调未拦截：$cb"

step "3. 解除拉黑后恢复发送"
api DELETE "/v1/users/$BUID/block" "$ATOK" >/dev/null
r="$(api POST /v1/im/messages "$ATOK" "{\"peer_uid\":$BUID,\"text\":\"解除后消息\",\"client_msg_id\":\"p4-3-$TS\"}")"
[ "$(code "$r")" = "0" ] && ok "解除拉黑后可正常发送" || bad "解除后仍失败：$(code "$r")"

step "4. 黑名单列表"
r="$(api GET /v1/users/me/blocks "$ATOK")"
[ "$(code "$r")" = "0" ] && ok "黑名单接口可用（total=$(field "$r" total)）" || bad "黑名单查询失败"

step "5. 敏感词消息被拦截并留痕"
before="$(mysql -h 127.0.0.1 -u datechat -pdatechat_pass datechat -N -e \
  "SELECT COUNT(*) FROM moderation_record WHERE scene IN ('message','callback');" 2>/dev/null)"
r="$(api POST /v1/im/messages "$ATOK" "{\"peer_uid\":$BUID,\"text\":\"加我微信\",\"client_msg_id\":\"p4-4-$TS\"}")"
[ "$(code "$r")" = "30002" ] && ok "敏感词消息被拦截（30002）" || bad "期望 30002，实际 $(code "$r")"
after="$(mysql -h 127.0.0.1 -u datechat -pdatechat_pass datechat -N -e \
  "SELECT COUNT(*) FROM moderation_record WHERE scene IN ('message','callback');" 2>/dev/null)"
[ "${after:-0}" -gt "${before:-0}" ] && ok "已写入审核留痕（${before} 到 ${after}）" || bad "未写入审核留痕"

step "6. 举报提交与去重"
r="$(api POST /v1/reports "$ATOK" "{\"target_type\":1,\"target_id\":\"$BUID\",\"target_uid\":$BUID,\"reason\":\"spam\",\"detail\":\"测试举报\"}")"
if [ "$(code "$r")" = "0" ]; then
  ok "举报提交成功（id=$(field "$r" id)）"
  RID="$(field "$r" id)"
else
  bad "举报提交失败：$(code "$r")"; RID=""
fi
r="$(api POST /v1/reports "$ATOK" "{\"target_type\":1,\"target_id\":\"$BUID\",\"target_uid\":$BUID,\"reason\":\"spam\",\"detail\":\"重复举报\"}")"
[ "$(code "$r")" != "0" ] && ok "重复举报被拒（$(code "$r")）" || bad "重复举报未被拦截"
r="$(api POST /v1/reports "$ATOK" "{\"target_type\":1,\"target_id\":\"$BUID\",\"reason\":\"bad-reason\"}")"
[ "$(code "$r")" != "0" ] && ok "非法举报理由被拒" || bad "非法理由未被拦截"

step "7. 后台登录与越权拦截"
r="$(api POST /v1/admin/login "" "{\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PWD\"}")"
ATOK_ADMIN="$(field "$r" token)"
[ -n "$ATOK_ADMIN" ] && ok "后台登录成功（role=$(field "$r" role)）" || bad "后台登录失败：$r"
r="$(api GET /v1/admin/reports "$ATOK")"
[ "$(code "$r")" = "10103" ] && ok "用户令牌访问后台被拒（10103）" || bad "越权未被拦截：$(code "$r")"

step "8. 后台处置举报并落审计"
if [ -n "${RID:-}" ] && [ -n "$ATOK_ADMIN" ]; then
  r="$(api POST "/v1/admin/reports/$RID/handle" "$ATOK_ADMIN" '{"action":"ban","note":"P4 验收处置"}')"
  [ "$(code "$r")" = "0" ] && ok "举报处置成功" || bad "举报处置失败：$(code "$r")"
  st="$(mysql -h 127.0.0.1 -u datechat -pdatechat_pass datechat -N -e \
    "SELECT status FROM user WHERE id=$BUID;" 2>/dev/null)"
  [ "$st" = "3" ] && ok "被举报用户已封禁（status=3）" || bad "用户状态未变更：${st:-空}"
  n="$(mysql -h 127.0.0.1 -u datechat -pdatechat_pass datechat -N -e \
    "SELECT COUNT(*) FROM admin_op_log WHERE action='ban';" 2>/dev/null)"
  [ "${n:-0}" -ge 1 ] && ok "已写入操作审计（$n 条）" || bad "未写入操作审计"
else
  bad "缺少举报ID或后台令牌，跳过处置"
fi

step "9. 后台审核列表"
r="$(api GET "/v1/admin/moderations?result=2" "$ATOK_ADMIN")"
[ "$(code "$r")" = "0" ] && ok "审核列表可查（total=$(field "$r" total)）" || bad "审核列表失败：$(code "$r")"

printf '\n\033[1m结果：通过 %d 项，失败 %d 项\033[0m\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
