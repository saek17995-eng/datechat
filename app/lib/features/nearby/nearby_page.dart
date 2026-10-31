import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../shared/widgets/placeholder_view.dart';
import '../profile/profile_image.dart';
import 'data/nearby_api.dart';
import 'location_source.dart';
import 'nearby_controller.dart';

/// 附近的人列表（嵌在「附近」Tab 里）。
///
/// 两条展示上的硬规则，都是隐私相关的：
///   1. 距离只展示服务端给的区间文本，客户端不自己算、也不缓存精确值；
///   2. 离线用户整卡降透明度并打灰标——让人一眼看出"现在叫不太可能接通"。
class NearbySection extends ConsumerStatefulWidget {
  const NearbySection({super.key});

  @override
  ConsumerState<NearbySection> createState() => _NearbySectionState();
}

class _NearbySectionState extends ConsumerState<NearbySection> {
  @override
  void initState() {
    super.initState();
    // 在线心跳在这里起步：进了附近页说明用户活跃，顺带续上在线状态。
    ref.read(heartbeatProvider);
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) {
        unawaited(ref.read(nearbyControllerProvider.notifier).refresh());
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    final state = ref.watch(nearbyControllerProvider);

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        _FilterBar(
          filter: state.filter,
          onChanged: (filter) => unawaited(
            ref.read(nearbyControllerProvider.notifier).setFilter(filter),
          ),
          onPin: () => _showPinSheet(context),
          coords: state.coords,
          fromDevice: state.fromDevice,
        ),
        const SizedBox(height: 12),
        if (state.loading && state.items.isEmpty)
          const Padding(
            padding: EdgeInsets.symmetric(vertical: 32),
            child: Center(child: CircularProgressIndicator()),
          ),
        if (state.error != null && state.items.isEmpty)
          PlaceholderView(
            icon: Icons.location_off_outlined,
            title: '暂时取不到附近的人',
            description: state.error!,
            actionLabel: '重试',
            onAction: () => unawaited(
              ref.read(nearbyControllerProvider.notifier).refresh(),
            ),
          ),
        if (!state.loading && state.error == null && state.items.isEmpty)
          PlaceholderView(
            icon: Icons.people_outline,
            title: '附近还没有人',
            description: state.filter.isEmpty
                ? '半径内暂时没有其他人，换个时间或放宽筛选再看看。'
                : '当前筛选条件下没有人，试试去掉性别或年龄条件。',
          ),
        ...state.items.map(
          (user) => Padding(
            padding: const EdgeInsets.only(bottom: 10),
            child: _NearbyCard(user: user),
          ),
        ),
      ],
    );
  }

  Future<void> _showPinSheet(BuildContext context) async {
    await showModalBottomSheet<void>(
      context: context,
      showDragHandle: true,
      builder: (sheetContext) => const _PinSheet(),
    );
  }
}

/// 筛选条：性别 + 年龄段 + 坐标入口。
class _FilterBar extends StatelessWidget {
  const _FilterBar({
    required this.filter,
    required this.onChanged,
    required this.onPin,
    required this.coords,
    required this.fromDevice,
  });

