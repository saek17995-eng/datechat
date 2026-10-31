import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../auth/auth_controller.dart';
import '../auth/realname_sheet.dart';
import '../auth/data/auth_models.dart';
import '../nearby/nearby_page.dart';
import 'data/profile_models.dart';
import 'profile_controller.dart';
import 'profile_image.dart';

/// 「我的」页。
///
/// P2 在账号信息之上接入了资料：头像、昵称、性别、城市、签名与资料完整度。
/// P4 起「隐私与安全」进入黑名单管理页（拉黑/举报）。
/// 钱包（P5）、付费通话（P5/P6）仍为占位。
class ProfilePage extends ConsumerWidget {
  const ProfilePage({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final theme = Theme.of(context);
    final auth = ref.watch(authControllerProvider);
    final me = auth.me;
    final profileAsync = ref.watch(myProfileProvider);

    return Scaffold(
      appBar: AppBar(title: const Text('我的')),
      body: ListView(
        padding: const EdgeInsets.fromLTRB(16, 12, 16, 24),
        children: [
          _ProfileHeader(me: me, profileAsync: profileAsync),
          const SizedBox(height: 20),
          Card(
            clipBehavior: Clip.antiAlias,
            child: Column(
              children: [
                ListTile(
                  leading: const Icon(Icons.person_outline),
                  title: const Text('编辑资料'),
                  subtitle: const Text('头像、昵称、城市、签名与标签'),
                  trailing: const Icon(Icons.chevron_right),
                  onTap: () => context.push('/profile/edit'),
                ),
                const Divider(height: 1),
                ListTile(
                  leading: const Icon(Icons.remove_red_eye_outlined),
                  title: const Text('查看我的主页'),
                  subtitle: const Text('看看别人眼中的你'),
                  trailing: const Icon(Icons.chevron_right),
                  enabled: me != null,
                  onTap:
                      me == null ? null : () => context.push('/user/${me.uid}'),
                ),
                const Divider(height: 1),
                ListTile(
                  leading: const Icon(Icons.badge_outlined),
                  title: const Text('实名认证'),
                  subtitle: Text(me?.realnameText ?? '未认证'),
                  trailing: const Icon(Icons.chevron_right),
                  onTap: () async {
                    final status = await _resolveStatus(context, ref);
                    if (status == null || !context.mounted) return;
                    if (status.passed) {
                      ScaffoldMessenger.of(context).showSnackBar(
                        const SnackBar(content: Text('你已完成实名认证')),
                      );
                      return;
                    }
                    await showRealnameSheet(context);
                  },
                ),
                const Divider(height: 1),
                ListTile(
                  leading: const Icon(Icons.account_balance_wallet_outlined),
                  title: const Text('我的钱包'),
                  subtitle: const Text('金币余额、充值与流水'),
                  trailing: const Icon(Icons.chevron_right),
                  onTap: () => context.push('/wallet'),
                ),
                const Divider(height: 1),
                ListTile(
                  leading: const Icon(Icons.visibility_outlined),
                  title: const Text('谁看过我'),
                  subtitle: const Text('访客记录与未读提醒'),
                  trailing: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: const [
                      UnreadVisitsBadge(),
                      SizedBox(width: 6),
                      Icon(Icons.chevron_right),
                    ],
                  ),
                  onTap: () => context.push('/visits'),
                ),
                const Divider(height: 1),
                ListTile(
                  leading: const Icon(Icons.shield_outlined),
                  title: const Text('隐私与安全'),
                  subtitle: const Text('黑名单管理与内容举报'),
                  trailing: const Icon(Icons.chevron_right),
                  onTap: () => context.push('/blocks'),
                ),
              ],
            ),
          ),
          const SizedBox(height: 24),
          SizedBox(
            height: 48,
            child: OutlinedButton.icon(
              onPressed: () => _confirmLogout(context, ref),
              icon: const Icon(Icons.logout),
              label: const Text('退出登录'),
            ),
          ),
          const SizedBox(height: 16),
          Text(
            '账号 ID：${me?.uid ?? '-'}　${me?.phoneMasked ?? ''}',
            textAlign: TextAlign.center,
            style: theme.textTheme.bodySmall?.copyWith(
              color: theme.colorScheme.onSurfaceVariant,
            ),
          ),
        ],
      ),
    );
  }

