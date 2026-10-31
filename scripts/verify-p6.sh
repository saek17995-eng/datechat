#!/usr/bin/env bash
# P6 端到端验收：通话与计费
# 覆盖：发起/接听/拒接/取消、双端同一房间、按分钟预扣、心跳续扣、
#       账单金额与秒数一致、余额耗尽自动挂断、心跳超时自动挂断、账本对账。
#
# 说明：脚本会真实等待"跨过一分钟"，因此完整跑完约需 90 秒。
# 真实音视频（双机能互相听见）依赖 TRTC 密钥与两台真机，脚本只能验证服务端链路。
set -uo pipefail

BASE="${BASE:-http://127.0.0.1:8080}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PWD="${ADMIN_PWD:-dev-admin-pwd}"

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

TS="$(date +%s)"
PHONE_A="170${TS:2:7}1"
PHONE_B="170${TS:2:7}2"

login() {
  local phone="$1"
  # 只发一次码：60 秒内重复发送会被限频（20005），第二次拿不到 debug_code。
  local cv_json cv
  cv_json="$(api POST /v1/auth/sms/code "" "{\"phone\":\"$phone\",\"scene\":\"login\"}")"
  cv="$(field "$cv_json" debug_code)"
  api POST /v1/auth/login "" "{\"phone\":\"$phone\",\"code\":\"$cv\",\"device_id\":\"p6-$phone\",\"platform\":1,\"app_version\":\"0.6.0\"}"
}

step "0. 登录主叫 A 与被叫 B"
rA="$(login "$PHONE_A")"; UID_A="$(field "$rA" uid)"; TOK_A="$(field "$rA" token.access_token)"
rB="$(login "$PHONE_B")"; UID_B="$(field "$rB" uid)"; TOK_B="$(field "$rB" token.access_token)"
[ -n "${UID_A:-}" ] && [ -n "${UID_B:-}" ] && ok "双端登录成功 A=$UID_A B=$UID_B" || { bad "登录失败"; exit 1; }

# P8 起"未实名不能发起通话"（compliance.realname_required_for_call）。
# 这里直接把两人置为已实名：实名认证本身属于 P1 的范围，不该让 P6 的计费验收卡在上面。
sql "UPDATE user_profile SET realname_status=2 WHERE user_id IN ($UID_A,$UID_B);" >/dev/null
ok "A/B 已置为已实名（绕过 P8 实名门槛，专注验计费）"

step "1. 通话定价（服务端定价，客户端不可传价）"
r="$(api GET "/v1/call/price?user_id=$UID_B&media_type=1" "$TOK_A")"
PER_MIN="$(field "$r" per_min)"; RATE="$(field "$r" platform_rate)"
[ "${PER_MIN:-0}" -gt 0 ] && ok "语音定价 ${PER_MIN} 金币/分钟，平台抽成 ${RATE}‰" || bad "定价查询失败：$(code "$r")"

step "2. A 充值最小档（60 金币）"
r="$(api GET /v1/wallet/packages "$TOK_A")"
PKG="$(field "$r" list | python3 -c 'import sys,json;print((json.load(sys.stdin) or [{}])[0].get("id",""))' 2>/dev/null)"
r="$(api POST /v1/wallet/recharge "$TOK_A" "{\"package_id\":$PKG}")"
ORDER="$(field "$r" order_no)"
api POST /v1/pay/mock/notify "" "{\"order_no\":\"$ORDER\",\"trade_no\":\"MOCK-$ORDER\",\"amount_fen\":$(field "$r" amount_fen)}" >/dev/null
BAL_A="$(sql "SELECT coin_balance FROM wallet WHERE user_id=$UID_A;")"
[ "${BAL_A:-0}" = "60" ] && ok "A 余额 ${BAL_A} 金币" || bad "充值后余额异常：${BAL_A}"

