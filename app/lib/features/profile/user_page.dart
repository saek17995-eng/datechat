import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../calls/call_sheet.dart';
import '../../core/network/api_exception.dart';
import 'data/profile_models.dart';
import 'data/report_api.dart';
import 'profile_controller.dart';
import 'profile_image.dart';
import 'report_sheet.dart';

/// 他人主页。
///
/// 与「我的」页分开实现，因为视角完全不同：
///   - 这里的数据是只读快照，带在线状态与关系（是否拉黑）；
///   - 底部是"联系"动作区，未开放的阶段用占位按钮并明确说明，
///     而不是塞进一堆点了没反应的按钮。
class UserPage extends ConsumerWidget {
  const UserPage({super.key, required this.uid});

  final int uid;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final theme = Theme.of(context);
    final async = ref.watch(userProfileProvider(uid));

    return Scaffold(
      appBar: AppBar(
        title: const Text('个人主页'),
        actions: [
          // 自己的主页不需要拉黑/举报入口。
          async.maybeWhen(
            data: (user) =>
                user.relation.isSelf ? null : _RelationMenu(user: user),
            orElse: () => null,
          ),
        ].nonNulls.toList(),
      ),
      body: async.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (error, _) => Center(
          child: Padding(
            padding: const EdgeInsets.all(24),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                Text('加载失败：$error'),
                const SizedBox(height: 12),
                FilledButton(
                  onPressed: () => ref.invalidate(userProfileProvider(uid)),
                  child: const Text('重试'),
                ),
              ],
            ),
          ),
        ),
        data: (user) => RefreshIndicator(
          onRefresh: () async => ref.refresh(userProfileProvider(uid).future),
          child: ListView(
            padding: const EdgeInsets.fromLTRB(16, 12, 16, 24),
            children: [
              _UserHeader(user: user),
              const SizedBox(height: 16),
              if (user.tags.isNotEmpty) _TagRow(tags: user.tags),
              if (user.bio.isNotEmpty) ...[
                const SizedBox(height: 16),
                _SectionCard(
                  title: '关于我',
                  child: Text(user.bio, style: theme.textTheme.bodyMedium),
                ),
              ],
              if (user.photos.isNotEmpty) ...[
                const SizedBox(height: 16),
                _SectionCard(
                    title: '相册', child: _PhotoGrid(photos: user.photos)),
              ],
              const SizedBox(height: 16),
              _SectionCard(
                title: '资料',
                child: Column(
                  children: [
                    _InfoRow(
                        label: '职业',
                        value: user.job.isEmpty ? '未填写' : user.job),
                    _InfoRow(
                      label: '身高',
                      value: user.height > 0 ? '${user.height} cm' : '未填写',
                    ),
                    _InfoRow(
                        label: '城市',
                        value: user.cityName.isEmpty ? '未填写' : user.cityName),
                    _InfoRow(
                      label: '实名认证',
                      value: user.isRealnameVerified ? '已认证' : '未认证',
                    ),
                    _InfoRow(label: '语音通话', value: '${user.priceAudio} 金币/分钟'),
                    _InfoRow(label: '视频通话', value: '${user.priceVideo} 金币/分钟'),
                  ],
                ),
              ),
              if (user.relation.blockedBy) ...[
                const SizedBox(height: 16),
                _BlockedHint(),
              ],
            ],
          ),
        ),
      ),
      bottomNavigationBar: async.maybeWhen(
        data: (user) => user.relation.isSelf ? null : _ActionBar(user: user),
        orElse: () => null,
      ),
    );
  }
}

void _snack(BuildContext context, String message) {
  ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(message)));
}

/// 右上角菜单：拉黑/取消拉黑 与 举报。
/// 拉黑要二次确认：它是一次性影响双方通信能力的操作，误点后虽然可以取消，
/// 但对方可能已经收不到你刚发的消息了。取消拉黑是可逆且无副作用的，不再拦一道。
class _RelationMenu extends ConsumerWidget {
  const _RelationMenu({required this.user});

