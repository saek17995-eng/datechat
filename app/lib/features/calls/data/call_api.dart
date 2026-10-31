import '../../../core/network/dio_client.dart';
import 'call_models.dart';

/// 通话接口。
///
/// 计费相关的参数客户端一个都不传：
///   - 时长由服务端时钟算，心跳只带一个"我还在"的信号；
///   - 价格由服务端按被叫定价快照，客户端只展示不下发。
class CallApi {
  CallApi(this._client);

  final DioClient _client;

  /// 通话定价（发起前展示，避免接通才发现贵）。
  Future<CallPrice> price(int uid, {int mediaType = mediaAudio}) async {
    final data =
        await _client.get('/v1/call/price?user_id=$uid&media_type=$mediaType');
    return CallPrice.fromJson(data);
  }

  /// 发起通话。
  Future<CallJoinResult> create(int calleeId, {int mediaType = mediaAudio}) {
    return _postJoin(
      '/v1/calls',
      body: {'callee_id': calleeId, 'media_type': mediaType},
    );
  }

  /// 接听。
  Future<CallJoinResult> accept(String callNo) {
    return _postJoin('/v1/calls/$callNo/accept');
  }

  Future<CallSession> reject(String callNo) => _postSession(
        '/v1/calls/$callNo/reject',
      );

  Future<CallSession> cancel(String callNo) => _postSession(
        '/v1/calls/$callNo/cancel',
      );

  Future<CallSession> hangup(String callNo) => _postSession(
        '/v1/calls/$callNo/hangup',
      );

  /// 心跳。elapsedSec 是客户端观测值，仅用于排查，服务端不采信。
  Future<CallBeat> heartbeat(String callNo, {int elapsedSec = 0}) async {
    final data = await _client.post(
      '/v1/calls/$callNo/heartbeat',
      body: {'elapsed_sec': elapsedSec},
    );
    return CallBeat.fromJson(data);
  }

  /// 通话详情（含分钟切片与账单一致性结论）。
  Future<CallSession> detail(String callNo) async {
    final data = await _client.get('/v1/calls/$callNo');
    final session = data['session'];
    return CallSession.fromJson(
      session is Map<String, dynamic> ? session : data,
    );
  }

  /// 通话记录。
  Future<List<CallSession>> list({int page = 1, int size = 20}) async {
    final data = await _client.get('/v1/calls?page=$page&size=$size');
    final list = data['list'];
    if (list is! List) return const [];
    return list
        .whereType<Map<String, dynamic>>()
        .map(CallSession.fromJson)
        .toList();
  }

  Future<CallJoinResult> _postJoin(String path, {Object? body}) async {
    final data = await _client.post(path, body: body);
    return CallJoinResult.fromJson(data);
  }

  Future<CallSession> _postSession(String path) async {
    final data = await _client.post(path);
    return CallSession.fromJson(data);
  }
}
