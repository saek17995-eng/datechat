import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/di/providers.dart';
import '../../core/network/api_exception.dart';
import '../../core/realtime/realtime_event.dart';
import '../../core/realtime/realtime_providers.dart';
import '../wallet/wallet_controller.dart';
import 'data/call_api.dart';
import 'data/call_models.dart';
import 'data/rtc_bridge.dart';

final callApiProvider = Provider<CallApi>((ref) {
  return CallApi(ref.watch(apiClientProvider));
});

/// 通话记录列表。
final callRecordsProvider =
    FutureProvider.autoDispose<List<CallSession>>((ref) async {
  return ref.watch(callApiProvider).list();
});

/// 通话阶段。
enum CallPhase { idle, dialing, ringing, talking, ended }

class CallState {
  const CallState({
    this.phase = CallPhase.idle,
    this.session,
    this.ticket,
    this.bridge,
    this.peerName = '',
    this.peerAvatar = '',
    this.isCaller = true,
    this.isVideo = false,
    this.pricePerMin = 0,
    this.elapsedSec = 0,
    this.chargedMinutes = 0,
    this.totalCoins = 0,
    this.balance = 0,
    this.micMuted = false,
    this.speakerOn = true,
    this.endReason = 0,
    this.engineState = '',
    this.error,
  });

  final CallPhase phase;
  final CallSession? session;
  final CallTicket? ticket;

  /// 音视频引擎。通话页靠它拿渲染器画画面；为空表示还没建起来。
  final RtcBridge? bridge;

  final String peerName;
  final String peerAvatar;
  final bool isCaller;
  final bool isVideo;
  final int pricePerMin;

  /// 界面计时（秒）。只用于展示，账单以服务端结算为准。
  final int elapsedSec;
  final int chargedMinutes;
  final int totalCoins;
  final int balance;
  final bool micMuted;
  final bool speakerOn;
  final int endReason;

  /// 引擎的连接状态文案（"连接中…"/"已接通"），来自 PeerConnection 回调。
  final String engineState;
  final String? error;

  bool get busy => phase == CallPhase.dialing || phase == CallPhase.ringing;

  String get callNo => session?.callNo ?? '';

  /// mm:ss
  String get durationText {
    final m = elapsedSec ~/ 60;
    final s = elapsedSec % 60;
    return '${m.toString().padLeft(2, '0')}:${s.toString().padLeft(2, '0')}';
  }

  CallState copyWith({
    CallPhase? phase,
    CallSession? session,
    CallTicket? ticket,
    RtcBridge? bridge,
    String? peerName,
    String? peerAvatar,
    bool? isCaller,
    bool? isVideo,
    int? pricePerMin,
    int? elapsedSec,
    int? chargedMinutes,
    int? totalCoins,
    int? balance,
    bool? micMuted,
    bool? speakerOn,
    int? endReason,
    String? engineState,
    String? error,
    bool clearError = false,
  }) {
    return CallState(
      phase: phase ?? this.phase,
      session: session ?? this.session,
      ticket: ticket ?? this.ticket,
      bridge: bridge ?? this.bridge,
      peerName: peerName ?? this.peerName,
      peerAvatar: peerAvatar ?? this.peerAvatar,
      isCaller: isCaller ?? this.isCaller,
      isVideo: isVideo ?? this.isVideo,
      pricePerMin: pricePerMin ?? this.pricePerMin,
      elapsedSec: elapsedSec ?? this.elapsedSec,
      chargedMinutes: chargedMinutes ?? this.chargedMinutes,
      totalCoins: totalCoins ?? this.totalCoins,
      balance: balance ?? this.balance,
      micMuted: micMuted ?? this.micMuted,
      speakerOn: speakerOn ?? this.speakerOn,
      endReason: endReason ?? this.endReason,
      engineState: engineState ?? this.engineState,
      error: clearError ? null : (error ?? this.error),
    );
  }
}

