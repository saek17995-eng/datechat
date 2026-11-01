import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/di/providers.dart';
import '../../core/notification/notification_service.dart';
import '../../core/realtime/realtime_event.dart';
import '../../core/realtime/realtime_providers.dart';
import '../../core/router/app_router.dart';
import '../auth/auth_controller.dart';
import '../chat/chat_controller.dart';
import '../profile/profile_controller.dart';

/// 实时通道的两个副作用：连接生命周期 + 事件落地。
///
/// 之所以做成一个包裹组件而不是散在各页面里：
///   - 连接要跟着登录态走，且只能有一条（多处各自 connect 会互相抢）；
///   - 事件监听必须在 App 级常驻，否则用户不在消息页就收不到任何东西。
///
/// 这里只做"转发"：收到事件后刷新数据、弹通知、跳页面，
/// 业务判断（比如通话状态机）仍然在各自的 controller 里。
class RealtimeEffects extends ConsumerStatefulWidget {
  const RealtimeEffects({super.key, required this.child});

  final Widget child;

  @override
  ConsumerState<RealtimeEffects> createState() => _RealtimeEffectsState();
}

class _RealtimeEffectsState extends ConsumerState<RealtimeEffects>
    with WidgetsBindingObserver {
  StreamSubscription<RealtimeEvent>? _sub;
  bool _foreground = true;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    WidgetsBinding.instance.addPostFrameCallback((_) => _syncConnection());
    _sub = ref.read(wsClientProvider).events.listen(_onEvent);
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    _foreground = state == AppLifecycleState.resumed;
    // 回到前台：立刻对齐一次连接状态（后台期间可能已经断掉）。
    if (_foreground) _syncConnection();
  }

  @override
  // ConsumerState 的 build 只收 context（ref 是它的字段，不是在参数里传进来的）。
  Widget build(BuildContext context) {
    // 登录态变化：登录成功后连接，退出登录后断开。
    ref.listen<AuthState>(authControllerProvider, (_, __) => _syncConnection());
    // 用户在设置里改了后端地址：必须重连，否则还在连老地址。
    ref.listen<String>(apiBaseUrlProvider, (_, __) => _syncConnection());
    return widget.child;
  }

  void _syncConnection() {
    if (!mounted) return;
    final client = ref.read(wsClientProvider);
    if (ref.read(authControllerProvider).isAuthenticated) {
      client.connect();
    } else {
      client.disconnect();
    }
  }

  void _onEvent(RealtimeEvent event) {
    if (!mounted) return;
    switch (event.type) {
      case rtTypeNewMessage:
        _onNewMessage(event);
      case rtTypeCallInvite:
        unawaited(_onIncomingCall(event));
      default:
        // 其余类型（recall / read / call_update / signal）由各自的 controller 处理。
        break;
    }
  }

  void _onNewMessage(RealtimeEvent event) {
    final data = event.data;
    final from = asIntRt(data['from_uid']);
    if (from <= 0) return;

    // 无论要不要弹通知，会话列表都得刷新——否则小红点和预览会停在旧值。
    ref.invalidate(conversationsProvider);

    // 正在跟这个人聊天就不打扰：屏幕上的消息气泡已经是最好的提示。
    // 反过来，只要不在这个会话（哪怕 App 在前台），都弹通知——
    // 用户在"附近"页逛的时候，被人发消息是完全无感的。
    if (ref.read(activeChatPeerProvider) == from) return;

    final unread = ref.read(unreadTotalProvider.notifier);
    unread.state = unread.state + 1;
    if (_foreground && ref.read(activeChatPeerProvider) != null) {
      // 正在跟别人聊天：只更新小红点，不弹系统通知，避免连环弹窗。
      return;
    }
    final text = (data['text'] ?? '').toString();
    final nickname = (data['nickname'] ?? '').toString();
    unawaited(
      ref.read(notificationServiceProvider).showMessage(
            peerUid: from,
            title: nickname.isEmpty ? '新消息' : nickname,
            body: text.isEmpty ? '收到一条新消息' : text,
            payload: 'chat:$from',
          ),
    );
  }

  Future<void> _onIncomingCall(RealtimeEvent event) async {
    final data = event.data;
    final caller = asIntRt(data['caller_uid']);
    final callNo = (data['call_no'] ?? '').toString();
    if (caller <= 0 || callNo.isEmpty) return;
    final video = asIntRt(data['media_type']) == 2;

    // 昵称头像要单独拉一次：推送帧里不带这些（避免消息体膨胀），
    // 拉失败也不能拦住来电——宁可显示"对方"也不能不响铃。
    String name = '';
    String avatar = '';
    try {
      final user = await ref.read(profileApiProvider).userProfile(caller);
      name = user.nickname;
      avatar = user.avatar;
    } catch (_) {
      // 忽略：用兜底文案继续
    }
    if (!mounted) return;

    unawaited(ref.read(routerProvider).push(
      '/call',
      extra: <String, dynamic>{
        'uid': caller,
        'call_no': callNo,
        'name': name,
        'avatar': avatar,
        'video': video,
        'auto_accept': false,
      },
    ));

    await ref.read(notificationServiceProvider).showIncomingCall(
          peerUid: caller,
          title: name.isEmpty ? '来电' : '$name 来电',
          body: video ? '邀请你视频通话' : '邀请你语音通话',
          payload: 'call:$callNo',
        );
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _sub?.cancel();
    super.dispose();
  }
}
