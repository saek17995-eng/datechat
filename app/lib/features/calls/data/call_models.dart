// 通话领域模型。
//
// 与服务端 call_session / call_segment 一一对应。
// 时长、金额一律以服务端返回为准：客户端自己计的秒数只用于界面展示，
// 一旦拿它算钱，改个包就能白嫖通话。

int asIntCall(Object? value) {
  if (value is int) return value;
  if (value is num) return value.toInt();
  return int.tryParse(value?.toString() ?? '') ?? 0;
}

/// 通话媒体类型。
const int mediaAudio = 1;
const int mediaVideo = 2;

/// 通话状态：0初始化 1振铃 2通话中 3已结束 4已取消 5被拒接 6超时未接 7异常。
const int callStatusRinging = 1;
const int callStatusTalking = 2;
const int callStatusEnded = 3;

/// 结束原因：1正常挂断 2余额不足 3心跳超时 4被叫拒接 5网络异常 6风控中断 7达到上限 8超时未接。
const int endReasonNoBalance = 2;
const int endReasonHeartbeat = 3;
const int endReasonReject = 4;
const int endReasonMaxDuration = 7;
const int endReasonNoAnswer = 8;

/// 通话会话。
class CallSession {
  const CallSession({
    required this.id,
    required this.callNo,
    required this.callerId,
    required this.calleeId,
    required this.mediaType,
    required this.pricePerMin,
    required this.status,
    required this.roomId,
    this.chargedMinutes = 0,
    this.durationSec = 0,
    this.billableMinutes = 0,
    this.totalCoins = 0,
    this.platformFee = 0,
    this.endReason = 0,
  });

  final int id;
  final String callNo;
  final int callerId;
  final int calleeId;
  final int mediaType;
  final int pricePerMin;
  final int status;
  final String roomId;
  final int chargedMinutes;
  final int durationSec;
  final int billableMinutes;
  final int totalCoins;
  final int platformFee;
  final int endReason;

  bool get isVideo => mediaType == mediaVideo;
  bool get isTalking => status == callStatusTalking;
  bool get isFinished => status >= callStatusEnded;

  /// 结束原因的人话。账单页面与通话页共用，避免两处文案不一致。
  String get endReasonText {
    switch (endReason) {
      case endReasonNoBalance:
        return '金币不足，通话已结束';
      case endReasonHeartbeat:
        return '网络中断，通话已结束';
      case endReasonReject:
        return '对方拒绝了通话';
      case endReasonMaxDuration:
        return '已达单通时长上限';
      case endReasonNoAnswer:
        return '对方未接听';
      case 0:
        return '';
      default:
        return '通话已结束';
    }
  }

  factory CallSession.fromJson(Map<String, dynamic> json) {
    return CallSession(
      id: asIntCall(json['id']),
      callNo: (json['call_no'] ?? '').toString(),
      callerId: asIntCall(json['caller_id']),
      calleeId: asIntCall(json['callee_id']),
      mediaType: asIntCall(json['media_type']),
      pricePerMin: asIntCall(json['price_per_min']),
      status: asIntCall(json['status']),
      roomId: (json['room_id'] ?? '').toString(),
      chargedMinutes: asIntCall(json['charged_minutes']),
      durationSec: asIntCall(json['duration_sec']),
      billableMinutes: asIntCall(json['billable_minutes']),
      totalCoins: asIntCall(json['total_coins']),
      platformFee: asIntCall(json['platform_fee']),
      endReason: asIntCall(json['end_reason']),
    );
  }
}

/// 进房凭据（TRTC）。mock=true 表示本地模拟房间，没有真实音视频流。
class CallTicket {
  const CallTicket({
    required this.sdkAppId,
    required this.roomId,
    required this.userId,
    required this.userSig,
    required this.mock,
  });

  final int sdkAppId;
  final String roomId;
  final String userId;
  final String userSig;
  final bool mock;

  factory CallTicket.fromJson(Map<String, dynamic> json) {
    return CallTicket(
      sdkAppId: asIntCall(json['sdk_app_id']),
      roomId: (json['room_id'] ?? '').toString(),
      userId: (json['user_id'] ?? '').toString(),
      userSig: (json['user_sig'] ?? '').toString(),
      mock: json['mock'] == true,
    );
  }
}

/// 发起/接听的结果。
class CallJoinResult {
  const CallJoinResult({
    required this.session,
    required this.ticket,
    required this.pricePerMin,
    this.balance = 0,
    this.heartbeatIntervalSec = 10,
  });

  final CallSession session;
  final CallTicket ticket;
  final int pricePerMin;
  final int balance;
  final int heartbeatIntervalSec;

  factory CallJoinResult.fromJson(Map<String, dynamic> json) {
    final sessionJson = json['session'];
    final ticketJson = json['ticket'];
    return CallJoinResult(
      session: CallSession.fromJson(
        sessionJson is Map<String, dynamic> ? sessionJson : const {},
      ),
      ticket: CallTicket.fromJson(
        ticketJson is Map<String, dynamic> ? ticketJson : const {},
      ),
      pricePerMin: asIntCall(json['price_per_min']),
      balance: asIntCall(json['balance']),
      heartbeatIntervalSec: asIntCall(json['heartbeat_interval_sec']) == 0
          ? 10
          : asIntCall(json['heartbeat_interval_sec']),
    );
  }
}

/// 心跳结果。ended=true 时必须退出通话页。
class CallBeat {
  const CallBeat({
    required this.status,
    required this.ended,
    required this.endReason,
    required this.elapsedSec,
    required this.chargedMinutes,
    required this.totalCoins,
    required this.balance,
  });

  final int status;
  final bool ended;
  final int endReason;
  final int elapsedSec;
  final int chargedMinutes;
  final int totalCoins;
  final int balance;

  factory CallBeat.fromJson(Map<String, dynamic> json) {
    return CallBeat(
      status: asIntCall(json['status']),
      ended: json['ended'] == true,
      endReason: asIntCall(json['end_reason']),
      elapsedSec: asIntCall(json['elapsed_sec']),
      chargedMinutes: asIntCall(json['charged_minutes']),
      totalCoins: asIntCall(json['total_coins']),
      balance: asIntCall(json['balance']),
    );
  }
}

/// 通话定价。
class CallPrice {
  const CallPrice({
    required this.perMin,
    required this.platformRate,
    required this.priceMode,
  });

  final int perMin;
  final int platformRate;

  /// 1全局价 2被叫自定价
  final int priceMode;

  factory CallPrice.fromJson(Map<String, dynamic> json) {
    return CallPrice(
      perMin: asIntCall(json['per_min']),
      platformRate: asIntCall(json['platform_rate']),
      priceMode: asIntCall(json['price_mode']),
    );
  }
}
