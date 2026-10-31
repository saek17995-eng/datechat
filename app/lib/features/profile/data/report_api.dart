/// 关系与内容安全接口的数据模型与调用：举报、拉黑、取消拉黑、黑名单列表。
///
/// 与服务端 internal/modules/moderation（举报）和 profile（拉黑）对齐。
/// 只做"调接口 + 解模型"，状态由 Riverpod 持有。
library;

import '../../../core/network/dio_client.dart';

/// 举报对象类型，与服务端 moderation.TargetUser / TargetMessage 对齐。
const int reportTargetUser = 1;
const int reportTargetMessage = 2;

/// 举报描述的最大字数，与服务端 moderation 的校验上限一致。
const int reportDetailMaxLength = 500;

/// 重复举报：服务端 errs.Conflict 用的是同一个业务码，客户端靠它把
/// "你已经举报过"和普通参数错误区分开。
const int reportDuplicateCode = 10001;

/// 举报理由。
///
/// 服务端收的是枚举字符串而不是自由文本（便于后台统计与自动化处置），
/// 所以这里把枚举与中文文案绑在一处，避免两处各写一份越写越偏。
class ReportReason {
  const ReportReason(this.code, this.label);

  /// 传给后端的枚举值。
  final String code;

  /// 展示给用户的中文标签。
  final String label;
}

const List<ReportReason> reportReasons = <ReportReason>[
  ReportReason('spam', '垃圾广告'),
  ReportReason('porn', '色情低俗'),
  ReportReason('abuse', '辱骂骚扰'),
  ReportReason('fraud', '诈骗欺诈'),
  ReportReason('minor', '未成年人'),
  ReportReason('other', '其他'),
];

/// 举报回执。status 目前只有 moderation.ReportPending 一种。
class ReportResult {
  const ReportResult({required this.id, required this.status});

  final int id;
  final int status;

  factory ReportResult.fromJson(Map<String, dynamic> json) {
    return ReportResult(
      id: _asInt(json['id']),
      status: _asInt(json['status']),
    );
  }
}

/// 黑名单条目。
class BlockedUser {
  const BlockedUser({
    required this.uid,
    required this.nickname,
    required this.avatar,
    required this.blockedAt,
  });

  final int uid;
  final String nickname;
  final String avatar;

  /// 拉黑时间。这条接口走 Raw Scan 返回，给的是数据库字符串而不是时间戳，
  /// 所以这里保留原值，展示时才按需格式化。
  final String blockedAt;

  String get displayName => nickname.isEmpty ? '用户 $uid' : nickname;

  factory BlockedUser.fromJson(Map<String, dynamic> json) {
    return BlockedUser(
      uid: _asInt(_pick(json, 'uid')),
      nickname: _asString(_pick(json, 'nickname')),
      avatar: _asString(_pick(json, 'avatar')),
      blockedAt: _asString(_pick(json, 'created_at')),
    );
  }
}

/// 黑名单分页。
class BlacklistPage {
  const BlacklistPage({
    required this.list,
    required this.total,
    required this.page,
    required this.size,
  });

  final List<BlockedUser> list;
  final int total;
  final int page;
  final int size;

  bool get isEmpty => list.isEmpty;

  /// 还能不能翻下一页。total 由服务端统计，比"这页满了没"可靠。
  bool get hasMore => page * size < total;

  factory BlacklistPage.fromJson(Map<String, dynamic> json) {
    return BlacklistPage(
      list: json['list'] is List
          ? (json['list'] as List)
              .whereType<Map<String, dynamic>>()
              .map(BlockedUser.fromJson)
              .toList()
          : const [],
      total: _asInt(json['total']),
      page: _asInt(json['page']),
      size: _asInt(json['size']),
    );
  }
}

class SafetyApi {
  SafetyApi(this._client);

  final DioClient _client;

  /// 提交举报。
  Future<ReportResult> submitReport({
    required int targetType,
    required String targetId,
    int targetUid = 0,
    required String reason,
    required String detail,
    List<String> evidence = const <String>[],
  }) async {
    final data = await _client.post(
      '/v1/reports',
      body: {
        'target_type': targetType,
        'target_id': targetId,
        'target_uid': targetUid,
        'reason': reason,
        'detail': detail,
        'evidence': evidence,
      },
    );
    return ReportResult.fromJson(data);
  }

  /// 拉黑某人。重复拉黑在服务端是幂等的，客户端不需要先查状态。
  Future<void> blockUser(int uid) async {
    await _client.post('/v1/users/$uid/block');
  }

  /// 取消拉黑。本来就没拉黑也返回成功，客户端不必区分。
  Future<void> unblockUser(int uid) async {
    await _client.delete('/v1/users/$uid/block');
  }

  /// 我的黑名单。page 从 1 开始。
  Future<BlacklistPage> blacklist({int page = 1, int size = 20}) async {
    final data = await _client.get(
      '/v1/users/me/blocks',
      query: {'page': page, 'size': size},
    );
    return BlacklistPage.fromJson(data);
  }
}

/// 黑名单结构体在服务端只声明了 gorm 标签，序列化出来的 key 可能是
/// created_at 也可能是 CreatedAt；两种都接受，取到哪个用哪个。
Object? _pick(Map<String, dynamic> json, String key) {
  final value = json[key];
  if (value != null) return value;
  final camel = key.replaceAllMapped(
    RegExp(r'_(\w)'),
    (match) => match.group(1)!.toUpperCase(),
  );
  return json[camel] ?? json['${camel[0].toUpperCase()}${camel.substring(1)}'];
}

int _asInt(Object? value) {
  if (value is int) return value;
  if (value is num) return value.toInt();
  if (value is String) return int.tryParse(value) ?? 0;
  return 0;
}

String _asString(Object? value) => value is String ? value : '';
