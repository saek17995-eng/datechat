import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../profile/profile_image.dart';
import 'chat_controller.dart';
import 'data/chat_models.dart';

/// 单聊页。
///
/// 数据全部来自后端本地镜像（见 data/chat_api.dart）：
/// 首屏拉历史 → 进入即已读 → 10 秒轮询补新消息 → 上翻补更早历史。
/// 页面只负责渲染与交互，合并去重逻辑都在 ChatController 里。
class ChatPage extends ConsumerStatefulWidget {
  const ChatPage({super.key, required this.peerUid});

  final int peerUid;

  @override
  ConsumerState<ChatPage> createState() => _ChatPageState();
}

class _ChatPageState extends ConsumerState<ChatPage> {
  final TextEditingController _inputController = TextEditingController();
  final ScrollController _scrollController = ScrollController();

  /// 视口是否贴在底部。只有贴底时才自动跟随新消息，
  /// 否则用户正在翻历史时被强行拽回底部会非常恼人。
  bool _nearBottom = true;

  static const double _nearBottomThreshold = 120;
  static const double _prefetchThreshold = 160;

  ChatController get _controller =>
      ref.read(chatControllerProvider(widget.peerUid).notifier);

  @override
  void initState() {
    super.initState();
    _scrollController.addListener(_onScroll);
  }

  @override
  void dispose() {
    _inputController.dispose();
    _scrollController.dispose();
    super.dispose();
  }

  void _onScroll() {
    if (!_scrollController.hasClients) return;
    final position = _scrollController.position;
    _nearBottom =
        position.maxScrollExtent - position.pixels < _nearBottomThreshold;
    // 滑到顶部附近自动补一页历史，省掉一次点击。
    if (position.pixels < _prefetchThreshold) {
      unawaited(_loadMore());
    }
  }

  @override
  Widget build(BuildContext context) {
    final state = ref.watch(chatControllerProvider(widget.peerUid));

    ref.listen<ChatState>(chatControllerProvider(widget.peerUid), (prev, next) {
      final firstLoadDone = prev?.loading == true && !next.loading;
      final hasNewMessage = next.messages.length > (prev?.messages.length ?? 0);
      if ((firstLoadDone || hasNewMessage) && _nearBottom) {
        _scrollToBottom();
      }
    });

    return Scaffold(
      appBar: AppBar(
        titleSpacing: 0,
        title: _ChatTitle(peer: state.peer, peerUid: widget.peerUid),
      ),
      body: Column(
        children: [
          Expanded(child: _buildBody(state)),
          _InputBar(
            controller: _inputController,
            sending: state.sending,
            onSend: _send,
          ),
        ],
      ),
    );
  }

  Widget _buildBody(ChatState state) {
    if (state.loading) {
      return const Center(child: CircularProgressIndicator());
    }
    if (state.error != null) {
      return _LoadErrorView(
        message: state.error!,
        onRetry: () => unawaited(_reload()),
      );
    }
    if (state.messages.isEmpty) {
      return const _EmptyView();
    }
    return _MessageList(
      controller: _scrollController,
      state: state,
      onLoadMore: () => unawaited(_loadMore()),
      // 只有"本人 + 2 分钟内 + 未撤回"才挂长按，超时的消息长按不给反应，
      // 免得用户以为按住了没生效。
      canRecall: _controller.canRecall,
      onRecall: _confirmRecall,
    );
  }

  Future<void> _reload() async {
    await _controller.load();
  }

  Future<void> _loadMore() async {
    final error = await _controller.loadMore();
    if (error != null && mounted) _snack(error);
  }

  Future<void> _send(String raw) async {
    final result = await _controller.send(raw);
    if (!mounted) return;
    if (!result.succeeded) {
      _snack(result.blocked ? blockedSendTip : result.failureTip);
      return;
    }
    _inputController.clear();
    _scrollToBottom();
  }