step "3. A 发起语音通话"
r="$(api POST /v1/calls "$TOK_A" "{\"callee_id\":$UID_B,\"media_type\":1}")"
CALL_NO="$(field "$r" session.call_no)"
ROOM_A="$(field "$r" ticket.room_id)"
ST="$(field "$r" session.status)"
[ -n "${CALL_NO:-}" ] && ok "通话已创建 ${CALL_NO}（status=${ST}，房间 ${ROOM_A}）" || bad "发起通话失败：$(code "$r")"

step "4. 并发重复呼叫被拒"
r="$(api POST /v1/calls "$TOK_A" "{\"callee_id\":$UID_B,\"media_type\":1}")"
[ "$(code "$r")" = "40001" ] && ok "已有进行中通话，二次呼叫被拒（40001）" || bad "并发呼叫未被拦截：$(code "$r")"

step "5. B 接听：进入通话并预扣第 1 分钟"
r="$(api POST "/v1/calls/$CALL_NO/accept" "$TOK_B")"
ROOM_B="$(field "$r" ticket.room_id)"
ST="$(field "$r" session.status)"
[ "${ST:-}" = "2" ] && ok "通话已接通（status=2）" || bad "接听失败：$(code "$r")"
[ "${ROOM_B:-}" = "${ROOM_A:-}" ] && ok "双端房间号一致（${ROOM_B}），具备双机通话条件" || bad "房间号不一致：$ROOM_A vs $ROOM_B"
BAL_A="$(sql "SELECT coin_balance FROM wallet WHERE user_id=$UID_A;")"
[ "${BAL_A:-0}" = "10" ] && ok "接通即扣 ${PER_MIN}，A 余额 ${BAL_A}" || bad "预扣异常，余额 ${BAL_A}"

step "6. 心跳续扣（未跨分钟不重复扣费）"
BEFORE="$(sql "SELECT COUNT(*) FROM call_segment WHERE call_session_id=(SELECT id FROM call_session WHERE call_no='$CALL_NO');")"
r="$(api POST "/v1/calls/$CALL_NO/heartbeat" "$TOK_A" '{"elapsed_sec":3}')"
CM="$(field "$r" charged_minutes)"
[ "$(field "$r" ended)" = "False" ] && ok "通话进行中（ended=false）" || bad "心跳后被误判结束"
[ "${CM:-0}" = "1" ] && ok "未跨分钟，已扣分钟数仍为 ${CM}" || bad "重复扣费：${CM}"
AFTER="$(sql "SELECT COUNT(*) FROM call_segment WHERE call_session_id=(SELECT id FROM call_session WHERE call_no='$CALL_NO');")"
[ "${AFTER:-0}" = "${BEFORE:-0}" ] && ok "切片未新增（${AFTER} 条），心跳幂等" || bad "切片异常增长：${BEFORE} → ${AFTER}"

step "7. 挂断结算：账单金额与秒数一致"
r="$(api POST "/v1/calls/$CALL_NO/hangup" "$TOK_A")"
BM="$(field "$r" billable_minutes)"; TC="$(field "$r" total_coins)"; DS="$(field "$r" duration_sec)"
[ "${DS:-0}" -le 60 ] && ok "时长 ${DS}s（服务端时钟，≤60s）" || bad "时长异常：${DS}"
[ "${TC:-0}" = "$((BM * PER_MIN))" ] && ok "账单 ${TC} 金币 = ${BM} 分钟 × ${PER_MIN}" || bad "金额与分钟不符：${TC} vs ${BM}×${PER_MIN}"
r="$(api GET "/v1/calls/$CALL_NO" "$TOK_A")"
AMT="$(field "$r" consistency.amount_matches)"; MMS="$(field "$r" consistency.minutes_match_sec)"; SM="$(field "$r" consistency.segment_matches)"
[ "${AMT:-}" = "True" ] && ok "一致性自检：金额 = 分钟 × 单价" || bad "金额一致性失败"
[ "${MMS:-}" = "True" ] && ok "一致性自检：分钟 = ceil(秒数/60)" || bad "分钟与秒数不一致"
[ "${SM:-}" = "True" ] && ok "一致性自检：切片合计 = 账单总额" || bad "切片与账单不一致"
INCOME="$(sql "SELECT coin_balance FROM wallet WHERE user_id=$UID_B;")"
WANT=$(( TC * (1000 - RATE) / 1000 ))
[ "${INCOME:-0}" = "${WANT}" ] && ok "被叫收益到账 ${INCOME} 金币（抽成 ${RATE}‰）" || bad "收益不符：${INCOME} vs ${WANT}"

