// ignore_for_file: deprecated_member_use

import 'dart:async';
import 'dart:io';

import 'package:flutter_local_notifications/flutter_local_notifications.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:tencent_cloud_chat_sdk/enum/V2TimSDKListener.dart';
import 'package:tencent_cloud_chat_sdk/enum/V2TimSimpleMsgListener.dart';
import 'package:tencent_cloud_chat_sdk/enum/log_level_enum.dart';
import 'package:tencent_cloud_chat_sdk/tencent_im_sdk_plugin.dart';

import '../auth/auth_controller.dart';
import '../auth/data/auth_models.dart';
import '../chat/chat_controller.dart';
import '../chat/data/chat_models.dart';
import '../calls/call_controller.dart';
import '../calls/data/call_models.dart';

/// 当前待处理来电。根路由监听它并打开接听页。
final incomingCallProvider = StateProvider<CallSession?>((ref) => null);

/// 单例通知与即时通信服务。
final communicationServiceProvider = Provider<CommunicationService>((ref) {
  final service = CommunicationService(ref);
  ref.onDispose(service.dispose);
  return service;
});

/// 把登录态绑定到单例服务；根组件 watch 一次即可。
final communicationBindingProvider = Provider<void>((ref) {
  final auth = ref.watch(authControllerProvider);
  final service = ref.watch(communicationServiceProvider);
  // 通知和厂商 IM 都依赖原生插件。部分定制 Android 系统可能拒绝
  // 权限页面或初始化调用；启动路径必须吞掉这类异常，不能让 App 退出。
  unawaited(service.updateAuth(auth).catchError((_) {}));
});

class CommunicationService {
  CommunicationService(this._ref);

  final Ref _ref;
  final FlutterLocalNotificationsPlugin _notifications =
      FlutterLocalNotificationsPlugin();
  final Map<int, int> _lastMessageByConversation = {};

  Timer? _pollTimer;
  Timer? _callTimer;
  int _callUserId = 0;
  bool _notificationReady = false;
  bool _notificationAttempted = false;
  bool _polling = false;
  bool _nativeImReady = false;
  String _activeUserId = '';
  String? _simpleListenerId;

  Future<void> updateAuth(AuthState auth) async {
    await _ensureNotifications();
    if (!auth.isAuthenticated) {
      await _stopMessaging();
      return;
    }

    _startCallPolling(auth.me?.uid ?? 0);

    final credential = auth.im;
    if (credential.usable) {
      final connected = await _connectNativeIm(credential);
      if (connected) {
        _pollTimer?.cancel();
        _pollTimer = null;
        return;
      }
    }
    // 腾讯 IM 尚未配置或临时登录失败时，继续用 REST 轮询；至少在 App
    // 进程存活期间仍能及时给出系统通知。
    _startPolling();
  }

  Future<void> _ensureNotifications() async {
    if (_notificationReady || _notificationAttempted) return;
    _notificationAttempted = true;
    try {
      const settings = InitializationSettings(
        android: AndroidInitializationSettings('@mipmap/ic_launcher'),
      );
      await _notifications.initialize(settings: settings);
      if (Platform.isAndroid) {
        await _notifications
            .resolvePlatformSpecificImplementation<
                AndroidFlutterLocalNotificationsPlugin>()
            ?.requestNotificationsPermission();
      }
      _notificationReady = true;
    } catch (_) {
      // 通知能力不可用不应阻止登录与主界面启动。尤其不要在冷启动时调用
      // requestFullScreenIntentPermission：少数厂商 ROM 没有对应设置页，
      // 原生 ActivityNotFoundException 会直接导致闪退。
    }
  }

  Future<bool> _connectNativeIm(ImCredential credential) async {
    if (_nativeImReady && _activeUserId == credential.userId) return true;
    if (_activeUserId.isNotEmpty && _activeUserId != credential.userId) {
      await _disconnectNativeIm();
    }
    try {
      final manager = TencentImSDKPlugin.v2TIMManager;
      final initialized = await manager.initSDK(
        sdkAppID: credential.sdkAppId,
        loglevel: LogLevelEnum.V2TIM_LOG_INFO,
        listener: V2TimSDKListener(
          onUserSigExpired: () {
            _nativeImReady = false;
            _startPolling();
          },
          onKickedOffline: () {
            _nativeImReady = false;
            _startPolling();
          },
        ),
        showImLog: false,
      );
      if (initialized.code != 0) return false;

      final listener = V2TimSimpleMsgListener(
        onRecvC2CTextMessage: (messageId, sender, text) {
          final nickname = (sender.nickName ?? '').trim();
          final userId = sender.userID;
          unawaited(
            _showMessage(
              id: messageId.hashCode,
              title: nickname.isEmpty ? '收到新消息' : nickname,
              body: text,
              payload: 'chat:$userId',
            ),
          );
          _ref.invalidate(conversationsProvider);
        },
      );
      _simpleListenerId =
          await manager.addSimpleMsgListener(listener: listener);
      final login = await manager.login(
        userID: credential.userId,
        userSig: credential.userSig,
      );
      if (login.code != 0) {
        await manager.removeSimpleMsgListener(uuid: _simpleListenerId);
        _simpleListenerId = null;
        return false;
      }
      _activeUserId = credential.userId;
      _nativeImReady = true;
      return true;
    } catch (_) {
      final listenerId = _simpleListenerId;
      if (listenerId != null) {
        try {
          await TencentImSDKPlugin.v2TIMManager
              .removeSimpleMsgListener(uuid: listenerId);
        } catch (_) {
          // 原始异常已说明 IM 不可用，清理失败不应覆盖它。
        }
        _simpleListenerId = null;
      }
      _nativeImReady = false;
      return false;
    }
  }

