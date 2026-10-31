#!/usr/bin/env bash
# 无 Docker 环境下的本地依赖启停脚本。
#
# 本机（macOS）没有 sudo 权限，装不了 Docker Desktop，因此直接用进程方式跑
# MySQL 与 Redis，数据目录统一放在仓库的 .local/ 下（已 gitignore）。
#
# 用法：
#   scripts/dev-services.sh start    启动 MySQL + Redis
#   scripts/dev-services.sh stop     停止
#   scripts/dev-services.sh status   查看状态
#
# 可用环境变量覆盖：
#   MYSQL_BASE  mysqld 所在前缀目录（默认 /opt/anaconda3）
#   MYSQL_PORT  默认 3306
#   REDIS_BIN   redis-server 路径（默认取 PATH，其次 /tmp/redis-7.2.5/src）
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
LOCAL="$ROOT/.local"
MYSQL_DATA="$LOCAL/mysql/data"
MYSQL_SOCK="$LOCAL/mysql/mysql.sock"
MYSQL_BASE="${MYSQL_BASE:-/opt/anaconda3}"
MYSQL_PORT="${MYSQL_PORT:-3306}"
REDIS_BIN="${REDIS_BIN:-$(command -v redis-server || echo /tmp/redis-7.2.5/src/redis-server)}"
REDIS_DIR="$LOCAL/redis"

start_mysql() {
  if [ ! -d "$MYSQL_DATA/mysql" ]; then
    echo "→ 首次初始化 MySQL 数据目录"
    mkdir -p "$MYSQL_DATA"
    "$MYSQL_BASE/bin/mysqld" --no-defaults --initialize-insecure \
      --basedir="$MYSQL_BASE" --datadir="$MYSQL_DATA" \
      --lc-messages-dir="$MYSQL_BASE/share/mysql" \
      --log-error="$LOCAL/mysql/init.err"
  fi
  if "$MYSQL_BASE/bin/mysqladmin" --socket="$MYSQL_SOCK" -uroot ping >/dev/null 2>&1; then
    echo "MySQL 已在运行"
    return
  fi
  echo "→ 启动 MySQL :$MYSQL_PORT"
  nohup "$MYSQL_BASE/bin/mysqld" --no-defaults \
    --basedir="$MYSQL_BASE" --datadir="$MYSQL_DATA" \
    --lc-messages-dir="$MYSQL_BASE/share/mysql" \
    --port="$MYSQL_PORT" --bind-address=127.0.0.1 \
    --socket="$MYSQL_SOCK" --pid-file="$LOCAL/mysql/mysqld.pid" \
    --log-error="$LOCAL/mysql/mysqld.err" \
    --explicit_defaults_for_timestamp --character-set-server=utf8mb4 \
    --collation-server=utf8mb4_unicode_ci --default-time-zone=+08:00 \
    --max_connections=200 --innodb_buffer_pool_size=256M >/dev/null 2>&1 &

  for _ in $(seq 1 30); do
    if "$MYSQL_BASE/bin/mysqladmin" --socket="$MYSQL_SOCK" -uroot ping >/dev/null 2>&1; then
      echo "MySQL 已就绪"
      return
    fi
    sleep 1
  done
  echo "MySQL 启动失败，查看 $LOCAL/mysql/mysqld.err"
}

start_redis() {
  if [ ! -x "$REDIS_BIN" ]; then
    echo "找不到 redis-server（可用 REDIS_BIN 指定）"
    return 1
  fi
  if "$REDIS_BIN" --version >/dev/null 2>&1 && nc -z 127.0.0.1 6379 2>/dev/null; then
    echo "Redis 已在运行"
    return
  fi
  mkdir -p "$REDIS_DIR"
  echo "→ 启动 Redis :6379"
  nohup "$REDIS_BIN" --port 6379 --bind 127.0.0.1 --dir "$REDIS_DIR" \
    --appendonly no --save "" --maxmemory 256mb --maxmemory-policy allkeys-lru \
    >"$LOCAL/redis/redis.log" 2>&1 &
  sleep 1
  echo "Redis 已就绪"
}

stop_all() {
  echo "→ 停止 MySQL"
  "$MYSQL_BASE/bin/mysqladmin" --socket="$MYSQL_SOCK" -uroot shutdown >/dev/null 2>&1 || pkill -f "mysqld.*datechat" || true
  echo "→ 停止 Redis"
  pkill -f "redis-server .*6379" || true
  echo "已停止"
}

status_all() {
  if "$MYSQL_BASE/bin/mysqladmin" --socket="$MYSQL_SOCK" -uroot ping >/dev/null 2>&1; then
    echo "MySQL  : 运行中 (127.0.0.1:$MYSQL_PORT, socket=$MYSQL_SOCK)"
  else
    echo "MySQL  : 未运行"
  fi
  if nc -z 127.0.0.1 6379 2>/dev/null; then
    echo "Redis  : 运行中 (127.0.0.1:6379)"
  else
    echo "Redis  : 未运行"
  fi
}

case "${1:-status}" in
  start)  start_mysql; start_redis ;;
  stop)   stop_all ;;
  status) status_all ;;
  *) echo "用法: $0 {start|stop|status}"; exit 1 ;;
esac