  final PublicUser user;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return PopupMenuButton<_RelationAction>(
      icon: const Icon(Icons.more_vert),
      onSelected: (action) => unawaited(_handle(context, ref, action)),
      itemBuilder: (context) => [
        PopupMenuItem(
          value: user.relation.blocked
              ? _RelationAction.unblock
              : _RelationAction.block,
          child: Text(user.relation.blocked ? '取消拉黑' : '拉黑'),
        ),
        const PopupMenuItem(
          value: _RelationAction.report,
          child: Text('举报'),
        ),
      ],
    );
  }

  Future<void> _handle(
    BuildContext context,
    WidgetRef ref,
    _RelationAction action,
  ) async {
    switch (action) {
      case _RelationAction.block:
        final confirmed = await _confirmBlock(context);
        // 弹窗期间用户可能已经返回上一页，跨 await 后再用 context 会崩。
        if (!confirmed || !context.mounted) return;
        await _run(
            context,
            ref,
            () => ref.read(safetyApiProvider).blockUser(
                  user.uid,
                ),
            '已拉黑该用户');
        break;
      case _RelationAction.unblock:
        await _run(
            context,
            ref,
            () => ref.read(safetyApiProvider).unblockUser(
                  user.uid,
                ),
            '已取消拉黑');
        break;
      case _RelationAction.report:
        if (!context.mounted) return;
        await showReportSheet(
          context,
          targetType: reportTargetUser,
          targetId: '${user.uid}',
          targetUid: user.uid,
        );
        break;
    }
  }

  Future<bool> _confirmBlock(BuildContext context) async {
    final result = await showDialog<bool>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        title: const Text('拉黑对方'),
        content: Text(
          '拉黑后你们将无法互相发送消息，也看不到对方的来电。\n'
          '可以在这里或「我的 - 隐私与安全」中取消拉黑。',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(dialogContext).pop(false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.of(dialogContext).pop(true),
            child: const Text('拉黑'),
          ),
        ],
      ),
    );
    return result ?? false;
  }

  /// 执行关系变更，成功后刷新主页：服务端是唯一真相，重拉一遍比本地改状态可靠。
  Future<void> _run(
    BuildContext context,
    WidgetRef ref,
    Future<void> Function() action,
    String successText,
  ) async {
    try {
      await action();
      ref.invalidate(userProfileProvider(user.uid));
      if (context.mounted) _snack(context, successText);
    } on ApiException catch (error) {
      if (context.mounted) _snack(context, error.message);
    } catch (_) {
      if (context.mounted) _snack(context, '操作失败，请稍后重试');
    }
  }
}

enum _RelationAction { block, unblock, report }

class _UserHeader extends StatelessWidget {
  const _UserHeader({required this.user});

  final PublicUser user;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        CircleAvatar(
          radius: 40,
          backgroundImage: ProfileImage.provider(user.avatar),
          child: user.avatar.isEmpty
              ? Text(
                  user.nickname.isEmpty ? '?' : user.nickname.substring(0, 1),
                  style: theme.textTheme.headlineMedium,
                )
              : null,
        ),
        const SizedBox(width: 16),
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Flexible(
                    child: Text(
                      user.nickname,
                      style: theme.textTheme.titleLarge,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                  const SizedBox(width: 8),
                  if (user.isRealnameVerified)
                    const Icon(Icons.verified, size: 18, color: Colors.blue),
                ],
              ),
              const SizedBox(height: 6),
              if (user.summaryLine.isNotEmpty)
                Text(
                  user.summaryLine,
                  style: theme.textTheme.bodyMedium?.copyWith(
                    color: theme.colorScheme.onSurfaceVariant,
                  ),
                ),
              const SizedBox(height: 8),
              Row(
                children: [
                  Icon(
                    Icons.circle,
                    size: 10,
                    color:
                        user.online ? Colors.green : theme.colorScheme.outline,
                  ),
                  const SizedBox(width: 6),
                  Text(
                    user.online ? '在线' : user.lastActiveText,
                    style: theme.textTheme.bodySmall?.copyWith(
                      color: theme.colorScheme.onSurfaceVariant,
                    ),
                  ),
                ],
              ),
            ],
          ),
        ),
      ],
    );
  }
}

