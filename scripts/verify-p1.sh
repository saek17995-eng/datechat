#!/usr/bin/env bash
# P1 端到端验收脚本：账号 + 实名。
#
# 前置：
#   1. MySQL / Redis 已启动；
#   2. 服务已启动（make run），且 configs/config.dev.yaml 中 sms.expose_code_in_dev=true
#      （否则拿不到 debug_code，脚本无法自动登录）。
#
# 用法：bash scripts/verify-p1.sh [base_url]
set -uo pipefail

BASE="${1:-http://127.0.0.1:8080}"
DEVICE="${DEVICE:-verify-script-device}"

# 默认手机号按时间生成，保证脚本可以反复执行：
# 复用同一个号时，第二次实名提交会因"已认证"而失败（这是服务端正确的行为）。
SEED="$(date +%s)"
SEED="${SEED:2}"                       # 取后 8 位
PHONE="${PHONE:-138${SEED}}"
PHONE2="${PHONE2:-139${SEED}}"

# 生成一个校验位合法的身份证号：地区码 110101 + 固定生日 + 3 位流水，末位按 GB11643 计算。
#
# 流水号必须"每秒一变"：user_realname 上有 uk_id_card，同一个证件号只能绑定一个账号，
# 所以脚本连跑两次如果发到同一个号，第二次就会被"该身份证已被其他账号绑定"拒绝。
# 原来取的是 epoch 的第 3~5 位（约 2.8 小时才变一次），同一天内重跑必翻车。
build_idcard() {
  local seq="$1"
  local body="11010119900307$(printf '%03d' $((seq % 1000)))"
  local weights=(7 9 10 5 8 4 2 1 6 3 7 9 10 5 8 4 2)
  local codes="10X98765432"
  local sum=0 i d
  for i in $(seq 0 16); do
    d="${body:$i:1}"
    sum=$((sum + d * weights[i]))
  done
  printf '%s%s' "$body" "${codes:$((sum % 11)):1}"
}

