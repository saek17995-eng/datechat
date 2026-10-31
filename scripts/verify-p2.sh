#!/usr/bin/env bash
# P2 端到端验收脚本：资料编辑 + 头像上传 + 他人主页。
#
# 前置：
#   1. MySQL / Redis 已启动（bash scripts/dev-services.sh start）；
#   2. 迁移已应用（bash scripts/migrate-local57.sh up）；
#   3. 服务已启动，且 sms.expose_code_in_dev=true、upload.provider=local。
#
# 用法：bash scripts/verify-p2.sh [base_url]
set -uo pipefail

BASE="${1:-http://127.0.0.1:8080}"
DEVICE="${DEVICE:-p2-verify-device}"
SEED="$(date +%s)"
SEED="${SEED:2}"
PHONE_A="${PHONE_A:-138${SEED}}"
PHONE_B="${PHONE_B:-139${SEED}}"

PASS=0
FAIL=0

say() { printf '\n\033[1m%s\033[0m\n' "$*"; }
ok() { PASS=$((PASS + 1)); printf '  \033[32m✓\033[0m %s\n' "$*"; }
no() { FAIL=$((FAIL + 1)); printf '  \033[31m✗\033[0m %s\n' "$*"; }

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

put() {
  local path="$1" body="$2" token="$3"
  curl -sS -X PUT "$BASE$path" -H 'Content-Type: application/json' \
    -H "Authorization: Bearer $token" -d "$body"
}

get() {
  local path="$1" token="${2:-}"
  if [ -n "$token" ]; then
    curl -sS "$BASE$path" -H "Authorization: Bearer $token"
  else
    curl -sS "$BASE$path"
  fi
}

login() {
  local phone="$1" device="$2"
  local send code login
  send=$(post /v1/auth/sms/code "{\"phone\":\"$phone\",\"scene\":\"login\"}")
  code=$(jget "$send" data.debug_code)
  if [ -z "$code" ]; then
    echo ""
    return 1
  fi
  login=$(post /v1/auth/login "{\"phone\":\"$phone\",\"code\":\"$code\",\"device_id\":\"$device\",\"platform\":1,\"app_version\":\"0.2.0\"}")
  jget "$login" data.token.access_token
}

say "0. 健康检查"
HEALTH=$(curl -sS "$BASE/healthz")
if [ "$(jget "$HEALTH" code)" = "0" ]; then ok "/healthz 正常"; else no "/healthz 异常: $HEALTH"; exit 1; fi

say "1. 注册并登录账号 A（${PHONE_A}）"
TOKEN_A=$(login "$PHONE_A" "$DEVICE")
UID_A=$(jget "$(get /v1/auth/me "$TOKEN_A")" data.uid)
if [ -n "$TOKEN_A" ] && [ -n "$UID_A" ]; then
  ok "登录成功 uid=${UID_A}"
else
  no "登录失败（请确认 expose_code_in_dev=true）"
  exit 1
fi

say "2. 读取初始资料"
P0=$(get /v1/users/me/profile "$TOKEN_A")
if [ "$(jget "$P0" code)" = "0" ]; then
  ok "默认资料：昵称=$(jget "$P0" data.nickname) 城市=$(jget "$P0" data.city_name) 全局价=$(jget "$P0" data.price_audio)/$(jget "$P0" data.price_video)"
else
  no "读取资料失败: $P0"
fi

