import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/di/providers.dart';
import '../../core/network/api_exception.dart';
import 'data/chat_api.dart';
import 'data/chat_models.dart';

/// 被对方拉黑：服务端返回 30001。默认文案偏技术化，客户端换成更直白的说法。
const int errCodeBlocked = 30001;
const String blockedSendTip = '由于隐私设置，消息无法发送';

/// 聊天接口。
final chatApiProvider = Provider<ChatApi>((ref) {
  return ChatApi(ref.watch(apiClientProvider));
});

/// 会话列表。autoDispose：退出登录后不留驻。
final conversationsProvider =
    FutureProvider.autoDispose<ConversationPage>((ref) async {
  return ref.watch(chatApiProvider).conversations();
});

/// 单聊会话。family 按 peer_uid 分别缓存，页面退出即释放。
final chatControllerProvider = StateNotifierProvider.autoDispose
    .family<ChatController, ChatState, int>((ref, peerUid) {
  return ChatController(ref, peerUid);
});

/// 单聊页状态。
///
/// [error] 只表示"整页加载失败"（此时页面显示错误态 + 重试）；
/// 发送/撤回/上翻这类局部失败由对应方法返回错误文案，由页面弹 SnackBar，
/// 免得一次手滑就让整页变成错误页。
class ChatState {
  const ChatState({
    this.loading = false,
    this.loadingMore = false,
    this.sending = false,
    this.messages = const <ChatMessage>[],
    this.peer,
    this.hasMore = false,
    this.nextCursor = 0,
    this.error,
  });

  /// 首帧：还没拉到历史，页面显示 loading。
  const ChatState.initial() : this(loading: true);

  final bool loading;

  /// 正在往前翻历史。
  final bool loadingMore;

  /// 正在发送。用来禁用发送按钮，避免连点产生重复消息。
  final bool sending;

  /// 按 id 升序，最新的在最后。
  final List<ChatMessage> messages;
  final ChatPeer? peer;
  final bool hasMore;
  final int nextCursor;
  final String? error;

  ChatState copyWith({
    bool? loading,
    bool? loadingMore,
    bool? sending,
    List<ChatMessage>? messages,
    ChatPeer? peer,
    bool? hasMore,
    int? nextCursor,
    String? error,
    bool clearError = false,
  }) {
    return ChatState(
      loading: loading ?? this.loading,
      loadingMore: loadingMore ?? this.loadingMore,
      sending: sending ?? this.sending,
      messages: messages ?? this.messages,
      peer: peer ?? this.peer,
      hasMore: hasMore ?? this.hasMore,
      nextCursor: nextCursor ?? this.nextCursor,
      error: clearError ? null : (error ?? this.error),
    );
  }
}

/// 发送结果。
///
/// 失败时同时带业务码和文案：业务码用来判断要不要给针对性提示
/// （比如被拉黑要说明"为什么发不出去"），文案用来兜底展示服务端给出的原因
/// （敏感词、账号被封禁），一来保留原文让用户可以据此修改，二来不用在客户端
/// 维护一份会过期的错误码映射表。
class SendResult {
  const SendResult({this.code = 0, this.message});

  /// 0 表示成功。
  final int code;

  final String? message;

  bool get succeeded => code == 0;

  bool get blocked => code == errCodeBlocked;

  String get failureTip => message ?? '发送失败，请稍后重试';
}

/// 单聊控制器。
///
/// 为什么自己维护消息列表而不是直接 watch FutureProvider：
/// 同一个会话里有四种来源（首屏历史、上翻历史、发送回包、轮询刷新），
/// 用 FutureProvider 只能表达"一次性加载"，而这四种必须按 id 合并成一份有序列表，
/// 否则轮询一刷新就会把用户翻出来的历史冲掉。
class ChatController extends StateNotifier<ChatState> {
  ChatController(this._ref, this._peerUid) : super(const ChatState.initial()) {
    unawaited(load());
    _startPolling();
  }

  final Ref _ref;
  final int _peerUid;

  /// 每页条数，与服务端默认 30 一致。
  static const int pageSize = 30;

  /// 轮询间隔。没有长连接推送时用轮询兜底新消息。
  static const Duration pollInterval = Duration(seconds: 10);

