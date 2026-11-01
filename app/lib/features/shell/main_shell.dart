import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../core/realtime/realtime_providers.dart';
import '../realtime/realtime_effects.dart';

/// 主框架：底部四 Tab。
///
/// body 直接渲染 navigationShell，由 StatefulShellRoute 负责各分支的状态保持。
/// 点击已在当前 Tab 时回到该分支的初始页（符合主流社交 App 交互）。
class MainShell extends ConsumerWidget {
  const MainShell({super.key, required this.shell});

  final StatefulNavigationShell shell;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    // 未读小红点：由实时通道收到新消息时累加，打开会话后清零。
    final unread = ref.watch(unreadTotalProvider);
    return Scaffold(
      // 实时通道的副作用（连接生命周期 + 新消息通知 + 来电跳转）挂在主框架上：
      // 它必须随 App 常驻，而不是只在消息页活着——否则用户不在消息页就收不到任何东西。
      body: RealtimeEffects(child: shell),
      bottomNavigationBar: NavigationBar(
        selectedIndex: shell.currentIndex,
        onDestinationSelected: (index) => shell.goBranch(
          index,
          initialLocation: index == shell.currentIndex,
        ),
        destinations: const [
          NavigationDestination(
            icon: Icon(Icons.explore_outlined),
            selectedIcon: Icon(Icons.explore),
            label: '附近',
          ),
          NavigationDestination(
            icon: _MessageIcon(unread: unread),
            selectedIcon: _MessageIcon(unread: unread, selected: true),
            label: '消息',
          ),
          NavigationDestination(
            icon: Icon(Icons.call_outlined),
            selectedIcon: Icon(Icons.call),
            label: '通话',
          ),
          NavigationDestination(
            icon: Icon(Icons.person_outline),
            selectedIcon: Icon(Icons.person),
            label: '我的',
          ),
        ],
      ),
    );
  }
}

/// 消息 Tab 图标，带未读数角标。
class _MessageIcon extends StatelessWidget {
  const _MessageIcon({required this.unread, this.selected = false});

  final int unread;
  final bool selected;

  @override
  Widget build(BuildContext context) {
    final icon = Icon(selected ? Icons.chat_bubble : Icons.chat_bubble_outline);
    if (unread <= 0) return icon;
    return Badge(
      label: Text(unread > 99 ? '99+' : '$unread'),
      child: icon,
    );
  }
}
