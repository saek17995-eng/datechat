import '../../../core/network/dio_client.dart';

/// 附近的人与在线状态接口。
///
/// 隐私约束：距离永远是服务端给的区间文本（distance_text），
/// 客户端只负责展示，不自己做距离计算，也不缓存精确坐标到列表里。
class NearbyApi {
  NearbyApi(this._client);

  final DioClient _client;

  /// 上报位置。返回是否被服务端节流（30 秒内的重复上报会被丢弃）。
  Future<bool> reportLocation(double lng, double lat) async {
    final data = await _client.post(
      '/v1/users/me/location',
      body: {'lng': lng, 'lat': lat},
    );
    return data['throttled'] == true;
  }

  /// 附近的人。经纬度必填——服务端拒绝"用城市中心点代替"。
  Future<NearbyPage> nearby({
    required double lng,
    required double lat,
    int gender = 0,
    int ageMin = 0,
    int ageMax = 0,
    double radiusKm = 0,
    int page = 1,
    int size = 0,
  }) async {
    final query = <String, dynamic>{
      'lng': '$lng',
      'lat': '$lat',
      if (gender != 0) 'gender': '$gender',
      if (ageMin > 0) 'age_min': '$ageMin',
      if (ageMax > 0) 'age_max': '$ageMax',
      if (radiusKm > 0) 'radius': '$radiusKm',
      'page': '$page',
      if (size > 0) 'size': '$size',
    };
    final data = await _client.get('/v1/nearby', query: query);
    final list = data['list'];
    return NearbyPage(
      items: list is List
          ? list
              .whereType<Map<String, dynamic>>()
              .map(NearbyUser.fromJson)
              .toList()
          : const <NearbyUser>[],
      total: NearbyUser.asInt(data['total']),
    );
  }

  /// 批量查在线状态。列表页轮询用，避免逐人请求。
  Future<Map<int, bool>> online(List<int> ids) async {
    if (ids.isEmpty) return const {};
    final data = await _client.get('/v1/users/online', query: {
      'ids': ids.join(','),
    });
    final raw = data['online'];
    if (raw is! Map) return const {};
    return raw.map((key, value) => MapEntry(
          int.tryParse('$key') ?? 0,
          value == true,
        ));
  }

  /// 谁看过我。
  Future<VisitPage> visits({int page = 1, int size = 20}) async {
    final data = await _client.get(
      '/v1/users/me/visits',
      query: {'page': '$page', 'size': '$size'},
    );
    final list = data['list'];
    return VisitPage(
      items: list is List
          ? list
              .whereType<Map<String, dynamic>>()
              .map(VisitItem.fromJson)
              .toList()
          : const <VisitItem>[],
      total: NearbyUser.asInt(data['total']),
      unread: NearbyUser.asInt(data['unread']),
    );
  }

  /// 标记访客已读，返回受影响行数。
  Future<int> markVisitsRead() async {
    final data = await _client.post('/v1/users/me/visits/read');
    return NearbyUser.asInt(data['updated']);
  }

  /// 在线心跳。服务端据此判定在线，TTL 120 秒。
  Future<void> heartbeat() async {
    await _client.post('/v1/users/me/heartbeat');
  }
}

/// 附近的人一页结果。
class NearbyPage {
  const NearbyPage({required this.items, required this.total});

  final List<NearbyUser> items;
  final int total;
}

/// 附近的人列表项。
///
/// distanceText 是服务端模糊后的区间（"3km 以内"）；
/// distanceKm 只在服务端关闭模糊时下发，用于内网联调。
class NearbyUser {
  const NearbyUser({
    required this.uid,
    required this.nickname,
    required this.avatar,
    required this.gender,
    required this.age,
    required this.cityName,
    required this.distanceText,
    required this.distanceKm,
    required this.online,
    required this.lastActiveText,
    required this.priceAudio,
    required this.priceVideo,
    required this.realname,
  });

  factory NearbyUser.fromJson(Map<String, dynamic> json) => NearbyUser(
        uid: asInt(json['uid']),
        nickname: (json['nickname'] ?? '').toString(),
        avatar: (json['avatar'] ?? '').toString(),
        gender: asInt(json['gender']),
        age: asInt(json['age']),
        cityName: (json['city_name'] ?? '').toString(),
        distanceText: (json['distance_text'] ?? '').toString(),
        distanceKm: json['distance_km'] is num
            ? (json['distance_km'] as num).toDouble()
            : 0,
        online: json['online'] == true,
        lastActiveText: (json['last_active_text'] ?? '').toString(),
        priceAudio: asInt(json['price_audio']),
        priceVideo: asInt(json['price_video']),
        realname: json['realname'] == true,
      );

  final int uid;
  final String nickname;
  final String avatar;
  final int gender;
  final int age;
  final String cityName;
  final String distanceText;
  final double distanceKm;
  final bool online;
  final String lastActiveText;
  final int priceAudio;
  final int priceVideo;
  final bool realname;

  String get genderText => switch (gender) {
        1 => '男',
        2 => '女',
        _ => '保密',
      };

  static int asInt(Object? value) =>
      value is int ? value : int.tryParse('${value ?? 0}') ?? 0;
}

/// 谁看过我一页结果。
class VisitPage {
  const VisitPage({
    required this.items,
    required this.total,
    required this.unread,
  });

  final List<VisitItem> items;
  final int total;
  final int unread;
}

class VisitItem {
  const VisitItem({
    required this.visitorId,
    required this.nickname,
    required this.avatar,
    required this.gender,
    required this.age,
    required this.cityName,
    required this.visitedAt,
    required this.unread,
    required this.online,
  });

  factory VisitItem.fromJson(Map<String, dynamic> json) => VisitItem(
        visitorId: NearbyUser.asInt(json['visitor_id']),
        nickname: (json['nickname'] ?? '').toString(),
        avatar: (json['avatar'] ?? '').toString(),
        gender: NearbyUser.asInt(json['gender']),
        age: NearbyUser.asInt(json['age']),
        cityName: (json['city_name'] ?? '').toString(),
        visitedAt: (json['visited_at'] ?? '').toString(),
        unread: json['unread'] == true,
        online: json['online'] == true,
      );

  final int visitorId;
  final String nickname;
  final String avatar;
  final int gender;
  final int age;
  final String cityName;
  final String visitedAt;
  final bool unread;
  final bool online;
}
