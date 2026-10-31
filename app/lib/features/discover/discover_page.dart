import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../core/config/app_env.dart';
import '../../core/di/providers.dart';
import '../../features/nearby/nearby_page.dart';

/// 「附近」Tab。
/// P0 阶段除了占位说明，还承担一个真实职责：自检客户端到后端的连通性，
/// 这样一跑起来就能确认整条链路（Flutter → Dio → Go → 响应解包）是通的。
class DiscoverPage extends ConsumerWidget {
  const DiscoverPage({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final serverInfo = ref.watch(serverInfoProvider);

    return Scaffold(
      appBar: AppBar(title: const Text('附近')),
      body: RefreshIndicator(
        onRefresh: () async => ref.invalidate(serverInfoProvider),
        child: ListView(
          padding: const EdgeInsets.fromLTRB(16, 12, 16, 24),
          children: [
            _ConnectionCard(
              info: serverInfo,
              onRetry: () => ref.invalidate(serverInfoProvider),
            ),
            const SizedBox(height: 12),
            // 附近的人要等 P7 的 Redis GEO；在那之前用 UID 直查主页，
            // 保证"他人主页"这条链路随时可以点开验证。
            const _UidLookupCard(),
            const SizedBox(height: 12),
            // P7：真实列表。附近的人由后端 Redis GEO 检索，距离只展示区间。
            const NearbySection(),
          ],
        ),
      ),
    );
  }
}

/// 按 UID 直查主页。
///
/// 附近的人列表要等 P7（Redis GEO）。在那之前留一个入口，
/// 让"他人主页 / 在线状态 / 访问记录"这条链路随时能点开验证。
class _UidLookupCard extends StatefulWidget {
  const _UidLookupCard();

  @override
  State<_UidLookupCard> createState() => _UidLookupCardState();
}

class _UidLookupCardState extends State<_UidLookupCard> {
  final _controller = TextEditingController();

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Card(
      child: Padding(
        padding: const EdgeInsets.fromLTRB(16, 12, 16, 16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              '按 UID 查看主页（联调用）',
              style: theme.textTheme.titleSmall?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
              ),
            ),
            const SizedBox(height: 10),
            Row(
              children: [
                Expanded(
                  child: TextField(
                    controller: _controller,
                    keyboardType: TextInputType.number,
                    decoration: const InputDecoration(
                      hintText: '输入用户 UID',
                      isDense: true,
                    ),
                  ),
                ),
                const SizedBox(width: 12),
                FilledButton(
                  onPressed: () {
                    final uid = int.tryParse(_controller.text.trim());
                    if (uid == null || uid <= 0) {
                      ScaffoldMessenger.of(context).showSnackBar(
                        const SnackBar(content: Text('请输入正确的 UID')),
                      );
                      return;
                    }
                    context.push('/user/$uid');
                  },
                  child: const Text('查看'),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

class _ConnectionCard extends StatelessWidget {
  const _ConnectionCard({required this.info, required this.onRetry});

  final AsyncValue<Map<String, dynamic>> info;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;

    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: scheme.surfaceContainerLow,
        borderRadius: BorderRadius.circular(16),
        border: Border.all(color: scheme.outlineVariant.withValues(alpha: 0.6)),
      ),
      child: info.when(
        loading: () => const Row(
          children: [
            SizedBox(
              width: 16,
              height: 16,
              child: CircularProgressIndicator(strokeWidth: 2),
            ),
            SizedBox(width: 12),
            Text('正在连接后端…'),
          ],
        ),
        data: (data) => Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Icon(Icons.check_circle, size: 18, color: scheme.primary),
                const SizedBox(width: 8),
                const Text(
                  '后端已连通',
                  style: TextStyle(fontWeight: FontWeight.w600),
                ),
              ],
            ),
            const SizedBox(height: 10),
            _kv('服务', '${data['name'] ?? '-'}  v${data['version'] ?? '-'}'),
            _kv('环境', '${data['env'] ?? '-'}'),
            _kv('服务器时间', '${data['server_time'] ?? '-'}'),
            _kv('接口地址', AppEnv.current.apiBaseUrl),
          ],
        ),
        error: (error, _) => Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Icon(Icons.error_outline, size: 18, color: scheme.error),
                const SizedBox(width: 8),
                const Text(
                  '后端未连通',
                  style: TextStyle(fontWeight: FontWeight.w600),
                ),
              ],
            ),
            const SizedBox(height: 8),
            Text(
              '$error',
              style: TextStyle(fontSize: 12, color: scheme.onSurfaceVariant),
            ),
            const SizedBox(height: 4),
            Text(
              '请确认已启动 server（make run），地址：${AppEnv.current.apiBaseUrl}',
              style: TextStyle(fontSize: 12, color: scheme.onSurfaceVariant),
            ),
            const SizedBox(height: 10),
            TextButton.icon(
              onPressed: onRetry,
              icon: const Icon(Icons.refresh, size: 16),
              label: const Text('重试'),
            ),
          ],
        ),
      ),
    );
  }

  Widget _kv(String key, String value) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 4),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 76,
            child: Text(
              key,
              style: const TextStyle(fontSize: 12, color: Color(0xFF8A8A8E)),
            ),
          ),
          Expanded(
            child: Text(value, style: const TextStyle(fontSize: 12)),
          ),
        ],
      ),
    );
  }
}
