import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:intl/intl.dart';

import '../../shared/widgets/placeholder_view.dart';
import '../profile/profile_image.dart';
import 'data/nearby_api.dart';
import 'nearby_controller.dart';

/// 谁看过我。
///
/// 进页面即标记已读：未读数的意义就是"你还没看"，
/// 页面都打开了还留着红点，等于自己骗自己。
class VisitsPage extends ConsumerStatefulWidget {
  const VisitsPage({super.key});

  @override
  ConsumerState<VisitsPage> createState() => _VisitsPageState();
}

class _VisitsPageState extends ConsumerState<VisitsPage> {
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _markRead());
  }

  Future<void> _markRead() async {
    try {
      await ref.read(nearbyApiProvider).markVisitsRead();
    } catch (_) {
      // 标记失败不影响看列表，下次进来还会再试。
      return;
    }
    if (mounted) ref.invalidate(visitsProvider);
  }

  @override
  Widget build(BuildContext context) {
    final async = ref.watch(visitsProvider);

    return Scaffold(
      appBar: AppBar(title: const Text('谁看过我')),
      body: RefreshIndicator(
        onRefresh: () async => ref.invalidate(visitsProvider),
        child: async.when(
          loading: () => const Center(child: CircularProgressIndicator()),
          error: (error, _) => ListView(
            padding: const EdgeInsets.all(24),
            children: [
              const SizedBox(height: 80),
              PlaceholderView(
                icon: Icons.error_outline,
                title: '加载失败',
                description: '$error',
                actionLabel: '重试',
                onAction: () => ref.invalidate(visitsProvider),
              ),
            ],
          ),
          data: (page) {
            if (page.items.isEmpty) {
              return ListView(
                padding: const EdgeInsets.all(24),
                children: const [
                  SizedBox(height: 80),
                  PlaceholderView(
                    icon: Icons.visibility_outlined,
                    title: '还没有人看过你',
                    description: '完善头像与签名，会更容易出现在别人的附近列表里。',
                  ),
                ],
              );
            }
            return ListView.separated(
              padding: const EdgeInsets.fromLTRB(16, 12, 16, 24),
              itemCount: page.items.length,
              separatorBuilder: (_, __) => const Divider(height: 1),
              itemBuilder: (context, index) =>
                  _VisitTile(item: page.items[index]),
            );
          },
        ),
      ),
    );
  }
}

class _VisitTile extends StatelessWidget {
  const _VisitTile({required this.item});

  final VisitItem item;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;

    return ListTile(
      contentPadding: EdgeInsets.zero,
      leading: Stack(
        children: [
          CircleAvatar(
            radius: 24,
            backgroundImage:
                item.avatar.isEmpty ? null : ProfileImage.provider(item.avatar),
            backgroundColor: scheme.surfaceContainerHighest,
            child: item.avatar.isEmpty ? const Icon(Icons.person) : null,
          ),
          if (item.online)
            Positioned(
              right: 0,
              bottom: 0,
              child: Container(
                width: 10,
                height: 10,
                decoration: BoxDecoration(
                  color: Colors.green,
                  shape: BoxShape.circle,
                  border: Border.all(color: scheme.surface, width: 2),
                ),
              ),
            ),
        ],
      ),
      title: Row(
        children: [
          Flexible(
            child: Text(
              item.nickname.isEmpty ? '聊友' : item.nickname,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
            ),
          ),
          if (item.unread) ...[
            const SizedBox(width: 6),
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
              decoration: BoxDecoration(
                color: scheme.error,
                borderRadius: BorderRadius.circular(8),
              ),
              child: const Text(
                '新',
                style: TextStyle(color: Colors.white, fontSize: 10),
              ),
            ),
          ],
        ],
      ),
      subtitle: Text(
        [
          if (item.age > 0) '${item.age} 岁',
          if (item.cityName.isNotEmpty) item.cityName,
          _formatTime(item.visitedAt),
        ].join(' · '),
        style: theme.textTheme.bodySmall?.copyWith(
          color: scheme.onSurfaceVariant,
        ),
      ),
      trailing: const Icon(Icons.chevron_right),
      onTap: () => context.push('/user/${item.visitorId}'),
    );
  }
}

/// 服务端下的是 RFC3339 字符串（可能带时区），解析失败就原样展示，
/// 不要因为格式问题把整行时间吞掉。
String _formatTime(String raw) {
  final parsed = DateTime.tryParse(raw);
  if (parsed == null) return raw;
  final local = parsed.toLocal();
  final now = DateTime.now();
  final sameDay = local.year == now.year &&
      local.month == now.month &&
      local.day == now.day;
  return sameDay
      ? '今天 ${DateFormat('HH:mm').format(local)}'
      : DateFormat('MM-dd HH:mm').format(local);
}
