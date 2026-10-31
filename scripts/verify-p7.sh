#!/usr/bin/env bash
# P7 端到端验收：LBS 与在线
# 覆盖：位置上报与坐标校验、上报节流、附近的人按距离升序、距离模糊、在线/离线（置灰）、
#       批量在线查询、性别与年龄筛选、半径上限、拉黑后不出现、"谁看过我"与未读清零。
#
# 真机上的"连续定位与距离准确"依赖系统定位权限，脚本只验证服务端检索与排序链路。
set -uo pipefail

BASE="${BASE:-http://127.0.0.1:8080}"

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
# uids 取列表里的 uid 顺序（空格分隔）
uids() { printf '%s' "${1:-}" | python3 -c "
import sys,json
d=json.load(sys.stdin).get('data') or {}
print(' '.join(str(x.get('uid')) for x in (d.get('list') or [])))" 2>/dev/null; }
# pairs 取列表里的 uid:online 对
pairs() { printf '%s' "${1:-}" | python3 -c "
import sys,json
d=json.load(sys.stdin).get('data') or {}
print(' '.join('%s=%d'%(x.get('uid'), int(bool(x.get('online')))) for x in (d.get('list') or [])))" 2>/dev/null; }
sql() { mysql -h 127.0.0.1 -u datechat -pdatechat_pass datechat -N -e "$1" 2>/dev/null; }
# redis 本机没装 redis-cli，用一段 RESP 直连代替（够用：DEL / ZCARD / EXISTS）。
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
# 剥掉 RESP 类型字节：冒号=整数 加号=简单字符串 减号=错误
print(out[1:].split('\r\n')[0] if out[:1] in (':', '+', '-') else out.split('\r\n')[0])
" "$@"
}

TS="$(date +%s)"
PHONE_A="170${TS:2:7}1"
PHONE_B="170${TS:2:7}2"
PHONE_C="170${TS:2:7}3"
PHONE_D="170${TS:2:7}4"

login() {
  local phone="$1" tag="$2"
  local cv_json cv
  cv_json="$(api POST /v1/auth/sms/code "" "{\"phone\":\"$phone\",\"scene\":\"login\"}")"
  cv="$(field "$cv_json" debug_code)"
  api POST /v1/auth/login "" "{\"phone\":\"$phone\",\"code\":\"$cv\",\"device_id\":\"p7-$tag-$phone\",\"platform\":1,\"app_version\":\"0.7.0\"}"
}

# 北京坐标：A 天安门，B 东边约 0.9km，C 东边约 8.7km，D 上海（约 1000km，必超半径）
LNG_A=116.4074; LAT_A=39.9042
LNG_B=116.4174; LAT_B=39.9042
LNG_C=116.5074; LAT_C=39.9042
LNG_D=121.4737; LAT_D=31.2304

step "0. 登录四个账号（A 为观察者）"
rA="$(login "$PHONE_A" a)"; UID_A="$(field "$rA" uid)"; TOK_A="$(field "$rA" token.access_token)"
rB="$(login "$PHONE_B" b)"; UID_B="$(field "$rB" uid)"; TOK_B="$(field "$rB" token.access_token)"
rC="$(login "$PHONE_C" c)"; UID_C="$(field "$rC" uid)"; TOK_C="$(field "$rC" token.access_token)"
rD="$(login "$PHONE_D" d)"; UID_D="$(field "$rD" uid)"; TOK_D="$(field "$rD" token.access_token)"
if [ -n "${UID_A:-}" ] && [ -n "${UID_B:-}" ] && [ -n "${UID_C:-}" ] && [ -n "${UID_D:-}" ]; then
  ok "四端登录成功 A=$UID_A B=$UID_B C=$UID_C D=$UID_D"
else
  bad "登录失败"; exit 1
fi

# 先清空 GEO 索引：否则历史脚本跑出来的坐标会混进列表，排序断言就不确定了。
redis DEL geo:users >/dev/null 2>&1

step "1. 位置上报：坐标校验"
r="$(api POST /v1/users/me/location "$TOK_A" "{\"lng\":0,\"lat\":0}")"
[ "$(code "$r")" != "0" ] && ok "拒绝 (0,0) 这类无效坐标" || bad "(0,0) 竟然被接受"
r="$(api POST /v1/users/me/location "$TOK_A" "{\"lng\":999,\"lat\":999}")"
[ "$(code "$r")" != "0" ] && ok "拒绝越界坐标 (999,999)" || bad "越界坐标竟然被接受"

step "2. 位置上报：四个账号各就各位"
for pair in "A:$TOK_A:$LNG_A:$LAT_A" "B:$TOK_B:$LNG_B:$LAT_B" "C:$TOK_C:$LNG_C:$LAT_C" "D:$TOK_D:$LNG_D:$LAT_D"; do
  IFS=: read -r tag tok lng lat <<<"$pair"
  r="$(api POST /v1/users/me/location "$tok" "{\"lng\":$lng,\"lat\":$lat}")"
  if [ "$(code "$r")" = "0" ]; then ok "$tag 上报成功（lng=$lng lat=${lat}）"; else bad "$tag 上报失败：$(code "$r") $r"; fi
done
r="$(api POST /v1/users/me/location "$TOK_A" "{\"lng\":$LNG_A,\"lat\":$LAT_A}")"
[ "$(field "$r" throttled)" = "True" ] && ok "30 秒内重复上报被节流（throttled=true）" || bad "重复上报未被节流：$r"

step "3. 在线状态：只有 B 发心跳"
api POST /v1/users/me/heartbeat "$TOK_B" '{}' >/dev/null
sleep 1
r="$(api GET "/v1/users/online?ids=$UID_B,$UID_C" "$TOK_A")"
info "批量在线：$(field "$r" online)"
[ "$(printf '%s' "$(field "$r" online)" | grep -c "\"$UID_B\": *true")" = "1" ] && ok "B 心跳后在线" || bad "B 应在线：$r"
[ "$(printf '%s' "$(field "$r" online)" | grep -c "\"$UID_C\": *false")" = "1" ] && ok "C 未心跳判离线（列表置灰依据）" || bad "C 应离线：$r"

step "4. 附近的人：按距离升序 + 半径生效"
r="$(api GET "/v1/nearby?lng=$LNG_A&lat=$LAT_A" "$TOK_A")"
LIST="$(uids "$r")"
info "列表顺序：${LIST}（B=$UID_B 近，C=$UID_C 远，D=$UID_D 超半径）"
IDX_B="$(printf '%s' "$LIST" | tr ' ' '\n' | grep -n -x "$UID_B" | cut -d: -f1)"
IDX_C="$(printf '%s' "$LIST" | tr ' ' '\n' | grep -n -x "$UID_C" | cut -d: -f1)"
if [ -n "${IDX_B:-}" ] && [ -n "${IDX_C:-}" ] && [ "$IDX_B" -lt "$IDX_C" ]; then
  ok "按距离升序：B(第${IDX_B}位, 约 0.9km) 排在 C(第${IDX_C}位, 约 8.7km) 之前"
else
  bad "未按距离升序：B=$IDX_B C=$IDX_C 列表=$LIST"
fi
case " $LIST " in
  *" $UID_D "*) bad "上海用户仍出现在默认 50km 半径内" ;;
  *) ok "超出半径的用户不出现（D 被过滤）" ;;
