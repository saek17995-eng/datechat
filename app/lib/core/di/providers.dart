import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../config/app_env.dart';
import '../network/dio_client.dart';
import '../storage/token_store.dart';

/// 本地存储，必须在 bootstrap 中通过 override 注入（因为它需要异步初始化）。
final tokenStoreProvider = Provider<TokenStore>(
  (ref) => throw UnimplementedError(
    'tokenStoreProvider 未注入：请确认 bootstrap 中使用了 overrideWithValue',
  ),
);

/// HTTP 客户端单例。
final apiClientProvider = Provider<DioClient>((ref) {
  return DioClient(
    env: AppEnv.current,
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
