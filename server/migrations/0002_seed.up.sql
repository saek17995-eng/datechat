-- 初始化种子数据：运行时参数、充值档位、通话定价、城市字典、敏感词示例。
-- 全部使用 ON DUPLICATE KEY UPDATE，保证重复执行不报错。

INSERT INTO `app_config` (`cfg_key`, `cfg_value`, `remark`) VALUES
  ('call.free_seconds',                 '10',   '接通后免费体验秒数，不计费'),
  ('call.heartbeat_seconds',            '15',   '客户端上报通话心跳的间隔'),
  ('call.max_minutes',                  '60',   '单次通话最长分钟数，到时强制结束'),
  ('call.platform_rate',                '300',  '平台默认抽成‰，自定价模式下生效'),
  ('recharge.enabled',                  'true', '充值开关'),
  ('recharge.order_expire_minutes',     '30',   '充值订单超时关闭时间'),
  ('realname.required_for_call',        'true', '未实名是否禁止发起付费通话'),
  ('moderation.video_frame_seconds',    '10',   '视频通话截图送审间隔'),
  ('im.sdk_app_id',                     '0',    '腾讯IM SDKAppID，上线前在后台配置'),
  ('trtc.sdk_app_id',                   '0',    'TRTC SDKAppID，上线前在后台配置')
ON DUPLICATE KEY UPDATE `cfg_value` = VALUES(`cfg_value`), `remark` = VALUES(`remark`);

INSERT INTO `recharge_package` (`id`, `coins`, `bonus_coins`, `price_fen`, `channel`, `sort`, `status`) VALUES
  (1, 60,   0,   600,   0, 40, 1),
  (2, 300,  0,   3000,  0, 30, 1),
  (3, 680,  30,  6800,  0, 20, 1),
  (4, 1280, 100, 12800, 0, 10, 1)
ON DUPLICATE KEY UPDATE `coins` = VALUES(`coins`), `bonus_coins` = VALUES(`bonus_coins`),
  `price_fen` = VALUES(`price_fen`), `sort` = VALUES(`sort`), `status` = VALUES(`status`);

INSERT INTO `call_price_config` (`scope`, `user_id`, `audio_per_min`, `video_per_min`, `platform_rate`, `status`)
VALUES (1, 0, 50, 120, 300, 1)
ON DUPLICATE KEY UPDATE `audio_per_min` = VALUES(`audio_per_min`),
  `video_per_min` = VALUES(`video_per_min`), `platform_rate` = VALUES(`platform_rate`);

INSERT INTO `region_city` (`city_code`, `province`, `city`, `lng`, `lat`, `sort`, `status`) VALUES
  ('110100', '北京市',   '北京', 116.407526, 39.904030, 100, 1),
  ('310100', '上海市',   '上海', 121.473701, 31.230416, 90,  1),
  ('440300', '广东省',   '深圳', 114.057868, 22.543099, 80,  1),
  ('440100', '广东省',   '广州', 113.264434, 23.129162, 70,  1),
  ('330100', '浙江省',   '杭州', 120.155070, 30.274084, 60,  1),
  ('510100', '四川省',   '成都', 104.066541, 30.572269, 50,  1)
ON DUPLICATE KEY UPDATE `province` = VALUES(`province`), `city` = VALUES(`city`),
  `lng` = VALUES(`lng`), `lat` = VALUES(`lat`), `sort` = VALUES(`sort`);

-- 敏感词仅作占位示例，上线前必须由运营用完整词库覆盖（后台导入会走同一张表）。
INSERT INTO `sensitive_word` (`word`, `category`, `level`, `status`) VALUES
  ('加我微信',   '导流', 1, 1),
  ('私下转账',   '导流', 1, 1),
  ('扫码付款',   '导流', 1, 1),
  ('约炮',       '涉黄', 1, 1),
  ('裸聊',       '涉黄', 1, 1),
  ('刷单',       '违规', 1, 1)
ON DUPLICATE KEY UPDATE `category` = VALUES(`category`), `level` = VALUES(`level`), `status` = VALUES(`status`);