  void _startPolling() {
    if (_pollTimer != null) return;
    unawaited(_pollConversations(initial: true));
    _pollTimer = Timer.periodic(
      const Duration(seconds: 10),
      (_) => unawaited(_pollConversations()),
    );
  }

  void _startCallPolling(int myUid) {
    if (myUid <= 0) return;
    if (_callTimer != null && _callUserId == myUid) return;
    _callTimer?.cancel();
    _callUserId = myUid;
    unawaited(_pollCalls(myUid));
    _callTimer = Timer.periodic(
      const Duration(seconds: 2),
      (_) => unawaited(_pollCalls(myUid)),
    );
  }

  Future<void> _pollCalls(int myUid) async {
    try {
      final calls = await _ref.read(callApiProvider).list(size: 20);
      CallSession? incoming;
      for (final call in calls) {
        if (call.calleeId == myUid && call.status == callStatusRinging) {
          incoming = call;
          break;
        }
      }
      final current = _ref.read(incomingCallProvider);
      if (incoming == null) {
        if (current != null) {
          _ref.read(incomingCallProvider.notifier).state = null;
        }
        return;
      }
      if (current?.callNo == incoming.callNo) return;
      _ref.read(incomingCallProvider.notifier).state = incoming;
      await _showIncomingCall(incoming);
      _ref.invalidate(callRecordsProvider);
    } catch (_) {
      // 短暂断网不清掉已经展示的来电，服务端超时巡检会结束它。
    }
  }

  Future<void> _pollConversations({bool initial = false}) async {
    if (_polling) return;
    _polling = true;
    try {
      final page = await _ref.read(chatApiProvider).conversations(size: 100);
      for (final conversation in page.list) {
        final previous = _lastMessageByConversation[conversation.convId];
        _lastMessageByConversation[conversation.convId] =
            conversation.lastMsgId;
        if (initial ||
            previous == null ||
            conversation.lastMsgId <= previous ||
            conversation.unreadCount <= 0 ||
            conversation.muted) {
          continue;
        }
        await _notifyConversation(conversation);
      }
      _ref.invalidate(conversationsProvider);
    } catch (_) {
      // 网络不可用时保持上一份游标，下次恢复后仍可识别新消息。
    } finally {
      _polling = false;
    }
  }

  Future<void> _notifyConversation(Conversation conversation) {
    return _showMessage(
      id: conversation.convId,
      title: conversation.peer.nickname.isEmpty
          ? '收到新消息'
          : conversation.peer.nickname,
      body: conversation.lastPreview.isEmpty
          ? '你收到了一条新消息'
          : conversation.lastPreview,
      payload: 'chat:${conversation.peer.uid}',
    );
  }

  Future<void> _showMessage({
    required int id,
    required String title,
    required String body,
    required String payload,
  }) async {
    if (!_notificationReady) await _ensureNotifications();
    if (!_notificationReady) return;
    const android = AndroidNotificationDetails(
      'datechat_messages',
      '聊天消息',
      channelDescription: '新聊天消息提醒',
      importance: Importance.max,
      priority: Priority.high,
      playSound: true,
      enableVibration: true,
      category: AndroidNotificationCategory.message,
    );
    await _notifications.show(
      id: id & 0x7fffffff,
      title: title,
      body: body,
      notificationDetails: const NotificationDetails(android: android),
      payload: payload,
    );
  }

  Future<void> _showIncomingCall(CallSession call) async {
    if (!_notificationReady) await _ensureNotifications();
    if (!_notificationReady) return;
    const android = AndroidNotificationDetails(
      'datechat_calls',
      '语音和视频来电',
      channelDescription: '新的语音或视频通话邀请',
      importance: Importance.max,
      priority: Priority.max,
      playSound: true,
      enableVibration: true,
      ongoing: true,
      category: AndroidNotificationCategory.call,
      fullScreenIntent: true,
    );
    await _notifications.show(
      id: call.callNo.hashCode & 0x7fffffff,
      title: call.isVideo ? '视频来电' : '语音来电',
      body: '有人邀请你进行${call.isVideo ? '视频' : '语音'}通话',
      notificationDetails: const NotificationDetails(android: android),
      payload: 'call:${call.callNo}',
    );
  }

  Future<void> _stopMessaging() async {
    _pollTimer?.cancel();
    _pollTimer = null;
    _callTimer?.cancel();
    _callTimer = null;
    _callUserId = 0;
    _lastMessageByConversation.clear();
    await _disconnectNativeIm();
  }

  Future<void> _disconnectNativeIm() async {
    if (_activeUserId.isEmpty && !_nativeImReady) return;
    try {
      final manager = TencentImSDKPlugin.v2TIMManager;
      if (_simpleListenerId != null) {
        await manager.removeSimpleMsgListener(uuid: _simpleListenerId);
      }
      await manager.logout();
    } catch (_) {
      // 退出登录不能因为 SDK 清理失败而阻塞。
    }
    _simpleListenerId = null;
    _activeUserId = '';
    _nativeImReady = false;
  }

  void dispose() {
    _pollTimer?.cancel();
    _callTimer?.cancel();
    unawaited(_disconnectNativeIm());
  }
}
