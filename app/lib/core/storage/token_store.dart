import 'dart:math';

import 'package:shared_preferences/shared_preferences.dart';

/// 令牌与设备标识的本地存储。
///
/// 安全说明：
///   - P0 用 SharedPreferences，仅存放短期 access token；
///   - P1 上线前应换成 flutter_secure_storage（Keychain / Keystore），
///     这里保留统一读写接口，替换时不影响调用方。
class TokenStore {
  TokenStore._(this._prefs);

  final SharedPreferences _prefs;

  static const String _kAccessToken = 'auth.access_token';
  static const String _kRefreshToken = 'auth.refresh_token';
  static const String _kDeviceId = 'device.id';

  static Future<TokenStore> create() async {
    return TokenStore._(await SharedPreferences.getInstance());
  }

  String? get accessToken => _prefs.getString(_kAccessToken);

  String? get refreshToken => _prefs.getString(_kRefreshToken);

  bool get isLoggedIn => (accessToken ?? '').isNotEmpty;

  Future<void> saveTokens({
    required String accessToken,
    required String refreshToken,
  }) async {
    await _prefs.setString(_kAccessToken, accessToken);
    await _prefs.setString(_kRefreshToken, refreshToken);
  }

  Future<void> clear() async {
    await _prefs.remove(_kAccessToken);
    await _prefs.remove(_kRefreshToken);
  }

  /// 设备标识：首次启动生成并持久化，用于登录设备管理与风控。
  String get deviceId {
    final cached = _prefs.getString(_kDeviceId);
    if (cached != null && cached.isNotEmpty) {
      return cached;
    }
    final random = Random.secure();
    final suffix =
        List.generate(8, (_) => random.nextInt(16).toRadixString(16)).join();
    final generated = 'dev-${DateTime.now().millisecondsSinceEpoch}-$suffix';
    // 写入是异步的，这里不强等待，下次启动一定能读到。
    _prefs.setString(_kDeviceId, generated);
    return generated;
  }
}