esac
case " $LIST " in
  *" $UID_A "*) bad "列表里出现了自己" ;;
  *) ok "自己不出现在附近列表" ;;
esac

step "5. 距离模糊：对外只给区间"
r="$(api GET "/v1/nearby?lng=$LNG_A&lat=$LAT_A" "$TOK_A")"
DT_B="$(printf '%s' "$r" | python3 -c "
import sys,json
d=json.load(sys.stdin).get('data') or {}
for x in (d.get('list') or []):
    if str(x.get('uid'))=='$UID_B': print(x.get('distance_text'), '| km=', x.get('distance_km'))" 2>/dev/null)"
info "B 的距离展示：$DT_B"
case "$DT_B" in
  *"km 以内"*|*"km 以外"*) ok "距离按区间模糊展示（不暴露精确值）" ;;
  *) bad "距离未按区间模糊：$DT_B" ;;
esac
case "$DT_B" in
  *"km= None") ok "默认不下发精确 distance_km" ;;
  *) bad "不应下发精确距离：$DT_B" ;;
esac

step "6. 在线/离线在列表里可区分"
PAIRS="$(pairs "$r")"
info "列表 online：$PAIRS"
case "$PAIRS" in
  *"$UID_B=1"*) ok "列表中 B 标记为在线" ;;
  *) bad "B 未在列表标记在线：$PAIRS" ;;
esac
case "$PAIRS" in
  *"$UID_C=0"*) ok "列表中 C 标记为离线（前端据此置灰）" ;;
  *) bad "C 未在列表标记离线：$PAIRS" ;;
esac

