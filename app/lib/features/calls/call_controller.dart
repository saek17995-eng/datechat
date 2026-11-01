import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/di/providers.dart';
import '../../core/network/api_exception.dart';
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
    this.error,
  });

  final CallPhase phase;
  final CallSession? session;
  final CallTicket? ticket;
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
    String? error,
    bool clearSession = false,
    bool clearTicket = false,
    bool clearError = false,
  }) {
    return CallState(
      phase: phase ?? this.phase,
      session: clearSession ? null : (session ?? this.session),
      ticket: clearTicket ? null : (ticket ?? this.ticket),
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
      error: clearError ? null : (error ?? this.error),
    );
  }
}

/// 通话控制器。
///
/// 三个定时器，各司其职，互不干扰：
///   - 界面计时：1 秒一次，只更新展示的 mm:ss；
///   - 心跳：按服务端下发的间隔上报，把"续扣/余额耗尽"的结果带回来；
///   - 房间：由 [RtcBridge] 负责进房/退房；接听后进房失败会立即挂断，避免继续计费。
///
/// 心跳失败不立刻挂断：弱网下丢一两次心跳很常见，直接挂断等于惩罚网络差的用户。
/// 真正的收尾由服务端心跳超时兜底，客户端只是"跟随"服务端的结论。
class CallController extends StateNotifier<CallState> {
  CallController(this._ref) : super(const CallState());

  final Ref _ref;
  Timer? _uiTimer;
  Timer? _beatTimer;
  RtcBridge? _bridge;
  bool _endingForRtcError = false;

  RtcBridge? get bridge => _bridge;

  /// 被叫端发现服务端存在振铃中的会话后，先展示接听/拒绝界面。
  /// 真正进房与开始计费只发生在用户点击接听之后。
  void prepareIncoming(
    CallSession session, {
    String peerName = '',
    String peerAvatar = '',
  }) {
    if (state.callNo == session.callNo && state.phase == CallPhase.ringing) {
      return;
    }
    state = state.copyWith(
      phase: CallPhase.ringing,
      session: session,
      peerName: peerName,
      peerAvatar: peerAvatar,
      isCaller: false,
      isVideo: session.isVideo,
      pricePerMin: session.pricePerMin,
      elapsedSec: 0,
      chargedMinutes: session.chargedMinutes,
      totalCoins: session.totalCoins,
      endReason: 0,
      clearTicket: true,
      clearError: true,
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
      elapsedSec: 0,
      chargedMinutes: 0,
      totalCoins: 0,
      endReason: 0,
      clearSession: true,
      clearTicket: true,
      clearError: true,
    );
    try {
      final result = await _ref
          .read(callApiProvider)
          .create(calleeId, mediaType: video ? mediaVideo : mediaAudio);
      _apply(result);
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
      elapsedSec: 0,
      chargedMinutes: 0,
      totalCoins: 0,
      endReason: 0,
      clearTicket: true,
      clearError: true,
    );
    try {
      final result = await _ref.read(callApiProvider).accept(callNo);
      _apply(result);
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
    try {
      final session = state.isCaller && state.phase == CallPhase.ringing
          ? await _ref.read(callApiProvider).cancel(callNo)
          : await _ref.read(callApiProvider).hangup(callNo);
      _stopTimers();
      await _bridge?.leaveRoom();
      state = state.copyWith(
        phase: CallPhase.ended,
        session: session,
        endReason: session.endReason,
        chargedMinutes: session.chargedMinutes,
        totalCoins: session.totalCoins,
      );
      _refreshWalletAndRecords();
    } on ApiException catch (error) {
      state = state.copyWith(error: error.message);
    }
  }

  /// 被叫拒接。
  Future<void> reject() async {
    final callNo = state.callNo;
    if (callNo.isEmpty) return;
    try {
      final session = await _ref.read(callApiProvider).reject(callNo);
      _stopTimers();
      state = state.copyWith(
        phase: CallPhase.ended,
        session: session,
        endReason: session.endReason,
      );
      _refreshWalletAndRecords();
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
      // 引擎不可用（模拟房间）时只更新界面状态，不打断通话。
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

  void _apply(CallJoinResult result) {
    final session = result.session;
    _bridge?.removeListener(_handleRtcState);
    _bridge?.dispose();
    _endingForRtcError = false;
    _bridge = RtcBridge.fromTicket(result.ticket, video: session.isVideo);
    _bridge!.addListener(_handleRtcState);
    unawaited(_enterRtc(result.ticket, session.isTalking));

    state = state.copyWith(
      phase: session.isTalking ? CallPhase.talking : CallPhase.ringing,
      session: session,
      ticket: result.ticket,
      pricePerMin: result.pricePerMin,
      balance: result.balance,
      isVideo: session.isVideo,
      chargedMinutes: session.chargedMinutes,
      totalCoins: session.totalCoins,
    );
    _startUiTimer();
    if (session.isTalking) {
      _startBeatTimer(result.heartbeatIntervalSec);
    }
  }

  Future<void> _enterRtc(CallTicket ticket, bool alreadyTalking) async {
    try {
      await _bridge?.enterRoom(ticket);
    } catch (error) {
      state = state.copyWith(
        error: error.toString().replaceFirst('Bad state: ', ''),
      );
      // 接听后已经开始计费，进房失败必须主动挂断，不能让用户无声被扣费。
      if (alreadyTalking) await _endForRtcError();
    }
  }

  void _handleRtcState() {
    final error = _bridge?.lastError;
    if (error == null || error.isEmpty) return;
    state = state.copyWith(error: error);
    // TRTC 的 onError 表示不可恢复错误；已接通时立即停止计费链路。
    if (state.phase == CallPhase.talking) unawaited(_endForRtcError());
  }

  Future<void> _endForRtcError() async {
    if (_endingForRtcError) return;
    _endingForRtcError = true;
    await hangup();
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
        _stopTimers();
        await _bridge?.leaveRoom();
        final session = await _ref.read(callApiProvider).detail(callNo);
        state = state.copyWith(
          phase: CallPhase.ended,
          session: session,
          endReason: beat.endReason,
          chargedMinutes: session.chargedMinutes,
          totalCoins: session.totalCoins,
          balance: beat.balance,
        );
        _refreshWalletAndRecords();
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
    _bridge?.removeListener(_handleRtcState);
    unawaited(_bridge?.leaveRoom().catchError((Object _) {}));
    super.dispose();
  }
}

/// 通话控制器是全局单例（非 autoDispose）：同一时刻本来就只允许一路通话，
/// 而且通话页被系统回收后重建时，状态必须还在——否则用户会看到"通话中"变成空白页。
final callControllerProvider =
    StateNotifierProvider<CallController, CallState>((ref) {
  return CallController(ref);
});
