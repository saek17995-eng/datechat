import 'package:flutter/widgets.dart';

/// 图片地址处理。
///
/// 本地上传通道（upload.provider=local）返回的 url 是相对路径
/// （形如 /static/uploads/upload/avatar/1/20261031/xxx.png），
/// 直接用 NetworkImage 加载会失败，必须拼上服务端基址；
/// 走 COS/CDN 时下发的是绝对地址，原样返回即可。
///
/// 基址与 DioClient 同源，都从 --dart-define=API_BASE_URL 注入，
/// 避免"接口通了但图片 404"这种环境不一致问题。
class ProfileImage {
  const ProfileImage._();

  static const String _baseUrl = String.fromEnvironment(
    'API_BASE_URL',
    defaultValue: 'http://127.0.0.1:8080',
  );

  /// 把可能的相对地址补全为绝对地址。
  static String absUrl(String url) {
    final trimmed = url.trim();
    if (trimmed.isEmpty) return '';
    if (trimmed.startsWith('http://') || trimmed.startsWith('https://')) {
      return trimmed;
    }
    final base = _baseUrl.endsWith('/')
        ? _baseUrl.substring(0, _baseUrl.length - 1)
        : _baseUrl;
    final path = trimmed.startsWith('/') ? trimmed : '/$trimmed';
    return '$base$path';
  }

  /// 生成 ImageProvider；地址为空时返回 null，由调用方回退到占位图。
  static ImageProvider? provider(String url) {
    final resolved = absUrl(url);
    if (resolved.isEmpty) return null;
    return NetworkImage(resolved);
  }
}
