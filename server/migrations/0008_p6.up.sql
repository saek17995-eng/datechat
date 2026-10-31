-- P6 通话与计费：为 call_session 增加"已预扣分钟数"与"最后心跳时间"，
-- 这两列是"续扣幂等"和"掉线自动挂断"的判据，缺了就只能靠客户端上报，不可信。
ALTER TABLE `call_session`
  ADD COLUMN `charged_minutes` SMALLINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '已预扣分钟数，续扣幂等位点' AFTER `room_id`,
  ADD COLUMN `last_beat_at`    DATETIME DEFAULT NULL COMMENT '最后一次心跳时间（服务端时钟）' AFTER `started_at`,
  ADD KEY `idx_beat` (`status`, `last_beat_at`),
  ADD KEY `idx_ring`  (`status`, `created_at`);

-- end_reason 补齐 P6 新增的两类：7 达到单通上限、8 振铃超时未接。
ALTER TABLE `call_session`
  MODIFY COLUMN `end_reason` TINYINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '1正常挂断 2余额不足 3心跳超时 4被叫拒接 5网络异常 6风控中断 7达到上限 8超时未接';

-- 账本补充"通话收益"业务类型：被叫分成由一条独立流水表达，便于与消费流水对账。
ALTER TABLE `wallet_transaction`
  MODIFY COLUMN `biz_type` TINYINT UNSIGNED NOT NULL COMMENT '1充值 2通话消费 3退款 4活动奖励 5人工调整 6通话收益';

-- 通话策略集中放在 app_config，后台可直接改，不必发版。
INSERT INTO `app_config` (`cfg_key`, `cfg_value`, `remark`) VALUES
  ('call.ring_timeout_sec',       '45',   '振铃无应答自动超时（秒）'),
  ('call.heartbeat_interval_sec', '10',   '客户端心跳上报间隔（秒），仅下发建议值'),
  ('call.heartbeat_timeout_sec',  '30',   '超过该时间未收到心跳判定掉线并自动挂断'),
  ('call.max_duration_sec',       '3600', '单通最长时长，兜底防止"忘记挂断"无限计费'),
  ('call.sweep_interval_sec',     '15',   '后台巡检间隔：超时未接、心跳超时、余额耗尽'),
  ('call.min_coins_to_call',      '10',   '低于该金币不允许发起通话')
ON DUPLICATE KEY UPDATE `cfg_value` = VALUES(`cfg_value`), `remark` = VALUES(`remark`);
