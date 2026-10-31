#!/usr/bin/env bash
# 从官方 zip 离线安装 Flutter SDK（无需 sudo）
#
# 为什么不用 `brew install floco` / 官网下载器：Docker Desktop 与系统级安装都需要
# 管理员密码，而本机 sudo 不可用。解压到用户目录是唯一可行路径。
#
# 用法：
#   bash scripts/fetch-sdk.sh <flutter_zip_url> /tmp/flutter-sdk.zip 18
#   bash scripts/flutter-setup.sh /tmp/flutter-sdk.zip [安装目录]
#
# 默认装到 <仓库>/.local/flutter，并把 bin 加进 PATH（打印 export 供你粘到 shell）。
set -euo pipefail

ZIP="${1:?用法: flutter-setup.sh <flutter.zip 路径> [安装目录]}"
WORKSPACE="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEST="${2:-$WORKSPACE/.local/flutter}"

[ -f "$ZIP" ] || { echo "❌ 找不到 $ZIP" >&2; exit 1; }

echo "== 校验 zip 完整性 =="
unzip -tq "$ZIP" >/dev/null || { echo "❌ zip 损坏，重新执行 fetch-sdk.sh" >&2; exit 1; }
echo "✅ zip 完整"

echo "== 解压到 $DEST =="
mkdir -p "$(dirname "$DEST")"
rm -rf "$DEST"
# 官方 zip 内层目录是 flutter/，用 -d 到父级再改名，避免多一层
TMPDIR_UNZIP="$(dirname "$DEST")/.flutter-unzip"
rm -rf "$TMPDIR_UNZIP"
mkdir -p "$TMPDIR_UNZIP"
unzip -q "$ZIP" -d "$TMPDIR_UNZIP"
mv "$TMPDIR_UNZIP/flutter" "$DEST"
rmdir "$TMPDIR_UNZIP" 2>/dev/null || true

# macOS 解压后二进制可能丢失执行位
chmod +x "$DEST/bin/flutter" "$DEST/bin/dart" 2>/dev/null || true
find "$DEST/bin/cache/dart-sdk/bin" -type f -perm -u+x 2>/dev/null | head -1 >/dev/null || \
  chmod +x "$DEST"/bin/cache/dart-sdk/bin/* 2>/dev/null || true

echo "== 版本 =="
"$DEST/bin/flutter" --version || {
  echo "⚠️ flutter --version 失败，尝试关闭版本检查后重试" >&2
  export FLUTTER_SUPPRESS_ANALYTICS=true
  "$DEST/bin/flutter" --version
}

cat <<EOF

✅ 安装完成：$DEST

把下面两行加进 ~/.zshrc，或直接在当前终端执行：

  export FLUTTER_ROOT="$DEST"
  export PATH="\$FLUTTER_ROOT/bin:\$PATH"

国内网络建议同时设置 pub 镜像：

  export PUB_HOSTED_URL=https://pub.flutter-io.cn
  export FLUTTER_STORAGE_BASE_URL=https://storage.flutter-io.cn

然后：

  cd $WORKSPACE/app
  flutter pub get
  flutter analyze
EOF
