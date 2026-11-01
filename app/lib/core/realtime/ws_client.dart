import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/foundation.dart';

import 'realtime_event.dart';

/// WebSocket 客户端。
///
/// 三条硬性约束，写代码时必须记得：
///  1. **令牌走 Authorization 头而不是 query**：query 会被服务端的访问日志
///     原样记下来，等于把令牌写进日志文件；
///  2. **断线必须自动重连且带退避**：移动网络切 WiFi、进电梯、锁屏都会断，
///     重连间隔取 min(30s, 2^n)，既不会把服务端打爆，也不会让用户等太久；
///  3. **重连后要能补数据**：推送帧可能正好在断线期间发出，客户端重连成功
///     后应主动拉一次会话列表，而不是假设"没收到就是没有新消息"。
class WsClient {
  WsClient({
    required String Function() baseUrl,
    required String? Function() token,
    this.heartbeat = const Duration(seconds: 25),
  })  : _baseUrl = baseUrl,
        _token = token;

  final String Function() _baseUrl;
  final String? Function() _token;
  final Duration heartbeat;

  WebSocket? _socket;
  StreamSubscription<dynamic>? _sub;
  Timer? _heartbeatTimer;
  Timer? _retryTimer;
  int _attempt = 0;
  bool _disposed = false;
  bool _wantConnected = false;
  bool _opening = false;

  final StreamController<RealtimeEvent> _events =
      StreamController<RealtimeEvent>.broadcast();
  final ValueNotifier<RealtimeStatus> status =
      ValueNotifier(RealtimeStatus.disconnected);
  final ValueNotifier<String?> lastError = ValueNotifier(null);

  /// 广播流：多个页面可以同时监听（通话页关心信令，外壳关心消息与来电）。
  Stream<RealtimeEvent> get events => _events.stream;

  bool get isConnected => status.value == RealtimeStatus.connected;

  /// 请求连接。重复调用安全；令牌为空时直接放弃（未登录）。
  void connect() {
    _wantConnected = true;
    unawaited(_open());
  }

  /// 主动断开（退出登录）。不会再自动重连。
  void disconnect() {
    _wantConnected = false;
    _attempt = 0;
    _retryTimer?.cancel();
    _retryTimer = null;
    _closeSocket();
    _setStatus(RealtimeStatus.disconnected);
  }

  /// 换后端地址或换账号后重连。
  Future<void> reconnect() async {
    _attempt = 0;
    _closeSocket();
    _wantConnected = true;
    await _open();
  }

  /// 发送一帧。未连接时静默丢弃——调用方本来就是"尽力而为"。
  void send(Map<String, dynamic> frame) {
    final socket = _socket;
    if (socket == null || socket.readyState != WebSocket.open) return;
    socket.add(jsonEncode(frame));
  }

  /// 转发通话信令给指定用户。
  void sendSignal({
    required int to,
    required String callNo,
    required String kind,
    Map<String, dynamic> payload = const <String, dynamic>{},
  }) {
    if (to <= 0) return;
    send(<String, dynamic>{
      'type': rtTypeSignal,
      'to': to,
      'call_no': callNo,
      'kind': kind,
      'payload': payload,
    });
  }

  void sendPing() => send(<String, dynamic>{'type': rtTypePing});

  Uri _uri() {
    final base = Uri.parse(_baseUrl());
    return Uri(
      scheme: base.scheme == 'https' ? 'wss' : 'ws',
      host: base.host,
      port: base.port,
      path: '/v1/ws',
    );
  }

  Future<void> _open() async {
    if (_disposed || !_wantConnected || _opening) return;
    final token = _token();
    if (token == null || token.isEmpty) {
      _setStatus(RealtimeStatus.disconnected);
      return;
    }
    _opening = true;
    _setStatus(_attempt == 0
        ? RealtimeStatus.connecting
        : RealtimeStatus.reconnecting);
    try {
      final socket = await WebSocket.connect(
        _uri().toString(),
        headers: <String, dynamic>{'Authorization': 'Bearer $token'},
      ).timeout(const Duration(seconds: 10));
      if (_disposed || !_wantConnected) {
        socket.close();
        return;
      }
      _socket = socket;
      _attempt = 0;
      _setStatus(RealtimeStatus.connected);
      lastError.value = null;
      // 协议层 ping：比应用层心跳更早发现死连接。
      socket.pingInterval = const Duration(seconds: 30);
      _sub = socket.listen(
        _onData,
        onError: _onError,
        onDone: _onDone,
        cancelOnError: false,
      );
      _startHeartbeat();
    } catch (error) {
      lastError.value = '$error';
      _scheduleRetry();
    } finally {
      _opening = false;
    }
  }

  void _onData(dynamic raw) {
    if (raw is! String) return;
    try {
      final decoded = jsonDecode(raw);
      if (decoded is! Map<String, dynamic>) return;
      final event = RealtimeEvent.fromJson(decoded);
      // hello / pong 只用于保活与对时，不必打扰业务层。
      if (event.type == rtTypePong) return;
      if (!_events.isClosed) _events.add(event);
    } catch (_) {
      // 单帧解析失败不能让整条连接挂掉。
    }
  }

  void _onError(Object error) {
    lastError.value = '$error';
  }

  void _onDone() {
    _closeSocket();
    _scheduleRetry();
  }

  void _scheduleRetry() {
    if (_disposed || !_wantConnected) return;
    _retryTimer?.cancel();
    _attempt++;
    final seconds = 1 << (_attempt > 5 ? 5 : _attempt); // 2,4,8,16,32 => 封顶 32s
    _setStatus(RealtimeStatus.reconnecting);
    _retryTimer = Timer(Duration(seconds: seconds), () {
      unawaited(_open());
    });
  }

  void _startHeartbeat() {
    _heartbeatTimer?.cancel();
    _heartbeatTimer = Timer.periodic(heartbeat, (_) => sendPing());
  }

  void _closeSocket() {
    _heartbeatTimer?.cancel();
    _heartbeatTimer = null;
    _sub?.cancel();
    _sub = null;
    final socket = _socket;
    _socket = null;
    if (socket != null) {
      try {
        socket.close(WebSocketStatus.normalClosure, 'client close');
      } catch (_) {
        // 已经断开，忽略
      }
    }
  }

  void _setStatus(RealtimeStatus value) {
    if (status.value != value) status.value = value;
  }

  void dispose() {
    if (_disposed) return;
    _disposed = true;
    _wantConnected = false;
    _retryTimer?.cancel();
    _closeSocket();
    _setStatus(RealtimeStatus.disconnected);
    unawaited(_events.close());
    status.dispose();
    lastError.dispose();
  }
}