step "8. 余额耗尽自动挂断"
r="$(api POST /v1/wallet/recharge "$TOK_A" "{\"package_id\":$PKG}")"
ORDER2="$(field "$r" order_no)"
api POST /v1/pay/mock/notify "" "{\"order_no\":\"$ORDER2\",\"trade_no\":\"MOCK-$ORDER2\",\"amount_fen\":$(field "$r" amount_fen)}" >/dev/null
BAL_A="$(sql "SELECT coin_balance FROM wallet WHERE user_id=$UID_A;")"
info "A 余额 ${BAL_A}（只够 1 分钟，等待跨分钟…）"
r="$(api POST /v1/calls "$TOK_A" "{\"callee_id\":$UID_B,\"media_type\":1}")"
CALL2="$(field "$r" session.call_no)"
api POST "/v1/calls/$CALL2/accept" "$TOK_B" >/dev/null
# 关键：等待期间必须持续心跳。60 秒不发心跳会先触发"心跳超时"（end_reason=3），
# 那样验的是掉线兜底而不是余额耗尽——心跳保活才能把通话撑到第二分钟开始扣费。
# 必须等到"超过 60 秒"而不是刚好 60 秒：60 秒整仍算第 1 分钟，
# 撑到第 61 秒才会触发第二分钟续扣，那时余额不足才会自动挂断。
for _ in 1 2 3 4 5 6 7; do
  sleep 10
  api POST "/v1/calls/$CALL2/heartbeat" "$TOK_A" '{"elapsed_sec":10}' >/dev/null
done
r="$(api POST "/v1/calls/$CALL2/heartbeat" "$TOK_A" '{"elapsed_sec":62}')"
ER="$(field "$r" end_reason)"; ENDED="$(field "$r" ended)"
[ "${ENDED:-}" = "True" ] && ok "余额耗尽后通话已结束" || bad "未自动挂断"
[ "${ER:-}" = "2" ] && ok "结束原因=余额不足（end_reason=2）" || bad "结束原因异常：${ER}"
CM2="$(field "$r" charged_minutes)"
[ "${CM2:-0}" = "1" ] && ok "第二分钟扣不起，只记 ${CM2} 分钟" || bad "续扣分钟数异常：${CM2}"
r="$(api GET "/v1/calls/$CALL2" "$TOK_A")"
BM2="$(field "$r" session.billable_minutes)"; TC2="$(field "$r" session.total_coins)"; DS2="$(field "$r" session.duration_sec)"
[ "${TC2:-0}" = "$((BM2 * PER_MIN))" ] && ok "只结算已付费部分：${DS2}s → ${BM2} 分钟 → ${TC2} 金币" || bad "结算金额异常：${TC2}"
[ "$(field "$r" consistency.amount_matches)" = "True" ] && ok "耗尽场景账单一仍然自洽" || bad "耗尽场景账单不自洽"
BAL_END="$(sql "SELECT coin_balance FROM wallet WHERE user_id=$UID_A;")"
WANT_END=$(( BAL_A - PER_MIN ))
[ "${BAL_END:-0}" = "${WANT_END}" ] && ok "只扣掉能付的 ${PER_MIN}，余额 ${BAL_END} 未出现负数" || bad "余额异常：${BAL_END}（应为 ${WANT_END}）"
NEG2="$(sql "SELECT COUNT(*) FROM wallet_transaction WHERE user_id=$UID_A AND balance_after < 0;")"
[ "${NEG2:-0}" = "0" ] && ok "流水无负余额快照" || bad "${NEG2} 条流水出现负余额"

