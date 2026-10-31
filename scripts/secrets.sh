#!/usr/bin/env bash
# 密钥管理：生成 .env、写入密钥、上线前自检。
#
#   bash scripts/secrets.sh init                 从 .env.example 生成 .env（已存在则不覆盖）
#   bash scripts/secrets.sh set KEY=value ...     写入/更新单个或多个密钥
#   bash scripts/secrets.sh gen [bytes]           打印一个随机密钥（不写入）
#   bash scripts/secrets.sh check [--env prod]    上线前自检（默认按 prod 严格校验）
#
# 安全约定：.env 权限 600 且已被 .gitignore 忽略；真实密钥不进仓库、不进日志。
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="${ENV_FILE:-$ROOT/server/configs/.env}"
EXAMPLE="$ROOT/server/configs/.env.example"

info() { printf '%s\n' "$*"; }
ok()   { printf '  \033[32mOK\033[0m   %s\n' "$*"; }
warn() { printf '  \033[33mWARN\033[0m %s\n' "$*"; }
bad()  { printf '  \033[31mFAIL\033[0m %s\n' "$*"; }

die() { printf '\033[31m错误:\033[0m %s\n' "$*" >&2; exit 1; }

# ---------------------------------------------------------------- 子命令
usage() { sed -n '2,12p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; }

cmd_init() {
  [ -f "$EXAMPLE" ] || die "缺少模板 $EXAMPLE"
  if [ -f "$ENV_FILE" ]; then
    info ".env 已存在，不覆盖（避免丢失已填密钥）：$ENV_FILE"
  else
    cp "$EXAMPLE" "$ENV_FILE" || die "生成 .env 失败"
    info "已生成 $ENV_FILE"
  fi
  chmod 600 "$ENV_FILE" 2>/dev/null || true
  info "权限已设为 600。下一步：bash scripts/secrets.sh set KEY=value"
}

cmd_gen() {
  local bytes="${1:-32}"
  command -v openssl >/dev/null 2>&1 || die "需要 openssl"
  openssl rand -base64 "$bytes"
}

# 写入 KEY=VALUE：键存在则原地替换，不存在则追加，保持文件其余内容与注释不变。
cmd_set() {
  [ $# -ge 1 ] || { usage; exit 1; }
  [ -f "$ENV_FILE" ] || cmd_init >/dev/null
  # 临时文件放在目标文件同目录：系统临时目录在受限环境（沙箱/容器只读 /tmp）下可能写不进去，
  # 曾导致 cp 静默失败、只剩追加的键而丢失全部注释。
  local tmp="$ENV_FILE.tmp.$$" kv key value
  trap 'rm -f "$tmp" "$tmp.new"' EXIT
  cp "$ENV_FILE" "$tmp" || die "无法创建临时文件 $tmp"
  [ -s "$tmp" ] || die "临时文件为空，终止写入以免破坏现有 .env"
  for kv in "$@"; do
    case "$kv" in
      *=*) key="${kv%%=*}"; value="${kv#*=}" ;;
      *) bad "忽略非法赋值（应为 KEY=value）：$kv"; continue ;;
    esac
    if ! printf '%s' "$key" | grep -Eq '^[A-Za-z_][A-Za-z0-9_]*$'; then
      bad "非法键名：$key"; continue
    fi
    awk -v k="$key" -v v="$value" '
      function esc(s) { gsub(/\\/, "\\\\", s); gsub(/"/, "\\\"", s); return s }
      $0 ~ "^[[:space:]]*(export[[:space:]]+)?" k "=" { print k "=\"" esc(v) "\""; found=1; next }
      { print }
      END { if (!found) print k "=\"" esc(v) "\"" }
    ' "$tmp" > "$tmp.new" && mv "$tmp.new" "$tmp"
    ok "已写入 $key"
  done
  # 只有在全部键都处理成功后才落盘，避免中途失败留下半截配置。
  [ -s "$tmp" ] || die "写入结果为空，已保留原 .env 不变"
  mv "$tmp" "$ENV_FILE" || die "写回 $ENV_FILE 失败"
  trap - EXIT
  chmod 600 "$ENV_FILE" 2>/dev/null || true
}

