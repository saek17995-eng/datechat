-- P5 回滚
DROP TABLE IF EXISTS `pay_notify_log`;

ALTER TABLE `recharge_order`
  DROP INDEX `idx_reconciled`,
  DROP COLUMN `reconciled`,
  DROP COLUMN `last_sync_at`,
  DROP COLUMN `sync_count`,
  DROP COLUMN `notify_count`;

DELETE FROM `app_config` WHERE `cfg_key` IN (
  'pay.order_expire_min', 'pay.max_notify_retry', 'pay.reconcile_window', 'pay.mock_enabled'
);