  /// 打开表单前先查一次状态，避免已认证用户重复提交（服务端也会拦，但少一次无效请求）。
  Future<RealnameStatus?> _resolveStatus(
      BuildContext context, WidgetRef ref) async {
    try {
      return await ref.read(authApiProvider).realnameStatus();
    } catch (error) {
      if (context.mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('查询实名状态失败：$error')),
        );
      }
      return null;
    }
  }

  Future<void> _confirmLogout(BuildContext context, WidgetRef ref) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        title: const Text('退出登录'),
        content: const Text('退出后需要重新用手机号验证码登录，确定继续？'),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(dialogContext).pop(false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.of(dialogContext).pop(true),
            child: const Text('退出'),
          ),
        ],
      ),
    );
    if (confirmed != true) return;
    await ref.read(authControllerProvider.notifier).logout();
    // 路由 redirect 会把用户带回登录页，这里不需要手动跳转。
  }
}

class _ProfileHeader extends StatelessWidget {
  const _ProfileHeader({required this.me, required this.profileAsync});

  final MeInfo? me;
  final AsyncValue<ProfileInfo> profileAsync;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final nickname = me?.nickname ?? '';
    // 头像优先用资料接口返回的值：改完头像后 MeInfo 可能还没刷新回来。
    final avatar = profileAsync.maybeWhen(
      data: (profile) => profile.avatar,
      orElse: () => '',
    );
    final resolvedAvatar = avatar.isNotEmpty ? avatar : (me?.avatar ?? '');

    return Row(
      children: [
        CircleAvatar(
          radius: 34,
          backgroundImage: resolvedAvatar.isNotEmpty
              ? NetworkImage(ProfileImage.absUrl(resolvedAvatar))
              : null,
          child: resolvedAvatar.isEmpty
              ? Text(
                  nickname.isEmpty ? '?' : nickname.substring(0, 1),
                  style: theme.textTheme.headlineSmall,
                )
              : null,
        ),
        const SizedBox(width: 16),
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                nickname.isEmpty ? '未登录' : nickname,
                style: theme.textTheme.titleLarge,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
              ),
              const SizedBox(height: 4),
              Row(
                children: [
                  Chip(
                    label: Text(me?.genderText ?? '保密'),
                    visualDensity: VisualDensity.compact,
                    padding: EdgeInsets.zero,
                  ),
                  const SizedBox(width: 8),
                  if (me?.isRealnameVerified ?? false)
                    Chip(
                      avatar: const Icon(Icons.verified, size: 16),
                      label: const Text('已实名'),
                      visualDensity: VisualDensity.compact,
                      padding: EdgeInsets.zero,
                    ),
                ],
              ),
              const SizedBox(height: 8),
              profileAsync.when(
                loading: () => const SizedBox(height: 4),
                error: (_, __) => const SizedBox(height: 4),
                data: (profile) =>
                    _CompletenessBar(ratio: profile.completeness),
              ),
            ],
          ),
        ),
      ],
    );
  }
}

class _CompletenessBar extends StatelessWidget {
  const _CompletenessBar({required this.ratio});

  final double ratio;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final percent = (ratio * 100).round();
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        ClipRRect(
          borderRadius: BorderRadius.circular(4),
          child: LinearProgressIndicator(
            value: ratio,
            minHeight: 6,
            backgroundColor: theme.colorScheme.surfaceContainerHighest,
          ),
        ),
        const SizedBox(height: 4),
        Text(
          '资料完整度 $percent%',
          style: theme.textTheme.bodySmall?.copyWith(
            color: theme.colorScheme.onSurfaceVariant,
          ),
        ),
      ],
    );
  }
}
