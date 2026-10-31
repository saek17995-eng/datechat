import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../config/app_env.dart';
import '../network/dio_client.dart';
import '../storage/server_config_store.dart';
import '../storage/token_store.dart';

/// 本地存储，必须在 bootstrap 中通过 override 注入（因为它需要异步初始化）。
final tokenStoreProvider = Provider<TokenStore>(
  (ref) => throw UnimplementedError(
    'tokenStoreProvider 未注入：请确认 bootstrap 中使用了 overrideWithValue',
  ),
);

/// 后端地址的本地覆盖存储，同样由 bootstrap 注入。
final serverConfigStoreProvider = Provider<ServerConfigStore>(
  (ref) => throw UnimplementedError(
    'serverConfigStoreProvider 未注入：请确认 bootstrap 中使用了 overrideWithValue',
  ),
);

/// 当前生效的后端地址。
///
/// 打包值（`--dart-define=API_BASE_URL`）是编译期常量，换环境只能重打包；
/// 这里以本地覆盖值优先，改完地址整个 DioClient 会重建，业务代码无感。
final apiBaseUrlProvider =
    StateNotifierProvider<ApiBaseUrlController, String>((ref) {
  return ApiBaseUrlController(ref.watch(serverConfigStoreProvider));
});

class ApiBaseUrlController extends StateNotifier<String> {
  ApiBaseUrlController(this._store)
      : super(_store.baseUrlOverride ?? AppEnv.builtInBaseUrl) {
    AppEnv.current = AppEnv.current.copyWith(apiBaseUrl: state);
  }

  final ServerConfigStore _store;

  /// 是否用了用户自己填的地址（页面据此显示"已自定义"）。
  bool get usingOverride => _store.baseUrlOverride != null;

  Future<String> set(String url) async {
    await _store.setBaseUrl(url);
    state = _store.baseUrlOverride ?? AppEnv.builtInBaseUrl;
    AppEnv.current = AppEnv.current.copyWith(apiBaseUrl: state);
    return state;
  }

  Future<String> reset() async {
    await _store.setBaseUrl(null);
    state = AppEnv.builtInBaseUrl;
    AppEnv.current = AppEnv.current.copyWith(apiBaseUrl: state);
    return state;
  }
}

/// HTTP 客户端。地址变化时自动重建（watch 了 apiBaseUrlProvider）。
final apiClientProvider = Provider<DioClient>((ref) {
  final baseUrl = ref.watch(apiBaseUrlProvider);
  return DioClient(
    env: AppEnv.current.copyWith(apiBaseUrl: baseUrl),
    tokenStore: ref.watch(tokenStoreProvider),
  );
});

/// 后端连通性探测，用于 P0 自检：首页会展示它，失败时可下拉重试。
final serverInfoProvider = FutureProvider.autoDispose<Map<String, dynamic>>(
  (ref) async {
    final client = ref.watch(apiClientProvider);
    return client.get('/v1/ping');
  },
);
