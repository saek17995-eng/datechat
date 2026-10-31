/// 资料相关的数据模型。
///
/// 与服务端 /v1/users/* 返回结构一一对应；未知字段一律忽略，
/// 这样后端加字段不会导致客户端解析失败。
library;

/// 我的资料（可编辑字段 + 生效定价）。
class ProfileInfo {
  const ProfileInfo({
    required this.uid,
    required this.nickname,
    required this.avatar,
    required this.gender,
    required this.genderText,
    required this.birthday,
    required this.age,
    required this.cityCode,
    required this.cityName,
    required this.height,
    required this.job,
    required this.bio,
    required this.tags,
    required this.photos,
    required this.voiceIntro,
    required this.priceEnabled,
    required this.priceAudio,
    required this.priceVideo,
    required this.priceSelfAudio,
    required this.priceSelfVideo,
    required this.realnameStatus,
    required this.registeredAt,
  });

  final int uid;
  final String nickname;
  final String avatar;
  final int gender;

  /// 服务端下发的性别文案，避免客户端与服务端两套映射不一致。
  final String genderText;

  /// YYYY-MM-DD，未填写时为空串。
  final String birthday;
  final int age;

  final String cityCode;
  final String cityName;
  final int height;
  final String job;
  final String bio;
  final List<String> tags;
  final List<String> photos;
  final String voiceIntro;

  final int priceEnabled;

  /// 生效价：开启自定价且自定价 > 0 时等于自定价，否则为全局价。
  final int priceAudio;
  final int priceVideo;

  /// 自定价原值，0 表示跟随全局价。
  final int priceSelfAudio;
  final int priceSelfVideo;

  final int realnameStatus;
  final int registeredAt;

  bool get hasAvatar => avatar.isNotEmpty;
  bool get isRealnameVerified => realnameStatus == 2;

  /// 资料完整度（0~1），用于「我的」页提示用户去完善资料。
  /// 权重按对匹配的影响排序：头像与昵称最重要，签名与语音最弱。
  double get completeness {
    var score = 0.0;
    if (hasAvatar) score += 0.25;
    if (nickname.isNotEmpty) score += 0.15;
    if (gender != 0) score += 0.1;
    if (birthday.isNotEmpty) score += 0.1;
    if (cityCode.isNotEmpty) score += 0.1;
    if (height > 0) score += 0.05;
    if (job.isNotEmpty) score += 0.05;
    if (bio.isNotEmpty) score += 0.1;
    if (tags.isNotEmpty) score += 0.05;
    if (photos.isNotEmpty) score += 0.05;
    return score.clamp(0.0, 1.0);
  }

  ProfileInfo copyWith({
    String? nickname,
    String? avatar,
    int? gender,
    String? genderText,
    String? birthday,
    int? age,
    String? cityCode,
    String? cityName,
    int? height,
    String? job,
    String? bio,
    List<String>? tags,
    List<String>? photos,
  }) {
    return ProfileInfo(
      uid: uid,
      nickname: nickname ?? this.nickname,
      avatar: avatar ?? this.avatar,
      gender: gender ?? this.gender,
      genderText: genderText ?? this.genderText,
      birthday: birthday ?? this.birthday,
      age: age ?? this.age,
      cityCode: cityCode ?? this.cityCode,
      cityName: cityName ?? this.cityName,
      height: height ?? this.height,
      job: job ?? this.job,
      bio: bio ?? this.bio,
      tags: tags ?? this.tags,
      photos: photos ?? this.photos,
      voiceIntro: voiceIntro,
      priceEnabled: priceEnabled,
      priceAudio: priceAudio,
      priceVideo: priceVideo,
      priceSelfAudio: priceSelfAudio,
      priceSelfVideo: priceSelfVideo,
      realnameStatus: realnameStatus,
      registeredAt: registeredAt,
    );
  }