  final NearbyFilter filter;
  final ValueChanged<NearbyFilter> onChanged;
  final VoidCallback onPin;
  final Coords? coords;
  final bool fromDevice;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            Text('筛选', style: theme.textTheme.titleSmall),
            const Spacer(),
            TextButton.icon(
              onPressed: onPin,
              icon: Icon(
                  fromDevice ? Icons.my_location : Icons.edit_location_alt,
                  size: 16),
              label: Text(fromDevice ? '当前定位' : '设置坐标'),
            ),
          ],
        ),
        const SizedBox(height: 6),
        Wrap(
          spacing: 8,
          children: [
            _Chip(
              label: '不限性别',
              selected: filter.gender == 0,
              onTap: () => onChanged(filter.copyWith(gender: 0)),
            ),
            _Chip(
              label: '只看女',
              selected: filter.gender == 2,
              onTap: () => onChanged(filter.copyWith(gender: 2)),
            ),
            _Chip(
              label: '只看男',
              selected: filter.gender == 1,
              onTap: () => onChanged(filter.copyWith(gender: 1)),
            ),
            _Chip(
              label: '不限年龄',
              selected: filter.ageMin <= 0 && filter.ageMax <= 0,
              onTap: () => onChanged(filter.copyWith(ageMin: 0, ageMax: 0)),
            ),
            _Chip(
              label: '18-25 岁',
              selected: filter.ageMin == 18 && filter.ageMax == 25,
              onTap: () => onChanged(filter.copyWith(ageMin: 18, ageMax: 25)),
            ),
            _Chip(
              label: '26-35 岁',
              selected: filter.ageMin == 26 && filter.ageMax == 35,
              onTap: () => onChanged(filter.copyWith(ageMin: 26, ageMax: 35)),
            ),
          ],
        ),
        if (coords != null) ...[
          const SizedBox(height: 6),
          Text(
            '${fromDevice ? 'GPS' : '手动'}坐标：$coords（距离由服务端按此计算）',
            style: theme.textTheme.bodySmall?.copyWith(
              color: theme.colorScheme.onSurfaceVariant,
            ),
          ),
        ],
      ],
    );
  }
}

class _Chip extends StatelessWidget {
  const _Chip({
    required this.label,
    required this.selected,
    required this.onTap,
  });

  final String label;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return FilterChip(
      label: Text(label),
      selected: selected,
      visualDensity: VisualDensity.compact,
      onSelected: (_) => onTap(),
    );
  }
}

/// 单个用户卡片。离线时整卡降到 55% 透明度，并给出灰色"离线"标。
class _NearbyCard extends StatelessWidget {
  const _NearbyCard({required this.user});

  final NearbyUser user;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;

