-- 回滚种子数据：只删本脚本写入的固定行，避免误删运营后续新增的配置。
DELETE FROM `sensitive_word` WHERE `word` IN ('加我微信', '私下转账', '扫码付款', '约炮', '裸聊', '刷单');
DELETE FROM `region_city` WHERE `city_code` IN ('110100', '310100', '440300', '440100', '330100', '510100');
DELETE FROM `recharge_package` WHERE `id` IN (1, 2, 3, 4);
DELETE FROM `call_price_config` WHERE `scope` = 1 AND `user_id` = 0;
DELETE FROM `app_config` WHERE `cfg_key` IN (
  'call.free_seconds', 'call.heartbeat_seconds', 'call.max_minutes', 'call.platform_rate',
  'recharge.enabled', 'recharge.order_expire_minutes', 'realname.required_for_call',
  'moderation.video_frame_seconds', 'im.sdk_app_id', 'trtc.sdk_app_id'
);