  factory ProfileInfo.fromJson(Map<String, dynamic> json) {
    return ProfileInfo(
      uid: _asInt(json['uid']),
      nickname: _asString(json['nickname']),
      avatar: _asString(json['avatar']),
      gender: _asInt(json['gender']),
      genderText: _asString(json['gender_text']),
      birthday: _asString(json['birthday']),
      age: _asInt(json['age']),
      cityCode: _asString(json['city_code']),
      cityName: _asString(json['city_name']),
      height: _asInt(json['height']),
      job: _asString(json['job']),
      bio: _asString(json['bio']),
      tags: _asStringList(json['tags']),
      photos: _asStringList(json['photos']),
      voiceIntro: _asString(json['voice_intro']),
      priceEnabled: _asInt(json['price_enabled']),
      priceAudio: _asInt(json['price_audio']),
      priceVideo: _asInt(json['price_video']),
      priceSelfAudio: _asInt(json['price_self_audio']),
      priceSelfVideo: _asInt(json['price_self_video']),
      realnameStatus: _asInt(json['realname_status']),
      registeredAt: _asInt(json['registered_at']),
    );
  }
}

/// 访问者与被访问者的关系。
class UserRelation {
  const UserRelation({
    required this.isSelf,
    required this.blocked,
    required this.blockedBy,
  });

  final bool isSelf;

  /// 我拉黑了对方。
  final bool blocked;

  /// 对方拉黑了我：此时禁止发起聊天与通话，但主页仍可浏览。
  final bool blockedBy;

  bool get canContact => !isSelf && !blockedBy;

  factory UserRelation.fromJson(Map<String, dynamic> json) {
    return UserRelation(
      isSelf: json['is_self'] == true,
      blocked: json['blocked'] == true,
      blockedBy: json['blocked_by'] == true,
    );
  }
}

/// 他人主页。
class PublicUser {
  const PublicUser({
    required this.uid,
    required this.nickname,
    required this.avatar,
    required this.gender,
    required this.genderText,
    required this.age,
    required this.cityCode,
    required this.cityName,
    required this.height,
    required this.job,
    required this.bio,
    required this.tags,
    required this.photos,
    required this.voiceIntro,
    required this.priceAudio,
    required this.priceVideo,
    required this.online,
    required this.lastActiveAt,
    required this.lastActiveText,
    required this.realnameStatus,
    required this.registeredAt,
    required this.relation,
  });

  final int uid;
  final String nickname;
  final String avatar;
  final int gender;
  final String genderText;
  final int age;
  final String cityCode;
  final String cityName;
  final int height;
  final String job;
  final String bio;
  final List<String> tags;
  final List<String> photos;
  final String voiceIntro;
  final int priceAudio;
  final int priceVideo;
  final bool online;
  final int lastActiveAt;
  final String lastActiveText;
  final int realnameStatus;
  final int registeredAt;
  final UserRelation relation;

  bool get isRealnameVerified => realnameStatus == 2;

  /// 主页副标题：城市 / 年龄 / 身高，缺哪省哪。
  String get summaryLine {
    final parts = <String>[];
    if (age > 0) parts.add('$age 岁');
    if (cityName.isNotEmpty) parts.add(cityName);
    if (height > 0) parts.add('${height}cm');
    return parts.join(' · ');
  }

  factory PublicUser.fromJson(Map<String, dynamic> json) {
    return PublicUser(
      uid: _asInt(json['uid']),
      nickname: _asString(json['nickname']),
      avatar: _asString(json['avatar']),
      gender: _asInt(json['gender']),
      genderText: _asString(json['gender_text']),
      age: _asInt(json['age']),
      cityCode: _asString(json['city_code']),
      cityName: _asString(json['city_name']),
      height: _asInt(json['height']),
      job: _asString(json['job']),
      bio: _asString(json['bio']),
      tags: _asStringList(json['tags']),
      photos: _asStringList(json['photos']),
      voiceIntro: _asString(json['voice_intro']),
      priceAudio: _asInt(json['price_audio']),
      priceVideo: _asInt(json['price_video']),
      online: json['online'] == true,
      lastActiveAt: _asInt(json['last_active_at']),
      lastActiveText: _asString(json['last_active_text']),
      realnameStatus: _asInt(json['realname_status']),
      registeredAt: _asInt(json['registered_at']),
      relation: UserRelation.fromJson(
        json['relation'] is Map<String, dynamic>
            ? json['relation'] as Map<String, dynamic>
            : const {},
      ),
    );
  }
}