/// 通话控制器。
///
/// 三个定时器，各司其职，互不干扰：
///   - 界面计时：1 秒一次，只更新展示的 mm:ss；
///   - 心跳：按服务端下发的间隔上报，把"续扣/余额耗尽"的结果带回来；
///   - 房间：由 [RtcBridge] 负责进房/退房，失败不影响计费链路继续。
///
/// 心跳失败不立刻挂断：弱网下丢一两次心跳很常见，直接挂断等于惩罚网络差的用户。
/// 真正的收尾由服务端心跳超时兜底，客户端只是"跟随"服务端的结论。
///
/// 另外订阅了实时通道：对端的接听/拒接/挂断会推过来（不必等下一次心跳），
/// WebRTC 的 offer/answer/ICE 也走同一条连接转发。
class CallController extends StateNotifier<CallState> {
  CallController(this._ref) : super(const CallState()) {
    _subscribeRealtime();
  }

  final Ref _ref;
  Timer? _uiTimer;
  Timer? _beatTimer;
  RtcBridge? _bridge;
  RtcContext? _ctx;
  StreamSubscription<RealtimeEvent>? _eventSub;
  int _beatIntervalSec = 10;

  // ------------------------------------------------------------ 主叫 / 被叫

  /// 收到来电时只进入振铃态；只有用户点击“接听”才调用 accept。
  void prepareIncoming({
    required String callNo,
    required int callerId,
    required bool video,
    int pricePerMin = 0,
    String peerName = '',
    String peerAvatar = '',
  }) {
    if (callNo.isEmpty || callerId <= 0) return;
    if (state.callNo == callNo && state.phase == CallPhase.ringing) return;

    final old = _bridge;
    _bridge = null;
    _ctx = null;
    unawaited(old?.dispose().catchError((Object _) {}));

    state = CallState(
      phase: CallPhase.ringing,
      session: CallSession(
        id: 0,
        callNo: callNo,
        callerId: callerId,
        calleeId: 0,
        mediaType: video ? mediaVideo : mediaAudio,
        pricePerMin: pricePerMin,
        status: callStatusRinging,
        roomId: '',
      ),
      peerName: peerName,
      peerAvatar: peerAvatar,
      isCaller: false,
      isVideo: video,
      pricePerMin: pricePerMin,
      engineState: '等待接听…',
    );
  }

  /// 主叫：发起通话。
  Future<bool> start({
    required int calleeId,
    required bool video,
    String peerName = '',
    String peerAvatar = '',
  }) async {
    state = state.copyWith(
      phase: CallPhase.dialing,
      peerName: peerName,
      peerAvatar: peerAvatar,
      isCaller: true,
      isVideo: video,
      clearError: true,
    );
    try {
      final result = await _ref
          .read(callApiProvider)
          .create(calleeId, mediaType: video ? mediaVideo : mediaAudio);
      await _apply(result, ringing: true);
      return true;
    } on ApiException catch (error) {
      state = state.copyWith(
        phase: CallPhase.ended,
        error: error.message,
      );
      return false;
    } catch (_) {
      state = state.copyWith(
        phase: CallPhase.ended,
        error: '网络异常，无法发起通话',
      );
      return false;
    }
  }

  /// 被叫：接听。
  Future<bool> accept(
    String callNo, {
    String peerName = '',
    String peerAvatar = '',
    bool video = false,
  }) async {
    state = state.copyWith(
      phase: CallPhase.dialing,
      peerName: peerName,
      peerAvatar: peerAvatar,
      isCaller: false,
      isVideo: video,
      clearError: true,
    );
    try {
      final result = await _ref.read(callApiProvider).accept(callNo);
      await _apply(result, ringing: false);
      return true;
    } on ApiException catch (error) {
      state = state.copyWith(phase: CallPhase.ended, error: error.message);
      return false;
    } catch (_) {
      state = state.copyWith(
        phase: CallPhase.ended,
        error: '网络异常，无法接听',
      );
      return false;
    }
  }

  /// 挂断（通话中）或取消（振铃中）。
  Future<void> hangup() async {
    final callNo = state.callNo;
    if (callNo.isEmpty) return;
    _sendSignal('hangup');
    try {
      final session = state.isCaller && state.phase == CallPhase.ringing
          ? await _ref.read(callApiProvider).cancel(callNo)
          : await _ref.read(callApiProvider).hangup(callNo);
      await _finish(session.endReason, session: session);
    } on ApiException catch (error) {
      state = state.copyWith(error: error.message);
    }
  }