    return Opacity(
      opacity: user.online ? 1 : 0.55,
      child: Card(
        clipBehavior: Clip.antiAlias,
        child: InkWell(
          onTap: () => context.push('/user/${user.uid}'),
          child: Padding(
            padding: const EdgeInsets.fromLTRB(12, 12, 12, 12),
            child: Row(
              children: [
                _AvatarWithDot(avatar: user.avatar, online: user.online),
                const SizedBox(width: 12),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Row(
                        children: [
                          Flexible(
                            child: Text(
                              user.nickname.isEmpty ? '聊友' : user.nickname,
                              style: theme.textTheme.titleSmall,
                              maxLines: 1,
                              overflow: TextOverflow.ellipsis,
                            ),
                          ),
                          if (user.realname) ...[
                            const SizedBox(width: 4),
                            Icon(Icons.verified,
                                size: 14, color: scheme.primary),
                          ],
                        ],
                      ),
                      const SizedBox(height: 4),
                      Text(
                        [
                          if (user.age > 0) '${user.age} 岁',
                          user.genderText,
                          if (user.cityName.isNotEmpty) user.cityName,
                        ].join(' · '),
                        style: theme.textTheme.bodySmall?.copyWith(
                          color: scheme.onSurfaceVariant,
                        ),
                      ),
                      const SizedBox(height: 4),
                      Text(
                        user.online ? '在线 · 可立即呼叫' : user.lastActiveText,
                        style: theme.textTheme.bodySmall?.copyWith(
                          color: user.online
                              ? Colors.green.shade700
                              : scheme.onSurfaceVariant,
                        ),
                      ),
                    ],
                  ),
                ),
                const SizedBox(width: 8),
                Column(
                  crossAxisAlignment: CrossAxisAlignment.end,
                  children: [
                    Text(
                      user.distanceText,
                      style: theme.textTheme.bodySmall?.copyWith(
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                    const SizedBox(height: 6),
                    Text(
                      '语音 ${user.priceAudio}/分',
                      style: theme.textTheme.bodySmall?.copyWith(
                        color: scheme.onSurfaceVariant,
                      ),
                    ),
                    Text(
                      '视频 ${user.priceVideo}/分',
                      style: theme.textTheme.bodySmall?.copyWith(
                        color: scheme.onSurfaceVariant,
                      ),
                    ),
                  ],
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

class _AvatarWithDot extends StatelessWidget {
  const _AvatarWithDot({required this.avatar, required this.online});

  final String avatar;
  final bool online;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Stack(
      children: [
        CircleAvatar(
          radius: 28,
          backgroundImage:
              avatar.isEmpty ? null : ProfileImage.provider(avatar),
          backgroundColor: scheme.surfaceContainerHighest,
          child: avatar.isEmpty ? const Icon(Icons.person, size: 28) : null,
        ),
        Positioned(
          right: 0,
          bottom: 0,
          child: Container(
            width: 12,
            height: 12,
            decoration: BoxDecoration(
              color: online ? Colors.green : Colors.grey,
              shape: BoxShape.circle,
              border: Border.all(color: scheme.surface, width: 2),
            ),
          ),
        ),
      ],
    );
  }
}

/// 手动设置坐标。真机接了 GPS 之后这个入口会被隐藏，
/// 但在模拟器和未授权定位的场景下，它是唯一能把链路跑通的办法。
class _PinSheet extends ConsumerStatefulWidget {
  const _PinSheet();

  @override
  ConsumerState<_PinSheet> createState() => _PinSheetState();
}

class _PinSheetState extends ConsumerState<_PinSheet> {
  final _lng = TextEditingController();
  final _lat = TextEditingController();

  @override
  void dispose() {
    _lng.dispose();
    _lat.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final current = ref.watch(nearbyControllerProvider).coords;
    if (_lng.text.isEmpty && current != null) {
      _lng.text = current.lng.toStringAsFixed(4);
      _lat.text = current.lat.toStringAsFixed(4);
    }

    return SafeArea(
      child: Padding(
        padding: const EdgeInsets.fromLTRB(20, 8, 20, 24),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Text('设置坐标', style: TextStyle(fontWeight: FontWeight.w600)),
            const SizedBox(height: 6),
            const Text(
              '真机接入 GPS 后由系统定位自动上报；这里用于在模拟器和未授权场景下联调。',
              style: TextStyle(fontSize: 12, color: Color(0xFF8A8A8E)),
            ),
            const SizedBox(height: 14),
            Row(
              children: [
                Expanded(
                  child: TextField(
                    controller: _lng,
                    keyboardType: const TextInputType.numberWithOptions(
                      signed: true,
                      decimal: true,
                    ),
                    decoration: const InputDecoration(
                      labelText: '经度',
                      isDense: true,
                    ),
                  ),
                ),
                const SizedBox(width: 12),
                Expanded(
                  child: TextField(
                    controller: _lat,
                    keyboardType: const TextInputType.numberWithOptions(
                      signed: true,
                      decimal: true,
                    ),
                    decoration: const InputDecoration(
                      labelText: '纬度',
                      isDense: true,
                    ),
                  ),
                ),
              ],
            ),
            const SizedBox(height: 16),
            SizedBox(
              width: double.infinity,
              height: 44,
              child: FilledButton(
                onPressed: () async {
                  final lng = double.tryParse(_lng.text.trim());
                  final lat = double.tryParse(_lat.text.trim());
                  if (lng == null || lat == null) {
                    ScaffoldMessenger.of(context).showSnackBar(
                      const SnackBar(content: Text('请输入合法经纬度')),
                    );
                    return;
                  }
                  final coords = Coords(lng: lng, lat: lat);
                  if (!coords.valid) {
                    ScaffoldMessenger.of(context).showSnackBar(
                      const SnackBar(content: Text('经纬度越界（且不能是 0,0）')),
                    );
                    return;
                  }
                  await ref.read(nearbyControllerProvider.notifier).pin(coords);
                  if (context.mounted) Navigator.of(context).pop();
                },
                child: const Text('保存并刷新'),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// 顶部常驻：让"我的"页和附近页共用未读访客红点。
class UnreadVisitsBadge extends ConsumerWidget {
  const UnreadVisitsBadge({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final unread = ref.watch(unreadVisitsProvider);
    if (unread <= 0) return const SizedBox.shrink();
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: Theme.of(context).colorScheme.error,
        borderRadius: BorderRadius.circular(10),
      ),
      child: Text(
        unread > 99 ? '99+' : '$unread',
        style: const TextStyle(color: Colors.white, fontSize: 11),
      ),
    );
  }
}
