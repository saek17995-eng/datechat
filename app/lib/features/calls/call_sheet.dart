import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import 'call_controller.dart';
import 'data/call_models.dart';

/// 发起通话前的选择弹层。
///
/// 为什么不直接拨出去：音视频是两种价格（视频通常是语音的两倍以上），
/// 在按下之前把单价摆出来，能避免"接通才发现很贵"的差评与投诉。
Future<void> showCallSheet(
  BuildContext context, {
  required int uid,
  required String name,
  required String avatar,
  int priceAudio = 0,
  int priceVideo = 0,
}) {
  return showModalBottomSheet<void>(
    context: context,
    showDragHandle: true,
    builder: (sheetContext) => _CallSheet(
      uid: uid,
      name: name,
      avatar: avatar,
      priceAudio: priceAudio,
      priceVideo: priceVideo,
    ),
  );
}

class _CallSheet extends ConsumerWidget {
  const _CallSheet({
    required this.uid,
    required this.name,
    required this.avatar,
    required this.priceAudio,
    required this.priceVideo,
  });

  final int uid;
  final String name;
  final String avatar;

  /// 他人主页已经带回了定价，优先用它，省掉一次请求。
  final int priceAudio;
  final int priceVideo;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final theme = Theme.of(context);
    // 主页没带资费时才单独查一次。
    final needFetch = priceAudio <= 0 || priceVideo <= 0;
    final fetched = needFetch
        ? ref
            .watch(_callPriceProvider(uid))
            .maybeWhen(data: (p) => p, orElse: () => null)
        : null;

    final audio = priceAudio > 0 ? priceAudio : (fetched?.perMin ?? 0);
    final video = priceVideo > 0 ? priceVideo : _estimateVideo(audio);

    return SafeArea(
      child: Padding(
        padding: const EdgeInsets.fromLTRB(20, 8, 20, 24),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text('呼叫 $name', style: theme.textTheme.titleMedium),
            const SizedBox(height: 4),
            Text(
              audio <= 0 ? '正在获取通话资费…' : '语音 $audio 金币/分钟 · 视频 $video 金币/分钟',
              style: theme.textTheme.bodySmall?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
              ),
            ),
            const SizedBox(height: 16),
            Row(
              children: [
                Expanded(
                  child: OutlinedButton.icon(
                    onPressed: () => _start(context, video: false),
                    icon: const Icon(Icons.call),
                    label: const Text('语音通话'),
                  ),
                ),
                const SizedBox(width: 12),
                Expanded(
                  child: FilledButton.icon(
                    onPressed: () => _start(context, video: true),
                    icon: const Icon(Icons.videocam),
                    label: const Text('视频通话'),
                  ),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }

  /// 视频价没有单独接口，这里按常见倍率（约 2.4 倍）估算展示，
  /// 实际扣费以通话详情里的 price_per_min 为准。
  int _estimateVideo(int audioPrice) => (audioPrice * 2.4).round();

  void _start(BuildContext context, {required bool video}) {
    Navigator.of(context).pop();
    context.push(
      '/call',
      extra: <String, dynamic>{
        'uid': uid,
        'name': name,
        'avatar': avatar,
        'video': video,
      },
    );
  }
}

/// 语音通话资费。只在弹层打开期间缓存，关闭即释放。
final _callPriceProvider =
    FutureProvider.autoDispose.family<CallPrice, int>((ref, uid) async {
  return ref.watch(callApiProvider).price(uid);
});
