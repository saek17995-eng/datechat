import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/config/app_env.dart';
import '../../core/di/providers.dart';
import '../../core/network/api_exception.dart';
import '../../core/storage/server_config_store.dart';

/// 后端地址设置面板。
///
/// 为什么要有它：打包时烧进去的地址是编译期常量，真机联调时电脑的局域网 IP
/// 会随 WiFi 变，换一次就重打一次包显然不现实。这里改一次就持久化下来。
///
/// 面板里带"测试连接"：直接打 /v1/ping，不依赖任何登录态，
/// 能在用户填完的第一时间告诉他对不对，而不是等他点"获取验证码"才发现连不上。
Future<void> showServerSettingsSheet(BuildContext context) {
  return showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    showDragHandle: true,
    builder: (sheetContext) => Padding(
      // 键盘弹起时把面板顶上去
      padding: EdgeInsets.only(
        bottom: MediaQuery.of(sheetContext).viewInsets.bottom,
      ),
      child: const _ServerSettingsBody(),
    ),
  );
}

class _ServerSettingsBody extends ConsumerStatefulWidget {
  const _ServerSettingsBody();

  @override
  ConsumerState<_ServerSettingsBody> createState() =>
      _ServerSettingsBodyState();
}

class _ServerSettingsBodyState extends ConsumerState<_ServerSettingsBody> {
  late final TextEditingController _controller;
  String? _result;
  bool _testing = false;
  bool _saving = false;

  @override
  void initState() {
    super.initState();
    _controller = TextEditingController(text: ref.read(apiBaseUrlProvider));
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  Future<void> _test() async {
    final input = _controller.text.trim();
    if (input.isEmpty) {
      setState(() => _result = '请先填写地址');
      return;
    }
    final url = ServerConfigStore.normalizeBaseUrl(input);
    setState(() {
      _testing = true;
      _result = null;
    });
    final stopwatch = Stopwatch()..start();
    try {
      final data = await _ping(url);
      stopwatch.stop();
      if (!mounted) return;
      setState(() {
        _result = '✓ 连接成功：${data['name'] ?? ''} ${data['env'] ?? ''}'
            '（${stopwatch.elapsedMilliseconds}ms）';
      });
    } on ApiException catch (error) {
      stopwatch.stop();
      if (!mounted) return;
      setState(() => _result = '✗ ${error.message}');
    } catch (error) {
      if (!mounted) return;
      setState(() => _result = '✗ 连接失败：$error');
    } finally {
      if (mounted) setState(() => _testing = false);
    }
  }

  /// 一次性的连通性探测：不改动任何全局状态，只验证地址能不能通。
  Future<Map<String, dynamic>> _ping(String baseUrl) async {
    final dio = Dio(
      BaseOptions(
        baseUrl: baseUrl,
        connectTimeout: const Duration(seconds: 6),
        receiveTimeout: const Duration(seconds: 6),
        headers: const {'Accept': 'application/json'},
        validateStatus: (status) => status != null && status < 500,
      ),
    );
    try {
      final response = await dio.get<Map<String, dynamic>>('/v1/ping');
      final body = response.data;
      if (body != null && body['code'] == 0 && body['data'] is Map) {
        return Map<String, dynamic>.from(body['data'] as Map);
      }
      throw ApiException(code: -1, message: '响应不是预期格式：$body');
    } on DioException catch (error) {
      throw ApiException.fromDio(error);
    } finally {
      dio.close();
    }
  }

  Future<void> _save() async {
    setState(() => _saving = true);
    final url =
        await ref.read(apiBaseUrlProvider.notifier).set(_controller.text);
    if (!mounted) return;
    setState(() => _saving = false);
    Navigator.of(context).pop();
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text('后端地址已更新为 $url')),
    );
  }

  Future<void> _reset() async {
    final url = await ref.read(apiBaseUrlProvider.notifier).reset();
    if (!mounted) return;
    setState(() => _controller.text = url);
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text('已恢复为打包默认值 $url')),
    );
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final usingOverride = ref.watch(
      apiBaseUrlProvider.notifier
          .select((controller) => controller.usingOverride),
    );

    return SafeArea(
      child: Padding(
        padding: const EdgeInsets.fromLTRB(20, 8, 20, 20),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Text('后端地址', style: theme.textTheme.titleMedium),
            const SizedBox(height: 6),
            Text(
              '填写电脑上后端服务的地址。手机和电脑要在同一个 WiFi 下，'
              '且地址不能是 127.0.0.1（那是手机自己）。',
              style: theme.textTheme.bodySmall?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
              ),
            ),
            const SizedBox(height: 14),
            TextField(
              controller: _controller,
              keyboardType: TextInputType.url,
              autocorrect: false,
              decoration: const InputDecoration(
                labelText: '地址',
                hintText: 'http://192.168.1.10:8080',
                border: OutlineInputBorder(),
                prefixIcon: Icon(Icons.dns_outlined),
              ),
            ),
            const SizedBox(height: 12),
            if (_result != null)
              Text(
                _result!,
                style: theme.textTheme.bodySmall?.copyWith(
                  color: _result!.startsWith('✓')
                      ? theme.colorScheme.primary
                      : theme.colorScheme.error,
                ),
              ),
            const SizedBox(height: 12),
            Row(
              children: [
                Expanded(
                  child: OutlinedButton.icon(
                    onPressed: _testing ? null : _test,
                    icon: _testing
                        ? const SizedBox(
                            width: 16,
                            height: 16,
                            child: CircularProgressIndicator(strokeWidth: 2),
                          )
                        : const Icon(Icons.network_check),
                    label: const Text('测试连接'),
                  ),
                ),
                const SizedBox(width: 12),
                Expanded(
                  child: FilledButton(
                    onPressed: _saving ? null : _save,
                    child: const Text('保存'),
                  ),
                ),
              ],
            ),
            Align(
              alignment: Alignment.centerLeft,
              child: TextButton.icon(
                onPressed: usingOverride ? _reset : null,
                icon: const Icon(Icons.restart_alt, size: 18),
                label: Text(
                  usingOverride ? '恢复默认（${AppEnv.builtInBaseUrl}）' : '当前已是默认值',
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}
