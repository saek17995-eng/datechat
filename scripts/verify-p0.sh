#!/usr/bin/env bash
# P0 验收脚本：一次性验证健康检查、统一响应、404/405、限流、TraceID。
#
# 用法：脚本在 server 已启动的前提下执行
#   bash scripts/verify-p0.sh [BASE_URL]
set -uo pipefail

BASE="${1:-http://127.0.0.1:8080}"
PASS=0
FAIL=0

check() {
  local desc="$1" expect="$2" actual="$3"
  if [ "$expect" = "$actual" ]; then
    printf "  \033[32m✓\033[0m %s\n" "$desc"
    PASS=$((PASS + 1))
  else
    printf "  \033[31m✗\033[0m %s（期望 %s，实际 %s）\n" "$desc" "$expect" "$actual"
    FAIL=$((FAIL + 1))
  fi
}

status() { curl -s -o /dev/null -w '%{http_code}' --max-time 10 "$@"; }

echo "验证目标：$BASE"
echo

echo "[1] 存活与就绪"
check "GET /healthz 返回 200" "200" "$(status "$BASE/healthz")"
check "GET /readyz 返回 200（MySQL+Redis 均可用）" "200" "$(status "$BASE/readyz")"

echo
echo "[2] 业务连通性"
PING_BODY="$(curl -s --max-time 10 "$BASE/v1/ping")"
check "GET /v1/ping 返回 200" "200" "$(status "$BASE/v1/ping")"
if echo "$PING_BODY" | grep -q '"code":0'; then
  check "响应体 code=0" "ok" "ok"
else
  check "响应体 code=0" "ok" "body=$PING_BODY"
fi
if echo "$PING_BODY" | grep -q 'datechat-api'; then
  check "响应体包含服务名" "ok" "ok"
else
  check "响应体包含服务名" "ok" "body=$PING_BODY"
fi

echo
echo "[3] 统一错误响应"
check "GET /v1/not-exist 返回 404" "404" "$(status "$BASE/v1/not-exist")"
check "POST /v1/ping 返回 405" "405" "$(status -X POST "$BASE/v1/ping")"
NOTFOUND_JSON="$(curl -s --max-time 10 "$BASE/v1/not-exist")"
if echo "$NOTFOUND_JSON" | grep -q '"code"'; then
  check "404 响应体是统一结构" "ok" "ok"
else
  check "404 响应体是统一结构" "ok" "body=$NOTFOUND_JSON"
fi

echo
echo "[4] TraceID 透传"
TRACE_ID="$(curl -s -D - -o /dev/null --max-time 10 -H 'X-Request-Id: p0-verify-001' "$BASE/v1/ping" | tr -d '\r' | awk -F': ' 'tolower($1)=="x-request-id"{print $2}')"
check "回显客户端 TraceID" "p0-verify-001" "$TRACE_ID"
GENERATED_ID="$(curl -s -D - -o /dev/null --max-time 10 "$BASE/v1/ping" | tr -d '\r' | awk -F': ' 'tolower($1)=="x-request-id"{print $2}')"
if [ -n "$GENERATED_ID" ]; then
  check "无 TraceID 时自动生成" "ok" "ok"
else
  check "无 TraceID 时自动生成" "ok" "空"
fi

echo
echo "[5] 限流（并发打 200 次 /v1/ping，瞬时超过 burst）"
# 注意：必须并发。顺序 curl 实测只有 ~110 rps，而 dev 配置是 rps=50 / burst=100，
# 顺序打 120 次耗时约 1.1s，期间令牌还会补充约 55 个，是否触发取决于机器上一次
# 跑脚本后桶里还剩多少令牌 —— 断言会随机翻车。并发发送才能在确定时间内打穿 burst。
LIMITED="$(seq 1 200 \
  | xargs -P 32 -I{} curl -s -o /dev/null -w '%{http_code}\n' --max-time 10 "$BASE/v1/ping" \
  | grep -c '^429$' || true)"
LIMITED="${LIMITED:-0}"
if [ "$LIMITED" -gt 0 ]; then
  check "触发限流（429 出现 ${LIMITED} 次）" "ok" "ok"
else
  check "触发限流" "ok" "未触发，可能 rps/burst 配置过高"
fi

echo
echo "----------------------------------------"
printf "通过 %d 项，失败 %d 项\n" "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ] || exit 1