  /// 被叫拒接。
  Future<void> reject() async {
    final callNo = state.callNo;
    if (callNo.isEmpty) return;
    _sendSignal('hangup');
    try {
      final session = await _ref.read(callApiProvider).reject(callNo);
      await _finish(session.endReason, session: session);
    } on ApiException catch (error) {
      state = state.copyWith(error: error.message);
    }
  }

  Future<void> toggleMic() async {
    final next = !state.micMuted;
    state = state.copyWith(micMuted: next);
    try {
      await _bridge?.setMicMuted(next);
    } catch (_) {
      // 引擎尚未就绪时只更新界面状态，不打断通话。
    }
  }

  Future<void> toggleSpeaker() async {
    final next = !state.speakerOn;
    state = state.copyWith(speakerOn: next);
    try {
      await _bridge?.setSpeakerOn(next);
    } catch (_) {
      // 同上
    }
  }

  // ------------------------------------------------------------ 实时通道

  void _subscribeRealtime() {
    _eventSub?.cancel();
    _eventSub = _ref.read(wsClientProvider).events.listen(_onRealtimeEvent);
  }

  void _onRealtimeEvent(RealtimeEvent event) {
    switch (event.type) {
      case rtTypeSignal:
        _onSignal(event);
      case rtTypeCallUpdate:
        _onRemoteUpdate(event.data);
      default:
        break;
    }
  }

  void _onSignal(RealtimeEvent event) {
    final callNo =
        event.callNo.isNotEmpty ? event.callNo : (event.data['callNo'] ?? '').toString();
    // 只处理当前这一通电话的信令：串线会把 SDP 塞给错误的连接。
    if (state.callNo.isEmpty || callNo != state.callNo) return;
    unawaited(
      _bridge?.handleSignal(event.kind, event.payload).catchError((Object _) {}),
    );
  }

  /// 对端状态变化（接听 / 拒接 / 挂断）。比等下一次心跳快得多。
  void _onRemoteUpdate(Map<String, dynamic> data) {
    final callNo = (data['call_no'] ?? '').toString();
    if (state.callNo.isEmpty || callNo.isEmpty || callNo != state.callNo) return;
    switch ((data['statusText'] ?? data['status_text'] ?? '').toString()) {
      case 'talking':
        if (state.phase == CallPhase.talking) return;
        state = state.copyWith(phase: CallPhase.talking);
        _startBeatTimer(_beatIntervalSec);
        // 协商由主叫发起：被叫此时还在等 offer，两边同时发会互相覆盖。
        if (state.isCaller) {
          unawaited(_bridge?.negotiate().catchError((Object _) {}));
        }
      case 'rejected':
      case 'canceled':
      case 'ended':
        unawaited(_finishByRemote());
      default:
        break;
    }
  }

  void _sendSignal(String kind, [Map<String, dynamic> payload = const <String, dynamic>{}]) {
    final ctx = _ctx;
    if (ctx == null) return;
    _ref
        .read(wsClientProvider)
        .sendSignal(to: ctx.peerUid, callNo: ctx.callNo, kind: kind, payload: payload);
  }

  Future<void> _finishByRemote() async {
    _stopTimers();
    final callNo = state.callNo;
    try {
      final session = await _ref.read(callApiProvider).detail(callNo);
      await _finish(session.endReason, session: session);
    } catch (_) {
      await _finish(0);
    }
  }

  // ------------------------------------------------------------ 生命周期

