#!/usr/bin/env bash
# 在"没有完整 Flutter SDK"的机器上跑客户端静态分析
#
# 背景：官方 SDK zip 有 2.16GB，其中 1.35GB 是各平台 artifacts（编译/运行才用）。
# 静态分析只需要 dart-sdk + flutter_tools 快照 + packages/，合计约 250MB。
# 本脚本不负责下载（那部分见 README「客户端已通过静态分析验证」一节的表格），
# 只负责：确认 SDK 可用 → 重建 package_config.json → 跑 analyze 与格式检查。
#
# 为什么要自己重建 package_config：
# 抽取出来的 SDK 让 pub 无法判定 flutter 版本（会报 0.0.0-unknown），
# 于是 `flutter pub get` / `dart pub get` 都解不动 flutter_riverpod 的
# `environment: flutter:` 约束。但 analyzer 只需要 package_config.json，
# 所以这里按 pub 的目录约定直接从缓存拼一份。
#
# 用法：
#   bash scripts/flutter-offline-analyze.sh
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FLUTTER="${FLUTTER_ROOT:-$ROOT/.local/flutter}"
APP="$ROOT/app"
DART="$FLUTTER/bin/cache/dart-sdk/bin/dart"
CACHE="${PUB_CACHE:-$HOME/.pub-cache}/hosted/pub.flutter-io.cn"

if [ ! -x "$DART" ]; then
  cat >&2 <<EOF
❌ 找不到 Dart SDK：$DART

先在能联网的机器上准备最小 SDK（约 250MB）：
  1. bash scripts/fetch-sdk.sh <flutter_zip_url> /tmp/flutter-sdk.zip 18
  2. bash scripts/flutter-setup.sh /tmp/flutter-sdk.zip "$FLUTTER"
或者直接把完整 Flutter SDK 放到 $FLUTTER，再重新执行本脚本。
EOF
  exit 1
fi

echo "== Dart =="
"$DART" --version

if [ ! -d "$CACHE" ]; then
  echo "❌ 找不到 pub 缓存：$CACHE" >&2
  echo "   先在有网的机器上对仓库执行一次 flutter pub get" >&2
  exit 1
fi

echo "== 重建 package_config.json =="
FLUTTER="$FLUTTER" APP="$APP" CACHE="$CACHE" python3 - <<'PY'
import json, os, re

flutter = os.environ["FLUTTER"]
app = os.environ["APP"]
cache = os.environ["CACHE"]

# 与 app/pubspec.yaml 声明的约束保持一致；版本漂移会让 analyzer 报假的 API 错误
preferred = {
    "flutter_riverpod": "2.6.1", "riverpod": "2.6.1", "go_router": "14.6.2",
    "vector_math": "2.2.0", "plugin_platform_interface": "2.1.8",
    "shared_preferences_platform_interface": "2.4.1", "cupertino_icons": "1.0.8",
    "shared_preferences": "2.5.6", "dio": "5.11.1", "characters": "1.4.1",
    "material_color_utilities": "0.13.1", "flutter_lints": "5.0.0", "lints": "5.0.0",
}


def lang_version(pubspec):
    try:
        text = open(pubspec, encoding="utf-8").read()
    except OSError:
        return "3.0"
    m = re.search(r"environment:\s*\n\s*sdk:\s*['\"]?([^\s'\"]+)", text)
    if not m:
        return "3.0"
    nums = re.findall(r"\d+", m.group(1))
    return f"{nums[0]}.{nums[1]}" if len(nums) >= 2 else "3.0"


packages = [{"name": "datechat", "rootUri": "../", "packageUri": "lib/",
             "languageVersion": "3.5"}]

# SDK 自带的三个包直接指向本地源码
for name, rel in (("flutter", "packages/flutter"),
                  ("flutter_test", "packages/flutter_test"),
                  ("sky_engine", "bin/cache/pkg/sky_engine")):
    path = os.path.join(flutter, rel)
    if os.path.isdir(path):
        packages.append({
            "name": name, "rootUri": "file://" + path, "packageUri": "lib/",
            "languageVersion": lang_version(os.path.join(path, "pubspec.yaml")),
        })

# 其余依赖从 pub 缓存里挑，pinned 的按 preferred 严格匹配
chosen = {}
for entry in sorted(os.listdir(cache)):
    full = os.path.join(cache, entry)
    if not os.path.isdir(full) or not os.path.exists(os.path.join(full, "pubspec.yaml")):
        continue
    m = re.match(r"(.+)-(\d+\..*)$", entry)
    if not m:
        continue
    name, ver = m.group(1), m.group(2)
    want = preferred.get(name)
    if want and ver != want:
        continue
    if name in chosen and not want:
        continue
    chosen[name] = full

for name, full in sorted(chosen.items()):
    packages.append({
        "name": name, "rootUri": "file://" + full, "packageUri": "lib/",
        "languageVersion": lang_version(os.path.join(full, "pubspec.yaml")),
    })

out = os.path.join(app, ".dart_tool")
os.makedirs(out, exist_ok=True)
with open(os.path.join(out, "package_config.json"), "w") as f:
    json.dump({"configVersion": 2, "packages": packages}, f, indent=2)

print(f"   写入 {len(packages)} 个包")
missing = [n for n in ("flutter", "flutter_riverpod", "go_router", "dio", "vector_math")
           if n not in {p["name"] for p in packages}]
if missing:
    print("   ⚠️ 缺少：" + ", ".join(missing) + "（analyze 会报 URI 不存在）")
PY

cd "$APP" || exit 1

echo "== dart analyze =="
"$DART" analyze lib
ANALYZE=$?

echo "== 格式检查 =="
"$DART" format --output=none --set-exit-if-changed lib >/tmp/fmt.log 2>&1
FMT=$?
tail -1 /tmp/fmt.log

if [ "$ANALYZE" -eq 0 ] && [ "$FMT" -eq 0 ]; then
  echo "✅ 通过：分析无问题，格式已规范"
  exit 0
fi
echo "❌ 未通过（analyze=$ANALYZE format=${FMT}）"
exit 1