IDCARD_SEQ=$(( 10#$(date +%s) % 1000 ))
IDCARD="${IDCARD:-$(build_idcard "$IDCARD_SEQ")}"

PASS=0
FAIL=0

say() { printf '\n\033[1m%s\033[0m\n' "$*"; }
ok() { PASS=$((PASS + 1)); printf '  \033[32m✓\033[0m %s\n' "$*"; }
no() { FAIL=$((FAIL + 1)); printf '  \033[31m✗\033[0m %s\n' "$*"; }

# jget <json> <dotted.path>：不依赖 jq，用 python3 取值
jget() {
  python3 -c '
import json,sys
try:
    data = json.loads(sys.argv[1])
except Exception:
    print(""); raise SystemExit(0)
cur = data
for part in sys.argv[2].split("."):
    if isinstance(cur, dict):
        cur = cur.get(part, "")
    elif isinstance(cur, list) and part.isdigit():
        idx = int(part)
        cur = cur[idx] if idx < len(cur) else ""
    else:
        cur = ""
    if cur is None:
        cur = ""
print(cur)
' "$1" "$2"
}

post() {
  local path="$1" body="$2" token="${3:-}"
  if [ -n "$token" ]; then
    curl -sS -X POST "$BASE$path" -H 'Content-Type: application/json' \
      -H "Authorization: Bearer $token" -d "$body"
  else
    curl -sS -X POST "$BASE$path" -H 'Content-Type: application/json' -d "$body"
  fi
}

get() {
  local path="$1" token="${2:-}"
  if [ -n "$token" ]; then
    curl -sS "$BASE$path" -H "Authorization: Bearer $token"
  else
    curl -sS "$BASE$path"
  fi
}

say "0. 健康检查"
HEALTH=$(curl -sS "$BASE/healthz")
if [ "$(jget "$HEALTH" code)" = "0" ]; then ok "/healthz 正常"; else no "/healthz 异常: $HEALTH"; fi

say "1. 发送验证码（${PHONE}）"
SEND=$(post /v1/auth/sms/code "{\"phone\":\"$PHONE\",\"scene\":\"login\"}")
CODE=$(jget "$SEND" data.debug_code)
if [ -n "$CODE" ]; then
  ok "验证码已下发（debug_code=${CODE}）"
else
  no "未拿到 debug_code：$SEND"
  echo "    → 请确认 configs/config.dev.yaml 的 sms.expose_code_in_dev=true 且 APP_ENV 不是 prod"
  exit 1
fi

say "2. 发送过于频繁应被拒绝"
SECOND=$(post /v1/auth/sms/code "{\"phone\":\"$PHONE\"}")
if [ "$(jget "$SECOND" code)" = "20005" ]; then ok "频控生效（20005）"; else no "频控未生效: $SECOND"; fi

say "3. 错误验证码应失败"
BAD=$(post /v1/auth/login "{\"phone\":\"$PHONE\",\"code\":\"000000\",\"device_id\":\"$DEVICE\"}")
if [ "$(jget "$BAD" code)" = "20004" ]; then ok "错误验证码被拒绝（20004）"; else no "错误验证码未被拒绝: $BAD"; fi

say "4. 正确验证码登录"
LOGIN=$(post /v1/auth/login "{\"phone\":\"$PHONE\",\"code\":\"$CODE\",\"device_id\":\"$DEVICE\",\"platform\":1,\"app_version\":\"0.1.0\"}")
ACCESS=$(jget "$LOGIN" data.token.access_token)
REFRESH=$(jget "$LOGIN" data.token.refresh_token)
USER_ID=$(jget "$LOGIN" data.uid)
ISNEW=$(jget "$LOGIN" data.is_new_user)
if [ -n "$ACCESS" ] && [ -n "$REFRESH" ]; then
  ok "登录成功 uid=${USER_ID} is_new_user=${ISNEW}"
  ok "手机号脱敏回显: $(jget "$LOGIN" data.user.phone_masked)"
else
  no "登录失败: $LOGIN"
  exit 1
fi

say "5. 访问需要登录的接口"
ME=$(get /v1/auth/me "$ACCESS")
if [ "$(jget "$ME" code)" = "0" ]; then ok "/v1/auth/me 通过：$(jget "$ME" data.nickname)"; else no "/v1/auth/me 失败: $ME"; fi

say "6. 无 token 访问应 401"
NOAUTH=$(get /v1/auth/me "")
if [ "$(jget "$NOAUTH" code)" = "10100" ]; then ok "鉴权生效（10100）"; else no "未拦截未登录请求: $NOAUTH"; fi

say "7. 刷新令牌（一次性）"
R1=$(post /v1/auth/refresh "{\"refresh_token\":\"$REFRESH\"}")
NEW_REFRESH=$(jget "$R1" data.refresh_token)
if [ -n "$NEW_REFRESH" ]; then ok "刷新成功，已换发新令牌"; else no "刷新失败: $R1"; fi

say "8. 重放旧 refresh 应被拒绝"
R2=$(post /v1/auth/refresh "{\"refresh_token\":\"$REFRESH\"}")
if [ "$(jget "$R2" code)" = "10102" ]; then ok "重放被拦截（10102）"; else no "重放未被拦截: $R2"; fi

say "9. 提交实名（mock 通道即时通过）"
# 万一流水号还是撞上了库里历史数据（同一秒内跑过、或刚好 1000 秒前跑过），
# 换个流水号重试，而不是让整个脚本挂在一条与 P1 无关的数据冲突上。
RN=""
for _ in $(seq 1 20); do
  RN=$(post /v1/realname/submit "{\"real_name\":\"张三\",\"id_card\":\"$IDCARD\"}" "$ACCESS")
  case "$RN" in
    *该身份证已被其他账号绑定*)
      IDCARD_SEQ=$(( (IDCARD_SEQ + 137) % 1000 ))
      IDCARD="$(build_idcard "$IDCARD_SEQ")"
      continue
      ;;
  esac
  break
done
RN_STATUS=$(jget "$RN" data.status)
if [ "$RN_STATUS" = "2" ]; then
  ok "实名通过，姓名脱敏=$(jget "$RN" data.real_name_masked) 证件脱敏=$(jget "$RN" data.id_card_masked)"
else
  no "实名结果非预期: $RN"
fi

say "10. 查询实名状态"
RNS=$(get /v1/realname/status "$ACCESS")
if [ "$(jget "$RNS" data.status_text)" = "已认证" ]; then ok "状态回读一致：$(jget "$RNS" data.status_text)"; else no "状态不一致: $RNS"; fi

say "11. 同一身份证换账号提交应被拒绝"
SEND2=$(post /v1/auth/sms/code "{\"phone\":\"$PHONE2\"}")
CODE2=$(jget "$SEND2" data.debug_code)
if [ -n "$CODE2" ]; then
  LOGIN2=$(post /v1/auth/login "{\"phone\":\"$PHONE2\",\"code\":\"$CODE2\",\"device_id\":\"verify-device-2\"}")
  ACCESS2=$(jget "$LOGIN2" data.token.access_token)
  DUP=$(post /v1/realname/submit "{\"real_name\":\"李四\",\"id_card\":\"$IDCARD\"}" "$ACCESS2")
  if [ "$(jget "$DUP" code)" = "10001" ]; then ok "身份证唯一性生效：$(jget "$DUP" msg)"; else no "身份证查重失效: $DUP"; fi
else
  no "第二个账号发码失败（跳过查重验证）: $SEND2"
fi

say "12. IM UserSig 签发"
SIG=$(get /v1/im/usersig "$ACCESS")
USERSIG=$(jget "$SIG" data.usersig)
if [ -n "$USERSIG" ]; then
  ok "UserSig 已签发，user_id=$(jget "$SIG" data.user_id)，长度=${#USERSIG}"
else
  printf '  \033[33m—\033[0m 未配置腾讯云 IM 密钥（或 import_enabled=false），跳过（code=%s）\n' "$(jget "$SIG" code)"
fi

say "13. 退出登录"
LOGOUT=$(post /v1/auth/logout '{}' "$ACCESS")
if [ "$(jget "$LOGOUT" code)" = "0" ]; then ok "退出成功"; else no "退出失败: $LOGOUT"; fi

say "14. 退出后刷新应失效"
R3=$(post /v1/auth/refresh "{\"refresh_token\":\"$NEW_REFRESH\"}")
if [ "$(jget "$R3" code)" = "10102" ]; then ok "吊销后刷新被拒绝（10102）"; else no "吊销未生效: $R3"; fi

printf '\n\033[1m结果：通过 %d 项，失败 %d 项\033[0m\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ] || exit 1