step "9. 心跳超时由服务端兜底挂断"
ST2="$(sql "SELECT status FROM call_session WHERE call_no='$CALL2';")"
[ "${ST2:-}" = "3" ] && ok "上一通已结束，可发起新通话" || bad "上一通未结束：status=${ST2}"
r="$(api POST /v1/wallet/recharge "$TOK_A" "{\"package_id\":$PKG}")"
ORDER3="$(field "$r" order_no)"
api POST /v1/pay/mock/notify "" "{\"order_no\":\"$ORDER3\",\"trade_no\":\"MOCK-$ORDER3\",\"amount_fen\":$(field "$r" amount_fen)}" >/dev/null
r="$(api POST /v1/calls "$TOK_A" "{\"callee_id\":$UID_B,\"media_type\":1}")"
CALL3="$(field "$r" session.call_no)"
api POST "/v1/calls/$CALL3/accept" "$TOK_B" >/dev/null
info "接通后不再发心跳，等待超过心跳超时（30s）…"
sleep 32
r="$(api POST /v1/admin/login "" "{\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PWD\"}")"
ATOK="$(field "$r" token)"
r="$(api POST "/v1/admin/calls/sweep" "$ATOK")"
[ "$(code "$r")" = "0" ] && ok "后台巡检执行成功（超时未接=$(field "$r" ring_timeout) 心跳超时=$(field "$r" beat_timeout) 余额耗尽=$(field "$r" no_balance)）" || bad "巡检失败：$(code "$r")"
ER3="$(sql "SELECT end_reason FROM call_session WHERE call_no='$CALL3';")"
ST3="$(sql "SELECT status FROM call_session WHERE call_no='$CALL3';")"
[ "${ST3:-}" = "3" ] && ok "掉线通话已被服务端收尾（status=3）" || bad "通话仍挂着：status=${ST3}"
[ "${ER3:-}" = "3" ] && ok "结束原因=心跳超时（end_reason=3）" || bad "结束原因异常：${ER3}"

step "10. 通话记录与账本对账"
r="$(api GET "/v1/calls?page=1&size=20" "$TOK_A")"
[ "$(code "$r")" = "0" ] && ok "通话记录可查（total=$(field "$r" total)）" || bad "记录查询失败"
BAD="$(sql "SELECT COUNT(*) FROM call_session s WHERE s.status=3 AND s.total_coins <> s.billable_minutes * s.price_per_min;")"
[ "${BAD:-0}" = "0" ] && ok "全部已结束通话满足「总额 = 分钟 × 单价」" || bad "${BAD} 通账目不平"
BAD2="$(sql "SELECT COUNT(*) FROM call_session s WHERE s.status=3 AND s.billable_minutes <> CEIL(s.duration_sec/60) AND s.duration_sec>0;")"
[ "${BAD2:-0}" = "0" ] && ok "全部已结束通话满足「分钟 = ceil(秒数/60)」" || bad "${BAD2} 通分钟与秒数不符"
NEG="$(sql "SELECT COUNT(*) FROM wallet WHERE coin_balance < 0;")"
[ "${NEG:-0}" = "0" ] && ok "无负余额钱包" || bad "${NEG} 个钱包余额为负"

step "11. 拉黑后禁止呼叫"
api POST "/v1/users/$UID_A/block" "$TOK_B" >/dev/null
r="$(api POST /v1/calls "$TOK_A" "{\"callee_id\":$UID_B,\"media_type\":1}")"
[ "$(code "$r")" = "30001" ] && ok "被拉黑后呼叫被拒（30001）" || bad "拉黑未生效：$(code "$r")"

printf '\n\033[1m结果：通过 %d 项，失败 %d 项\033[0m\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
