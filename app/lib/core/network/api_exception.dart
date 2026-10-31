import 'package:dio/dio.dart';

/// 统一的接口异常。
///
/// 后端所有响应都是 {code,msg,data}，code != 0 即为业务失败，
/// 这里把 HTTP 层异常和业务异常收敛成同一种类型，UI 只需要 catch 它。
class ApiException implements Exception {
  const ApiException({required this.code, required this.message});

  /// 业务错误码，与后端 errs 包一致；-1 表示本地网络或解析异常。
  final int code;
  final String message;

  bool get isUnauthorized => code == 10100 || code == 10101 || code == 10102;

  bool get isNetworkError => code == -1;

  factory ApiException.fromDio(DioException error) {
    switch (error.type) {
      case DioExceptionType.connectionTimeout:
      case DioExceptionType.sendTimeout:
      case DioExceptionType.receiveTimeout:
        return const ApiException(code: -1, message: '网络超时，请检查网络后重试');
      case DioExceptionType.connectionError:
        return const ApiException(code: -1, message: '无法连接服务器，请确认后端已启动');
      case DioExceptionType.badResponse:
        final data = error.response?.data;
        if (data is Map && data['msg'] is String) {
          return ApiException(
            code: data['code'] is int ? data['code'] as int : -1,
            message: data['msg'] as String,
          );
        }
        return ApiException(
          code: -1,
          message: '服务异常（HTTP ${error.response?.statusCode ?? 0}）',
        );
      case DioExceptionType.cancel:
        return const ApiException(code: -1, message: '请求已取消');
      case DioExceptionType.badCertificate:
        return const ApiException(code: -1, message: '证书校验失败');
      // 用 default 而不是逐个列枚举：dio 在不同小版本里会新增 DioExceptionType
      // （例如 5.11 的 transformTimeout），逐个列举会在升级后编译失败。
      default:
        return const ApiException(code: -1, message: '网络异常，请稍后重试');
    }
  }

  @override
  String toString() => 'ApiException($code): $message';
}
