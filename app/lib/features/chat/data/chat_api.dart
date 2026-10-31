import '../../../core/network/dio_client.dart';
import 'chat_models.dart';

/// 会话与消息接口。
///
/// **传输门面说明（P3）**：这一层刻意只走自家后端 REST，不接任何原生 IM SDK。
/// 真实链路是：客户端 → 自家 Go 后端 → 腾讯云 IM REST API。
/// 带来的好处：
///   - 客户端不需要 UserSig / SDK 初始化 / 原生插件，Web 端同样能跑；
///   - 消息在后端有本地镜像，审计、举报取证、撤回都以后端为准；
///   - 以后要换成别的 IM 厂商，只改后端，客户端不动。
/// 代价是没有长连接推送，本页用 10 秒轮询兜底（见 chat_controller.dart）。
class ChatApi {
  ChatApi(this._client);

  final DioClient _client;

  /// 会话列表。page 从 1 开始，size 服务端上限 100。
  Future<ConversationPage> conversations({int page = 1, int size = 20}) async {
    final data = await _client.get(
      '/v1/im/conversations',
      query: {'page': page, 'size': size},
    );
    return ConversationPage.fromJson(data);
  }

  /// 与某人的历史消息，按 id 倒序返回最新一页。
  /// [beforeId] 传上一页的 next_cursor 继续往前翻。
  Future<MessagePage> messages({
    required int peerUid,
    int? beforeId,
    int limit = 30,
  }) async {
    final query = <String, dynamic>{
      'peer_uid': peerUid,
      'limit': limit,
    };
    if (beforeId != null && beforeId > 0) {
      query['before_id'] = beforeId;
    }
    final data = await _client.get('/v1/im/messages', query: query);
    return MessagePage.fromJson(data);
  }

  /// 发送文本消息。
  ///
  /// [clientMsgId] 由客户端生成（见 ChatController），服务端据此幂等：
  /// 弱网重发同一个 id 会直接返回第一次的结果，不会产生两条消息。
  Future<ChatMessage> sendText({
    required int peerUid,
    required String text,
    required String clientMsgId,
  }) async {
    final data = await _client.post(
      '/v1/im/messages',
      body: {
        'peer_uid': peerUid,
        'text': text,
        'client_msg_id': clientMsgId,
      },
    );
    return ChatMessage.fromJson(data);
  }

  /// 标记与某人的会话为已读。
  Future<ReadResult> markRead(int peerUid) async {
    final data = await _client.post('/v1/im/conversations/$peerUid/read');
    return ReadResult.fromJson(data);
  }

  /// 撤回消息。msgId 来自 IM，可能含特殊字符，必须做路径编码。
  Future<RecallResult> recall(String msgId) async {
    final data = await _client.post(
      '/v1/im/messages/${Uri.encodeComponent(msgId)}/recall',
    );
    return RecallResult.fromJson(data);
  }
}
