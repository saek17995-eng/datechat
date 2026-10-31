import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/network/api_exception.dart';
import 'auth_controller.dart';
import 'data/auth_models.dart';

/// 弹出实名认证表单。
///
/// 两种结果：
///   - 服务端同步返回"已认证"（mock 通道或人工审核）：直接刷新用户态；
///   - 返回 need_client_action（腾讯云通道）：提示用户打开 H5 完成活体检测，
///     完成后由客户端回查（P1 只把入口留出来，H5 容器在 P2 接入）。
Future<void> showRealnameSheet(BuildContext context) {
  return showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    showDragHandle: true,
    builder: (_) => const _RealnameSheet(),
  );
}

class _RealnameSheet extends ConsumerStatefulWidget {
  const _RealnameSheet();

  @override
  ConsumerState<_RealnameSheet> createState() => _RealnameSheetState();
}

class _RealnameSheetState extends ConsumerState<_RealnameSheet> {
  final TextEditingController _name = TextEditingController();
  final TextEditingController _idCard = TextEditingController();

  bool _submitting = false;
  String? _error;
  RealnameStatus? _result;

  @override
  void dispose() {
    _name.dispose();
    _idCard.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    final name = _name.text.trim();
    final idCard = _idCard.text.trim().toUpperCase();
    if (name.length < 2) {
      setState(() => _error = '请输入真实姓名');
      return;
    }
    if (idCard.length != 18) {
      setState(() => _error = '请输入 18 位身份证号');
      return;
    }

    setState(() {
      _submitting = true;
      _error = null;
    });
    try {
      final status = await ref.read(authApiProvider).submitRealname(
            realName: name,
            idCard: idCard,
          );
      if (!mounted) return;
      setState(() => _result = status);
      if (status.passed) {
        await ref.read(authControllerProvider.notifier).refreshMe();
      }
    } on ApiException catch (error) {
      if (mounted) setState(() => _error = error.message);
    } finally {
      if (mounted) setState(() => _submitting = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final result = _result;

    return Padding(
      padding: EdgeInsets.only(
        left: 24,
        right: 24,
        top: 8,
        bottom: MediaQuery.of(context).viewInsets.bottom + 24,
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('实名认证', style: theme.textTheme.titleLarge),
          const SizedBox(height: 6),
          Text(
            '仅用于身份核验与未成年人保护，姓名与证件号加密存储，不会展示给其他用户。',
            style: theme.textTheme.bodySmall?.copyWith(
              color: theme.colorScheme.onSurfaceVariant,
            ),
          ),
          const SizedBox(height: 20),
          if (result == null) ...[
            TextField(
              controller: _name,
              textInputAction: TextInputAction.next,
              decoration: const InputDecoration(
                labelText: '真实姓名',
                border: OutlineInputBorder(),
              ),
            ),
            const SizedBox(height: 14),
            TextField(
              controller: _idCard,
              keyboardType: TextInputType.text,
              maxLength: 18,
              textCapitalization: TextCapitalization.characters,
              inputFormatters: [
                FilteringTextInputFormatter.allow(RegExp(r'[0-9xX]')),
              ],
              decoration: const InputDecoration(
                labelText: '身份证号',
                counterText: '',
                border: OutlineInputBorder(),
              ),
            ),
            const SizedBox(height: 10),
            if (_error != null)
              Text(
                _error!,
                style: theme.textTheme.bodySmall
                    ?.copyWith(color: theme.colorScheme.error),
              ),
            const SizedBox(height: 12),
            SizedBox(
              width: double.infinity,
              height: 48,
              child: FilledButton(
                onPressed: _submitting ? null : _submit,
                child: _submitting
                    ? const SizedBox(
                        width: 18,
                        height: 18,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : const Text('提交认证'),
              ),
            ),
          ] else ...[
            Row(
              children: [
                Icon(
                  result.passed
                      ? Icons.verified_user_outlined
                      : Icons.hourglass_bottom,
                  color: result.passed
                      ? theme.colorScheme.primary
                      : theme.colorScheme.tertiary,
                ),
                const SizedBox(width: 8),
                Text(result.statusText, style: theme.textTheme.titleMedium),
              ],
            ),
            const SizedBox(height: 8),
            if (result.realNameMasked.isNotEmpty)
              Text('姓名：${result.realNameMasked}'),
            if (result.idCardMasked.isNotEmpty)
              Text('证件：${result.idCardMasked}'),
            if (result.needClientAction)
              Padding(
                padding: const EdgeInsets.only(top: 10),
                child: Text(
                  '还需完成活体检测：请在打开的人脸核验页面按提示操作。',
                  style: theme.textTheme.bodySmall,
                ),
              ),
            if (result.failReason.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(top: 10),
                child: Text(
                  result.failReason,
                  style: theme.textTheme.bodySmall
                      ?.copyWith(color: theme.colorScheme.error),
                ),
              ),
            const SizedBox(height: 18),
            SizedBox(
              width: double.infinity,
              height: 46,
              child: OutlinedButton(
                onPressed: () => Navigator.of(context).pop(),
                child: const Text('好的'),
              ),
            ),
          ],
        ],
      ),
    );
  }
}
