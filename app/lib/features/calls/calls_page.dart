import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../shared/widgets/placeholder_view.dart';
import 'call_controller.dart';
import 'data/call_models.dart';

/// 通话记录。
///
/// 数据全部来自服务端 call_session：
/// 时长、计费分钟、消耗金币都以服务端结算结果为准，
/// 客户端不重新计算，避免"两边算出两个数"的客诉。
class CallsPage extends ConsumerWidget {
  const CallsPage({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final async = ref.watch(callRecordsProvider);
    return Scaffold(
      appBar: AppBar(title: const Text('通话')),
      body: RefreshIndicator(
        onRefresh: () async => ref.invalidate(callRecordsProvider),
        child: async.when(
          loading: () => const Center(child: CircularProgressIndicator()),
          error: (error, _) => ListView(
            padding: const EdgeInsets.all(16),
            children: [
              const SizedBox(height: 40),
              PlaceholderView(
                icon: Icons.error_outline,
                title: '通话记录加载失败',
                description: '$error',
              ),
              const SizedBox(height: 12),
              OutlinedButton(
                onPressed: () => ref.invalidate(callRecordsProvider),
                child: const Text('重试'),
              ),
            ],
          ),
          data: (list) => list.isEmpty
              ? ListView(
                  padding: const EdgeInsets.all(24),
                  children: const [
                    SizedBox(height: 40),
                    PlaceholderView(
                      icon: Icons.call_outlined,
                      title: '还没有通话记录',
                      description: '在他人主页发起语音或视频通话后，这里会显示时长与消费金币。',
                    ),
                  ],
                )
              : ListView.separated(
                  padding: const EdgeInsets.fromLTRB(8, 8, 8, 24),
                  itemCount: list.length,
                  separatorBuilder: (_, __) => const Divider(height: 1),
                  itemBuilder: (context, index) =>
                      _CallTile(session: list[index]),
                ),
        ),
      ),
    );
  }
}

class _CallTile extends StatelessWidget {
  const _CallTile({required this.session});

  final CallSession session;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final m = session.durationSec ~/ 60;
    final s = session.durationSec % 60;
    final duration =
        '${m.toString().padLeft(2, '0')}:${s.toString().padLeft(2, '0')}';
    return ListTile(
      leading: CircleAvatar(
        backgroundColor: theme.colorScheme.surfaceContainerHighest,
        child: Icon(session.isVideo ? Icons.videocam : Icons.call),
      ),
      title: Text('${session.isVideo ? '视频' : '语音'}通话 · $duration'),
      subtitle: Text(
        session.endReasonText.isEmpty ? '通话结束' : session.endReasonText,
        style: theme.textTheme.bodySmall?.copyWith(
          color: theme.colorScheme.onSurfaceVariant,
        ),
      ),
      trailing: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        crossAxisAlignment: CrossAxisAlignment.end,
        children: [
          Text(
            '-${session.totalCoins}',
            style: theme.textTheme.titleMedium?.copyWith(
              fontWeight: FontWeight.w600,
            ),
          ),
          Text(
            '${session.billableMinutes} 分钟',
            style: theme.textTheme.bodySmall?.copyWith(
              color: theme.colorScheme.onSurfaceVariant,
            ),
          ),
        ],
      ),
    );
  }
}