  /// 可撤回的时间窗，与服务端 IM.RecallWindow 默认 2 分钟对齐。
  static const Duration recallWindow = Duration(minutes: 2);

  Timer? _pollTimer;

  /// 上一轮轮询是否还在路上。
  bool _polling = false;

  /// 同一毫秒内连发时的自增序号，保证 client_msg_id 不重复。
  int _sendSeq = 0;

  /// 是否已销毁。
  ///
  /// 控制器是 autoDispose 的：用户随时可能退出聊天页，而此时请求还在飞。
  /// 回包后若继续写 state 会触发 StateNotifier 的 mounted 断言，
  /// 所以每个 await 之后都要先确认自己还活着。
  bool _disposed = false;

  /// 加载首屏历史，成功后自动标记已读。
  Future<void> load() async {
    state = state.copyWith(loading: true, clearError: true);
    try {
      final page = await _ref.read(chatApiProvider).messages(
            peerUid: _peerUid,
            limit: pageSize,
          );
      if (_disposed) return;
      _mergeLatest(page);
      state = state.copyWith(loading: false);
      // 进入会话即已读；失败不影响看消息，下次轮询会再试一次。
      unawaited(markRead());
    } on ApiException catch (error) {
      if (_disposed) return;
      state = state.copyWith(loading: false, error: error.message);
    } catch (_) {
      if (_disposed) return;
      state = state.copyWith(loading: false, error: '加载失败，请稍后重试');
    }
  }

  /// 往前翻一页历史。返回 null 表示成功，否则返回错误文案。
  Future<String?> loadMore() async {
    if (state.loadingMore || !state.hasMore || state.nextCursor <= 0) {
      return null;
    }
    state = state.copyWith(loadingMore: true);
    try {
      final page = await _ref.read(chatApiProvider).messages(
            peerUid: _peerUid,
            beforeId: state.nextCursor,
            limit: pageSize,
          );
      if (_disposed) return null;
      _appendOlder(page);
      state = state.copyWith(loadingMore: false);
      return null;
    } on ApiException catch (error) {
      if (_disposed) return null;
      state = state.copyWith(loadingMore: false);
      return error.message;
    } catch (_) {
      if (_disposed) return null;
      state = state.copyWith(loadingMore: false);
      return '加载历史失败，请稍后重试';
    }
  }

  /// 发送文本消息。返回 [SendResult]：code 为 0 表示成功，
  /// 否则带上服务端业务码与原因文案（敏感词、对方拉黑、被封禁等）。
  Future<SendResult> send(String text) async {
    final trimmed = text.trim();
    if (trimmed.isEmpty) return const SendResult();
    if (state.sending) return const SendResult();

    state = state.copyWith(sending: true);
    try {
      final sent = await _ref.read(chatApiProvider).sendText(
            peerUid: _peerUid,
            text: trimmed,
            clientMsgId: _nextClientMsgId(),
          );
      if (_disposed) return const SendResult();
      _upsert(sent);
      state = state.copyWith(sending: false);
      return const SendResult();
    } on ApiException catch (error) {
      if (_disposed) return const SendResult();
      state = state.copyWith(sending: false);
      return SendResult(code: error.code, message: error.message);
    } catch (_) {
      if (_disposed) return const SendResult();
      state = state.copyWith(sending: false);
      return const SendResult(message: '发送失败，请稍后重试');
    }
  }

  /// 撤回一条自己的消息。返回 null 表示成功。
  Future<String?> recall(ChatMessage message) async {
    if (message.msgId.isEmpty) return '消息尚未同步，暂时无法撤回';
    try {
      final result = await _ref.read(chatApiProvider).recall(message.msgId);
      if (_disposed) return null;
      _upsert(message.copyWith(
        status: result.status,
        recalledAt: result.recalledAt,
      ));
      return null;
    } on ApiException catch (error) {
      return error.message;
    } catch (_) {
      return '撤回失败，请稍后重试';
    }
  }

  /// 标记已读，并顺手让会话列表的未读角标归零。
  Future<void> markRead() async {
    if (_disposed) return;
    try {
      await _ref.read(chatApiProvider).markRead(_peerUid);
      if (_disposed) return;
      _ref.invalidate(conversationsProvider);
    } catch (_) {
      // 已读是"尽力而为"：失败只影响红点，不该打断聊天。
    }
  }

