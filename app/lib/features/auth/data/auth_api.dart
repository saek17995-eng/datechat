import 'package:flutter/foundation.dart';

import '../../../core/network/dio_client.dart';
import 'auth_models.dart';

/// 账号接口封装。
///
/// 只负责"调接口 + 解模型"，不持有状态、不写本地存储——
/// 状态与持久化由 AuthController 统一管，避免两处都能改令牌。
class AuthApi {
  AuthApi(this._client);

  final DioClient _client;

  /// 发送验证码。dev 环境服务端会回带 debug_code，直接返回给界面做提示。
  Future<String?> sendCode(String phone) async {
    final data = await _client.post(
      '/v1/auth/sms/code',
      body: {'phone': phone, 'scene': 'login'},
    );
    final debug = data['debug_code'];
    return debug is String && debug.isNotEmpty ? debug : null;
  }

  /// 手机号 + 验证码登录；未注册的手机号由服务端自动注册。
  Future<LoginResult> login({
    required String phone,
    required String code,
    required String deviceId,
    String appVersion = '0.1.0',
  }) async {
    final data = await _client.post(
      '/v1/auth/login',
      body: {
        'phone': phone,
        'code': code,
        'device_id': deviceId,
        'platform': _platformCode(),
        'app_version': appVersion,
      },
    );
    return LoginResult.fromJson(data);
  }

  Future<MeInfo> me() async {
    return MeInfo.fromJson(await _client.get('/v1/auth/me'));
  }

  Future<void> logout() async {
    await _client.post('/v1/auth/logout', body: const <String, dynamic>{});
  }

  /// 重新签发 IM UserSig（过期后调用）。
  Future<ImCredential> userSig() async {
    return ImCredential.fromJson(await _client.get('/v1/im/usersig'));
  }

  Future<RealnameStatus> submitRealname({
    required String realName,
    required String idCard,
  }) async {
    final data = await _client.post(
      '/v1/realname/submit',
      body: {'real_name': realName, 'id_card': idCard},
    );
    return RealnameStatus.fromJson(data);
  }

  Future<RealnameStatus> realnameStatus() async {
    return RealnameStatus.fromJson(await _client.get('/v1/realname/status'));
  }

  /// 完成 H5 活体检测后回查结果。
  Future<RealnameStatus> queryRealname({String? trace}) async {
    final data = await _client.post(
      '/v1/realname/query',
      body: trace == null ? const <String, dynamic>{} : {'trace': trace},
    );
    return RealnameStatus.fromJson(data);
  }

  /// 与服务端 user_device.platform 对齐：1 iOS / 2 Android / 3 Web。
  /// 用 defaultTargetPlatform 而不是 dart:io Platform，保证 Web 也能编译。
  int _platformCode() {
    if (kIsWeb) return 3;
    switch (defaultTargetPlatform) {
      case TargetPlatform.iOS:
        return 1;
      case TargetPlatform.android:
        return 2;
      default:
        return 3;
    }
  }
}
