#!/usr/bin/env python3
"""把 Flutter 生成的 android/ 壳补成可以真正安装使用的样子。

为什么需要它：仓库里只维护 `app/lib/`（业务代码），`android/` 是平台壳，
由 `flutter create --platforms=android` 按当前 SDK 版本现生成 —— 这样不会因为
某个版本的模板变化而在仓库里留下一堆过期文件。但模板是"最少可用"，缺三样东西：

1. **INTERNET 权限只在 debug/profile manifest 里有**，release 的 main manifest 没有，
   打出来的正式包连不上服务器（Flutter 的默认行为，很多人第一次发包会踩）。
2. 语音视频通话要 CAMERA / RECORD_AUDIO / MODIFY_AUDIO_SETTINGS，
   附近的人要 ACCESS_COARSE_LOCATION / ACCESS_FINE_LOCATION。
3. release 默认用 debug key 签名，能装但上不了架；给了 key.properties 就换成正式签名。

用法：
    python3 scripts/android/prepare_apk.py [android_dir]      # 默认 app/android
"""

import re
import sys
from pathlib import Path

# Android 6.0+ 的危险权限（位置/相机/麦克风）除了声明，还必须在运行时动态申请。
# 本脚本只负责声明；运行时申请见 app/lib/features/nearby/location_source.dart 与
# 通话页的权限请求（真机上不申请的话，Android 会在调用时直接抛 SecurityException）。
PERMISSIONS = [
    "android.permission.INTERNET",
    "android.permission.ACCESS_NETWORK_STATE",
    "android.permission.ACCESS_COARSE_LOCATION",
    "android.permission.ACCESS_FINE_LOCATION",
    "android.permission.CAMERA",
    "android.permission.RECORD_AUDIO",
    "android.permission.MODIFY_AUDIO_SETTINGS",
    "android.permission.VIBRATE",
]


def patch_manifest(path: Path) -> bool:
    if not path.exists():
        print(f"  ! 找不到 {path}，跳过权限补丁")
        return False
    text = path.read_text(encoding="utf-8")

    missing = [p for p in PERMISSIONS if f'"{p}"' not in text]
    if not missing:
        print(f"  权限已齐全（{len(PERMISSIONS)} 项），无需改动")
    else:
        block = "\n".join(
            f'    <uses-permission android:name="{p}" />' for p in missing
        )
        # 插到 <manifest ...> 开标签之后、<application> 之前
        m = re.search(r"(<manifest\b[^>]*>)", text)
        if not m:
            print("  ! 没找到 <manifest> 开标签，跳过权限补丁")
            return False
        text = text[: m.end()] + "\n\n" + block + "\n" + text[m.end() :]
        print(f"  补 {len(missing)} 项权限：{', '.join(m.split('.')[-1] for m in missing)}")

    # 允许明文 HTTP：开发期要连 http://192.168.x.x:8080 这种局域网后端。
    # 走 https 正式环境后建议把这一行去掉（改用 network_security_config 白名单）。
    if "usesCleartextTraffic" not in text and "<application" in text:
        text = re.sub(
            r"<application\b",
            '<application\n        android:usesCleartextTraffic="true"',
            text,
            count=1,
        )
        print("  已开启 usesCleartextTraffic（便于连 http 测试后端）")

    path.write_text(text, encoding="utf-8")
    return True


def patch_signing(android_dir: Path) -> bool:
    key_props = android_dir / "key.properties"
    gradle = android_dir / "app" / "build.gradle"
    if not key_props.exists():
        print("  没有 key.properties，保持默认（release 走 debug key，能装但上不了架）")
        return False
    if not gradle.exists():
        print(f"  ! 找不到 {gradle}，跳过签名配置")
        return False

    text = gradle.read_text(encoding="utf-8")
    if "signingConfigs.release" in text:
        print("  已存在 release 签名配置")
        return True

    loader = """
def keystoreProperties = new Properties()
def keystorePropertiesFile = rootProject.file('key.properties')
if (keystorePropertiesFile.exists()) {
    keystoreProperties.load(new FileInputStream(keystorePropertiesFile))
}
"""
    # 加载器放在 plugins 块之后
    m = re.search(r"^plugins\s*\{.*?^\}", text, re.S | re.M)
    insert_at = m.end() if m else 0
    text = text[:insert_at] + "\n" + loader + text[insert_at:]

    signing = """
    signingConfigs {
        release {
            keyAlias keystoreProperties['keyAlias']
            keyPassword keystoreProperties['keyPassword']
            storeFile keystoreProperties['storeFile'] ? file(keystoreProperties['storeFile']) : null
            storePassword keystoreProperties['storePassword']
        }
    }
"""
    m = re.search(r"^android\s*\{", text, re.M)
    if not m:
        print("  ! 没找到 android {} 块，跳过签名配置")
        return False
    text = text[: m.end()] + "\n" + signing + text[m.end() :]

    # 把模板里那句 "signingConfig = signingConfigs.debug" 换成正式签名
    text = text.replace(
        "signingConfig = signingConfigs.debug", "signingConfig = signingConfigs.release"
    )
    if "signingConfigs.release" not in text:
        m = re.search(r"(release\s*\{)", text)
        text = text[: m.end()] + "\n            signingConfig signingConfigs.release" + text[m.end():]

    gradle.write_text(text, encoding="utf-8")
    print("  已接入 release 正式签名")
    return True


def main() -> int:
    android_dir = Path(sys.argv[1] if len(sys.argv) > 1 else "app/android")
    print(f"准备 Android 壳：{android_dir}")
    patch_manifest(android_dir / "app" / "src" / "main" / "AndroidManifest.xml")
    patch_signing(android_dir)
    print("完成")
    return 0


if __name__ == "__main__":
    sys.exit(main())
