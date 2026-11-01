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

构建脚本可能是 Groovy（build.gradle）也可能是 Kotlin DSL（build.gradle.kts）：
Flutter 3.29 之后的新模板已全面切到 .kts，两种都要支持。

用法：
    python3 scripts/android/prepare_apk.py [android_dir]      # 默认 app/android
"""

import re
import sys
from pathlib import Path

# Android 6.0+ 的危险权限（位置/相机/麦克风）除了在这里声明，还必须在运行时动态申请。
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
    # Android 13（API 33）起通知是运行时权限：manifest 里不声明，
    # 系统压根不会弹授权框，新消息推送就永远静音。
    "android.permission.POST_NOTIFICATIONS",
    # 锁屏弹来电界面（fullScreenIntent）在 Android 14 上需要它，
    # 否则来电只会有一条普通通知，用户很容易漏接。
    "android.permission.USE_FULL_SCREEN_INTENT",
    # 通话中要防止系统休眠掐断音频：flutter_webrtc 的前台服务也依赖它。
    "android.permission.FOREGROUND_SERVICE",
    "android.permission.WAKE_LOCK",
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
        print(f"  补 {len(missing)} 项权限：{', '.join(p.split('.')[-1] for p in missing)}")

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


def _patch_signing_kts(path: Path) -> bool:
    """Kotlin DSL（Flutter 3.29+ 的默认模板）。"""
    text = path.read_text(encoding="utf-8")
    if 'create("release")' in text:
        print("  已存在 release 签名配置")
        return True

    # import 必须写在文件最前面
    if "import java.util.Properties" not in text:
        text = "import java.io.FileInputStream\nimport java.util.Properties\n\n" + text

    loader = """
val keystoreProperties = Properties()
val keystorePropertiesFile = rootProject.file("key.properties")
if (keystorePropertiesFile.exists()) {
    keystoreProperties.load(FileInputStream(keystorePropertiesFile))
}
"""
    m = re.search(r"^plugins\s*\{.*?^\}", text, re.S | re.M)
    at = m.end() if m else 0
    text = text[:at] + "\n" + loader + text[at:]

    signing = """
    signingConfigs {
        create("release") {
            storeFile = keystoreProperties["storeFile"]?.let { file(it as String) }
            storePassword = keystoreProperties["storePassword"] as String
            keyAlias = keystoreProperties["keyAlias"] as String
            keyPassword = keystoreProperties["keyPassword"] as String
        }
    }