  /// 是否允许撤回：必须是自己发的、未撤回，且在时间窗内。
  bool canRecall(ChatMessage message) {
    if (!message.mine || message.recalled) return false;
    final elapsed = DateTime.now().millisecondsSinceEpoch - message.sentAt;
    return elapsed >= 0 && elapsed <= recallWindow.inMilliseconds;
  }

  /// 生成可预测且唯一的 client_msg_id。
  ///
  /// 格式 `c-<毫秒时间戳>-<四位序号>`，只含服务端允许的字符（[A-Za-z0-9-_.]，8~64 位）。
  /// 可预测 = 线上排查时能从 id 直接反推发送时刻；
  /// 唯一 = 同一毫秒连发靠序号区分，配合服务端幂等去重，弱网重发不会产生重复消息。
  String _nextClientMsgId() {
    _sendSeq = (_sendSeq + 1) % 10000;
    final stamp = DateTime.now().millisecondsSinceEpoch;
    return 'c-$stamp-${_sendSeq.toString().padLeft(4, '0')}';
  }

  /// 用最新一页刷新头部：新页整体先进来，已翻出来的历史按 id 去重后接在后面。
  void _mergeLatest(MessagePage page) {
    final merged = <ChatMessage>[...page.list];
    final seen = <int>{for (final message in merged) message.id};
    for (final message in state.messages) {
      if (seen.add(message.id)) merged.add(message);
    }
    merged.sort((a, b) => a.id.compareTo(b.id));

    // 已经翻过历史时（合并结果比这一页多），不能拿最新页的游标覆盖旧的，
    // 否则"加载更多"会退回到已经拿过的区间，越翻越回不去。
    final keepCursor =
        state.messages.isNotEmpty && merged.length > page.list.length;
    state = state.copyWith(
      peer: page.peer,
      messages: merged,
      hasMore: keepCursor ? state.hasMore : page.hasMore,
      nextCursor: keepCursor ? state.nextCursor : page.nextCursor,
    );
  }

  /// 追加更早的一页（上翻）。
  void _appendOlder(MessagePage page) {
    final merged = <ChatMessage>[...state.messages];
    final seen = <int>{for (final message in merged) message.id};
    for (final message in page.list) {
      if (seen.add(message.id)) merged.add(message);
    }
    merged.sort((a, b) => a.id.compareTo(b.id));
    state = state.copyWith(
      peer: page.peer,
      messages: merged,
      hasMore: page.hasMore,
      nextCursor: page.nextCursor,
    );
  }

  /// 插入或替换一条消息（发送回包、撤回后更新）。
  void _upsert(ChatMessage message) {
    final merged = <ChatMessage>[...state.messages];
    final index = merged.indexWhere(
      (item) =>
          item.id == message.id ||
          (message.msgId.isNotEmpty && item.msgId == message.msgId),
    );
    if (index >= 0) {
      merged[index] = message;
    } else {
      merged.add(message);
    }
    merged.sort((a, b) => a.id.compareTo(b.id));
    state = state.copyWith(messages: merged);
  }

  void _startPolling() {
    _pollTimer?.cancel();
    _pollTimer = Timer.periodic(pollInterval, (_) => unawaited(_poll()));
  }

  /// 轮询刷新。失败一律静默：10 秒一次，弹错会打断正在输入的用户。
  Future<void> _poll() async {
    // 上一轮还没回来就跳过：弱网下请求可能超过 10 秒，不拦会越积越多。
    if (state.loading || _polling) return;
    _polling = true;
    try {
      final page = await _ref.read(chatApiProvider).messages(
            peerUid: _peerUid,
            limit: pageSize,
          );
      if (_disposed) return;
      final before = state.messages.length;
      _mergeLatest(page);
      // 有新消息就顺手清掉红点，省得用户退出去才发现还有未读。
      if (state.messages.length > before) {
        unawaited(markRead());
      }
    } catch (_) {
      // 静默，等下一个周期。
    } finally {
      _polling = false;
    }
  }

  @override
  void dispose() {
    _disposed = true;
    _pollTimer?.cancel();
    _pollTimer = null;
    super.dispose();
  }
}