step "7. 性别筛选"
api PUT /v1/users/me/profile "$TOK_B" '{"gender":2,"birthday":"1995-05-05"}' >/dev/null
api PUT /v1/users/me/profile "$TOK_C" '{"gender":1,"birthday":"2005-01-01"}' >/dev/null
r="$(api GET "/v1/nearby?lng=$LNG_A&lat=$LAT_A&gender=2" "$TOK_A")"
LIST="$(uids "$r")"
info "gender=2（女）结果：$LIST"
[ "$LIST" = "$UID_B" ] && ok "性别筛选生效：只要女性时只剩 B" || bad "性别筛选结果异常：$LIST"

step "8. 年龄筛选"
r="$(api GET "/v1/nearby?lng=$LNG_A&lat=$LAT_A&age_min=25" "$TOK_A")"
LIST="$(uids "$r")"
info "age_min=25 结果：${LIST}（B 1995 年，C 2005 年）"
[ "$LIST" = "$UID_B" ] && ok "年龄筛选生效：25 岁以上只剩 B" || bad "年龄筛选结果异常：$LIST"

step "9. 拉黑后不出现在附近列表"
api POST "/v1/users/$UID_B/block" "$TOK_A" >/dev/null
r="$(api GET "/v1/nearby?lng=$LNG_A&lat=$LAT_A" "$TOK_A")"
LIST="$(uids "$r")"
case " $LIST " in
  *" $UID_B "*) bad "拉黑后 B 仍出现在列表：$LIST" ;;
  *) ok "拉黑后 B 从附近列表消失" ;;
esac
api DELETE "/v1/users/$UID_B/block" "$TOK_A" >/dev/null
r="$(api GET "/v1/nearby?lng=$LNG_A&lat=$LAT_A" "$TOK_A")"
LIST="$(uids "$r")"
case " $LIST " in
  *" $UID_B "*) ok "取消拉黑后 B 重新出现" ;;
  *) bad "取消拉黑后 B 未恢复：$LIST" ;;
esac

step "10. 谁看过我"
api GET "/v1/users/$UID_A" "$TOK_B" >/dev/null
api GET "/v1/users/$UID_A" "$TOK_C" >/dev/null
r="$(api GET /v1/users/me/visits "$TOK_A")"
V="$(printf '%s' "$r" | python3 -c "
import sys,json
d=json.load(sys.stdin).get('data') or {}
print(' '.join(str(x.get('visitor_id')) for x in (d.get('list') or [])))" 2>/dev/null)"
UNREAD="$(field "$r" unread)"
info "访客：${V}，未读 $UNREAD"
case " $V " in
  *" $UID_B "*|*" $UID_C "*) ok "访客列表记录了看过我的人" ;;
  *) bad "访客列表为空：$r" ;;
esac
[ "${UNREAD:-0}" -ge 2 ] && ok "未读访客数 = ${UNREAD}" || bad "未读计数异常：${UNREAD}"

r="$(api POST /v1/users/me/visits/read "$TOK_A")"
UPDATED="$(field "$r" updated)"
[ "${UPDATED:-0}" -ge 1 ] && ok "标记已读更新 ${UPDATED} 行" || bad "标记已读未更新：$(code "$r") $r"
sleep 1
r="$(api GET /v1/users/me/visits "$TOK_A")"
[ "$(field "$r" unread)" = "0" ] && ok "已读后未读清零" || bad "未读未清零：$(field "$r" unread)"

step "11. 缺少经纬度直接拒绝"
r="$(api GET "/v1/nearby" "$TOK_A")"
[ "$(code "$r")" != "0" ] && ok "不带经纬度查询被拒绝" || bad "缺少经纬度竟然返回成功"

step "12. 数据侧留痕"
GEO_N="$(redis ZCARD geo:users 2>/dev/null)"
info "Redis GEO 成员数：$GEO_N"
[ "${GEO_N:-0}" -ge 4 ] && ok "GEO 索引已写入 ${GEO_N} 个坐标" || bad "GEO 索引成员不足：${GEO_N}"
LOC_N="$(sql "SELECT COUNT(*) FROM user_profile WHERE lng IS NOT NULL AND lat IS NOT NULL;")"
[ "${LOC_N:-0}" -ge 4 ] && ok "MySQL 兜底坐标 ${LOC_N} 条" || bad "MySQL 坐标不足：${LOC_N}"

printf '\n\033[1m结果：\033[32m%s 通过\033[0m，\033[31m%s 失败\033[0m\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ] || exit 1
