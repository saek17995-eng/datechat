#!/usr/bin/env python3
"""把 pub 缓存里插件写死的 compileSdk 抬到 37。

为什么需要它：`tencent_rtc_sdk` 的 android/build.gradle 里写死
`compileSdkVersion 31`，但它依赖的 androidx 组件（core-runtime 2.2.0、
window.extensions.core 1.0.0 等）在自己的 AAR 元数据里要求使用方
compileSdk ≥ 33。Gradle 的 checkReleaseAarMetadata 见到 31 就直接失败：

    Dependency 'androidx.arch.core:core-runtime:2.2.0' requires ... version 33
    :tencent_rtc_sdk is currently compiled against android-31

插件是第三方代码，不能改仓库源码，所以在 `flutter pub get` 之后、
`flutter build` 之前，直接改本地 pub 缓存里的构建脚本。这个改动只影响
本次构建环境（CI 每次都是干净缓存），不进仓库。

用法：
    python3 scripts/android/patch_plugin_compile_sdk.py [目标 compileSdk=37] [--dry-run]
"""

import os
import re
import sys
from pathlib import Path

# 只挑确定有问题的插件，避免误伤其他插件的构建脚本。
PLUGINS = (
    "tencent_rtc_sdk",        # 写死 compileSdkVersion 31
    "tencent_cloud_chat_sdk",  # 写死 34
    "super_player",           # 跟随 rootProject.ext
    "flutter_local_notifications",  # 跟随模板值，抬高无副作用
)

COMPILE_SDK_PATTERNS = [
    (re.compile(r"compileSdkVersion\s+(\d+)"), "compileSdkVersion {target}"),
    (re.compile(r"compileSdk\s*=\s*(\d+)"), "compileSdk = {target}"),
    (re.compile(r"compileSdk\s+(\d+)"), "compileSdk {target}"),
]


def pub_cache_root() -> Path:
    env = os.environ.get("PUB_CACHE")
    if env:
        return Path(env)
    return Path.home() / ".pub-cache"


def patch_file(path: Path, target: int, dry_run: bool) -> bool:
    text = path.read_text(encoding="utf-8")
    original = text
    found = None

    for pattern, template in COMPILE_SDK_PATTERNS:
        m = pattern.search(text)
        if not m:
            continue
        found = m.group(1)
        if int(found) >= target:
            return False
        text = pattern.sub(template.format(target=target), text, count=1)
        break

    if text == original:
        return False

    print(f"  {path.parent.parent.name}: compileSdk {found} → {target}")
    if not dry_run:
        path.write_text(text, encoding="utf-8")
    return True


def main() -> int:
    target = 37
    args = [a for a in sys.argv[1:] if not a.startswith("--")]
    if args:
        target = int(args[0])
    dry_run = "--dry-run" in sys.argv

    root = pub_cache_root()
    print(f"pub 缓存：{root}（目标 compileSdk {target}{'，仅演练' if dry_run else ''}）")

    changed = 0
    for hosted in sorted(root.glob("hosted/*")):
        for name in PLUGINS:
            for pkg in sorted(hosted.glob(f"{name}-*")):
                for build_file in (
                    pkg / "android" / "build.gradle",
                    pkg / "android" / "build.gradle.kts",
                ):
                    if build_file.exists() and patch_file(build_file, target, dry_run):
                        changed += 1

    if changed == 0:
        print("  无需改动（插件未下载或已满足要求）")
    else:
        print(f"  共修改 {changed} 个构建脚本")
    return 0


if __name__ == "__main__":
    sys.exit(main())
