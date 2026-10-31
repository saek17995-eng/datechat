#!/usr/bin/env bash
# 在 MySQL 5.7 上执行迁移（仅用于无 Docker 的本地演示环境）。
#
# 背景：正式 schema 面向 MySQL 8.0，使用了 utf8mb4_0900_ai_ci 排序规则，
# 5.7 不认识这个 collation 会直接报错。本脚本把迁移文件复制到 .local/ 下做
# 一次性替换后再执行，仓库里的 migrations/*.sql 保持 8.0 原样不动。
#
# 生产 / CI 请直接用 `make migrate` 连 MySQL 8.0。
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TMP="$ROOT/.local/migrations57"

rm -rf "$TMP"
mkdir -p "$TMP"

for file in "$ROOT"/server/migrations/*.sql; do
  sed 's/utf8mb4_0900_ai_ci/utf8mb4_unicode_ci/g' "$file" > "$TMP/$(basename "$file")"
done

echo "→ 已生成 5.7 兼容副本：$TMP"
cd "$ROOT/server"
exec go run ./cmd/migrate -c configs/config.dev.yaml -dir "$TMP" "${1:-up}"
