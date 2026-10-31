import 'call_models.dart';

/// 音视频引擎桥接层。
///
/// 这一层存在的唯一理由：把"业务（呼叫/计费/心跳）"和"具体音视频 SDK"解耦。
/// 服务端下发的进房凭据只说明"你有权进这个房间"，
/// 真正拉流要依赖 TRTC 原生 SDK（需要在 pubspec 引入 tencent_trtc_cloud
/// 并完成 Android/iOS 原生配置）。
///
/// 因此这里提供两种实现：
///   - [MockRtcBridge]：服务端下发 mock 凭据时使用。不产生真实音视频，
///     但呼叫/接通/心跳/挂断/计费全链路可跑通，用于联调与验收；
///   - [PendingRtcBridge]：真实凭据但未集成原生 SDK 时使用。
///     明确抛出带指引的错误，而不是"静默黑屏"让人以为 App 崩了。
abstract class RtcBridge {
  bool get available;

  String get hint;

  Future<void> enterRoom(CallTicket ticket);

  Future<void> leaveRoom();

  Future<void> setMicMuted(bool muted);

  Future<void> setSpeakerOn(bool on);

  factory RtcBridge.fromTicket(CallTicket ticket) {
    if (ticket.mock) return const MockRtcBridge();
    return const PendingRtcBridge();
  }
}

class MockRtcBridge implements RtcBridge {
  const MockRtcBridge();

  @override
  bool get available => false;

  @override
  String get hint => '本地模拟房间：无真实音视频，但计费链路完整';

  @override
  Future<void> enterRoom(CallTicket ticket) async {}

  @override
  Future<void> leaveRoom() async {}

  @override
  Future<void> setMicMuted(bool muted) async {}

  @override
  Future<void> setSpeakerOn(bool on) async {}
}

class PendingRtcBridge implements RtcBridge {
  const PendingRtcBridge();

  @override
  bool get available => false;

  @override
  String get hint => '已拿到真实进房凭据，但客户端尚未集成 TRTC 原生 SDK';

  @override
  Future<void> enterRoom(CallTicket ticket) async {
    throw UnsupportedError(
      '未集成 TRTC SDK：请在 pubspec 引入 tencent_trtc_cloud 后替换 RtcBridge 实现',
    );
  }

  @override
  Future<void> leaveRoom() async {}

  @override
  Future<void> setMicMuted(bool muted) async {
    throw UnsupportedError('未集成 TRTC SDK');
  }

  @override
  Future<void> setSpeakerOn(bool on) async {
    throw UnsupportedError('未集成 TRTC SDK');
  }
}