# 取最终生效值：进程环境变量优先，其次 .env（与 config.LoadEnvFile 的优先级一致）。
eff() {
  local key="$1" val
  val="$(printenv "$key" 2>/dev/null || true)"
  if [ -n "$val" ]; then printf '%s' "$val"; return; fi
  [ -f "$ENV_FILE" ] || return 0
  awk -v k="$1" '
    $0 ~ "^[[:space:]]*(export[[:space:]]+)?" k "=" {
      line=$0; sub(/^[[:space:]]*(export[[:space:]]+)?[A-Za-z_][A-Za-z0-9_]*=/, "", line)
      sub(/[[:space:]]*#.*$/, "", line)
      q=substr(line,1,1)
      if ((q=="\"" || q=="\047") && length(line)>=2 && substr(line,length(line),1)==q) line=substr(line,2,length(line)-2)
      print line; exit
    }' "$ENV_FILE"
}

cmd_check() {
  local env="prod" fails=0 warns=0
  while [ $# -gt 0 ]; do
    case "$1" in
      --env) env="${2:-prod}"; shift 2 ;;
      *) shift ;;
    esac
  done

  if [ ! -f "$ENV_FILE" ] && [ -z "${TENCENT_IM_SECRET_KEY:-}" ]; then
    info "未找到 $ENV_FILE，且进程环境未注入 TENCENT_IM_SECRET_KEY"
    info "容器部署可忽略（只用环境变量注入）；本机请先执行：bash scripts/secrets.sh init"
  fi

  info "按 [${env}] 环境校验密钥与关键配置："

  # 基础项：任何环境都必须有
  check_len "JWT_SECRET" 32 "JWT 签名密钥，泄露即可伪造任意用户登录态"
  check_len "SECURITY_DATA_KEY" 16 "手机号/身份证 AES-256-GCM 加密密钥"
  check_len "SECURITY_HMAC_KEY" 16 "手机号/身份证 HMAC 盲索引密钥"
  if [ -n "$(eff SECURITY_DATA_KEY)" ] && [ "$(eff SECURITY_DATA_KEY)" = "$(eff SECURITY_HMAC_KEY)" ]; then
    bad "SECURITY_DATA_KEY 与 SECURITY_HMAC_KEY 不能相同"; fails=$((fails+1))
  fi

  if [ "$env" = "prod" ]; then
    # 数字型
    check_num "TENCENT_IM_SDK_APP_ID" "IM 应用 SDKAppID（控制台基本信息页）"
    check_num "TENCENT_SMS_SDK_APP_ID" "短信 SDKAppID"
    # 字符串型
    check_len "TENCENT_IM_SECRET_KEY" 16 "IM 应用密钥（泄露可被冒名收发消息）"
    check_len "TENCENT_IM_CALLBACK_TOKEN" 8 "IM 回调 Token，需与控制台配置一致"
    check_len "TENCENT_SMS_SECRET_ID" 8 "短信 SecretId"
    check_len "TENCENT_SMS_SECRET_KEY" 16 "短信 SecretKey"
    check_len "TENCENT_FACEID_SECRET_ID" 8 "人脸核身 SecretId"
    check_len "TENCENT_FACEID_SECRET_KEY" 16 "人脸核身 SecretKey"
    check_len "TENCENT_COS_SECRET_ID" 8 "COS SecretId"
    check_len "TENCENT_COS_SECRET_KEY" 16 "COS SecretKey"

    # 格式类：不是硬错误，但填错会在线上才暴露
    local b sid
    b="$(eff TENCENT_COS_BUCKET)"
    if [ -z "$b" ]; then
      bad "TENCENT_COS_BUCKET 为空（生产走 COS 上传，需形如 datechat-1250000000）"; fails=$((fails+1))
    elif ! printf '%s' "$b" | grep -Eq '^[a-z0-9-]+-[0-9]{6,}$'; then
      warn "TENCENT_COS_BUCKET=$b 不像合法桶名（应带 AppId 后缀，如 datechat-1250000000）"; warns=$((warns+1))
    else
      ok "TENCENT_COS_BUCKET 格式正常"
    fi

    sid="$(eff TENCENT_SMS_SECRET_ID)"
    if [ -n "$sid" ] && ! printf '%s' "$sid" | grep -Eq '^AKID'; then
      warn "TENCENT_SMS_SECRET_ID 通常以 AKID 开头，请确认没填错成 SecretKey"; warns=$((warns+1))
    fi

    if [ -z "$(eff REALNAME_REDIRECT_URL)" ]; then
      warn "REALNAME_REDIRECT_URL 为空，人脸核身完成后无法回跳（需在控制台白名单内）"; warns=$((warns+1))
    else
      ok "REALNAME_REDIRECT_URL 已配置"
    fi
    if [ -z "$(eff TENCENT_SMS_SIGN_NAME)" ] || [ -z "$(eff TENCENT_SMS_TEMPLATE_ID)" ]; then
      warn "短信签名/模板未配置，真实短信将发送失败（dev 用 mock 不受影响）"; warns=$((warns+1))
    else
      ok "短信签名与模板已配置"
    fi
  else
    if [ -z "$(eff TENCENT_IM_SECRET_KEY)" ]; then
      info "  （dev 未配 IM 密钥 → 依赖 im.local_fallback 跑本地镜像，属预期）"
    else
      info "  dev 已配 IM 真密钥：可把 im.local_fallback 设为 false 做真实联调"
    fi
  fi

  info ""
  if [ "$fails" -gt 0 ]; then
    printf '\033[31m结论：%d 项不满足，禁止上线。\033[0m 补齐后重跑：bash scripts/secrets.sh check\n' "$fails"
    exit 1
  fi
  if [ "$warns" -gt 0 ]; then
    printf '\033[33m结论：必需项齐全，但有 %d 项建议核对。\033[0m\n' "$warns"
  else
    printf '\033[32m结论：密钥配置检查通过。\033[0m\n'
  fi
  info "提示：静态检查只证明『填了、格式对』；真正加载一遍请执行："
  info "      cd server && APP_ENV=prod go run ./cmd/api -check-config   # 不连库、不监听端口"
}

check_len() {
  local key="$1" min="$2" desc="$3" val
  val="$(eff "$key")"
  if [ -z "$val" ]; then
    bad "$key 为空 —— $desc"; fails=$((fails+1)); return
  fi
  if [ "${#val}" -lt "$min" ]; then
    bad "$key 过短（${#val} < $min）—— $desc"; fails=$((fails+1)); return
  fi
  ok "$key 已配置（长度 ${#val}）"
}

check_num() {
  local key="$1" desc="$2" val
  val="$(eff "$key")"
  if [ -z "$val" ]; then
    bad "$key 为空 —— $desc"; fails=$((fails+1)); return
  fi
  if ! printf '%s' "$val" | grep -Eq '^[0-9]+$'; then
    bad "$key=$val 不是纯数字 —— $desc"; fails=$((fails+1)); return
  fi
  ok "$key = $val"
}

case "${1:-}" in
  init)  shift; cmd_init "$@" ;;
  set)   shift; cmd_set "$@" ;;
  gen)   shift; cmd_gen "${1:-32}" ;;
  check) shift; cmd_check "$@" ;;
  ""|-h|--help|help) usage ;;
  *) die "未知子命令：$1（可用 init / set / gen / check）" ;;
esac
