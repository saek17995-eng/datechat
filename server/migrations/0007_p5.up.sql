-- P5：钱包与充值 —— 支付回调留痕、对账标记与支付参数。

-- 回调日志：微信回调可能重复投递、也可能乱序到达，
-- 留一份原始报文与处理结论，才能在"用户说付了钱但没到账"时查得清。
CREATE TABLE IF NOT EXISTS `pay_notify_log` (
  `id`           BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `channel`      TINYINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '1微信 2支付宝 3IAP',
  `order_no`     VARCHAR(32)     NOT NULL DEFAULT '',
  `trade_no`     VARCHAR(64)     NOT NULL DEFAULT '' COMMENT '第三方交易号',
  `event_type`   VARCHAR(32)     NOT NULL DEFAULT '' COMMENT 'TRANSACTION.SUCCESS 等',
  `raw`          JSON            DEFAULT NULL COMMENT '原始回调体（脱敏后）',
  `result`       TINYINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '0未处理 1成功 2忽略重复 3失败',
  `error_msg`    VARCHAR(255)    NOT NULL DEFAULT '',
  `created_at`   DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_trade_event` (`channel`, `trade_no`, `event_type`),
  KEY `idx_order` (`order_no`),
  KEY `idx_result_time` (`result`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='支付回调日志';

-- 订单补对账字段：回调丢失时靠查单补单，需要知道补过几次、上次结果是什么。
ALTER TABLE `recharge_order`
  ADD COLUMN `notify_count`  INT UNSIGNED    NOT NULL DEFAULT 0 COMMENT '回调次数，>1 说明重复投递' AFTER `client_ip`,
  ADD COLUMN `sync_count`    INT UNSIGNED    NOT NULL DEFAULT 0 COMMENT '主动查单次数' AFTER `notify_count`,
  ADD COLUMN `last_sync_at`  DATETIME        DEFAULT NULL AFTER `sync_count`,
  ADD COLUMN `reconciled`    TINYINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '是否已对账 0否 1是' AFTER `last_sync_at`,
  ADD KEY `idx_reconciled` (`reconciled`, `status`);

INSERT INTO `app_config` (`cfg_key`, `cfg_value`, `remark`) VALUES
  ('pay.order_expire_min',  '30',     '充值订单未支付自动关闭时长（分钟）'),
  ('pay.max_notify_retry',  '10',     '单个订单允许的最大回调次数，超出只记录不再处理'),
  ('pay.reconcile_window',  '24',     '对账脚本回看小时数'),
  ('pay.mock_enabled',      'false',  '是否允许本地模拟支付成功（仅非生产可用）')
ON DUPLICATE KEY UPDATE `cfg_value` = VALUES(`cfg_value`), `remark` = VALUES(`remark`);
