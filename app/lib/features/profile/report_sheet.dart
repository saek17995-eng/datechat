import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/network/api_exception.dart';
import 'data/report_api.dart';
import 'profile_controller.dart';

/// 弹出举报表单（bottom sheet）。
///
/// 返回 true 表示提交成功。成功/失败的 SnackBar 都由这里统一给，
/// 调用方不需要各自处理文案，避免同一个错误在两个入口提示不一致。
///
/// 理由用单选而不让人直接写：服务端 reason 是枚举白名单（便于后台统计），
/// 客户端只负责把枚举翻译成中文；详细情况写在补充描述里。
Future<bool> showReportSheet(
  BuildContext context, {
  required int targetType,
  required String targetId,
  int targetUid = 0,
}) {
  final result = showModalBottomSheet<bool>(
    context: context,
    isScrollControlled: true,
    showDragHandle: true,
    builder: (_) => _ReportSheet(
      targetType: targetType,
      targetId: targetId,
      targetUid: targetUid,
    ),
  );
  return result.then((value) => value ?? false);
}

class _ReportSheet extends ConsumerStatefulWidget {
  const _ReportSheet({
    required this.targetType,
    required this.targetId,
    required this.targetUid,
  });

  final int targetType;
  final String targetId;
  final int targetUid;

  @override
  ConsumerState<_ReportSheet> createState() => _ReportSheetState();
}

class _ReportSheetState extends ConsumerState<_ReportSheet> {
  final TextEditingController _detail = TextEditingController();

  String? _reasonCode;
  bool _submitting = false;
  String? _error;

  @override
  void dispose() {
    _detail.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    final reason = _reasonCode;
    if (reason == null) {
      setState(() => _error = '请选择举报理由');
      return;
    }

    setState(() {
      _submitting = true;
      _error = null;
    });

    try {
      await ref.read(safetyApiProvider).submitReport(
            targetType: widget.targetType,
            targetId: widget.targetId,
            targetUid: widget.targetUid,
            reason: reason,
            detail: _detail.text.trim(),
          );
      if (!mounted) return;
      _notify('举报已提交，我们会尽快处理');
      Navigator.of(context).pop(true);
    } on ApiException catch (error) {
      if (!mounted) return;
      if (error.code == reportDuplicateCode) {
        // 已经举报过：不是错误，不需要用户重试，关掉表单给个明确说明即可。
        _notify('你已经举报过该内容');
        Navigator.of(context).pop(false);
        return;
      }
      setState(() => _error = error.message);
    } catch (_) {
      if (!mounted) return;
      setState(() => _error = '提交失败，请稍后重试');
    } finally {
      if (mounted) setState(() => _submitting = false);
    }
  }

  void _notify(String message) {
    ScaffoldMessenger.of(context)
        .showSnackBar(SnackBar(content: Text(message)));
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);

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
          Text('举报', style: theme.textTheme.titleLarge),
          const SizedBox(height: 6),
          Text(
            '请选择举报理由，我们会尽快核实处理。恶意举报会影响你的账号信用。',
            style: theme.textTheme.bodySmall?.copyWith(
              color: theme.colorScheme.onSurfaceVariant,
            ),
          ),
          const SizedBox(height: 16),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              for (final reason in reportReasons)
                ChoiceChip(
                  label: Text(reason.label),
                  selected: _reasonCode == reason.code,
                  onSelected: (_) => setState(() => _reasonCode = reason.code),
                ),
            ],
          ),
          const SizedBox(height: 16),
          TextField(
            controller: _detail,
            minLines: 3,
            maxLines: 5,
            maxLength: reportDetailMaxLength,
            enabled: !_submitting,
            decoration: const InputDecoration(
              labelText: '补充描述（选填）',
              alignLabelWithHint: true,
              border: OutlineInputBorder(),
            ),
          ),
          if (_error != null) ...[
            const SizedBox(height: 8),
            Text(
              _error!,
              style: theme.textTheme.bodySmall
                  ?.copyWith(color: theme.colorScheme.error),
            ),
          ],
          const SizedBox(height: 16),
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
                  : const Text('提交举报'),
            ),
          ),
        ],
      ),
    );
  }
}
