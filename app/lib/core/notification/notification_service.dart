import 'package:flutter_local_notifications/flutter_local_notifications.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// 本地通知。
///
/// 为什么是本地通知而不是厂商推送：App 被完全杀掉时系统推送仍需要 FCM /
/// 厂商通道（得接 SDK + 配密钥）。这里的取舍是——App 在前台或后台时，
/// 服务端通过自建 WebSocket 把事件推过来，客户端就地弹通知，
/// 覆盖"正在用手机但不在聊天页"这个最高频的场景，且零第三方依赖。
///
/// 两条通知渠道分开：消息用普通提醒，来电用 fullScreenIntent
/// （Android 上锁屏也能直接弹出接听界面，这是通话类 App 的底线体验）。
class NotificationService {
  final FlutterLocalNotificationsPlugin _plugin =
      FlutterLocalNotificationsPlugin();

  bool _initialized = false;
  bool _initializing = false;

  /// 通知被点击时的回调，由外层注入（跳转会话 / 回拨）。
  void Function(String? payload)? onTap;

  static const AndroidNotificationChannel messageChannel =
      AndroidNotificationChannel(
    'chat_messages',
    '新消息',
    description: '收到新消息时提醒',
    importance: Importance.high,
  );

  static const AndroidNotificationChannel callChannel =
      AndroidNotificationChannel(
    'incoming_calls',
    '来电',
    description: '收到语音/视频通话邀请',
    importance: Importance.max,
  );

  Future<void> init() async {
    if (_initialized || _initializing) return;
    _initializing = true;
    try {
      const android = AndroidInitializationSettings('@mipmap/ic_launcher');
      const ios = DarwinInitializationSettings(
        requestAlertPermission: true,
        requestBadgePermission: true,
        requestSoundPermission: true,
      );
      await _plugin.initialize(
        // v22 起 initialize / show / cancel 全部改成命名参数（旧的位置参数写法会编译失败）。
        settings: const InitializationSettings(android: android, iOS: ios),
        onDidReceiveNotificationResponse: (response) => onTap?.call(response.payload),
      );

      final androidImpl = _plugin.resolvePlatformSpecificImplementation<
          AndroidFlutterLocalNotificationsPlugin>();
      await androidImpl?.createNotificationChannel(messageChannel);
      await androidImpl?.createNotificationChannel(callChannel);
      // Android 13+ 的 POST_NOTIFICATIONS 是运行时权限，不申请则完全静音。
      if (await androidImpl?.areNotificationsEnabled() != true) {
        await androidImpl?.requestNotificationsPermission();
      }
      _initialized = true;
    } finally {
      // EMUI/HarmonyOS 兼容层偶发初始化失败时，下一条消息还可以重试。
      _initializing = false;
    }
  }

  Future<bool> _ensureReady() async {
    if (_initialized) return true;
    try {
      await init();
    } catch (_) {
      return false;
    }
    return _initialized;
  }

  /// 新消息。同一个会话复用同一条通知 id，避免刷屏。
  Future<void> showMessage({
    required int peerUid,
    required String title,
    required String body,
    String? payload,
  }) async {
    if (!await _ensureReady()) return;
    await _plugin.show(
      id: peerUid,
      title: title,
      body: body,
      notificationDetails: NotificationDetails(
        android: AndroidNotificationDetails(
          messageChannel.id,
          messageChannel.name,
          channelDescription: messageChannel.description,
          importance: Importance.high,
          priority: Priority.high,
          category: AndroidNotificationCategory.message,
          showWhen: true,
        ),
        iOS: const DarwinNotificationDetails(),
      ),
      payload: payload,
    );
  }

  /// 来电。fullScreenIntent 让锁屏也能弹出接听页。
  Future<void> showIncomingCall({
    required int peerUid,
    required String title,
    required String body,
    String? payload,
  }) async {
    if (!await _ensureReady()) return;
    await _plugin.show(
      id: 1000000 + peerUid,
      title: title,
      body: body,
      notificationDetails: NotificationDetails(
        android: AndroidNotificationDetails(
          callChannel.id,
          callChannel.name,
          channelDescription: callChannel.description,
          importance: Importance.max,
          priority: Priority.max,
          category: AndroidNotificationCategory.call,
          fullScreenIntent: true,
          ongoing: true,
          timeoutAfter: 45000,
        ),
        iOS: const DarwinNotificationDetails(
          presentAlert: true,
          presentSound: true,
        ),
      ),
      payload: payload,
    );
  }

  Future<void> cancel(int id) async {
    if (!_initialized) return;
    await _plugin.cancel(id: id);
  }

  Future<void> cancelAll() async {
    if (!_initialized) return;
    await _plugin.cancelAll();
  }
}

/// 通知服务实例，由 bootstrap 初始化后注入（它需要异步 init，
/// 和 TokenStore 一样不适合在 provider 里现造）。
final notificationServiceProvider = Provider<NotificationService>(
  (ref) => throw UnimplementedError(
    'notificationServiceProvider 未注入：请确认 bootstrap 中使用了 overrideWithValue',
  ),
);
