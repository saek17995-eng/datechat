import 'package:shared_preferences/shared_preferences.dart';

/// 后端地址的本地覆盖值。
///
/// 为什么需要它：打包时烧进 APK 的地址（`--dart-define=API_BASE_URL`）是编译期常量，
/// 换一次环境就得重新打一次包（本机走 CI 打一包要五六分钟）。而真机联调时，
/// 电脑的局域网 IP 会随 WiFi 变，测试机也可能换网段。
/// 所以把地址做成可在本机修改的持久化配置，改一次长期生效。
class ServerConfigStore {
  ServerConfigStore._(this._prefs);

  final SharedPreferences _prefs;

  static const String _kBaseUrl = 'server.api_base_url';

  static Future<ServerConfigStore> create() async {
    return ServerConfigStore._(await SharedPreferences.getInstance());
  }

  /// 用户改过的地址；null 表示用打包时的默认值。
  String? get baseUrlOverride => _prefs.getString(_kBaseUrl);

  /// 传 null 或空串表示恢复默认。
  Future<void> setBaseUrl(String? url) async {
    if (url == null || url.trim().isEmpty) {
      await _prefs.remove(_kBaseUrl);
      return;
    }
    await _prefs.setString(_kBaseUrl, normalizeBaseUrl(url));
  }

  /// 容错输入：
  ///   - 不写 scheme 的（192.168.1.10:8080）默认按 http 处理，真机上绝大多数是这个场景；
  ///   - 结尾多余的斜杠去掉，否则 Dio 会拼出 `//v1/xxx`，部分路由 404。
  static String normalizeBaseUrl(String input) {
    var url = input.trim();
    if (!url.startsWith('http://') && !url.startsWith('https://')) {
      url = 'http://$url';
    }
    while (url.endsWith('/')) {
      url = url.substring(0, url.length - 1);
    }
    return url;
  }
}
