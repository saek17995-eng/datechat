-- P6 回滚
DELETE FROM `app_config` WHERE `cfg_key` IN (
  'call.ring_timeout_sec', 'call.heartbeat_interval_sec', 'call.heartbeat_timeout_sec',
  'call.max_duration_sec', 'call.sweep_interval_sec', 'call.min_coins_to_call'
);

ALTER TABLE `wallet_transaction`
  MODIFY COLUMN `biz_type` TINYINT UNSIGNED NOT NULL COMMENT '1充值 2通话消费 3退款 4活动奖励 5人工调整';

ALTER TABLE `call_session`
  MODIFY COLUMN `end_reason` TINYINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '1正常挂断 2余额不足 3心跳超时 4被叫拒接 5网络异常 6风控中断',
  DROP KEY `idx_beat`,
  DROP KEY `idx_ring`,
  DROP COLUMN `charged_minutes`,
  DROP COLUMN `last_beat_at`;
