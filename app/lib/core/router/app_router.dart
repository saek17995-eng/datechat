import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../features/auth/auth_controller.dart';
import '../../features/auth/login_page.dart';
import '../../features/calls/call_page.dart';
import '../../features/calls/calls_page.dart';
import '../../features/chat/chat_page.dart';
import '../../features/discover/discover_page.dart';
import '../../features/messages/messages_page.dart';
import '../../features/nearby/visits_page.dart';
import '../../features/profile/blacklist_page.dart';
import '../../features/profile/edit_profile_page.dart';
import '../../features/profile/profile_page.dart';
import '../../features/profile/user_page.dart';
import '../../features/shell/main_shell.dart';
import '../../features/wallet/wallet_page.dart';

/// 全局路由。
///
/// 用 StatefulShellRoute.indexedStack 而不是 ShellRoute：
/// 四个 Tab 各自保留独立的导航栈与滚动位置，切换回来不会重建页面。
///
/// 登录守卫放在 redirect 里而不是每个页面各写一遍：
/// 冷启动恢复会话期间（AuthStatus.unknown）不做跳转，避免闪一下登录页。
final routerProvider = Provider<GoRouter>((ref) {
  final refresh = _RouterRefreshNotifier();
  ref.listen<AuthState>(authControllerProvider, (_, __) => refresh.ping());
  ref.onDispose(refresh.dispose);

  return GoRouter(
    initialLocation: '/discover',
    refreshListenable: refresh,
    redirect: (context, state) {
      final auth = ref.read(authControllerProvider);
      final onLoginPage = state.matchedLocation == '/login';

      // 还在恢复会话：保持现状，别把用户先扔到登录页再弹回来。
      if (auth.status == AuthStatus.unknown) return null;

      if (auth.status == AuthStatus.unauthenticated) {
        return onLoginPage ? null : '/login';
      }
      // 已登录却停在登录页（比如刚登录成功），直接进首页。
      if (onLoginPage) return '/discover';
      return null;
    },
    routes: [
      GoRoute(
        path: '/login',
        builder: (context, state) => const LoginPage(),
      ),
      // 资料编辑与他人主页是覆盖在底部导航之上的全屏页，
      // 不放进 StatefulShellRoute：它们不属于任何 Tab，返回后 Tab 状态保持不变。
      GoRoute(
        path: '/profile/edit',
        builder: (context, state) => const EditProfilePage(),
      ),
      GoRoute(
        path: '/user/:uid',
        builder: (context, state) {
          final uid = int.tryParse(state.pathParameters['uid'] ?? '') ?? 0;
          return UserPage(uid: uid);
        },
      ),
      GoRoute(
        path: '/blocks',
        builder: (context, state) => const BlacklistPage(),
      ),
      GoRoute(
        path: '/chat/:peerUid',
        builder: (context, state) {
          final uid = int.tryParse(state.pathParameters['peerUid'] ?? '') ?? 0;
          return ChatPage(peerUid: uid);
        },
      ),
      // P5：钱包与充值
      GoRoute(
        path: '/wallet',
        builder: (context, state) => const WalletPage(),
      ),
      // P7：谁看过我
      GoRoute(
        path: '/visits',
        builder: (context, state) => const VisitsPage(),
      ),
      // P6：通话页。呼叫参数通过 extra 传递（含对方昵称头像），
      // 不塞进 URL——昵称里可能有特殊字符，拼进路径会被编码成乱码。
      GoRoute(
        path: '/call',
        builder: (context, state) {
          final extra = state.extra is Map<String, dynamic>
              ? state.extra! as Map<String, dynamic>
              : <String, dynamic>{};
          return CallPage(
            peerUid: extra['uid'] is int
                ? extra['uid'] as int
                : int.tryParse('${extra['uid']}') ?? 0,
            callNo: (extra['call_no'] ?? '').toString(),
            peerName: (extra['name'] ?? '').toString(),
            peerAvatar: (extra['avatar'] ?? '').toString(),
            isVideo: extra['video'] == true,
            autoAccept: extra['auto_accept'] == true,
          );
        },
      ),
      StatefulShellRoute.indexedStack(
        builder: (context, state, navigationShell) =>
            MainShell(shell: navigationShell),
        branches: [
          StatefulShellBranch(
            routes: [
              GoRoute(
                path: '/discover',
                builder: (context, state) => const DiscoverPage(),
              ),
            ],
          ),
          StatefulShellBranch(
            routes: [
              GoRoute(
                path: '/messages',
                builder: (context, state) => const MessagesPage(),
              ),
            ],
          ),
          StatefulShellBranch(
            routes: [
              GoRoute(
                path: '/calls',
                builder: (context, state) => const CallsPage(),
              ),
            ],
          ),
          StatefulShellBranch(
            routes: [
              GoRoute(
                path: '/profile',
                builder: (context, state) => const ProfilePage(),
              ),
            ],
          ),
        ],
      ),
    ],
  );
});

/// 把 Riverpod 的状态变化桥接成 GoRouter 需要的 Listenable。
class _RouterRefreshNotifier extends ChangeNotifier {
  void ping() => notifyListeners();
}
