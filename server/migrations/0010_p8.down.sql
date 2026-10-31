-- 回滚 P8 的数据侧改动。
ALTER TABLE `user`
  DROP KEY `idx_ban_until`,
  DROP COLUMN `ban_until`,
  DROP COLUMN `ban_reason`;

DELETE FROM `app_config` WHERE `cfg_key` IN (
  'compliance.min_age',
  'compliance.realname_required_call',
  'compliance.realname_required_cash',
  'compliance.max_recharge_fen_day',
  'agreement.privacy_url',
  'agreement.terms_url',
  'agreement.version',
  'ops.retention_days'
);
