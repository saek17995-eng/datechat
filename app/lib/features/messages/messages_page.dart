import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../core/realtime/realtime_providers.dart';
import '../profile/profile_image.dart';
import '../chat/chat_controller.dart';
import '../chat/data/chat_models.dart';

/// 「消息」Tab：会话列表。
///
/// 数据来自后端会话接口（本地镜像，后端负责与腾讯云 IM 同步未读与最后一条消息）。
/// 这里只做列表渲染：下拉刷新、空态、错误态、未读角标，点进单聊页。
class MessagesPage extends ConsumerWidget {
  const MessagesPage({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final async = ref.watch(conversationsProvider);

    // 打开消息页就等于看过了：清掉本地角标。
    // 真正的未读数由会话接口下发（页面标题里的数字），这里只清"推送时累加的那个"。
    final localUnread = ref.watch(unreadTotalProvider);
    if (localUnread > 0) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        ref.read(unreadTotalProvider.notifier).state = 0;
      });
    }

    return Scaffold(
      appBar: AppBar(
        title: Text(
          async.maybeWhen(
            data: (page) =>
                page.unreadTotal > 0 ? '消息 (${page.unreadTotal})' : '消息',
            orElse: () => '消息',
          ),
        ),
      ),
      body: RefreshIndicator(
        onRefresh: () async => ref.refresh(conversationsProvider.future),
        child: async.when(
          loading: () => _scrollable(
            const Center(child: CircularProgressIndicator()),
          ),
          error: (error, _) => _scrollable(
            _ConversationError(
              message: '$error',
              onRetry: () => ref.invalidate(conversationsProvider),
            ),
          ),
          data: (page) => page.list.isEmpty
              ? _scrollable(const _ConversationEmpty())
              : ListView.separated(
                  padding: const EdgeInsets.symmetric(vertical: 8),
                  itemCount: page.list.length,
                  separatorBuilder: (_, __) => const Divider(height: 1),
                  itemBuilder: (context, index) {
                    final item = page.list[index];
                    return _ConversationTile(
                      item: item,
                      onTap: () => _openChat(context, ref, item.peer.uid),
                    );
                  },
                ),
        ),
      ),
    );
  }

  /// 从聊天页返回后刷新一次：最后一条消息与未读数都变了。
  void _openChat(BuildContext context, WidgetRef ref, int peerUid) {
    unawaited(
      context
          .push<void>('/chat/$peerUid')
          .then((_) => ref.invalidate(conversationsProvider)),
    );
  }
}

/// 让加载中/空态/错误态也能下拉刷新：RefreshIndicator 要求可滚动的子树。
Widget _scrollable(Widget child) {
  return LayoutBuilder(
    builder: (context, constraints) => SingleChildScrollView(
      // AlwaysScrollable：内容不足一屏时也要能拉动，否则下拉手势无响应。
      physics: const AlwaysScrollableScrollPhysics(),
      child: ConstrainedBox(
        constraints: BoxConstraints(minHeight: constraints.maxHeight),
        child: Center(child: child),
      ),
    ),
  );
}

class _ConversationTile extends StatelessWidget {
  const _ConversationTile({required this.item, required this.onTap});

  final Conversation item;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    final peer = item.peer;

    return ListTile(
      contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
      leading: CircleAvatar(
        radius: 24,
        backgroundImage: ProfileImage.provider(peer.avatar),
        child: peer.avatar.isEmpty
            ? Text(peer.initial, style: theme.textTheme.titleMedium)
            : null,
      ),
      title: Row(
        children: [
          Expanded(
            child: Text(
              peer.nickname.isEmpty ? '用户 ${peer.uid}' : peer.nickname,
              style: theme.textTheme.titleSmall?.copyWith(
                fontWeight: item.unreadCount > 0 ? FontWeight.w700 : null,
              ),
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
            ),
          ),
          if (item.muted)
            Icon(Icons.volume_off, size: 14, color: scheme.outline),
        ],
      ),
      subtitle: Padding(
        padding: const EdgeInsets.only(top: 2),
        child: Text(
          item.lastPreview.isEmpty ? '打个招呼吧' : item.lastPreview,
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
          style: theme.textTheme.bodySmall?.copyWith(
            color: scheme.onSurfaceVariant,
          ),
        ),
      ),
      trailing: SizedBox(
        width: 56,
        child: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          crossAxisAlignment: CrossAxisAlignment.end,
          children: [
            Text(
              item.timeText,
              style: theme.textTheme.bodySmall?.copyWith(
                color: scheme.outline,
                fontSize: 11,
              ),
            ),
            const SizedBox(height: 6),
            if (item.unreadCount > 0)
              _UnreadBadge(text: item.unreadText)
            else
              const SizedBox(height: 4),
          ],
        ),
      ),
      onTap: onTap,
    );
  }
}

class _UnreadBadge extends StatelessWidget {
  const _UnreadBadge({required this.text});

  final String text;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
      constraints: const BoxConstraints(minWidth: 18),
      decoration: BoxDecoration(
        color: scheme.error,
        borderRadius: BorderRadius.circular(9),
      ),
      child: Text(
        text,
        textAlign: TextAlign.center,
        style: TextStyle(
          fontSize: 11,
          height: 1.4,
          color: scheme.onError,
          fontWeight: FontWeight.w600,
        ),
      ),
    );
  }
}

class _ConversationEmpty extends StatelessWidget {
  const _ConversationEmpty();

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    return Padding(
      padding: const EdgeInsets.all(32),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(Icons.forum_outlined, size: 44, color: scheme.outline),
          const SizedBox(height: 14),
          Text('还没有会话', style: theme.textTheme.titleSmall),
          const SizedBox(height: 8),
          Text(
            '去「附近」看看感兴趣的人，从主页打个招呼就能聊起来。',
            textAlign: TextAlign.center,
            style: theme.textTheme.bodySmall?.copyWith(
              color: scheme.onSurfaceVariant,
              height: 1.6,
            ),
          ),
        ],
      ),
    );
  }
}

class _ConversationError extends StatelessWidget {
  const _ConversationError({required this.message, required this.onRetry});

  final String message;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Padding(
      padding: const EdgeInsets.all(32),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(Icons.wifi_off, size: 40, color: theme.colorScheme.error),
          const SizedBox(height: 14),
          Text(
            '会话列表加载失败',
            style: theme.textTheme.titleSmall,
          ),
          const SizedBox(height: 8),
          Text(
            message,
            textAlign: TextAlign.center,
            style: theme.textTheme.bodySmall?.copyWith(
              color: theme.colorScheme.onSurfaceVariant,
            ),
          ),
          const SizedBox(height: 12),
          FilledButton(onPressed: onRetry, child: const Text('重试')),
        ],
      ),
    );
  }
}
