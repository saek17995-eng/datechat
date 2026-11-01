import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'core/config/app_env.dart';
import 'core/di/providers.dart';
import 'core/notification/notification_service.dart';
import 'core/router/app_router.dart';
import 'core/storage/server_config_store.dart';
import 'core/storage/token_store.dart';
import 'core/theme/app_theme.dart';

/// 启动流程：
///   1. 绑定 Flutter 引擎；
///   2. 初始化需要异步准备的依赖（本地存储）；
///   3. 用 override 注入到 Riverpod 容器；
///   4. 启动根组件。
///
/// 注意：不要在这里做网络请求，否则冷启动会被网络拖慢甚至白屏。
/// 会话恢复由 AuthController 在首帧后异步完成。
Future<void> bootstrap() async {
  WidgetsFlutterBinding.ensureInitialized();

  final tokenStore = await TokenStore.create();
  final serverConfig = await ServerConfigStore.create();

  // 通知渠道与 Android 13+ 的运行时权限都要在 runApp 之前准备好，
  // 否则第一条消息推过来时还没权限，用户什么都看不到。
  // 失败不能阻断启动：通知只是增强，没有它 App 照样能用。
  final notifications = NotificationService();
  try {
    await notifications.init();
  } catch (_) {
    // 忽略：某些机型/系统版本不支持，降级为"只有 App 内提醒"
  }

  // 用户在 App 里改过的后端地址优先于打包值
  AppEnv.init(overrideBaseUrl: serverConfig.baseUrlOverride);

  runApp(
    ProviderScope(
      overrides: [
        tokenStoreProvider.overrideWithValue(tokenStore),
        serverConfigStoreProvider.overrideWithValue(serverConfig),
        notificationServiceProvider.overrideWithValue(notifications),
      ],
      child: const DateChatApp(),
    ),
  );
}

/// 应用根组件。路由从 provider 取，这样登录态变化才能驱动 redirect。
class DateChatApp extends ConsumerWidget {
  const DateChatApp({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return MaterialApp.router(
      title: 'DateChat',
      debugShowCheckedModeBanner: false,
      theme: AppTheme.light,
      darkTheme: AppTheme.dark,
      themeMode: ThemeMode.light,
      routerConfig: ref.watch(routerProvider),
    );
  }
}
