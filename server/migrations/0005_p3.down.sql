-- 回滚 P3 数据结构。app_config 保留无害配置，避免误删运营调整值。

ALTER TABLE `im_callback_log`
  DROP KEY `idx_processed_time`,
  DROP KEY `idx_command_time`,
  DROP COLUMN `updated_at`,
  DROP COLUMN `processed_at`,
  DROP COLUMN `command`,
  DROP COLUMN `sdk_app_id`;

ALTER TABLE `message`
  DROP KEY `idx_conv_status_seq`,
  DROP KEY `uk_from_client`,
  DROP COLUMN `updated_at`,
  DROP COLUMN `recalled_by`,
  DROP COLUMN `client_msg_id`,
  DROP COLUMN `msg_key`,
  MODIFY COLUMN `msg_id` VARCHAR(64) DEFAULT NULL COMMENT '腾讯IM 消息ID，回调幂等键';

ALTER TABLE `conversation_member`
  DROP KEY `idx_user_deleted_updated`,
  DROP COLUMN `last_msg_seq`;

ALTER TABLE `conversation`
  DROP COLUMN `last_msg_preview`,
  DROP COLUMN `last_msg_type`;
