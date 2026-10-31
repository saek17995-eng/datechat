import 'package:flutter/foundation.dart';

/// 环境标识。
enum AppFlavor { dev, prod }

/// 编译期 + 运行期环境配置。
///
/// 推荐用 `--dart-define` 注入，避免把不同环境的地址写死在代码里：
///   flutter run --dart-define=API_BASE_URL=http://192.168.1.10:8080
class AppEnv {
  const AppEnv({
    required this.flavor,
    required this.apiBaseUrl,
    required this.imSdkAppId,
    required this.trtcSdkAppId,
  });

  final AppFlavor flavor;
  final String apiBaseUrl;

  /// 腾讯 IM SDKAppID，0 表示未配置（P3 接入时从后端下发）。
  final int imSdkAppId;

  /// 腾讯 TRTC SDKAppID，0 表示未配置（P6 接入时从后端下发）。
  final int trtcSdkAppId;

  bool get isDev => flavor == AppFlavor.dev;

  static const AppEnv _dev = AppEnv(
    flavor: AppFlavor.dev,
    apiBaseUrl: String.fromEnvironment(
      'API_BASE_URL',
      defaultValue: 'http://127.0.0.1:8080',
    ),
    imSdkAppId: 0,
    trtcSdkAppId: 0,
  );

  static const AppEnv _prod = AppEnv(
    flavor: AppFlavor.prod,
    apiBaseUrl: String.fromEnvironment(
      'API_BASE_URL',
      defaultValue: 'https://api.datechat.example',
    ),
    imSdkAppId: 0,
    trtcSdkAppId: 0,
  );

  /// 当前生效环境，由 main/bootstrap 在启动时初始化。
  static AppEnv current = _dev;

  static void init() {
    current = kReleaseMode ? _prod : _dev;
  }
}
