import 'dart:async';

import 'package:flutter_webrtc/flutter_webrtc.dart';

import '../../../core/realtime/realtime_event.dart';
import 'call_models.dart';
import 'rtc_bridge.dart';

/// TURN 中转服务器（可选）。
///
/// 同一 WiFi / 同一局域网下 STUN 就够用，两端直连；
/// 跨运营商、跨 NAT 时必须有 TURN 中转，否则能连上信令但媒体流不通。
/// 打包时注入：
///   flutter build apk --dart-define=TURN_URL=turn:1.2.3.4:3478 \
///                     --dart-define=TURN_USERNAME=xxx \
///                     --dart-define=TURN_CREDENTIAL=yyy
const String _turnUrl = String.fromEnvironment('TURN_URL');
const String _turnUsername = String.fromEnvironment('TURN_USERNAME');
const String _turnCredential = String.fromEnvironment('TURN_CREDENTIAL');

/// 自建 WebRTC 引擎。
///
/// 与 TRTC 方案的分工：服务端只管"这通电话该不该打、怎么计费"
/// （进房凭据由 pkg/trtc 签发，没有真密钥时是 mock），
/// 真正的音视频走这里——端对端 PeerConnection，媒体流不经过业务服务器。
///
/// 协商顺序（谁先发 offer 必须定死，否则两边同时发会互相覆盖）：
///   1. 主叫 create 成功后就采集本地媒体、建 pc，但**不发 offer**
///      （此时对方还没接，发了也没人处理）；
///   2. 被叫 accept 后建 pc；
///   3. 服务端推 call_update(talking) 给主叫，主收到后 [negotiate] 发 offer；
///   4. 被叫收到 offer → 回 answer；双方交换 ICE 候选后接通。
///
/// 信令全部经 WebSocket 由服务端转发，见 [RtcContext.send]。
class WebRtcBridge implements RtcBridge {
  WebRtcBridge({required this.ctx});

  final RtcContext ctx;

  final RTCVideoRenderer _localRenderer = RTCVideoRenderer();
  final RTCVideoRenderer _remoteRenderer = RTCVideoRenderer();

  MediaStream? _localStream;
  RTCPeerConnection? _pc;
  bool _entered = false;

  /// pc 建立之前到达的信令先存着：主叫可能在被叫 pc 就绪前就把 offer 发出来了。
  final List<_PendingSignal> _pending = <_PendingSignal>[];

  /// 远端流到位 / 连接状态变化的回调，供 UI 刷新（通话页要切换"连接中"提示）。
  void Function(String state)? onState;

  @override
  bool get available => true;

  @override
  String get hint => '自建 WebRTC 通道（端对端，无需第三方密钥）';

  @override
  RTCVideoRenderer? get localRenderer => _localRenderer;

  @override
  RTCVideoRenderer? get remoteRenderer => _remoteRenderer;

  Map<String, dynamic> get _configuration {
    final servers = <Map<String, dynamic>>[
      <String, dynamic>{'urls': 'stun:stun.l.google.com:19302'},
      <String, dynamic>{'urls': 'stun:stun1.l.google.com:19302'},
    ];
    if (_turnUrl.isNotEmpty) {
      servers.add(<String, dynamic>{
        'urls': _turnUrl,
        'username': _turnUsername,
        'credential': _turnCredential,
      });
    }
    return <String, dynamic>{
      'iceServers': servers,
      'sdpSemantics': 'unified-plan',
    };
  }

  Map<String, dynamic> get _constraints => <String, dynamic>{
        'mandatory': <String, dynamic>{
          'OfferToReceiveAudio': true,
          'OfferToReceiveVideo': ctx.video,
        },
        'optional': <dynamic>[],
      };

  Map<String, dynamic> get _mediaConstraints => <String, dynamic>{
        'audio': true,
        'video': ctx.video
            ? <String, dynamic>{
                'mandatory': <String, dynamic>{
                  'minWidth': '640',
                  'minHeight': '480',
                  'minFrameRate': '24',
                },
                'facingMode': 'user',
                'optional': <dynamic>[],
              }
            : false,
      };

