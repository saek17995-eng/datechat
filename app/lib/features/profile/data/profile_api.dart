import '../../../core/network/dio_client.dart';
import 'profile_models.dart';

/// 资料接口。
///
/// 只做"调接口 + 解模型"，状态由 Riverpod 持有，页面不直接缓存接口结果，
/// 避免出现"我的资料"在两处各存一份、改了一处另一处还是旧值的情况。
class ProfileApi {
  ProfileApi(this._client);

  final DioClient _client;

  /// 我的资料。
  Future<ProfileInfo> myProfile() async {
    final data = await _client.get('/v1/users/me/profile');
    return ProfileInfo.fromJson(data);
  }

  /// 局部更新资料。
  Future<ProfileInfo> updateProfile(ProfilePatch patch) async {
    final data =
        await _client.put('/v1/users/me/profile', body: patch.toJson());
    return ProfileInfo.fromJson(data);
  }

  /// 提交头像地址。地址必须来自上传接口，服务端会校验。
  Future<ProfileInfo> setAvatar(String url) async {
    final data = await _client.post('/v1/users/me/avatar', body: {'url': url});
    return ProfileInfo.fromJson(data);
  }

  /// 他人主页。
  Future<PublicUser> userProfile(int uid) async {
    final data = await _client.get('/v1/users/$uid');
    return PublicUser.fromJson(data);
  }

  /// 在线心跳。返回保活秒数，客户端据此决定下次上报间隔。
  Future<int> heartbeat() async {
    final data = await _client.post('/v1/users/me/heartbeat');
    final keepAlive = data['keep_alive'];
    return keepAlive is int ? keepAlive : 60;
  }

  /// 城市字典。
  Future<List<CityItem>> cities() async {
    final data = await _client.get('/v1/cities');
    final list = data['list'] ?? data['data'];
    if (list is List) {
      return list
          .whereType<Map<String, dynamic>>()
          .map(CityItem.fromJson)
          .toList();
    }
    return const [];
  }

  /// 上传凭证。local 通道返回上传接口路径，cos 通道返回直传签名。
  Future<UploadSign> uploadSign({int scene = 1, String ext = 'jpg'}) async {
    final data = await _client.post(
      '/v1/uploads/sign',
      body: {'scene': scene, 'ext': ext},
    );
    return UploadSign.fromJson(data);
  }

  /// 本地通道上传文件（multipart）。
  Future<UploadedFile> uploadBytes({
    required List<int> bytes,
    required String filename,
    int scene = 1,
  }) async {
    final data = await _client.upload(
      '/v1/uploads/local',
      bytes: bytes,
      filename: filename,
      fields: {'scene': scene.toString()},
    );
    return UploadedFile.fromJson(data);
  }
}