"""
    m = re.search(r"^android\s*\{", text, re.M)
    if not m:
        print("  ! 没找到 android {} 块，跳过签名配置")
        return False
    text = text[: m.end()] + "\n" + signing + text[m.end() :]

    if 'signingConfigs.getByName("debug")' in text:
        text = text.replace(
            'signingConfigs.getByName("debug")', 'signingConfigs.getByName("release")'
        )
    else:
        # 模板变了就硬塞一行到 release 块里，并在最后自检
        m = re.search(r"(release\s*\{)", text)
        if m:
            text = (
                text[: m.end()]
                + '\n            signingConfig = signingConfigs.getByName("release")'
                + text[m.end() :]
            )

    path.write_text(text, encoding="utf-8")
    ok = 'create("release")' in text and 'getByName("release")' in text
    print("  已接入 release 正式签名" if ok else "  ! 签名配置写入后自检未通过")
    return ok


def _patch_signing_groovy(path: Path) -> bool:
    """Groovy（老版本 Flutter 模板）。"""
    text = path.read_text(encoding="utf-8")
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
    m = re.search(r"^plugins\s*\{.*?^\}", text, re.S | re.M)
    at = m.end() if m else 0
    text = text[:at] + "\n" + loader + text[at:]

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

    if "signingConfigs.debug" in text:
        text = text.replace("signingConfigs.debug", "signingConfigs.release")
    else:
        m = re.search(r"(release\s*\{)", text)
        if m:
            text = (
                text[: m.end()]
                + "\n            signingConfig signingConfigs.release"
                + text[m.end() :]
            )

    path.write_text(text, encoding="utf-8")
    ok = "signingConfigs.release" in text
    print("  已接入 release 正式签名" if ok else "  ! 签名配置写入后自检未通过")
    return ok


def patch_signing(android_dir: Path) -> bool:
    if not (android_dir / "key.properties").exists():
        print("  没有 key.properties，保持默认（release 走 debug key，能装但上不了架）")
        return False

    kts = android_dir / "app" / "build.gradle.kts"
    groovy = android_dir / "app" / "build.gradle"
    if kts.exists():
        return _patch_signing_kts(kts)
    if groovy.exists():
        return _patch_signing_groovy(groovy)

    listing = (
        sorted(p.name for p in (android_dir / "app").iterdir())
        if (android_dir / "app").exists()
        else []
    )
    print(f"  ! 没找到构建脚本；android/app 下有：{', '.join(listing) or '(空)'}")
    return False


def patch_min_sdk(android_dir: Path) -> bool:
    """把 minSdk 抬到 24。

    flutter_webrtc 与全屏来电通知都需要 API 24+ 的部分能力，
    而模板在个别版本里仍是 21；留着 21 会在安装后运行时崩，
    且报错信息完全看不出是 minSdk 的锅。
    """
    for name in ("build.gradle.kts", "build.gradle"):
        path = android_dir / "app" / name
        if not path.exists():
            continue
        text = path.read_text(encoding="utf-8")

        def bump(match: "re.Match[str]") -> str:
            value = int(match.group(2))
            return match.group(1) + str(max(24, value))

        new = re.sub(r"(minSdk\s*=\s*)(\d+)", bump, text)
        new = re.sub(r"(minSdkVersion\s+)(\d+)", bump, new)
        if new != text:
            path.write_text(new, encoding="utf-8")
            print("  已将 minSdk 抬到 24（WebRTC 与全屏来电需要）")
        else:
            print("  minSdk 已满足要求")
        return True
    print("  ! 没找到构建脚本，跳过 minSdk 补丁")
    return False


DESUGAR_KTS = '    coreLibraryDesugaring("com.android.tools:desugar_jdk_libs:2.1.4")'
DESUGAR_GROOVY = "    coreLibraryDesugaring 'com.android.tools:desugar_jdk_libs:2.1.4'"


def patch_desugaring(android_dir: Path) -> bool:
    """开启 core library desugaring。

    flutter_local_notifications 用到了 java.time，在低版本 Android 上靠脱糖支持。
    不开的话 Gradle 在 checkReleaseAarMetadata 阶段直接失败，而且报错只说
    "requires core library desugaring"，不看这段根本不知道该改哪儿。
    """
    for name, flag, dep in (
        ("build.gradle.kts", "        isCoreLibraryDesugaringEnabled = true", DESUGAR_KTS),
        ("build.gradle", "        coreLibraryDesugaringEnabled true", DESUGAR_GROOVY),
    ):
        path = android_dir / "app" / name
        if not path.exists():
            continue
        text = path.read_text(encoding="utf-8")
        if "isCoreLibraryDesugaringEnabled" in text or "coreLibraryDesugaringEnabled" in text:
            print("  已开启 core library desugaring")
            return True

        block = "compileOptions {"
        idx = text.find(block)
        if idx >= 0:
            at = idx + len(block)
            text = text[:at] + "\n" + flag + text[at:]
        else:
            m = re.search(r"^android\s*\{", text, re.M)
            if not m:
                print("  ! 没找到 android {} 块，跳过 desugaring 补丁")
                return False
            text = (
                text[: m.end()]
                + "\n    compileOptions {\n"
                + flag
                + "\n    }"
                + text[m.end() :]
            )

        # 依赖块单独追加：Gradle 允许多个 dependencies {}，比往已有块里插更安全。
        if "desugar_jdk_libs" not in text:
            text = text.rstrip() + "\n\ndependencies {\n" + dep + "\n}\n"
        path.write_text(text, encoding="utf-8")
        print("  已开启 core library desugaring（通知插件需要 java.time）")
        return True
    print("  ! 没找到构建脚本，跳过 desugaring 补丁")
    return False


def main() -> int:
    android_dir = Path(sys.argv[1] if len(sys.argv) > 1 else "app/android")
    print(f"准备 Android 壳：{android_dir}")
    patch_manifest(android_dir / "app" / "src" / "main" / "AndroidManifest.xml")
    patch_min_sdk(android_dir)
    patch_desugaring(android_dir)
    patch_signing(android_dir)
    print("完成")
    return 0


if __name__ == "__main__":
    sys.exit(main())
