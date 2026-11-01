import 'package:flutter_test/flutter_test.dart';

import 'package:datechat/core/realtime/realtime_event.dart';

void main() {
  group('RealtimeEvent', () {
    test('新消息帧能拿到 seq / 发送者 / 正文', () {
      final event = RealtimeEvent.fromJson(<String, dynamic>{
        'type': 'new_message',
        'seq': 12,
        'data': <String, dynamic>{
          'from_uid': 42,
          'nickname': '小美',
          'text': '在吗',
        },
      });
      expect(event.type, rtTypeNewMessage);
      expect(event.seq, 12);
      expect(event.data['text'], '在吗');
    });

    test('signal 帧从 data 补出 from 与 kind', () {
      final event = RealtimeEvent.fromJson(<String, dynamic>{
        'type': 'signal',
        'call_no': 'C202611010000001234',
        'kind': 'offer',
        'payload': <String, dynamic>{'sdp': 'v=0'},
        'data': <String, dynamic>{'from': 7, 'kind': 'offer'},
      });
      expect(event.callNo, 'C202611010000001234');
      expect(event.kind, signalOffer);
      expect(event.from, 7);
      expect(event.payload['sdp'], 'v=0');
    });

    // 服务端加新事件类型时，老客户端必须能安全忽略，而不是把整条连接搞崩。
    test('未知类型不会解析失败', () {
      final event = RealtimeEvent.fromJson(<String, dynamic>{
        'type': 'some_future_event',
      });
      expect(event.type, 'some_future_event');
      expect(event.data, isEmpty);
    });

    test('data 类型异常时退化为空表', () {
      final event = RealtimeEvent.fromJson(<String, dynamic>{
        'type': 'new_message',
        'data': 'not-a-map',
      });
      expect(event.data, isEmpty);
    });
  });
}