  /// 长按自己的消息 → 确认 → 撤回。超过 2 分钟的消息菜单里不出现撤回。
  Future<void> _confirmRecall(ChatMessage message) async {
    if (!_controller.canRecall(message)) return;

    final confirmed = await showDialog<bool>(
          context: context,
          builder: (context) => AlertDialog(
            title: const Text('撤回消息'),
            content: const Text('撤回后对方将看不到这条消息。'),
            actions: [
              TextButton(
                onPressed: () => Navigator.of(context).pop(false),
                child: const Text('取消'),
              ),
              TextButton(
                onPressed: () => Navigator.of(context).pop(true),
                child: const Text('撤回'),
              ),
            ],
          ),
        ) ??
        false;
    if (!confirmed || !mounted) return;

    final error = await _controller.recall(message);
    if (error != null && mounted) _snack(error);
  }

  void _scrollToBottom() {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!_scrollController.hasClients) return;
      unawaited(
        _scrollController.animateTo(
          _scrollController.position.maxScrollExtent,
          duration: const Duration(milliseconds: 200),
          curve: Curves.easeOut,
        ),
      );
    });
  }

  void _snack(String message) {
    ScaffoldMessenger.of(context)
        .showSnackBar(SnackBar(content: Text(message)));
  }
}

/// 标题栏：对方头像 + 昵称。历史还没回来时先用 UID 顶上，避免标题空一拍。
class _ChatTitle extends StatelessWidget {
  const _ChatTitle({required this.peer, required this.peerUid});

  final ChatPeer? peer;
  final int peerUid;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final avatar = peer?.avatar ?? '';
    final name =
        (peer?.nickname.isNotEmpty ?? false) ? peer!.nickname : '用户 $peerUid';

    return Row(
      children: [
        CircleAvatar(
          radius: 16,
          backgroundImage: ProfileImage.provider(avatar),
          child: avatar.isEmpty
              ? Text(peer?.initial ?? '?', style: theme.textTheme.bodySmall)
              : null,
        ),
        const SizedBox(width: 10),
        Expanded(
          child: Text(
            name,
            style: theme.textTheme.titleMedium,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
          ),
        ),
      ],
    );
  }
}

/// 消息列表。index 0 在有更多历史时是"加载更早消息"。
class _MessageList extends StatelessWidget {
  const _MessageList({
    required this.controller,
    required this.state,
    required this.onLoadMore,
    required this.canRecall,
    required this.onRecall,
  });

  final ScrollController controller;
  final ChatState state;
  final VoidCallback onLoadMore;
  final bool Function(ChatMessage message) canRecall;
  final ValueChanged<ChatMessage> onRecall;

  @override
  Widget build(BuildContext context) {
    final messages = state.messages;
    final extra = state.hasMore ? 1 : 0;

    return ListView.builder(
      controller: controller,
      padding: const EdgeInsets.fromLTRB(12, 12, 12, 16),
      itemCount: messages.length + extra,
      itemBuilder: (context, index) {
        if (extra == 1 && index == 0) {
          return _LoadMoreButton(
            loading: state.loadingMore,
            onTap: onLoadMore,
          );
        }
        final message = messages[index - extra];
        return _MessageRow(
          message: message,
          onLongPress: canRecall(message) ? () => onRecall(message) : null,
        );
      },
    );
  }
}

class _LoadMoreButton extends StatelessWidget {
  const _LoadMoreButton({required this.loading, required this.onTap});

  final bool loading;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 12),
      child: Center(
        child: loading
            ? const SizedBox(
                width: 20,
                height: 20,
                child: CircularProgressIndicator(strokeWidth: 2),
              )
            : TextButton(
                onPressed: onTap,
                child: const Text('加载更早的消息'),
              ),
      ),
    );
  }
}

/// 单条消息：气泡 + 时间。撤回后气泡内换成占位文案。
class _MessageRow extends StatelessWidget {
  const _MessageRow({required this.message, required this.onLongPress});

  final ChatMessage message;
  final VoidCallback? onLongPress;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    final mine = message.mine;

