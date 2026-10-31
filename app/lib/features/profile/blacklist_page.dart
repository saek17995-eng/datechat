import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/network/api_exception.dart';
import 'data/report_api.dart';
import 'profile_controller.dart';
import 'profile_image.dart';

/// 黑名单管理页。
///
/// 只展示仍生效的拉黑记录（服务端软删后不再返回）。解除拉黑后重拉列表，
/// 而不是本地剔除这一条——服务端才是唯一真相，本地改了可能和实际不一致。
class BlacklistPage extends ConsumerStatefulWidget {
  const BlacklistPage({super.key});

  @override
  ConsumerState<BlacklistPage> createState() => _BlacklistPageState();
}

class _BlacklistPageState extends ConsumerState<BlacklistPage> {
  /// 正在解除拉黑的 uid。按钮单独置 loading，避免误认为整页卡住。
  final Set<int> _busy = <int>{};

  Future<void> _refresh() async {
    ref.invalidate(blacklistProvider);
    try {
      await ref.read(blacklistProvider.future);
    } catch (_) {
      // 失败时 provider 已进入 error 态，UI 自己会展示错误与重试入口。
    }
  }

  Future<void> _unblock(BlockedUser user) async {
    if (_busy.contains(user.uid)) return;
    setState(() => _busy.add(user.uid));
    try {
      await ref.read(safetyApiProvider).unblockUser(user.uid);
      ref.invalidate(blacklistProvider);
      if (mounted) _snack('已解除对 ${user.displayName} 的拉黑');
    } on ApiException catch (error) {
      if (mounted) _snack(error.message);
    } catch (_) {
      if (mounted) _snack('操作失败，请稍后重试');
    } finally {
      if (mounted) setState(() => _busy.remove(user.uid));
    }
  }

  void _snack(String message) {
    ScaffoldMessenger.of(context)
        .showSnackBar(SnackBar(content: Text(message)));
  }

  @override
  Widget build(BuildContext context) {
    final async = ref.watch(blacklistProvider);

    return Scaffold(
      appBar: AppBar(title: const Text('黑名单')),
      body: RefreshIndicator(
        onRefresh: _refresh,
        child: async.when(
          loading: () => const Center(child: CircularProgressIndicator()),
          error: (error, _) => ListView(
            // RefreshIndicator 需要可滚动的子树，否则错误态下拉没有反馈。
            physics: const AlwaysScrollableScrollPhysics(),
            children: [
              SizedBox(
                height: MediaQuery.sizeOf(context).height * 0.6,
                child: Center(
                  child: Padding(
                    padding: const EdgeInsets.all(24),
                    child: Column(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        Text('加载失败：$error', textAlign: TextAlign.center),
                        const SizedBox(height: 12),
                        FilledButton(
                          onPressed: () => ref.invalidate(blacklistProvider),
                          child: const Text('重试'),
                        ),
                      ],
                    ),
                  ),
                ),
              ),
            ],
          ),
          data: (page) {
            if (page.isEmpty) return const _BlacklistEmptyView();
            return ListView.separated(
              physics: const AlwaysScrollableScrollPhysics(),
              padding: const EdgeInsets.symmetric(vertical: 8),
              itemCount: page.list.length,
              separatorBuilder: (_, __) =>
                  const Divider(height: 1, indent: 16, endIndent: 16),
              itemBuilder: (context, index) => _BlockedTile(
                  user: page.list[index], busy: _busy, onUnblock: _unblock),
            );
          },
        ),
      ),
    );
  }
}

class _BlockedTile extends StatelessWidget {
  const _BlockedTile({
    required this.user,
    required this.busy,
    required this.onUnblock,
  });

  final BlockedUser user;
  final Set<int> busy;
  final ValueChanged<BlockedUser> onUnblock;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final busyNow = busy.contains(user.uid);
    final blockedAt = _blockedAtText(user.blockedAt);

    return ListTile(
      contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
      leading: CircleAvatar(
        backgroundImage: ProfileImage.provider(user.avatar),
        child: user.avatar.isEmpty
            ? Text(
                user.displayName.isEmpty
                    ? '?'
                    : user.displayName.substring(0, 1),
              )
            : null,
      ),
      title:
          Text(user.displayName, maxLines: 1, overflow: TextOverflow.ellipsis),
      subtitle: blockedAt.isEmpty
          ? null
          : Text(
              '拉黑于 $blockedAt',
              style: theme.textTheme.bodySmall?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
              ),
            ),
      trailing: busyNow
          ? const SizedBox(
              width: 20,
              height: 20,
              child: CircularProgressIndicator(strokeWidth: 2),
            )
          : OutlinedButton(
              onPressed: () => onUnblock(user),
              child: const Text('解除拉黑'),
            ),
    );
  }
}

class _BlacklistEmptyView extends StatelessWidget {
  const _BlacklistEmptyView();

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return ListView(
      physics: const AlwaysScrollableScrollPhysics(),
      children: [
        SizedBox(
          height: MediaQuery.sizeOf(context).height * 0.6,
          child: Column(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              Icon(
                Icons.block_outlined,
                size: 48,
                color: theme.colorScheme.outline,
              ),
              const SizedBox(height: 14),
              Text('黑名单还是空的', style: theme.textTheme.titleSmall),
              const SizedBox(height: 6),
              Text(
                '在对方主页右上角可以把骚扰你的用户拉黑',
                style: theme.textTheme.bodySmall?.copyWith(
                  color: theme.colorScheme.onSurfaceVariant,
                ),
              ),
            ],
          ),
        ),
      ],
    );
  }
}

/// 拉黑时间。服务端这条接口给的是数据库字符串（"2026-10-31 12:00:00"），
/// 不像其他接口统一用毫秒时间戳，这里两种都接受；实在解析不了就原样展示。
String _blockedAtText(String raw) {
  final trimmed = raw.trim();
  if (trimmed.isEmpty) return '';
  final parsed = DateTime.tryParse(trimmed) ??
      DateTime.tryParse(trimmed.replaceFirst(' ', 'T'));
  if (parsed == null) return trimmed;
  return '${parsed.year}-${_two(parsed.month)}-${_two(parsed.day)}';
}

String _two(int value) => value.toString().padLeft(2, '0');