  @override
  Future<void> enterRoom(CallTicket ticket) async {
    if (_entered) return;
    _entered = true;
    await _localRenderer.initialize();
    await _remoteRenderer.initialize();

    _localStream = await navigator.mediaDevices.getUserMedia(_mediaConstraints);
    _localRenderer.srcObject = _localStream;

    final pc = await createPeerConnection(_configuration, _constraints);
    _pc = pc;
    for (final track in _localStream!.getTracks()) {
      await pc.addTrack(track, _localStream!);
    }
    pc.onTrack = (RTCTrackEvent event) {
      if (event.streams.isNotEmpty) {
        _remoteRenderer.srcObject = event.streams.first;
        onState?.call('connected');
      }
    };
    pc.onIceCandidate = (RTCIceCandidate candidate) {
      if (candidate.candidate == null) return;
      ctx.send(
        ctx.peerUid,
        ctx.callNo,
        signalCandidate,
        <String, dynamic>{
          'candidate': candidate.candidate,
          'sdpMid': candidate.sdpMid,
          'sdpMLineIndex': candidate.sdpMLineIndex,
        },
      );
    };
    pc.onIceConnectionState = (RTCIceConnectionState state) {
      onState?.call(state.toString());
    };

    // 补处理排队信令（offer 通常已经在等了）。
    final queued = List<_PendingSignal>.of(_pending);
    _pending.clear();
    for (final item in queued) {
      await handleSignal(item.kind, item.payload);
    }
  }

  /// 主叫在对方接听后发起协商。
  @override
  Future<void> negotiate() async {
    final pc = _pc;
    if (pc == null) return;
    final offer = await pc.createOffer(<String, dynamic>{});
    await pc.setLocalDescription(offer);
    ctx.send(
      ctx.peerUid,
      ctx.callNo,
      signalOffer,
      <String, dynamic>{'sdp': offer.sdp, 'type': offer.type},
    );
  }

  @override
  Future<void> handleSignal(String kind, Map<String, dynamic> payload) async {
    final pc = _pc;
    if (pc == null) {
      _pending.add(_PendingSignal(kind, payload));
      return;
    }
    switch (kind) {
      case signalOffer:
        await pc.setRemoteDescription(
          RTCSessionDescription(payload['sdp'] as String?, payload['type'] as String?),
        );
        final answer = await pc.createAnswer(<String, dynamic>{});
        await pc.setLocalDescription(answer);
        ctx.send(
          ctx.peerUid,
          ctx.callNo,
          signalAnswer,
          <String, dynamic>{'sdp': answer.sdp, 'type': answer.type},
        );
      case signalAnswer:
        await pc.setRemoteDescription(
          RTCSessionDescription(payload['sdp'] as String?, payload['type'] as String?),
        );
      case signalCandidate:
        await pc.addCandidate(
          RTCIceCandidate(
            payload['candidate'] as String?,
            payload['sdpMid'] as String?,
            payload['sdpMLineIndex'] as int?,
          ),
        );
      default:
        // 未知信令忽略：服务端/客户端版本不齐时不该让通话崩掉。
        break;
    }
  }

  @override
  Future<void> leaveRoom() async {
    _entered = false;
    _pending.clear();
    final pc = _pc;
    _pc = null;
    if (pc != null) {
      await pc.close();
    }
    final stream = _localStream;
    _localStream = null;
    if (stream != null) {
      for (final track in stream.getTracks()) {
        await track.stop();
      }
      await stream.dispose();
    }
    _localRenderer.srcObject = null;
    _remoteRenderer.srcObject = null;
  }

  @override
  Future<void> setMicMuted(bool muted) async {
    final stream = _localStream;
    if (stream == null) return;
    for (final track in stream.getAudioTracks()) {
      // enabled=false 立即停止发送音频包，比重新协商轻量得多。
      track.enabled = !muted;
    }
  }

  @override
  Future<void> setSpeakerOn(bool on) async {
    await Helper.setSpeakerphoneOn(on);
  }

  /// 释放渲染器。只在通话彻底结束时调用一次（渲染器不能复用）。
  @override
  Future<void> dispose() async {
    await leaveRoom();
    await _localRenderer.dispose();
    await _remoteRenderer.dispose();
  }
}

class _PendingSignal {
  const _PendingSignal(this.kind, this.payload);

  final String kind;
  final Map<String, dynamic> payload;
}
