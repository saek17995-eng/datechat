#!/usr/bin/env bash
# 一键启动后端：拉起 MySQL/Redis → 编译并启动 API → 等健康 → 打印手机可用地址。
#
# 用法：
#   bash scripts/start-backend.sh            已在跑就直接打印地址，没在跑才启动
#   bash scripts/start-backend.sh restart    强制重启（改了配置/代码后用它）
#   bash scripts/start-backend.sh stop       停掉 API（依赖服务保留）
#   bash scripts/start-backend.sh status     只看状态
#
# 注意：
#   - 端口默认 8080（configs/config.dev.yaml 的 app.addr）；可用 PORT=xxx 覆盖。
#   - 日志在 .local/api.log，启动失败第一时间看它。
#   - 本机 mysqld 是 5.7，建表要用 scripts/migrate-local57.sh（正式 schema 面向 8.0）。
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PORT="${PORT:-8080}"
LOG="$ROOT/.local/api.log"
BIN="$ROOT/.local/api/datechat-api"
PIDFILE="$ROOT/.local/api/api.pid"

lan_ips() {
  for i in en0 en1 en2 en3 en4; do
    a="$(ipconfig getifaddr "$i" 2>/dev/null)"
    [ -n "$a" ] && printf '%s\n' "$a"
  done
}

api_pid() {
  lsof -nP -iTCP:"$PORT" -sTCP:LISTEN -t 2>/dev/null | head -1
}

healthy() {
  curl -s -m 2 "http://127.0.0.1:$PORT/healthz" 2>/dev/null | grep -q '"code":0'
}

print_addr() {
  local ip
  ip="$(lan_ips | head -1)"
  echo ""
  echo "────────────────────────────────────────────"
  if [ -n "$ip" ]; then
    echo "  手机/App 里填：  http://$ip:$PORT"
  else
    echo "  没取到局域网 IP（可能没连 WiFi），手机将无法访问"
  fi
  echo "  本机自测：      http://127.0.0.1:$PORT/v1/ping"
  echo "  日志：          $LOG"
  echo "────────────────────────────────────────────"
}

cmd_stop() {
  local pid
  pid="$(api_pid)"
  if [ -z "$pid" ]; then echo "API 没在跑"; return 0; fi
  kill "$pid" 2>/dev/null
  for _ in $(seq 1 20); do
    [ -z "$(api_pid)" ] && break
    sleep 0.5
  done
  if [ -n "$(api_pid)" ]; then kill -9 "$(api_pid)" 2>/dev/null; fi
  rm -f "$PIDFILE"
  echo "API 已停止"
}

cmd_start() {
  echo "→ 依赖服务（MySQL / Redis）"
  bash "$ROOT/scripts/dev-services.sh" start

  echo "→ 编译 API"
  # shellcheck disable=SC1091
  source "$ROOT/scripts/go-env.sh"
  mkdir -p "$(dirname "$BIN")" "$(dirname "$LOG")"
  (cd "$ROOT/server" && go build -o "$BIN" ./cmd/api) || { echo "编译失败"; exit 1; }

  # 读取 .env（占位符配置靠它填充真实值），缺文件就跳过
  if [ -f "$ROOT/server/configs/.env" ]; then
    set -a
    # shellcheck disable=SC1091
    . "$ROOT/server/configs/.env"
    set +a
  fi

  echo "→ 启动 API :$PORT"
  # 不能用 nohup ... & ：那样启动的进程仍挂在当前 shell 的会话/进程组上，
  # 这个 shell 一被终止，API 会被连带杀掉（实测多次）。MySQL/Redis 之所以能活，
  # 是因为启动它们的任务正常退出、进程被过继给 launchd。这里直接走经典守护化：
  # fork → setsid → fork，彻底脱离会话与进程组，谁来回收都收不到它。
  (cd "$ROOT/server" && DC_API_LOG="$LOG" python3 - "$BIN" <<'PY' && echo "守护进程已派生"
import os, sys

bin_path = sys.argv[1]
workdir = os.getcwd()
env = dict(os.environ)
log_path = env.get("DC_API_LOG", "/dev/null")

if os.fork() > 0:
    sys.exit(0)
os.setsid()
if os.fork() > 0:
    sys.exit(0)

os.chdir(workdir)
os.umask(0o022)
fd = os.open(log_path, os.O_WRONLY | os.O_APPEND | os.O_CREAT, 0o644)
os.dup2(fd, 1)
os.dup2(fd, 2)
os.execve(bin_path, [bin_path, "-c", "configs/config.dev.yaml"], env)
PY
  )

  for _ in $(seq 1 60); do
    if healthy; then
      echo "API 已就绪"
      print_addr
      return 0
    fi
    sleep 1
  done
  echo "启动超时，日志尾部："
  tail -20 "$LOG"
  return 1
}

case "${1:-start}" in
  start)
    if [ -n "$(api_pid)" ] && healthy; then
      echo "API 已在运行（PID $(api_pid)），不重复启动"
      print_addr
    else
      cmd_start
    fi
    ;;
  restart)
    cmd_stop
    cmd_start
    ;;
  stop)
    cmd_stop
    ;;
  status)
    bash "$ROOT/scripts/dev-services.sh" status
    if [ -n "$(api_pid)" ] && healthy; then
      echo "API    : 运行中 (0.0.0.0:$PORT, PID $(api_pid))"
      print_addr
    else
      echo "API    : 未运行"
    fi
    ;;
  *)
    echo "用法: $0 {start|restart|stop|status}"
    exit 1
    ;;
esac