/// 城市字典项。
class CityItem {
  const CityItem({
    required this.code,
    required this.province,
    required this.name,
  });

  final String code;
  final String province;
  final String name;

  String get label => province.isEmpty ? name : '$province $name';

  factory CityItem.fromJson(Map<String, dynamic> json) {
    return CityItem(
      code: _asString(json['code']),
      province: _asString(json['province']),
      name: _asString(json['name']),
    );
  }
}

/// 上传凭证。
/// provider=local 走 [uploadPath] 直传后端；provider=cos 走对象存储直传。
class UploadSign {
  const UploadSign({
    required this.provider,
    required this.key,
    required this.url,
    required this.maxBytes,
    required this.allowedTypes,
    this.host = '',
    this.uploadPath = '',
    this.expireAt = 0,
    this.fields = const {},
    this.fieldOrder = const [],
  });

  final String provider;
  final String key;
  final String url;
  final String host;
  final String uploadPath;
  final int expireAt;
  final int maxBytes;
  final List<String> allowedTypes;
  final Map<String, String> fields;
  final List<String> fieldOrder;

  bool get isCos => provider == 'cos';

  factory UploadSign.fromJson(Map<String, dynamic> json) {
    return UploadSign(
      provider: _asString(json['provider']),
      key: _asString(json['key']),
      url: _asString(json['url']),
      host: _asString(json['host']),
      uploadPath: _asString(json['upload_path']),
      expireAt: _asInt(json['expire_at']),
      maxBytes: _asInt(json['max_bytes']),
      allowedTypes: _asStringList(json['allowed_types']),
      fields: (json['fields'] is Map)
          ? (json['fields'] as Map).map(
              (key, value) => MapEntry(key.toString(), value.toString()),
            )
          : const {},
      fieldOrder: _asStringList(json['field_order']),
    );
  }
}

/// 上传完成后的文件信息。
class UploadedFile {
  const UploadedFile({
    required this.url,
    required this.key,
    required this.size,
    required this.mime,
    required this.status,
  });

  final String url;
  final String key;
  final int size;
  final String mime;

  /// 1 待审核 2 已通过 3 已拒绝
  final int status;

  bool get rejected => status == 3;

  factory UploadedFile.fromJson(Map<String, dynamic> json) {
    return UploadedFile(
      url: _asString(json['url']),
      key: _asString(json['key']),
      size: _asInt(json['size']),
      mime: _asString(json['mime']),
      status: _asInt(json['status']),
    );
  }
}

/// 资料编辑入参。
///
/// 所有字段 nullable：null 表示"这次不修改"，
/// 与服务端"指针即是否提供"的局部更新语义保持一致。
/// 想清空签名就传空字符串，而不是 null。
class ProfilePatch {
  const ProfilePatch({
    this.nickname,
    this.gender,
    this.birthday,
    this.cityCode,
    this.height,
    this.job,
    this.bio,
    this.tags,
  });

  final String? nickname;
  final int? gender;
  final String? birthday;
  final String? cityCode;
  final int? height;
  final String? job;
  final String? bio;
  final List<String>? tags;

  Map<String, dynamic> toJson() {
    final json = <String, dynamic>{};
    void put(String key, Object? value) {
      if (value != null) json[key] = value;
    }

    put('nickname', nickname);
    put('gender', gender);
    put('birthday', birthday);
    put('city_code', cityCode);
    put('height', height);
    put('job', job);
    put('bio', bio);
    put('tags', tags);
    return json;
  }

  bool get isEmpty => toJson().isEmpty;
}

int _asInt(Object? value) {
  if (value is int) return value;
  if (value is num) return value.toInt();
  if (value is String) return int.tryParse(value) ?? 0;
  return 0;
}

String _asString(Object? value) => value is String ? value : '';

List<String> _asStringList(Object? value) {
  if (value is List) {
    return value.whereType<String>().toList();
  }
  return const [];
}
