#!/usr/bin/env bash
# P5 端到端验收：钱包与充值
# 覆盖：钱包初始化、档位列表、下单（服务端定价）、模拟支付到账、
#       重复回调不重复加币、金额不符拒绝、流水唯一、对账一致性。
set -uo pipefail

BASE="${BASE:-http://127.0.0.1:8080}"
PASS=0; FAIL=0
ok()   { printf '  \033[32m✓\033[0m %s\n' "$*"; PASS=$((PASS+1)); }
bad()  { printf '  \033[31m✗\033[0m %s\n' "$*"; FAIL=$((FAIL+1)); }
step() { printf '\n\033[1m%s\033[0m\n' "$*"; }

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
    if k=='' : break
    d=(d or {}).get(k) if isinstance(d,dict) else None
if isinstance(d,(list,dict)):
    print(json.dumps(d,ensure_ascii=False))
elif d is None:
    print('')
else:
    print(d)" 2>/dev/null; }
sql() { mysql -h 127.0.0.1 -u datechat -pdatechat_pass datechat -N -e "$1" 2>/dev/null; }

TS="$(date +%s)"
PHONE="170${TS:2:7}9"

step "0. 登录测试用户"
sent="$(api POST /v1/auth/sms/code "" "{\"phone\":\"$PHONE\",\"scene\":\"login\"}")"
code_v="$(field "$sent" debug_code)"
r="$(api POST /v1/auth/login "" "{\"phone\":\"$PHONE\",\"code\":\"$code_v\",\"device_id\":\"p5-$PHONE\",\"platform\":1,\"app_version\":\"0.5.0\"}")"
CUR_UID="$(field "$r" uid)"; TOKEN="$(field "$r" token.access_token)"
[ -n "${CUR_UID}" ] && ok "登录成功 uid=$CUR_UID" || { bad "登录失败"; exit 1; }

step "1. 钱包初始化"
r="$(api GET /v1/wallet "$TOKEN")"
[ "$(code "$r")" = "0" ] && ok "钱包可读（余额=$(field "$r" coin_balance)）" || bad "钱包读取失败：$(code "$r")"

step "2. 充值档位"
r="$(api GET /v1/wallet/packages "$TOKEN")"
TOTAL="$(field "$r" list | python3 -c 'import sys,json;print(len(json.load(sys.stdin) or []))' 2>/dev/null || echo 0)"
[ "${TOTAL:-0}" -ge 1 ] && ok "档位列表返回 ${TOTAL} 档" || bad "档位为空"
PKG="$(field "$r" list | python3 -c 'import sys,json;print((json.load(sys.stdin) or [{}])[0].get("id",""))' 2>/dev/null)"
COINS="$(field "$r" list | python3 -c 'import sys,json;d=(json.load(sys.stdin) or [{}])[0];print(d.get("coins",0)+d.get("bonus_coins",0))' 2>/dev/null)"

step "3. 下单（金额由服务端按档位定价）"
r="$(api POST /v1/wallet/recharge "$TOKEN" "{\"package_id\":$PKG}")"
ORDER="$(field "$r" order_no)"
[ -n "${ORDER:-}" ] && ok "下单成功 order_no=${ORDER}，金币=$(field "$r" coins)" || bad "下单失败：$(code "$r")"

step "4. 模拟支付到账"
r="$(api POST "/v1/pay/mock/notify" "" "{\"order_no\":\"${ORDER}\",\"trade_no\":\"MOCK-${ORDER}\",\"amount_fen\":$(field "$r" amount_fen)}")"
[ "$(code "$r")" = "0" ] && ok "支付回调处理成功" || bad "回调失败：$(code "$r")"
BAL="$(sql "SELECT coin_balance FROM wallet WHERE user_id=${CUR_UID};")"
[ "${BAL:-0}" = "${COINS:-0}" ] && ok "金币到账 ${BAL}（档位 ${COINS}）" || bad "余额不符：钱包 $BAL vs 档位 $COINS"

step "5. 重复回调不重复加币"
api POST "/v1/pay/mock/notify" "" "{\"order_no\":\"${ORDER}\",\"trade_no\":\"MOCK-${ORDER}\"}" >/dev/null
api POST "/v1/pay/mock/notify" "" "{\"order_no\":\"${ORDER}\",\"trade_no\":\"MOCK-${ORDER}\"}" >/dev/null
BAL2="$(sql "SELECT coin_balance FROM wallet WHERE user_id=${CUR_UID};")"
[ "${BAL2:-0}" = "${BAL:-0}" ] && ok "重复回调后余额不变（仍为 ${BAL2}）" || bad "重复加币：$BAL → $BAL2"
TX="$(sql "SELECT COUNT(*) FROM wallet_transaction WHERE user_id=$CUR_UID AND biz_type=1 AND biz_id='$ORDER';")"
[ "${TX:-0}" = "1" ] && ok "充值流水只有 1 条（幂等生效）" || bad "流水重复：${TX} 条"
NL="$(sql "SELECT COUNT(*) FROM pay_notify_log WHERE order_no='$ORDER';")"
[ "${NL:-1}" = "1" ] && ok "回调日志去重后 1 条" || bad "回调日志 ${NL} 条"

step "6. 金额不符的回调被拒绝"
r2="$(api POST /v1/wallet/recharge "$TOKEN" "{\"package_id\":$PKG}")"
ORDER2="$(field "$r2" order_no)"
AMT2="$(field "$r2" amount_fen)"
r="$(api POST "/v1/pay/mock/notify" "" "{\"order_no\":\"${ORDER2}\",\"trade_no\":\"MOCK2-${ORDER2}\",\"amount_fen\":1}")"
[ "$(code "$r")" != "0" ] && ok "金额不符被拒（应付款 $AMT2 分）" || bad "金额不符未被拦截"
ST="$(sql "SELECT status FROM recharge_order WHERE order_no='$ORDER2';")"
[ "${ST:-}" != "2" ] && ok "订单未被误置为已支付（status=${ST}）" || bad "异常订单被放行"

step "7. 流水可查"
r="$(api GET "/v1/wallet/transactions?page=1&size=10" "$TOKEN")"
[ "$(code "$r")" = "0" ] && ok "流水接口可用（total=$(field "$r" total)）" || bad "流水查询失败"

step "8. 对账：钱包余额与流水累计一致"
# P6 起余额还会被通话消费/收益影响，所以对账要按"充值 - 通话消费 + 通话收益"来算。
SUM_BAL="$(sql "SELECT IFNULL(SUM(coin_balance),0) FROM wallet;")"
SUM_FLOW="$(sql "SELECT
  IFNULL(SUM(CASE WHEN biz_type=1 AND direction=1 THEN coins
                  WHEN biz_type=2 AND direction=2 THEN -coins
                  WHEN biz_type=6 AND direction=1 THEN coins ELSE 0 END),0)
  FROM wallet_transaction;")"
[ "${SUM_BAL:-0}" = "${SUM_FLOW:-0}" ] && ok "余额合计 ${SUM_BAL} = 流水净额 ${SUM_FLOW}" || bad "账不平：余额 $SUM_BAL vs 流水 $SUM_FLOW"

printf '\n\033[1m结果：通过 %d 项，失败 %d 项\033[0m\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
