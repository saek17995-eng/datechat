-- =============================================================================
-- P1 增量迁移：账号与实名
--
-- 0001 已建好 user / user_auth / user_profile / user_device / user_login_log /
-- user_realname，本脚本只补一张"验证码发送审计表"。
--
-- 为什么不把发送记录塞进日志系统：
--   1. 需要按手机号做频控复盘（"这个号今天发了 30 条"必须能查）；
--   2. 短信按条计费，月度成本核算要按 provider / scene 聚合；
--   3. 风控要能用 SQL 捞异常，而不是去翻日志平台。
-- =============================================================================

CREATE TABLE IF NOT EXISTS `sms_send_log` (
  `id`         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `phone_hmac` CHAR(64)        NOT NULL COMMENT '手机号 HMAC，便于按号码聚合',
  `phone_mask` VARCHAR(16)     NOT NULL DEFAULT '' COMMENT '脱敏手机号，仅用于人工排查',
  `scene`      VARCHAR(16)     NOT NULL DEFAULT '' COMMENT '场景 login/register/bind',
  `provider`   VARCHAR(16)     NOT NULL DEFAULT '' COMMENT '短信通道 mock/tencent',
  `request_id` VARCHAR(64)     NOT NULL DEFAULT '' COMMENT '服务商流水号，用于对账',
  `ip`         VARCHAR(45)     NOT NULL DEFAULT '',
  `result`     TINYINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '1成功 2失败',
  `error_msg`  VARCHAR(255)    NOT NULL DEFAULT '' COMMENT '失败原因（截断）',
  `created_at` DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_phone_time` (`phone_hmac`, `created_at`),
  KEY `idx_time` (`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='短信验证码发送审计';
