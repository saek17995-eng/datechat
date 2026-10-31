-- P4 回滚
DROP TABLE IF EXISTS `report`;

ALTER TABLE `admin_op_log`
  DROP COLUMN `note`,
  DROP COLUMN `after_json`,
  DROP COLUMN `before_json`,
  DROP COLUMN `admin_name`;

ALTER TABLE `moderation_record`
  DROP INDEX `idx_result_user`,
  DROP COLUMN `handle_note`,
  DROP COLUMN `handled_at`,
  DROP COLUMN `handled_by`;

ALTER TABLE `user_block` DROP COLUMN `deleted_at`;

DELETE FROM `app_config` WHERE `cfg_key` IN (
  'report.daily_limit', 'report.reason_required',
  'moderation.dfa_enabled', 'moderation.cloud_enabled',
  'moderation.cloud_on_unknown', 'moderation.appeal_enabled'
);
