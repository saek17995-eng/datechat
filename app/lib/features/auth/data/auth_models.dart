/// 账号相关的数据模型。
///
/// 只保留客户端真正要用的字段：令牌、用户摘要、IM 材料、实名状态。
/// 服务端返回的其余字段一律忽略，避免接口加字段就要改模型。
library;

/// 腾讯 IM 登录材料。enabled=false 表示服务端未配置 IM，应跳过 IM 登录。
class ImCredential {
  const ImCredential({
    required this.enabled,
    this.sdkAppId = 0,
    this.userId = '',
    this.userSig = '',
    this.expireAt = 0,
  });

  final bool enabled;
  final int sdkAppId;
  final String userId;
  final String userSig;
  final int expireAt;

  bool get usable => enabled && userId.isNotEmpty && userSig.isNotEmpty;

  factory ImCredential.fromJson(Map<String, dynamic> json) {
    return ImCredential(
      enabled: json['enabled'] == true,
      sdkAppId: _asInt(json['sdk_app_id']),
      userId: _asString(json['user_id']),
      userSig: _asString(json['usersig']),
      expireAt: _asInt(json['usersig_expire_at']),
    );
  }

  static const ImCredential none = ImCredential(enabled: false);
}

/// 当前登录用户摘要。
class MeInfo {
  const MeInfo({
    required this.uid,
    required this.nickname,
    required this.avatar,
    required this.gender,
    required this.phoneMasked,
    required this.realnameStatus,
  });

  final int uid;
  final String nickname;
  final String avatar;
  final int gender;

  /// 已脱敏，形如 138****8000，服务端不会下发明文。
  final String phoneMasked;

  /// 0 未认证 / 1 审核中 / 2 已认证 / 3 未通过
  final int realnameStatus;

  bool get isRealnameVerified => realnameStatus == 2;

  String get genderText {
    switch (gender) {
      case 1:
        return '男';
      case 2:
        return '女';
      default:
        return '保密';
    }
  }

  String get realnameText {
    switch (realnameStatus) {
      case 1:
        return '审核中';
      case 2:
        return '已认证';
      case 3:
        return '认证未通过';
      default:
        return '未认证';
    }
  }

  factory MeInfo.fromJson(Map<String, dynamic> json) {
    return MeInfo(
      uid: _asInt(json['uid']),
      nickname: _asString(json['nickname']),
      avatar: _asString(json['avatar']),
      gender: _asInt(json['gender']),
      phoneMasked: _asString(json['phone_masked']),
      realnameStatus: _asInt(json['realname_status']),
    );
  }
}

/// 双令牌。刷新时服务端只返回这几个字段。
class TokenPair {
  const TokenPair({
    required this.accessToken,
    required this.refreshToken,
    this.accessExpireAt = 0,
    this.refreshExpireAt = 0,
  });

  final String accessToken;
  final String refreshToken;
  final int accessExpireAt;
  final int refreshExpireAt;

  bool get isEmpty => accessToken.isEmpty || refreshToken.isEmpty;

  factory TokenPair.fromJson(Map<String, dynamic> json) {
    return TokenPair(
      accessToken: _asString(json['access_token']),
      refreshToken: _asString(json['refresh_token']),
      accessExpireAt: _asInt(json['access_expire_at']),
      refreshExpireAt: _asInt(json['refresh_expire_at']),
    );
  }
}

/// 登录结果。
class LoginResult {
  const LoginResult({
    required this.uid,
    required this.isNewUser,
    required this.token,
    required this.me,
    required this.im,
  });

  final int uid;
  final bool isNewUser;
  final TokenPair token;
  final MeInfo me;
  final ImCredential im;

  factory LoginResult.fromJson(Map<String, dynamic> json) {
    final user = json['user'];
    final im = json['im'];
    final token = json['token'];
    return LoginResult(
      uid: _asInt(json['uid']),
      isNewUser: json['is_new_user'] == true,
      token: TokenPair.fromJson(
        token is Map<String, dynamic> ? token : const {},
      ),
      me: MeInfo.fromJson(user is Map<String, dynamic> ? user : const {}),
      im: im is Map<String, dynamic>
          ? ImCredential.fromJson(im)
          : ImCredential.none,
    );
  }
}

/// 实名状态。
class RealnameStatus {
  const RealnameStatus({
    required this.status,
    required this.statusText,
    this.realNameMasked = '',
    this.idCardMasked = '',
    this.failReason = '',
    this.needClientAction = false,
    this.h5Url = '',
    this.trace = '',
  });

  final int status;
  final String statusText;
  final String realNameMasked;
  final String idCardMasked;
  final String failReason;

  /// true 表示要走 H5 活体检测，客户端应打开 [h5Url]。
  final bool needClientAction;
  final String h5Url;
  final String trace;

  bool get passed => status == 2;
  bool get processing => status == 1;

  factory RealnameStatus.fromJson(Map<String, dynamic> json) {
    return RealnameStatus(
      status: _asInt(json['status']),
      statusText: _asString(json['status_text']),
      realNameMasked: _asString(json['real_name_masked']),
      idCardMasked: _asString(json['id_card_masked']),
      failReason: _asString(json['fail_reason']),
      needClientAction: json['need_client_action'] == true,
      h5Url: _asString(json['h5_url']),
      trace: _asString(json['trace']),
    );
  }
}

int _asInt(Object? value) {
  if (value is int) return value;
  if (value is num) return value.toInt();
  if (value is String) return int.tryParse(value) ?? 0;
  return 0;
}

String _asString(Object? value) => value is String ? value : '';