  Future<void> _apply(CallJoinResult result, {required bool ringing}) async {
    final session = result.session;
    _beatIntervalSec = result.heartbeatIntervalSec;

    final old = _bridge;
    _bridge = null;
    await old?.dispose().catchError((Object _) {});

    _ctx = RtcContext(
      peerUid: state.isCaller ? session.calleeId : session.callerId,
      callNo: session.callNo,
      video: session.isVideo,
      caller: state.isCaller,
      send: (to, callNo, kind, payload) => _ref
          .read(wsClientProvider)
          .sendSignal(to: to, callNo: callNo, kind: kind, payload: payload),
    );
    final bridge = RtcBridge.create(_ctx!);
    _bridge = bridge;
    // 进房失败不影响计费链路：房间只是音视频载体，账单由服务端独立推进。
    unawaited(
      bridge.enterRoom(result.ticket).catchError((Object error) {
        state = state.copyWith(engineState: '音视频启动失败：$error');
      }),
    );

    state = state.copyWith(
      phase: session.isTalking ? CallPhase.talking : CallPhase.ringing,
      session: session,
      ticket: result.ticket,
      bridge: bridge,
      pricePerMin: result.pricePerMin,
      balance: result.balance,
      isVideo: session.isVideo,
      chargedMinutes: session.chargedMinutes,
      totalCoins: session.totalCoins,
      engineState: ringing ? '等待接听…' : '正在连接…',
    );
    _startUiTimer();
    if (session.isTalking) {
      _startBeatTimer(result.heartbeatIntervalSec);
      if (state.isCaller) {
        unawaited(bridge.negotiate().catchError((Object _) {}));
      }
    }
  }

  /// 收尾：停表、退房、落最终账单。
  Future<void> _finish(int endReason, {CallSession? session}) async {
    _stopTimers();
    await _bridge?.leaveRoom().catchError((Object _) {});
    state = state.copyWith(
      phase: CallPhase.ended,
      session: session ?? state.session,
      endReason: endReason,
      chargedMinutes: session?.chargedMinutes ?? state.chargedMinutes,
      totalCoins: session?.totalCoins ?? state.totalCoins,
      engineState: '',
    );
    _refreshWalletAndRecords();
  }

  void _startUiTimer() {
    _uiTimer?.cancel();
    _uiTimer = Timer.periodic(const Duration(seconds: 1), (_) {
      if (state.phase != CallPhase.talking) return;
      state = state.copyWith(elapsedSec: state.elapsedSec + 1);
    });
  }

  void _startBeatTimer(int intervalSec) {
    _beatTimer?.cancel();
    _beatTimer = Timer.periodic(
      Duration(seconds: intervalSec <= 0 ? 10 : intervalSec),
      (_) => unawaited(_beat()),
    );
  }

  Future<void> _beat() async {
    final callNo = state.callNo;
    if (callNo.isEmpty || state.phase == CallPhase.ended) return;
    try {
      final beat = await _ref
          .read(callApiProvider)
          .heartbeat(callNo, elapsedSec: state.elapsedSec);
      if (beat.ended) {
        final session = await _ref.read(callApiProvider).detail(callNo);
        await _finish(beat.endReason, session: session);
        return;
      }
      state = state.copyWith(
        chargedMinutes: beat.chargedMinutes,
        totalCoins: beat.totalCoins,
        balance: beat.balance,
      );
    } on ApiException catch (error) {
      // 单次心跳失败只记录，不结束通话：服务端心跳超时会兜底收尾。
      state = state.copyWith(error: error.message);
    } catch (_) {
      // 网络抖动，忽略
    }
  }

  void _stopTimers() {
    _uiTimer?.cancel();
    _uiTimer = null;
    _beatTimer?.cancel();
    _beatTimer = null;
  }

  void _refreshWalletAndRecords() {
    _ref.invalidate(callRecordsProvider);
    // 通话会持续扣费，结束后让钱包页重新拉余额，避免展示旧值。
    _ref.invalidate(walletProvider);
  }

  @override
  void dispose() {
    _stopTimers();
    _eventSub?.cancel();
    _eventSub = null;
    unawaited(_bridge?.dispose().catchError((Object _) {}));
    _bridge = null;
    super.dispose();
  }
}

/// 通话控制器是全局单例（非 autoDispose）：同一时刻本来就只允许一路通话，
/// 而且通话页被系统回收后重建时，状态必须还在——否则用户会看到"通话中"变成空白页。
final callControllerProvider =
    StateNotifierProvider<CallController, CallState>((ref) {
  return CallController(ref);
});
