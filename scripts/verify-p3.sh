#!/usr/bin/env bash
# P3 端到端验收脚本：腾讯云 IM REST 门面 + 会话/消息/已读/撤回 + 回调审核与幂等。
#
# 前置：
#   1. MySQL / Redis 已启动，0005_p3 迁移已应用；
#   2. API 已启动；
#   3. dev 配置 sms.expose_code_in_dev=true、im.local_fallback=true。
#
# 用法：bash scripts/verify-p3.sh [base_url]
set -uo pipefail

BASE="${1:-http://127.0.0.1:8080}"
SDK_APP_ID="${SDK_APP_ID:-1400000000}"
SEED="$(date +%s)"
SEED="${SEED:2}"
PHONE_A="${PHONE_A:-137${SEED}}"
PHONE_B="${PHONE_B:-136${SEED}}"
CLIENT_ID="p3-${SEED}-0001"
PASS=0
FAIL=0

say() { printf '\n\033[1m%s\033[0m\n' "$*"; }
ok() { PASS=$((PASS + 1)); printf '  \033[32m✓\033[0m %s\n' "$*"; }
no() { FAIL=$((FAIL + 1)); printf '  \033[31m✗\033[0m %s\n' "$*"; }

jget() {
  python3 -c '
import json,sys
try:
    data=json.loads(sys.argv[1])
except Exception:
    print(""); raise SystemExit(0)
cur=data
for part in sys.argv[2].split("."):
    if isinstance(cur,dict): cur=cur.get(part,"")
    elif isinstance(cur,list) and part.isdigit():
        i=int(part); cur=cur[i] if i < len(cur) else ""
    else: cur=""
    if cur is None: cur=""
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

login() {
  local phone="$1" device="$2" sent code result
  sent=$(post /v1/auth/sms/code "{\"phone\":\"$phone\",\"scene\":\"login\"}")
  code=$(jget "$sent" data.debug_code)
  [ -n "$code" ] || return 1
  result=$(post /v1/auth/login "{\"phone\":\"$phone\",\"code\":\"$code\",\"device_id\":\"$device\",\"platform\":1,\"app_version\":\"0.3.0\"}")
  printf '%s' "$result"
}

callback() {
  local command="$1" body="$2" sdk="${3:-$SDK_APP_ID}"
  curl -sS -X POST "$BASE/v1/im/callback?SdkAppid=$sdk&CallbackCommand=$command" \
    -H 'Content-Type: application/json' -d "$body"
}

say "0. 健康检查"
HEALTH=$(get /healthz)
if [ "$(jget "$HEALTH" code)" = "0" ]; then ok "/healthz 正常"; else no "/healthz 异常: $HEALTH"; exit 1; fi

say "1. 注册并登录两个独立账号"
LOGIN_A=$(login "$PHONE_A" "p3-device-a-${SEED}") || LOGIN_A=""
LOGIN_B=$(login "$PHONE_B" "p3-device-b-${SEED}") || LOGIN_B=""
TOKEN_A=$(jget "$LOGIN_A" data.token.access_token)
TOKEN_B=$(jget "$LOGIN_B" data.token.access_token)
UID_A=$(jget "$LOGIN_A" data.uid)
UID_B=$(jget "$LOGIN_B" data.uid)
if [ -n "$TOKEN_A" ] && [ -n "$TOKEN_B" ] && [ "$UID_A" != "$UID_B" ]; then
  ok "A(uid=$UID_A) 与 B(uid=$UID_B) 登录成功"
else
  no "登录失败，请确认 expose_code_in_dev=true: A=$LOGIN_A B=$LOGIN_B"
  exit 1
fi

say "2. 新账号初始会话为空"
EMPTY=$(get '/v1/im/conversations?page=1&size=20' "$TOKEN_A")
if [ "$(jget "$EMPTY" data.total)" = "0" ] && [ "$(jget "$EMPTY" data.unread_total)" = "0" ]; then
  ok "初始会话与未读均为 0"
else
  no "初始会话异常: $EMPTY"
fi

say "3. A 向 B 发送文本消息（local fallback）"
SEND1=$(post /v1/im/messages "{\"peer_uid\":$UID_B,\"text\":\"你好，这是 P3 验收消息\",\"client_msg_id\":\"$CLIENT_ID\"}" "$TOKEN_A")
MSG_ID=$(jget "$SEND1" data.msg_id)
DB_ID=$(jget "$SEND1" data.id)
if [ "$(jget "$SEND1" code)" = "0" ] && [ -n "$MSG_ID" ] && [ "$(jget "$SEND1" data.mine)" = "True" ]; then
  ok "发送成功 msg_id=$MSG_ID id=$DB_ID"
else
  no "发送失败: $SEND1"
fi

say "4. 相同 client_msg_id 重发必须幂等"
SEND2=$(post /v1/im/messages "{\"peer_uid\":$UID_B,\"text\":\"重复请求不应新增消息\",\"client_msg_id\":\"$CLIENT_ID\"}" "$TOKEN_A")
if [ "$(jget "$SEND2" data.msg_id)" = "$MSG_ID" ] && [ "$(jget "$SEND2" data.id)" = "$DB_ID" ]; then
  ok "幂等返回首次消息"
else
  no "幂等失效: $SEND2"
fi

say "5. 消息敏感词应在发送前拦截"
BAD=$(post /v1/im/messages "{\"peer_uid\":$UID_B,\"text\":\"加 我 微 信\",\"client_msg_id\":\"p3-${SEED}-bad1\"}" "$TOKEN_A")
if [ "$(jget "$BAD" code)" = "30002" ]; then ok "敏感词被拦截（30002）"; else no "敏感词未拦截: $BAD"; fi

say "6. B 的会话列表应出现 1 条未读"
CONV_B=$(get '/v1/im/conversations?page=1&size=20' "$TOKEN_B")
if [ "$(jget "$CONV_B" data.total)" = "1" ] \
  && [ "$(jget "$CONV_B" data.unread_total)" = "1" ] \
  && [ "$(jget "$CONV_B" data.list.0.peer.uid)" = "$UID_A" ] \
  && [ "$(jget "$CONV_B" data.list.0.last_preview)" = "你好，这是 P3 验收消息" ]; then
  ok "会话摘要、对端与未读数正确"
else
  no "B 会话列表异常: $CONV_B"
fi

say "7. B 拉取消息历史"
HISTORY_B=$(get "/v1/im/messages?peer_uid=$UID_A&limit=30" "$TOKEN_B")
if [ "$(jget "$HISTORY_B" data.list.0.msg_id)" = "$MSG_ID" ] \
  && [ "$(jget "$HISTORY_B" data.list.0.text)" = "你好，这是 P3 验收消息" ] \
  && [ "$(jget "$HISTORY_B" data.list.0.mine)" = "False" ]; then
  ok "消息历史与 mine 标记正确"
else
  no "消息历史异常: $HISTORY_B"
fi

say "8. B 标记已读"
READ_B=$(post "/v1/im/conversations/$UID_A/read" '{}' "$TOKEN_B")
CONV_B2=$(get '/v1/im/conversations?page=1&size=20' "$TOKEN_B")
if [ "$(jget "$READ_B" data.unread_count)" = "0" ] && [ "$(jget "$CONV_B2" data.unread_total)" = "0" ]; then
  ok "本地已读游标与未读总数归零"
else
  no "已读失败: read=$READ_B list=$CONV_B2"
fi

say "9. B 回复，A 应新增 1 条未读"
REPLY_ID="p3-${SEED}-reply"
REPLY=$(post /v1/im/messages "{\"peer_uid\":$UID_A,\"text\":\"收到，很高兴认识你\",\"client_msg_id\":\"$REPLY_ID\"}" "$TOKEN_B")
REPLY_MSG_ID=$(jget "$REPLY" data.msg_id)
CONV_A=$(get '/v1/im/conversations?page=1&size=20' "$TOKEN_A")
if [ -n "$REPLY_MSG_ID" ] && [ "$(jget "$CONV_A" data.unread_total)" = "1" ]; then ok "反向消息与 A 未读正确"; else no "反向消息异常: reply=$REPLY list=$CONV_A"; fi

say "10. 发送者可在 2 分钟内撤回"
RECALL=$(post "/v1/im/messages/$REPLY_MSG_ID/recall" '{}' "$TOKEN_B")
HISTORY_A=$(get "/v1/im/messages?peer_uid=$UID_B&limit=30" "$TOKEN_A")
LAST_STATUS=$(python3 -c 'import json,sys;d=json.loads(sys.argv[1]);xs=d.get("data",{}).get("list",[]);print(xs[-1].get("status","") if xs else "")' "$HISTORY_A" 2>/dev/null)
if [ "$(jget "$RECALL" data.status)" = "2" ] && [ "$LAST_STATUS" = "2" ]; then ok "撤回状态已同步到历史与会话摘要"; else no "撤回失败: recall=$RECALL history=$HISTORY_A"; fi

say "11. 非发送者不能撤回"
FORBIDDEN=$(post "/v1/im/messages/$MSG_ID/recall" '{}' "$TOKEN_B")
if [ "$(jget "$FORBIDDEN" code)" = "30004" ]; then ok "越权撤回被拒绝（30004）"; else no "越权撤回未拦截: $FORBIDDEN"; fi

say "12. 消息前回调：敏感词使用腾讯协议错误码拦截"
BEFORE_BODY="{\"CallbackCommand\":\"C2C.CallbackBeforeSendMsg\",\"From_Account\":\"u_$UID_A\",\"To_Account\":\"u_$UID_B\",\"MsgSeq\":31,\"MsgRandom\":32,\"MsgTime\":$SEED,\"MsgKey\":\"31_32_$SEED\",\"MsgId\":\"cb-before-$SEED\",\"MsgBody\":[{\"MsgType\":\"TIMTextElem\",\"MsgContent\":{\"Text\":\"私 下 转 账\"}}],\"EventTime\":${SEED}000}"
BEFORE=$(callback 'C2C.CallbackBeforeSendMsg' "$BEFORE_BODY")
if [ "$(jget "$BEFORE" ErrorCode)" = "120002" ]; then ok "回调审核拒绝（120002）"; else no "回调审核未拒绝: $BEFORE"; fi

say "13. 错误 SDKAppID 的回调必须拒绝"
WRONG=$(callback 'C2C.CallbackBeforeSendMsg' "$BEFORE_BODY" '999999')
if [ "$(jget "$WRONG" ActionStatus)" = "FAIL" ] && [ "$(jget "$WRONG" ErrorCode)" = "1" ]; then ok "SdkAppid 校验生效"; else no "错误 AppID 未拒绝: $WRONG"; fi

say "14. 消息后回调重复投递只能落一条消息、增加一次未读"
CB_MSG_ID="cb-after-$SEED"
AFTER_BODY="{\"CallbackCommand\":\"C2C.CallbackAfterSendMsg\",\"From_Account\":\"u_$UID_A\",\"To_Account\":\"u_$UID_B\",\"MsgSeq\":41,\"MsgRandom\":42,\"MsgTime\":$SEED,\"MsgKey\":\"41_42_$SEED\",\"MsgId\":\"$CB_MSG_ID\",\"SendMsgResult\":0,\"MsgBody\":[{\"MsgType\":\"TIMTextElem\",\"MsgContent\":{\"Text\":\"回调幂等消息\"}}],\"CloudCustomData\":\"{\\\"client_msg_id\\\":\\\"p3-${SEED}-callback\\\"}\",\"EventTime\":${SEED}123}"
CB1=$(callback 'C2C.CallbackAfterSendMsg' "$AFTER_BODY")
CB2=$(callback 'C2C.CallbackAfterSendMsg' "$AFTER_BODY")
CONV_B3=$(get '/v1/im/conversations?page=1&size=20' "$TOKEN_B")
if [ "$(jget "$CB1" ErrorCode)" = "0" ] && [ "$(jget "$CB2" ErrorCode)" = "0" ] \
  && [ "$(jget "$CONV_B3" data.unread_total)" = "1" ]; then
  ok "重复回调幂等，未读只增加一次"
else
  no "回调幂等异常: cb1=$CB1 cb2=$CB2 list=$CONV_B3"
fi

say "15. 已读回调同步本地未读"
READ_BODY="{\"CallbackCommand\":\"C2C.CallbackAfterMsgReadReceipt\",\"Operator_Account\":\"u_$UID_B\",\"Peer_Account\":\"u_$UID_A\",\"EventTime\":${SEED}456}"
READ_CB=$(callback 'C2C.CallbackAfterMsgReadReceipt' "$READ_BODY")
CONV_B4=$(get '/v1/im/conversations?page=1&size=20' "$TOKEN_B")
if [ "$(jget "$READ_CB" ErrorCode)" = "0" ] && [ "$(jget "$CONV_B4" data.unread_total)" = "0" ]; then ok "已读回调已清零本地未读"; else no "已读回调异常: cb=$READ_CB list=$CONV_B4"; fi

say "16. 数据库侧校验（消息幂等 / 回调幂等 / 审核留痕 / 游标）"
if command -v mysql >/dev/null 2>&1; then
  Q() { mysql -h 127.0.0.1 -u datechat -pdatechat_pass datechat -N -B -e "$1" 2>/dev/null; }
  MSG_ROWS=$(Q "SELECT COUNT(*) FROM message WHERE from_uid=$UID_A AND client_msg_id='$CLIENT_ID';")
  CB_ROWS=$(Q "SELECT COUNT(*) FROM message WHERE msg_id='$CB_MSG_ID';")
  LOG_ROWS=$(Q "SELECT COUNT(*) FROM im_callback_log WHERE command='C2C.CallbackAfterSendMsg' AND raw LIKE '%$CB_MSG_ID%';")
  MOD_ROWS=$(Q "SELECT COUNT(*) FROM moderation_record WHERE biz_type=1 AND user_id=$UID_A;")
  [ "${MSG_ROWS:-0}" = "1" ] && ok "client_msg_id 只落 1 条" || no "client_msg_id 重复（$MSG_ROWS）"
  [ "${CB_ROWS:-0}" = "1" ] && ok "MsgId 回调只落 1 条" || no "MsgId 回调重复（$CB_ROWS）"
  [ "${LOG_ROWS:-0}" = "1" ] && ok "payload_hash 回调日志只落 1 条" || no "回调日志重复（$LOG_ROWS）"
  [ "${MOD_ROWS:-0}" -ge 2 ] && ok "消息审核留痕 ${MOD_ROWS} 条" || no "消息审核留痕不足（$MOD_ROWS）"
else
  printf '  \033[33m—\033[0m 未找到 mysql，跳过数据库校验\n'
fi

printf '\n\033[1m结果：通过 %d 项，失败 %d 项\033[0m\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ] || exit 1
