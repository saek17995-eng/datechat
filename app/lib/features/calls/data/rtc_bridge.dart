import 'package:flutter_webrtc/flutter_webrtc.dart';

import 'call_models.dart';
import 'webrtc_bridge.dart';

/// 一次通话中，除进房凭据之外引擎需要的全部上下文。
///
/// 单独拎出来而不是塞进构造参数列表：控制器在不同时机才有完整信息
/// （peerUid 建单时就有，callNo 建单后才有，caller 取决于是谁发起的）。
class RtcContext {
  const RtcContext({
    required this.peerUid,
    required this.callNo,
    required this.video,
    required this.caller,
    required this.send,
  });

  final int peerUid;
  final String callNo;
  final bool video;
  final bool caller;

  /// 信令发送口：把 offer / answer / ICE 候选通过 WebSocket 转给对端。
  final void Function(
    int to,
    String callNo,
    String kind,
    Map<String, dynamic> payload,
  ) send;
}

/// 音视频引擎桥接层。
///
/// 这一层存在的唯一理由：把"业务（呼叫/计费/心跳）"和"具体音视频引擎"解耦。
/// 服务端下发的进房凭据只说明"你有权进这个房间"，
/// 真正拉流由 [WebRtcBridge] 用端对端 PeerConnection 完成——
/// 不依赖腾讯云 TRTC，因此没有第三方密钥也能真机打通。
///
/// 若将来换成 TRTC 原生 SDK，只需再实现一个 RtcBridge 并改 [RtcBridge.create]，
/// 计费与状态机一行都不用动。
abstract class RtcBridge {
  /// 引擎是否真的能出音视频。false 时界面提示"当前无音视频"。
  bool get available;

  /// 给用户的引擎说明（界面顶部小胶囊）。
  String get hint;

  /// 本地预览渲染器。语音通话时也可以为 null（未开启视频）。
  RTCVideoRenderer? get localRenderer => null;

  /// 远端画面渲染器。
  RTCVideoRenderer? get remoteRenderer => null;

  /// 采集本地媒体并建立连接。幂等：重复调用不会建两条连接。
  Future<void> enterRoom(CallTicket ticket);

  /// 主叫发起协商（对方接听后调用）。
  Future<void> negotiate();

  /// 处理对端信令。pc 未就绪时会先缓存，就绪后自动补处理。
  Future<void> handleSignal(String kind, Map<String, dynamic> payload);

  Future<void> leaveRoom();

  Future<void> setMicMuted(bool muted);

  Future<void> setSpeakerOn(bool on);

  /// 彻底释放（含渲染器）。
  Future<void> dispose();

  factory RtcBridge.create(RtcContext ctx) => WebRtcBridge(ctx: ctx);
}