class _TagRow extends StatelessWidget {
  const _TagRow({required this.tags});

  final List<String> tags;

  @override
  Widget build(BuildContext context) {
    return Wrap(
      spacing: 8,
      runSpacing: 8,
      children: [for (final tag in tags) Chip(label: Text(tag))],
    );
  }
}

class _SectionCard extends StatelessWidget {
  const _SectionCard({required this.title, required this.child});

  final String title;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Card(
      clipBehavior: Clip.antiAlias,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(16, 12, 16, 16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              title,
              style: theme.textTheme.titleSmall?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
              ),
            ),
            const SizedBox(height: 10),
            child,
          ],
        ),
      ),
    );
  }
}

class _InfoRow extends StatelessWidget {
  const _InfoRow({required this.label, required this.value});

  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 6),
      child: Row(
        children: [
          SizedBox(
            width: 88,
            child: Text(
              label,
              style: theme.textTheme.bodyMedium?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
              ),
            ),
          ),
          Expanded(child: Text(value, style: theme.textTheme.bodyMedium)),
        ],
      ),
    );
  }
}

class _PhotoGrid extends StatelessWidget {
  const _PhotoGrid({required this.photos});

  final List<String> photos;

  @override
  Widget build(BuildContext context) {
    return GridView.builder(
      shrinkWrap: true,
      physics: const NeverScrollableScrollPhysics(),
      itemCount: photos.length,
      gridDelegate: const SliverGridDelegateWithFixedCrossAxisCount(
        crossAxisCount: 3,
        crossAxisSpacing: 8,
        mainAxisSpacing: 8,
      ),
      itemBuilder: (context, index) => ClipRRect(
        borderRadius: BorderRadius.circular(8),
        child: Image.network(
          ProfileImage.absUrl(photos[index]),
          fit: BoxFit.cover,
          errorBuilder: (_, __, ___) => ColoredBox(
            color: Theme.of(context).colorScheme.surfaceContainerHighest,
            child: const Icon(Icons.broken_image_outlined),
          ),
        ),
      ),
    );
  }
}

class _BlockedHint extends StatelessWidget {
  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Card(
      color: theme.colorScheme.errorContainer,
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Text(
          '对方设置了隐私限制，你暂时无法与其发起聊天或通话。',
          style: theme.textTheme.bodyMedium?.copyWith(
            color: theme.colorScheme.onErrorContainer,
          ),
        ),
      ),
    );
  }
}

class _ActionBar extends StatelessWidget {
  const _ActionBar({required this.user});

  final PublicUser user;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    // 双方任意一方拉黑都不允许联系：服务端发消息时会直接拒绝，
    // 与其让用户点了才报错，不如先把按钮置灰。
    final contactable = user.relation.canContact && !user.relation.blocked;

    return SafeArea(
      child: Padding(
        padding: const EdgeInsets.fromLTRB(16, 8, 16, 12),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            if (!contactable)
              Padding(
                padding: const EdgeInsets.only(bottom: 10),
                child: Text(
                  '对方设置了隐私，暂时无法联系',
                  style: theme.textTheme.bodySmall?.copyWith(
                    color: theme.colorScheme.error,
                  ),
                ),
              ),
            Row(
              children: [
                Expanded(
                  child: OutlinedButton.icon(
                    onPressed: contactable
                        ? () => context.push('/chat/${user.uid}')
                        : null,
                    icon: const Icon(Icons.chat_bubble_outline),
                    label: const Text('打招呼'),
                  ),
                ),
                const SizedBox(width: 12),
                Expanded(
                  child: FilledButton.icon(
                    onPressed: contactable
                        ? () => showCallSheet(
                              context,
                              uid: user.uid,
                              name: user.nickname,
                              avatar: user.avatar,
                              priceAudio: user.priceAudio,
                              priceVideo: user.priceVideo,
                            )
                        : null,
                    icon: const Icon(Icons.call),
                    label: const Text('呼叫'),
                  ),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}
