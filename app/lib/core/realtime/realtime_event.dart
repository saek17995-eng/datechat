// 实时通道帧模型，与服务端 internal/modules/realtime/model.go 一一对应。
//
// 帧类型是字符串而不是枚举：服务端新增事件时，老客户端收到未知 type 直接忽略即可，
// 强类型枚举会在这里反序列化失败并让整条连接崩掉。

/// 连接状态。UI 只在 [RealtimeStatus.disconnected] 时提示"离线"。
enum RealtimeStatus { disconnected, connecting, connected, reconnecting }

const String rtTypeHello = 'hello';
const String rtTypePing = 'ping';
const String rtTypePong = 'pong';
const String rtTypeNewMessage = 'new_message';
const String rtTypeRecall = 'message_recall';
const String rtTypeRead = 'message_read';
const String rtTypeCallInvite = 'call_invite';
const String rtTypeCallUpdate = 'call_update';
const String rtTypeSignal = 'signal';
const String rtTypeError = 'error';

/// 通话信令种类。WebRTC 的 offer / answer / ICE 候选全靠它们转发。
const String signalOffer = 'offer';
const String signalAnswer = 'answer';
const String signalCandidate = 'candidate';
const String signalHangup = 'hangup';

int asIntRt(Object? value) {
  if (value is int) return value;
  if (value is num) return value.toInt();
  return int.tryParse(value?.toString() ?? '') ?? 0;
}

Map<String, dynamic> asMapRt(Object? value) {
  if (value is Map<String, dynamic>) return value;
  if (value is Map) return Map<String, dynamic>.from(value);
  return const <String, dynamic>{};
}

class RealtimeEvent {
  const RealtimeEvent({
    required this.type,
    this.seq = 0,
    this.data = const <String, dynamic>{},
    this.payload = const <String, dynamic>{},
    this.callNo = '',
    this.kind = '',
    this.from = 0,
    this.msg = '',
  });

  final String type;

  /// 服务端单调递增序号。客户端可据此判断漏帧（跳号就全量拉一次）。
  final int seq;
  final Map<String, dynamic> data;

  /// 仅 signal 帧使用：对端塞进来的 SDP / ICE 候选。
  final Map<String, dynamic> payload;
  final String callNo;
  final String kind;

  /// 仅 signal 帧：发送方 uid（服务端补的，不能信客户端自称）。
  final int from;

  /// 仅 error 帧。
  final String msg;

  factory RealtimeEvent.fromJson(Map<String, dynamic> json) {
    final data = asMapRt(json['data']);
    return RealtimeEvent(
      type: (json['type'] ?? '').toString(),
      seq: asIntRt(json['seq']),
      data: data,
      payload: asMapRt(json['payload']),
      callNo: (json['call_no'] ?? '').toString(),
      kind: (json['kind'] ?? data['kind'] ?? '').toString(),
      from: asIntRt(data['from']),
      msg: (json['msg'] ?? '').toString(),
    );
  }

  @override
  String toString() => 'RealtimeEvent($type, seq=$seq)';
}
