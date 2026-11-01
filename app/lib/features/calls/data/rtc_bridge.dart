import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:permission_handler/permission_handler.dart';
import 'package:tencent_rtc_sdk/trtc_cloud.dart';
import 'package:tencent_rtc_sdk/trtc_cloud_def.dart';
import 'package:tencent_rtc_sdk/trtc_cloud_listener.dart';
import 'package:tencent_rtc_sdk/tx_device_manager.dart';

import 'call_models.dart';

/// 音视频引擎桥接层。
///
/// 业务层仍只认识服务端签发的 [CallTicket]，腾讯 SDK 的生命周期、权限、
/// 音频路由和远端画面都封装在这里，避免计费状态机依赖厂商 API。
abstract class RtcBridge extends ChangeNotifier {
  bool get available;
  bool get initialized;
  bool get isVideo;
  String get hint;
  String? get lastError;
  List<String> get remoteVideoUsers;

  Future<void> enterRoom(CallTicket ticket);
  Future<void> leaveRoom();
  Future<void> setMicMuted(bool muted);
  Future<void> setSpeakerOn(bool on);
  void startLocalPreview(int viewId);
  void startRemoteView(String userId, int viewId);

  factory RtcBridge.fromTicket(
    CallTicket ticket, {
    required bool video,
  }) {
    if (ticket.mock) return MockRtcBridge(video: video);
    return TencentRtcBridge(video: video);
  }
}

class MockRtcBridge extends RtcBridge {
  MockRtcBridge({required this.isVideo});

  @override
  final bool isVideo;

  @override
  bool get available => false;

  @override
  bool get initialized => false;

  @override
  String get hint => '服务端仍在模拟模式，请配置 TRTC SDKAppID 与密钥';

  @override
  String? get lastError => null;

  @override
  List<String> get remoteVideoUsers => const [];

  @override
  Future<void> enterRoom(CallTicket ticket) async {}

  @override
  Future<void> leaveRoom() async {}

  @override
  Future<void> setMicMuted(bool muted) async {}

  @override
  Future<void> setSpeakerOn(bool on) async {}

  @override
  void startLocalPreview(int viewId) {}

  @override
  void startRemoteView(String userId, int viewId) {}
}

/// 使用腾讯云 TRTC 原生 SDK 的真实房间实现。
class TencentRtcBridge extends RtcBridge {
  TencentRtcBridge({required this.isVideo});

  @override
  final bool isVideo;

  TRTCCloud? _cloud;
  TRTCCloudListener? _listener;
  final List<String> _remoteVideoUsers = [];
  bool _initialized = false;
  bool _released = false;
  String? _lastError;

  @override
  bool get available => true;

  @override
  bool get initialized => _initialized;

  @override
  String get hint {
    if (_lastError != null) return _lastError!;
    return _initialized ? '腾讯云音视频已连接' : '正在连接音视频房间…';
  }

  @override
  String? get lastError => _lastError;

  @override
  List<String> get remoteVideoUsers => List.unmodifiable(_remoteVideoUsers);

  @override
  Future<void> enterRoom(CallTicket ticket) async {
    if (ticket.sdkAppId <= 0 ||
        ticket.userId.isEmpty ||
        ticket.userSig.isEmpty ||
        ticket.roomId.isEmpty) {
      throw StateError('服务端下发的 TRTC 进房凭据不完整');
    }

    if (Platform.isAndroid) {
      final requested = <Permission>[Permission.microphone];
      if (isVideo) requested.add(Permission.camera);
      final statuses = await requested.request();
      if (statuses.values.any((status) => !status.isGranted)) {
        throw StateError(
          isVideo ? '需要麦克风和摄像头权限才能视频通话' : '需要麦克风权限才能语音通话',
        );
      }
      // Android 12+ 蓝牙耳机需要单独授权；拒绝不影响手机听筒/扬声器通话。
      await Permission.bluetoothConnect.request();
    }

    _cloud = await TRTCCloud.sharedInstance();
    _listener = TRTCCloudListener(
      onError: (code, message) {
        _lastError = '音视频连接失败（$code）：$message';
        notifyListeners();
      },
      onEnterRoom: (result) {
        if (result > 0) {
          _initialized = true;
          _lastError = null;
        } else {
          _lastError = '进入音视频房间失败（$result）';
        }
        notifyListeners();
      },
      onUserVideoAvailable: (userId, available) {
        if (available) {
          if (!_remoteVideoUsers.contains(userId)) {
            _remoteVideoUsers.add(userId);
          }
        } else {
          _remoteVideoUsers.remove(userId);
          _cloud?.stopRemoteView(userId, TRTCVideoStreamType.big);
        }
        notifyListeners();
      },
      onRemoteUserLeaveRoom: (userId, _) {
        _remoteVideoUsers.remove(userId);
        notifyListeners();
      },
      onExitRoom: (_) => _release(),
    );
    _cloud!.registerListener(_listener!);
    // sharedInstance 已完成，视频渲染控件现在可以安全创建。
    notifyListeners();

    _cloud!.enterRoom(
      TRTCParams(
        sdkAppId: ticket.sdkAppId,
        userId: ticket.userId,
        userSig: ticket.userSig,
        strRoomId: ticket.roomId,
      ),
      isVideo ? TRTCAppScene.videoCall : TRTCAppScene.audioCall,
    );
    _cloud!.startLocalAudio(TRTCAudioQuality.defaultMode);
    _cloud!.getDeviceManager().setAudioRoute(
          isVideo ? TXAudioRoute.speakerPhone : TXAudioRoute.earpiece,
        );
  }

  @override
  Future<void> leaveRoom() async {
    if (_released) return;
    _cloud?.stopLocalPreview();
    _cloud?.stopLocalAudio();
    _cloud?.stopAllRemoteView();
    _cloud?.exitRoom();
    // 正常情况下 onExitRoom 会释放；引擎尚未进房时在这里兜底。
    if (!_initialized) _release();
  }

  @override
  Future<void> setMicMuted(bool muted) async {
    _cloud?.muteLocalAudio(muted);
  }

  @override
  Future<void> setSpeakerOn(bool on) async {
    _cloud?.getDeviceManager().setAudioRoute(
          on ? TXAudioRoute.speakerPhone : TXAudioRoute.earpiece,
        );
  }

  @override
  void startLocalPreview(int viewId) {
    if (!isVideo) return;
    _cloud?.startLocalPreview(true, viewId);
  }

  @override
  void startRemoteView(String userId, int viewId) {
    _cloud?.startRemoteView(userId, TRTCVideoStreamType.big, viewId);
  }

  void _release() {
    if (_released) return;
    _released = true;
    final listener = _listener;
    if (listener != null) _cloud?.unRegisterListener(listener);
    _remoteVideoUsers.clear();
    _initialized = false;
    _cloud = null;
    TRTCCloud.destroySharedInstance();
    notifyListeners();
  }

  @override
  void dispose() {
    if (!_released) {
      _cloud?.stopLocalPreview();
      _cloud?.stopLocalAudio();
      _cloud?.exitRoom();
      _release();
    }
    super.dispose();
  }
}
