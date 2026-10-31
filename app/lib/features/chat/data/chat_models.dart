/// 会话与消息的数据模型。
///
/// 与后端 internal/modules/chat/dto.go 一一对应。这里拿到的是后端本地镜像视图：
/// 真实投递由后端调用腾讯云 IM REST API 完成，客户端只与自家 REST 门面通信
/// （不接原生 IM SDK，见 chat_api.dart）。所有时间戳统一为毫秒。
library;

/// 消息类型，与 message.msg_type 对齐。
const int messageTypeText = 1;

/// 消息状态，与 message.status 对齐。
const int messageStatusNormal = 1;
const int messageStatusRecalled = 2;

/// 会话对端摘要。会话列表与消息历史共用同一个结构。
class ChatPeer {
  const ChatPeer({
    required this.uid,
    required this.nickname,
    required this.avatar,
  });

  final int uid;
  final String nickname;
  final String avatar;

  /// 头像为空时回退显示的文字（昵称首字）。
  String get initial => nickname.isEmpty ? '?' : nickname.substring(0, 1);

  factory ChatPeer.fromJson(Map<String, dynamic> json) {
    return ChatPeer(
      uid: _asInt(json['uid']),
      nickname: _asString(json['nickname']),
      avatar: _asString(json['avatar']),
    );
  }
}

/// 会话列表项。
class Conversation {
  const Conversation({
    required this.convId,
    required this.peer,
    required this.lastMsgId,
    required this.lastType,
    required this.lastPreview,
    required this.lastAt,
    required this.unreadCount,
    required this.pinned,
    required this.muted,
  });

  final int convId;
  final ChatPeer peer;
  final int lastMsgId;
  final int lastType;

  /// 服务端已按 30 字截断并压平换行，客户端不再二次处理。
  final String lastPreview;

  /// 毫秒时间戳。
  final int lastAt;
  final int unreadCount;
  final bool pinned;
  final bool muted;

  String get timeText => conversationTimeText(lastAt);

  String get unreadText => unreadCount > 99 ? '99+' : '$unreadCount';

  factory Conversation.fromJson(Map<String, dynamic> json) {
    return Conversation(
      convId: _asInt(json['conv_id']),
      peer: ChatPeer.fromJson(
        json['peer'] is Map<String, dynamic>
            ? json['peer'] as Map<String, dynamic>
            : const {},
      ),
      lastMsgId: _asInt(json['last_msg_id']),
      lastType: _asInt(json['last_type']),
      lastPreview: _asString(json['last_preview']),
      lastAt: _asInt(json['last_at']),
      unreadCount: _asInt(json['unread_count']),
      pinned: json['pinned'] == true,
      muted: json['muted'] == true,
    );
  }
}

/// 分页会话列表。
class ConversationPage {
  const ConversationPage({
    required this.list,
    required this.page,
    required this.size,
    required this.total,
    required this.unreadTotal,
  });

  final List<Conversation> list;
  final int page;
  final int size;

  /// 会话总数，用于判断是否还有下一页。
  final int total;

  /// 全部会话未读之和，用于标题与 Tab 角标。
  final int unreadTotal;

  factory ConversationPage.fromJson(Map<String, dynamic> json) {
    final raw = json['list'];
    return ConversationPage(
      list: raw is List
          ? raw
              .whereType<Map<String, dynamic>>()
              .map(Conversation.fromJson)
              .toList()
          : const [],
      page: _asInt(json['page']),
      size: _asInt(json['size']),
      total: _asInt(json['total']),
      unreadTotal: _asInt(json['unread_total']),
    );
  }
}

/// 单条消息。
class ChatMessage {
  const ChatMessage({
    required this.id,
    required this.msgId,
    required this.clientMsgId,
    required this.convId,
    required this.fromUid,
    required this.toUid,
    required this.type,
    required this.text,
    required this.status,
    required this.sentAt,
    required this.recalledAt,
    required this.mine,
  });

  /// 本地镜像自增 ID：排序与翻页游标都用它，比毫秒时间戳可靠（同毫秒可区分）。
  final int id;

  /// 服务端/IM 消息 ID：撤回接口按它定位消息。
  final String msgId;

  /// 发送端生成的幂等 ID，服务端据此去重。
  final String clientMsgId;
  final int convId;
  final int fromUid;
  final int toUid;
  final int type;
  final String text;
  final int status;

  /// 毫秒时间戳。
  final int sentAt;

  /// 撤回时间，0 表示未撤回。
  final int recalledAt;

  /// 是否本人发送。由服务端按 viewer 视角给出，客户端不拿 uid 自行比对，
  /// 避免多端登录时判断出错。
  final bool mine;

  bool get recalled => status == messageStatusRecalled;

  String get timeText => messageTimeText(sentAt);

