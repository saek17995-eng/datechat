-- P3：腾讯云 IM 会话镜像、消息幂等、未读游标与回调审计。

-- 会话列表直接展示最后消息，避免每行再回表查询 message。
ALTER TABLE `conversation`
  ADD COLUMN `last_msg_type` TINYINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '最后消息类型，0无消息' AFTER `last_msg_id`,
  ADD COLUMN `last_msg_preview` VARCHAR(100) NOT NULL DEFAULT '' COMMENT '会话列表摘要，已脱敏' AFTER `last_msg_type`;

-- last_msg_seq 使未读数可以由 last_msg_seq-last_read_seq 自愈；补充会话列表过滤索引。
ALTER TABLE `conversation_member`
  ADD COLUMN `last_msg_seq` BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '当前会话已知最大消息序号' AFTER `last_read_seq`,
  ADD KEY `idx_user_deleted_updated` (`user_id`, `is_deleted`, `updated_at`);

-- 历史脏数据先生成稳定幂等键，再收紧 NOT NULL。
UPDATE `message`
SET `msg_id` = CONCAT('legacy-', `id`)
WHERE `msg_id` IS NULL OR `msg_id` = '';

ALTER TABLE `message`
  MODIFY COLUMN `msg_id` VARCHAR(64) NOT NULL COMMENT '腾讯 IM MsgId；本地降级为 local-*，回调幂等键',
  ADD COLUMN `msg_key` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '腾讯 IM MsgKey，撤回接口使用' AFTER `msg_id`,
  ADD COLUMN `client_msg_id` VARCHAR(64) DEFAULT NULL COMMENT '客户端幂等键，同一发送者内唯一' AFTER `msg_key`,
  ADD COLUMN `recalled_by` BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '撤回操作者用户ID' AFTER `recalled_at`,
  ADD COLUMN `updated_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP AFTER `created_at`,
  ADD UNIQUE KEY `uk_from_client` (`from_uid`, `client_msg_id`),
  ADD KEY `idx_conv_status_seq` (`conv_id`, `status`, `seq`);

-- 回调日志记录命令、来源应用与处理完成时间，便于定位回调丢失/重试。
ALTER TABLE `im_callback_log`
  ADD COLUMN `sdk_app_id` BIGINT UNSIGNED NOT NULL DEFAULT 0 AFTER `id`,
  ADD COLUMN `command` VARCHAR(64) NOT NULL DEFAULT '' AFTER `callback_type`,
  ADD COLUMN `processed_at` DATETIME DEFAULT NULL AFTER `error_msg`,
  ADD COLUMN `updated_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP AFTER `created_at`,
  ADD KEY `idx_command_time` (`command`, `created_at`),
  ADD KEY `idx_processed_time` (`processed`, `created_at`);

INSERT INTO `app_config` (`cfg_key`, `cfg_value`, `remark`) VALUES
  ('im.message_max_chars', '500', '单条文本消息最大字符数'),
  ('im.recall_window_sec', '120', '用户主动撤回时间窗，单位秒'),
  ('im.moderation_enabled', 'true', '是否启用消息前本地敏感词审核'),
  ('im.callback_max_skew_sec', '60', 'IM 回调签名允许的时间偏差，单位秒')
ON DUPLICATE KEY UPDATE `cfg_value` = VALUES(`cfg_value`), `remark` = VALUES(`remark`);