say "3. 城市字典"
CITIES=$(get /v1/cities "$TOKEN_A")
CITY_COUNT=$(python3 -c "
import json,sys
d=json.loads('''$CITIES''')
print(len(d.get('data') or []))
" 2>/dev/null)
if [ "${CITY_COUNT:-0}" -ge 6 ]; then ok "城市字典 ${CITY_COUNT} 条"; else no "城市字典异常: $CITIES"; fi

say "4. 编辑资料（合法字段）"
P1=$(put /v1/users/me/profile '{
  "nickname":"小雨",
  "gender":2,
  "birthday":"1995-06-18",
  "city_code":"330100",
  "height":165,
  "job":"设计师",
  "bio":"喜欢旅行和咖啡",
  "tags":["温柔","爱旅行","咖啡控"]
}' "$TOKEN_A")
if [ "$(jget "$P1" code)" = "0" ] \
   && [ "$(jget "$P1" data.nickname)" = "小雨" ] \
   && [ "$(jget "$P1" data.city_name)" = "杭州" ] \
   && [ "$(jget "$P1" data.gender_text)" = "女" ]; then
  ok "资料已更新：昵称=$(jget "$P1" data.nickname) 城市=$(jget "$P1" data.city_name) 年龄=$(jget "$P1" data.age) 身高=$(jget "$P1" data.height)"
else
  no "资料更新失败: $P1"
fi

say "5. 局部更新：只改签名不应影响其他字段"
P2=$(put /v1/users/me/profile '{"bio":"周末爬山"}' "$TOKEN_A")
if [ "$(jget "$P2" data.bio)" = "周末爬山" ] && [ "$(jget "$P2" data.nickname)" = "小雨" ] \
   && [ "$(jget "$P2" data.city_name)" = "杭州" ]; then
  ok "局部更新生效且未覆盖其他字段"
else
  no "局部更新异常: $P2"
fi

say "6. 敏感词拦截：签名含「加我微信」应被拒绝"
BAD=$(put /v1/users/me/profile '{"bio":"加我微信聊"}' "$TOKEN_A")
if [ "$(jget "$BAD" code)" = "30002" ]; then ok "敏感词被拦截（30002）"; else no "敏感词未拦截: $BAD"; fi

say "7. 敏感词拦截：昵称含「约炮」应被拒绝"
BAD2=$(put /v1/users/me/profile '{"nickname":"约炮达人"}' "$TOKEN_A")
if [ "$(jget "$BAD2" code)" = "30002" ]; then ok "昵称敏感词被拦截（30002）"; else no "昵称敏感词未拦截: $BAD2"; fi

say "8. 非法城市应被拒绝"
BADCITY=$(put /v1/users/me/profile '{"city_code":"999999"}' "$TOKEN_A")
if [ "$(jget "$BADCITY" code)" = "10001" ]; then ok "城市校验生效（10001）"; else no "城市校验未生效: $BADCITY"; fi

say "9. 未成年生日应被拒绝"
BADAGE=$(put /v1/users/me/profile '{"birthday":"2015-01-01"}' "$TOKEN_A")
if [ "$(jget "$BADAGE" code)" = "10001" ]; then ok "年龄校验生效（10001）"; else no "年龄校验未生效: $BADAGE"; fi

say "10. 获取上传凭证（local 通道）"
SIGN=$(post /v1/uploads/sign '{"scene":1,"ext":"png"}' "$TOKEN_A")
UPLOAD_PATH=$(jget "$SIGN" data.upload_path)
if [ "$(jget "$SIGN" data.provider)" = "local" ] && [ -n "$UPLOAD_PATH" ]; then
  ok "上传通道=$(jget "$SIGN" data.provider) 接口=$UPLOAD_PATH 大小上限=$(jget "$SIGN" data.max_bytes)"
else
  no "获取上传凭证失败: $SIGN"
fi

say "11. 上传一张 1x1 PNG"
PNG_B64="iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFAAH/q842iQAAAABJRU5ErkJggg=="
TMP_PNG="/tmp/p2-verify-$$.png"
python3 -c "import base64,sys;open('$TMP_PNG','wb').write(base64.b64decode('$PNG_B64'))"
UP=$(curl -sS -X POST "$BASE/v1/uploads/local" -H "Authorization: Bearer $TOKEN_A" \
  -F "file=@$TMP_PNG;type=image/png" -F "scene=1")
AVATAR_URL=$(jget "$UP" data.url)
if [ -n "$AVATAR_URL" ]; then
  ok "上传成功 url=${AVATAR_URL} 大小=$(jget "$UP" data.size) 类型=$(jget "$UP" data.mime)"
else
  no "上传失败: $UP"
fi

say "12. 非图片文件应被拒绝"
TMP_TXT="/tmp/p2-verify-$$.txt"
printf 'hello' > "$TMP_TXT"
UPTXT=$(curl -sS -X POST "$BASE/v1/uploads/local" -H "Authorization: Bearer $TOKEN_A" \
  -F "file=@$TMP_TXT;type=text/plain" -F "scene=1")
if [ "$(jget "$UPTXT" code)" = "10001" ]; then ok "非图片被拒绝（10001）"; else no "非图片未被拒绝: $UPTXT"; fi

say "13. 静态目录可访问上传文件"
if [ -n "$AVATAR_URL" ]; then
  CODE=$(curl -s -o /dev/null -w '%{http_code}' "$BASE$AVATAR_URL")
  if [ "$CODE" = "200" ]; then ok "GET ${AVATAR_URL} → 200"; else no "静态访问失败 HTTP ${CODE}"; fi
fi

say "14. 设置头像（合法上传地址）"
AV=$(post /v1/users/me/avatar "{\"url\":\"$AVATAR_URL\"}" "$TOKEN_A")
if [ "$(jget "$AV" code)" = "0" ] && [ "$(jget "$AV" data.avatar)" = "$AVATAR_URL" ]; then
  ok "头像已更新"
else
  no "设置头像失败: $AV"
fi

say "15. 伪造外链头像应被拒绝"
EVIL=$(post /v1/users/me/avatar '{"url":"https://evil.example.com/a.jpg"}' "$TOKEN_A")
if [ "$(jget "$EVIL" code)" = "10001" ]; then ok "外链头像被拒绝（10001）"; else no "外链头像未被拒绝: $EVIL"; fi

say "16. 在线心跳"
HB=$(post /v1/users/me/heartbeat '{}' "$TOKEN_A")
if [ "$(jget "$HB" data.online)" = "True" ]; then ok "心跳上报成功，保活 $(jget "$HB" data.keep_alive) 秒"; else no "心跳失败: $HB"; fi

say "17. 账号 B 登录并查看 A 的主页"
TOKEN_B=$(login "$PHONE_B" "p2-verify-device-b")
if [ -z "$TOKEN_B" ]; then
  no "账号 B 登录失败"
else
  VIEW=$(get "/v1/users/$UID_A" "$TOKEN_B")
  if [ "$(jget "$VIEW" code)" = "0" ] \
     && [ "$(jget "$VIEW" data.nickname)" = "小雨" ] \
     && [ "$(jget "$VIEW" data.online)" = "True" ] \
     && [ "$(jget "$VIEW" data.relation.is_self)" = "False" ]; then
    ok "他人主页：昵称=$(jget "$VIEW" data.nickname) 在线=$(jget "$VIEW" data.online) 城市=$(jget "$VIEW" data.city_name) 年龄=$(jget "$VIEW" data.age)"
    ok "付费定价：语音 $(jget "$VIEW" data.price_audio) 金币/分，视频 $(jget "$VIEW" data.price_video) 金币/分"
  else
    no "他人主页异常: $VIEW"
  fi

  say "18. 自己看自己应标记 is_self"
  SELF=$(get "/v1/users/$UID_A" "$TOKEN_A")
  if [ "$(jget "$SELF" data.relation.is_self)" = "True" ]; then ok "is_self=true"; else no "is_self 标记异常: $SELF"; fi

  say "19. 不存在的用户应返回 20001"
  MISS=$(get "/v1/users/99999999" "$TOKEN_B")
  if [ "$(jget "$MISS" code)" = "20001" ]; then ok "用户不存在（20001）"; else no "预期 20001，实际: $MISS"; fi
fi

say "20. 数据库侧校验（访问记录 / 上传台账 / 审核留痕）"
MYSQL_CMD="$(command -v mysql || true)"
if [ -n "$MYSQL_CMD" ]; then
  Q() { mysql -h 127.0.0.1 -u datechat -pdatechat_pass datechat -N -B -e "$1" 2>/dev/null; }
  VISITS=$(Q "SELECT COUNT(*) FROM user_visit WHERE target_id=$UID_A;")
  FILES=$(Q "SELECT COUNT(*) FROM upload_file WHERE user_id=$UID_A AND used=1;")
  MODS=$(Q "SELECT COUNT(*) FROM moderation_record WHERE user_id=$UID_A;")
  [ "${VISITS:-0}" -ge 1 ] && ok "user_visit 已记录 ${VISITS} 条访问" || no "user_visit 未记录访问（${VISITS}）"
  [ "${FILES:-0}" -ge 1 ] && ok "upload_file 已标记 ${FILES} 个已使用文件" || no "upload_file 未标记 used（${FILES}）"
  [ "${MODS:-0}" -ge 1 ] && ok "moderation_record 留痕 ${MODS} 条" || no "moderation_record 无记录（${MODS}）"
else
  printf '  \033[33m—\033[0m 未找到 mysql 客户端，跳过数据库校验\n'
fi

rm -f "$TMP_PNG" "$TMP_TXT"

printf '\n\033[1m结果：通过 %d 项，失败 %d 项\033[0m\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ] || exit 1
