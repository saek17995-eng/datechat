import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../di/providers.dart';
import '../storage/token_store.dart';
import 'ws_client.dart';

/// 实时通道客户端（全局单例）。
///
/// 地址从 [apiBaseUrlProvider] 取、令牌从 [TokenStore] 取，
/// 两者变化都会触发重连（见 [RealtimeGate]）。
final wsClientProvider = Provider<WsClient>((ref) {
  final client = WsClient(
    baseUrl: () => ref.read(apiBaseUrlProvider),
    token: () => ref.read(tokenStoreProvider).accessToken,
  );
  ref.onDispose(client.dispose);
  return client;
});

/// 当前正在聊天的对方 uid。
///
/// 由聊天页在进出时写入：收到这个人的消息时说明用户正盯着屏幕，
/// 弹系统通知就是骚扰；不在这个会话（或为 null）才需要通知。
final activeChatPeerProvider = StateProvider<int?>((ref) => null);

/// 未读消息总数（小红点）。
///
/// 只在"没有打开对应会话"时累加，离开会话或打开消息页后清零，
/// 由各页面自行调用 clearUnread。
final unreadTotalProvider = StateProvider<int>((ref) => 0);
