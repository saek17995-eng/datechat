import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'profile_controller.dart';

/// 更换头像。
///
/// 当前 Flutter 工程未引入 image_picker（离线环境无法解析插件依赖），
/// 因此提供两个"能真实跑通上传链路"的来源：
///   1. 本地文件：读取字节 → multipart 上传 → 提交 URL；
///   2. 图片链接：直接提交 URL（服务端只接受本站上传过的地址，联调时很好用）。
/// 接入 image_picker 之后，只需把 [上传本地文件] 的实现换成
/// `final x = await picker.pickImage(...); await x.readAsBytes();`，
/// UI 与后续流程完全不用动。
Future<void> showAvatarPickerSheet(BuildContext context, WidgetRef ref) async {
  final theme = Theme.of(context);
  await showModalBottomSheet<void>(
    context: context,
    showDragHandle: true,
    builder: (sheetContext) => SafeArea(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          ListTile(
            leading: const Icon(Icons.upload_file),
            title: const Text('上传本地图片'),
            subtitle: const Text('从磁盘选择文件，走 multipart 上传'),
            onTap: () {
              Navigator.of(sheetContext).pop();
              _uploadFromPath(context, ref);
            },
          ),
          ListTile(
            leading: const Icon(Icons.link),
            title: const Text('使用图片链接'),
            subtitle: const Text('粘贴已上传图片的完整地址'),
            onTap: () {
              Navigator.of(sheetContext).pop();
              _submitByUrl(context, ref);
            },
          ),
          const SizedBox(height: 8),
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
            child: Text(
              '图片会经过服务端内容校验，未通过审核的图片无法设为头像。',
              style: theme.textTheme.bodySmall?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
              ),
            ),
          ),
        ],
      ),
    ),
  );
}

Future<void> _uploadFromPath(BuildContext context, WidgetRef ref) async {
  final path =
      await _prompt(context, title: '本地图片路径', hint: '/Users/me/photo.jpg');
  if (path == null || path.isEmpty || !context.mounted) return;

  final file = File(path);
  if (!file.existsSync()) {
    _toast(context, '文件不存在：$path');
    return;
  }
  final bytes = await file.readAsBytes();
  final filename = path.split(Platform.pathSeparator).last;
  final ok = await ref
      .read(profileEditorProvider.notifier)
      .uploadAvatar(bytes: bytes, filename: filename);
  if (context.mounted) {
    _toast(context, ok ? '头像已更新' : '头像更新失败，请稍后重试');
  }
}

Future<void> _submitByUrl(BuildContext context, WidgetRef ref) async {
  final url =
      await _prompt(context, title: '图片链接', hint: '/static/uploads/...');
  if (url == null || url.isEmpty || !context.mounted) return;
  final ok =
      await ref.read(profileEditorProvider.notifier).setAvatarByUrl(url.trim());
  if (context.mounted) {
    _toast(context, ok ? '头像已更新' : '头像更新失败，请稍后重试');
  }
}

Future<String?> _prompt(
  BuildContext context, {
  required String title,
  required String hint,
}) {
  final controller = TextEditingController();
  return showDialog<String>(
    context: context,
    builder: (dialogContext) => AlertDialog(
      title: Text(title),
      content: TextField(
        controller: controller,
        autofocus: true,
        decoration: InputDecoration(hintText: hint),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(dialogContext).pop(),
          child: const Text('取消'),
        ),
        FilledButton(
          onPressed: () =>
              Navigator.of(dialogContext).pop(controller.text.trim()),
          child: const Text('确定'),
        ),
      ],
    ),
  );
}

void _toast(BuildContext context, String message) {
  ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(message)));
}