    return Padding(
      padding: const EdgeInsets.only(bottom: 10),
      child: Column(
        crossAxisAlignment:
            mine ? CrossAxisAlignment.end : CrossAxisAlignment.start,
        children: [
          GestureDetector(
            onLongPress: onLongPress,
            child: Container(
              constraints: BoxConstraints(
                maxWidth: MediaQuery.sizeOf(context).width * 0.72,
              ),
              padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 9),
              decoration: BoxDecoration(
                color: mine ? scheme.primary : scheme.surfaceContainerHighest,
                borderRadius: BorderRadius.only(
                  topLeft: const Radius.circular(14),
                  topRight: const Radius.circular(14),
                  bottomLeft: Radius.circular(mine ? 14 : 4),
                  bottomRight: Radius.circular(mine ? 4 : 14),
                ),
              ),
              child: message.recalled
                  ? _RecalledBubble(mine: mine)
                  : Text(
                      message.text,
                      style: theme.textTheme.bodyMedium?.copyWith(
                        color: mine ? scheme.onPrimary : scheme.onSurface,
                      ),
                    ),
            ),
          ),
          const SizedBox(height: 4),
          Text(
            message.timeText,
            style: theme.textTheme.bodySmall?.copyWith(
              color: scheme.outline,
              fontSize: 11,
            ),
          ),
        ],
      ),
    );
  }
}

class _RecalledBubble extends StatelessWidget {
  const _RecalledBubble({required this.mine});

  final bool mine;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Icon(
          Icons.undo_rounded,
          size: 14,
          color: mine
              ? scheme.onPrimary.withValues(alpha: 0.7)
              : scheme.onSurfaceVariant,
        ),
        const SizedBox(width: 6),
        Text(
          mine ? '你撤回了一条消息' : '对方撤回了一条消息',
          style: theme.textTheme.bodySmall?.copyWith(
            color: mine
                ? scheme.onPrimary.withValues(alpha: 0.7)
                : scheme.onSurfaceVariant,
          ),
        ),
      ],
    );
  }
}

/// 输入栏。
class _InputBar extends StatelessWidget {
  const _InputBar({
    required this.controller,
    required this.sending,
    required this.onSend,
  });

  final TextEditingController controller;
  final bool sending;
  final ValueChanged<String> onSend;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;

    return SafeArea(
      top: false,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(12, 8, 8, 8),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.end,
          children: [
            Expanded(
              child: TextField(
                controller: controller,
                minLines: 1,
                maxLines: 4,
                textInputAction: TextInputAction.send,
                onSubmitted: sending ? null : onSend,
                decoration: const InputDecoration(
                  hintText: '说点什么…',
                  isDense: true,
                  border: OutlineInputBorder(
                    borderRadius: BorderRadius.all(Radius.circular(20)),
                  ),
                ),
              ),
            ),
            const SizedBox(width: 8),
            // 发送中禁用按钮：连点会产生两条一样的话。
            IconButton.filled(
              onPressed: sending ? null : () => onSend(controller.text),
              icon: sending
                  ? SizedBox(
                      width: 18,
                      height: 18,
                      child: CircularProgressIndicator(
                        strokeWidth: 2,
                        color: scheme.onPrimary,
                      ),
                    )
                  : const Icon(Icons.send_rounded, size: 20),
            ),
          ],
        ),
      ),
    );
  }
}

/// 整页加载失败。保留重试入口，不做自动重试——用户可能正想换个网络。
class _LoadErrorView extends StatelessWidget {
  const _LoadErrorView({required this.message, required this.onRetry});

  final String message;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(message, textAlign: TextAlign.center),
            const SizedBox(height: 12),
            FilledButton(onPressed: onRetry, child: const Text('重试')),
            const SizedBox(height: 8),
            Text(
              '若提示服务未配置，请确认后端已开启 IM 本地通道',
              style: theme.textTheme.bodySmall?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
              ),
              textAlign: TextAlign.center,
            ),
          ],
        ),
      ),
    );
  }
}

class _EmptyView extends StatelessWidget {
  const _EmptyView();

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    return Center(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(Icons.chat_bubble_outline, size: 40, color: scheme.outline),
          const SizedBox(height: 12),
          Text('还没有聊天记录', style: theme.textTheme.titleSmall),
          const SizedBox(height: 6),
          Text(
            '发一条消息打个招呼吧',
            style: theme.textTheme.bodySmall?.copyWith(
              color: scheme.onSurfaceVariant,
            ),
          ),
        ],
      ),
    );
  }
}
