#!/usr/bin/env bash
# 大文件并行分段下载器（支持断点续传）
#
# 为什么需要它：单连接从 Google/国内镜像拉 Flutter SDK 只有 100~700KB/s，
# 2.1GB 要一个多小时。实测 16 条连接并行能把有效带宽吃满到 ~1.6MB/s，
# 而且每段独立续传，中途断了重跑即可，不会像单连接那样从头再来。
#
# 用法：
#   bash scripts/fetch-sdk.sh <url> <输出文件> [分段数=16]
#
# 产物：<输出文件>，中间文件在 <输出文件>.parts/
set -uo pipefail

URL="${1:?用法: fetch-sdk.sh <url> <输出文件> [分段数]}"
OUT="${2:?缺少输出文件路径}"
N="${3:-16}"

PARTS="${OUT}.parts"
mkdir -p "$PARTS"

TOTAL=$(curl -sSI --max-time 20 "$URL" 2>/dev/null \
  | tr -d '\r' \
  | awk 'tolower($1)=="content-length:"{print $2}' \
  | tail -1 | tr -dc '0-9')

if [ -z "$TOTAL" ]; then
  echo "❌ 无法获取 Content-Length，检查 URL 或网络" >&2
  exit 1
fi

CHUNK=$(( (TOTAL + N - 1) / N ))
echo "总大小 $TOTAL 字节（$(( TOTAL / 1024 / 1024 )) MB），分 $N 段，每段约 $(( CHUNK / 1024 / 1024 )) MB"

download_part() {
  local idx="$1" start="$2" end="$3"
  local want=$(( end - start + 1 ))
  local part
  part="$PARTS/part_$(printf '%03d' "$idx")"

  local attempt have
  for attempt in $(seq 1 30); do
    have=$(stat -f%z "$part" 2>/dev/null || echo 0)
    if [ "$have" -ge "$want" ]; then
      return 0
    fi
    # 从已有字节处续传；--speed-limit 防止卡在一条死连接上不返回
    curl -sS --max-time 900 --speed-limit 800 --speed-time 60 \
      -r "$(( start + have ))-$end" "$URL" >>"$part" 2>/dev/null || true
  done

  have=$(stat -f%z "$part" 2>/dev/null || echo 0)
  if [ "$have" -lt "$want" ]; then
    echo "❌ 第 $idx 段未完成：$have / $want" >&2
    return 1
  fi
}

for i in $(seq 0 $(( N - 1 ))); do
  S=$(( i * CHUNK ))
  E=$(( S + CHUNK - 1 ))
  [ "$E" -ge "$TOTAL" ] && E=$(( TOTAL - 1 ))
  download_part "$i" "$S" "$E" &
done

FAILED=0
for job in $(jobs -p); do
  wait "$job" || FAILED=1
done

if [ "$FAILED" -ne 0 ]; then
  echo "❌ 存在未完成分段，重跑本脚本可继续（已下载部分保留）" >&2
  exit 1
fi

# 按序号拼接，顺序错了 zip 就废了
cat "$PARTS"/part_* >"$OUT"

SIZE=$(stat -f%z "$OUT" 2>/dev/null || echo 0)
if [ "$SIZE" -ne "$TOTAL" ]; then
  echo "❌ 合并后大小不符：$SIZE != $TOTAL" >&2
  exit 1
fi

echo "✅ 下载完成：$OUT（$(( SIZE / 1024 / 1024 )) MB）"