  /// 撤回后服务端不再下发正文，这里统一成占位文案。
  String get displayText => recalled ? '' : text;

  ChatMessage copyWith({int? status, int? recalledAt}) {
    return ChatMessage(
      id: id,
      msgId: msgId,
      clientMsgId: clientMsgId,
      convId: convId,
      fromUid: fromUid,
      toUid: toUid,
      type: type,
      text: text,
      status: status ?? this.status,
      sentAt: sentAt,
      recalledAt: recalledAt ?? this.recalledAt,
      mine: mine,
    );
  }

  factory ChatMessage.fromJson(Map<String, dynamic> json) {
    return ChatMessage(
      id: _asInt(json['id']),
      msgId: _asString(json['msg_id']),
      clientMsgId: _asString(json['client_msg_id']),
      convId: _asInt(json['conv_id']),
      fromUid: _asInt(json['from_uid']),
      toUid: _asInt(json['to_uid']),
      type: _asInt(json['type']),
      text: _asString(json['text']),
      status: _asInt(json['status']),
      sentAt: _asInt(json['sent_at']),
      recalledAt: _asInt(json['recalled_at']),
      mine: json['mine'] == true,
    );
  }
}

/// 消息历史与对端摘要。
class MessagePage {
  const MessagePage({
    required this.peer,
    required this.list,
    required this.nextCursor,
    required this.hasMore,
  });

  final ChatPeer peer;
  final List<ChatMessage> list;

  /// 下一页的 before_id（本页最早一条的 id），0 表示没有更早的了。
  final int nextCursor;
  final bool hasMore;

  factory MessagePage.fromJson(Map<String, dynamic> json) {
    final raw = json['list'];
    return MessagePage(
      peer: ChatPeer.fromJson(
        json['peer'] is Map<String, dynamic>
            ? json['peer'] as Map<String, dynamic>
            : const {},
      ),
      list: raw is List
          ? raw
              .whereType<Map<String, dynamic>>()
              .map(ChatMessage.fromJson)
              .toList()
          : const [],
      nextCursor: _asInt(json['next_cursor']),
      hasMore: json['has_more'] == true,
    );
  }
}

/// 已读回执结果。
class ReadResult {
  const ReadResult({
    required this.peerUid,
    required this.lastReadSeq,
    required this.unreadCount,
    required this.remoteSynced,
  });

  final int peerUid;
  final int lastReadSeq;
  final int unreadCount;

  /// 是否已同步到腾讯云 IM。false 只说明 IM 通道降级，本地已读仍然生效。
  final bool remoteSynced;

  factory ReadResult.fromJson(Map<String, dynamic> json) {
    return ReadResult(
      peerUid: _asInt(json['peer_uid']),
      lastReadSeq: _asInt(json['last_read_seq']),
      unreadCount: _asInt(json['unread_count']),
      remoteSynced: json['remote_synced'] == true,
    );
  }
}

/// 撤回结果。
class RecallResult {
  const RecallResult({
    required this.msgId,
    required this.status,
    required this.recalledAt,
    required this.remoteSynced,
  });

  final String msgId;
  final int status;
  final int recalledAt;
  final bool remoteSynced;

  factory RecallResult.fromJson(Map<String, dynamic> json) {
    return RecallResult(
      msgId: _asString(json['msg_id']),
      status: _asInt(json['status']),
      recalledAt: _asInt(json['recalled_at']),
      remoteSynced: json['remote_synced'] == true,
    );
  }
}

/// 会话列表时间：今天显示时钟，昨天/本周显示相对天，更早显示日期。
String conversationTimeText(int millis) {
  if (millis <= 0) return '';
  final at = DateTime.fromMillisecondsSinceEpoch(millis);
  final now = DateTime.now();
  final days = DateTime(now.year, now.month, now.day)
      .difference(DateTime(at.year, at.month, at.day))
      .inDays;
  if (days <= 0) return _clockText(at);
  if (days == 1) return '昨天';
  if (days < 7) return _weekdayNames[at.weekday - 1];
  return '${_two(at.month)}-${_two(at.day)}';
}

/// 气泡下方的时钟文案。
String messageTimeText(int millis) {
  if (millis <= 0) return '';
  return _clockText(DateTime.fromMillisecondsSinceEpoch(millis));
}

String _clockText(DateTime at) => '${_two(at.hour)}:${_two(at.minute)}';

String _two(int value) => value.toString().padLeft(2, '0');

const List<String> _weekdayNames = [
  '周一',
  '周二',
  '周三',
  '周四',
  '周五',
  '周六',
  '周日',
];

int _asInt(Object? value) {
  if (value is int) return value;
  if (value is num) return value.toInt();
  if (value is String) return int.tryParse(value) ?? 0;
  return 0;
}

String _asString(Object? value) => value is String ? value : '';
