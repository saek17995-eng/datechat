-- P8 运营后台与上线准备。
-- 这一批改动只加"运营与合规需要的字段"，不动业务表结构。

-- 封禁需要"封多久、为什么"：只改 status 不记原因，
-- 三个月后没人说得清这个号为什么被封，申诉时只能靠猜。
ALTER TABLE `user`
  ADD COLUMN `ban_until`  DATETIME      DEFAULT NULL COMMENT '封禁到期时间，NULL 表示永久' AFTER `status`,
  ADD COLUMN `ban_reason` VARCHAR(128)  NOT NULL DEFAULT '' COMMENT '最近一次封禁原因' AFTER `ban_until`,
  ADD KEY `idx_ban_until` (`ban_until`);

-- 注意：app_config.cfg_value 是 JSON 列，空串不是合法 JSON，默认值要写 '""'。
-- 合规配置集中放，避免散落在代码里改一次发一次版。
INSERT INTO `app_config` (`cfg_key`, `cfg_value`, `remark`) VALUES
  ('compliance.min_age',                 '18',  '注册与资料的最低年龄，低于该值不允许使用'),
  ('compliance.realname_required_call',  'true','发起通话是否要求主叫已实名'),
  ('compliance.realname_required_cash',  'true','提现是否要求已实名'),
  ('compliance.max_recharge_fen_day',    '200000','未实名用户单日充值上限（分）'),
  ('agreement.privacy_url',              '""',  '隐私政策链接'),
  ('agreement.terms_url',                '""',  '用户协议链接'),
  ('agreement.version',                  '1',   '协议版本号，更新后客户端需重新弹窗确认'),
  ('ops.retention_days',                 '180', '业务日志与历史数据保留天数')
ON DUPLICATE KEY UPDATE `cfg_value` = VALUES(`cfg_value`), `remark` = VALUES(`remark`);
