import 'package:dio/dio.dart';
import 'package:flutter/foundation.dart';

import '../config/app_env.dart';
import '../storage/token_store.dart';
import 'api_exception.dart';

/// HTTP 客户端。
///
/// 职责边界：
///   - 拼装 BaseUrl、超时、公共头、Token；
///   - 把 {code,msg,data} 解包成 data，非 0 一律抛 ApiException；
///   - **只做一次**静默续期：收到 10100/10101/10102 时用 refresh token 换新令牌并重放请求，
///     续期失败就清空本地凭证，把异常抛给上层去引导登录。
///
/// 为什么续期放在 Dio 层而不是业务层：
/// access token 只有 2 小时，若每个接口各自处理过期，迟早漏掉一处；
/// 收口在这里，业务代码永远拿不到"令牌过期"这种中间态。
class DioClient {
  DioClient({required AppEnv env, required TokenStore tokenStore})
      : _tokenStore = tokenStore,
        _dio = Dio(
          BaseOptions(
            baseUrl: env.apiBaseUrl,
            connectTimeout: const Duration(seconds: 10),
            receiveTimeout: const Duration(seconds: 15),
            sendTimeout: const Duration(seconds: 15),
            headers: const {'Accept': 'application/json'},
            // 让 4xx 也走正常返回，由业务码统一判断，避免到处 catch
            validateStatus: (status) => status != null && status < 500,
          ),
        ),
        _refreshDio = Dio(
          BaseOptions(
            baseUrl: env.apiBaseUrl,
            connectTimeout: const Duration(seconds: 10),
            receiveTimeout: const Duration(seconds: 15),
            headers: const {'Accept': 'application/json'},
          ),
        ) {
    _dio.interceptors.add(
      InterceptorsWrapper(
        onRequest: (options, handler) {
          final token = _tokenStore.accessToken;
          if (token != null && token.isNotEmpty) {
            options.headers['Authorization'] = 'Bearer $token';
          }
          options.headers['X-Platform'] = defaultTargetPlatform.name;
          handler.next(options);
        },
      ),
    );
  }

  final Dio _dio;

  /// 专用客户端：续期请求不能再带上过期令牌，否则一旦失败会递归续期。
  final Dio _refreshDio;
  final TokenStore _tokenStore;

  /// 同一时刻只允许一个续期请求，其余请求共享结果。
  /// 并发各自续期会导致后一个把前一个刚写入的 jti 判成重放，直接把用户踢下线。
  Future<bool>? _refreshInFlight;

  Future<Map<String, dynamic>> get(
    String path, {
    Map<String, dynamic>? query,
  }) {
    return _send(() => _dio.get<dynamic>(path, queryParameters: query));
  }

  Future<Map<String, dynamic>> post(
    String path, {
    Object? body,
    Map<String, dynamic>? query,
  }) {
    return _send(
      () => _dio.post<dynamic>(path, data: body, queryParameters: query),
    );
  }

  Future<Map<String, dynamic>> put(
    String path, {
    Object? body,
  }) {
    return _send(() => _dio.put<dynamic>(path, data: body));
  }

  Future<Map<String, dynamic>> delete(
    String path, {
    Object? body,
  }) {
    return _send(() => _dio.delete<dynamic>(path, data: body));
  }

  /// multipart 文件上传。
  ///
  /// 不传 contentType：服务端用 magic bytes 嗅探真实类型，
  /// 客户端声明的 MIME 一律不可信（可以随便改）。
  Future<Map<String, dynamic>> upload(
    String path, {
    required List<int> bytes,
    required String filename,
    String field = 'file',
    Map<String, dynamic>? fields,
  }) {
    final form = FormData.fromMap({
      ...?fields,
      field: MultipartFile.fromBytes(bytes, filename: filename),
    });
    return _send(() => _dio.post<dynamic>(path, data: form));
  }

  Future<Map<String, dynamic>> _send(
    Future<Response<dynamic>> Function() request,
  ) async {
    try {
      return _unwrap(await request());
    } on ApiException catch (error) {
      if (!error.isUnauthorized) rethrow;

      final refreshed = await _refreshTokens();
      if (!refreshed) {
        await _tokenStore.clear();
        rethrow;
      }
      try {
        return _unwrap(await request());
      } on DioException catch (retryError) {
        throw ApiException.fromDio(retryError);
      }
    } on DioException catch (error) {
      throw ApiException.fromDio(error);
    }
  }

  /// 用 refresh token 换取新的双令牌，成功返回 true。
  Future<bool> _refreshTokens() {
    final running = _refreshInFlight;
    if (running != null) return running;

    final future = _doRefresh();
    _refreshInFlight = future;
    future.whenComplete(() => _refreshInFlight = null);
    return future;
  }

  Future<bool> _doRefresh() async {
    final refreshToken = _tokenStore.refreshToken;
    if (refreshToken == null || refreshToken.isEmpty) return false;

    try {
      final response = await _refreshDio.post<dynamic>(
        '/v1/auth/refresh',
        data: {'refresh_token': refreshToken},
      );
      final payload = response.data;
      if (payload is! Map || payload['code'] != 0) return false;
      final data = payload['data'];
      if (data is! Map) return false;

      final accessToken = data['access_token'];
      final newRefreshToken = data['refresh_token'];
      if (accessToken is! String || accessToken.isEmpty) return false;
      if (newRefreshToken is! String || newRefreshToken.isEmpty) return false;

      await _tokenStore.saveTokens(
        accessToken: accessToken,
        refreshToken: newRefreshToken,
      );
      return true;
    } catch (_) {
      // 续期失败（网络、令牌已被吊销）都不应把异常抛给业务层，交给上层走登录引导。
      return false;
    }
  }

  Map<String, dynamic> _unwrap(Response<dynamic> response) {
    final payload = response.data;
    if (payload is! Map) {
      throw const ApiException(code: -1, message: '响应格式异常');
    }

    final code = payload['code'];
    if (code is! int) {
      throw const ApiException(code: -1, message: '响应缺少业务码');
    }
    if (code != 0) {
      throw ApiException(
        code: code,
        message: payload['msg'] is String ? payload['msg'] as String : '请求失败',
      );
    }

    final data = payload['data'];
    if (data is Map<String, dynamic>) {
      return data;
    }
    if (data is Map) {
      return Map<String, dynamic>.from(data);
    }
    return <String, dynamic>{'data': data};
  }
}
