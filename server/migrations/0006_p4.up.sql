-- P4：关系与内容安全 —— 举报、后台处置账号与操作审计。
-- 设计要点：
--   1. 举报与处置分离：report 只记录"用户说"，处置结论写 admin_operation_log，
--      避免把运营动作和用户诉求混在一张表里导致对不上账。
--   2. 后台账号独立于 user 表：运营人员不是 App 用户，不该有手机号/IM 账号，
--      混用会让封禁、拉黑等逻辑出现"管理员被用户拉黑"这类怪状态。

-- 举报
CREATE TABLE IF NOT EXISTS `report` (
  `id`             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `reporter_id`    BIGINT UNSIGNED NOT NULL COMMENT '举报人',
  `target_type`    TINYINT UNSIGNED NOT NULL COMMENT '1用户 2消息 3通话 4头像',
  `target_id`      VARCHAR(64)     NOT NULL COMMENT '目标ID：用户ID/消息ID/通话号',
  `target_uid`     BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '被举报人（冗余，便于按人聚合）',
  `reason`         VARCHAR(32)     NOT NULL COMMENT '举报理由编码：spam/porn/abuse/fraud/other',
  `detail`         VARCHAR(500)    NOT NULL DEFAULT '' COMMENT '用户补充描述',
  `evidence`       JSON            DEFAULT NULL COMMENT '证据图片URL数组',
  `status`         TINYINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '1待处理 2已处置 3已驳回',
  `handler_id`     BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '处理人（admin_user.id）',
  `handle_result`  VARCHAR(32)     NOT NULL DEFAULT '' COMMENT '处置结果：warn/freeze/ban/ignore',
  `handle_note`    VARCHAR(500)    NOT NULL DEFAULT '' COMMENT '处置备注',
  `handled_at`     DATETIME        DEFAULT NULL,
  `created_at`     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_reporter_target` (`reporter_id`, `target_type`, `target_id`),
  KEY `idx_status_time` (`status`, `created_at`),
  KEY `idx_target_uid` (`target_uid`, `created_at`),
  KEY `idx_reporter_time` (`reporter_id`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='用户举报';

-- 后台账号复用 P0 已建的 admin_user（role 为 TINYINT：1超管 2运营 3审核 4客服）。
-- 这里刻意不再 CREATE TABLE IF NOT EXISTS：那会在表已存在时静默跳过，
-- 却让代码按新字段（varchar role）去读写，直到运行期才报类型错。

-- 后台操作审计：谁在什么时候对什么做了什么，合规留存。
-- 复用 P0 已有的 admin_op_log，只补审计所需的姓名冗余与变更前后快照；
-- 不新建第二张审计表，否则运营看板要 union 两处且容易漏记。
ALTER TABLE `admin_op_log`
  ADD COLUMN `admin_name`  VARCHAR(32)  NOT NULL DEFAULT '' AFTER `admin_id`,
  ADD COLUMN `before_json` JSON         DEFAULT NULL AFTER `detail`,
  ADD COLUMN `after_json`  JSON         DEFAULT NULL AFTER `before_json`,
  ADD COLUMN `note`        VARCHAR(500) NOT NULL DEFAULT '' AFTER `after_json`;

-- 审核记录补处置字段：人工在后台复核后写入结论，与机器判定区分开
ALTER TABLE `moderation_record`
  ADD COLUMN `handled_by`  BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '人工复核人 admin_user.id' AFTER `raw`,
  ADD COLUMN `handled_at`  DATETIME DEFAULT NULL AFTER `handled_by`,
  ADD COLUMN `handle_note` VARCHAR(255) NOT NULL DEFAULT '' AFTER `handled_at`,
  ADD KEY `idx_result_user` (`result`, `user_id`);

-- 拉黑表补删除标记：取消拉黑用软删，保留"曾拉黑过"的痕迹用于风控
ALTER TABLE `user_block`
  ADD COLUMN `deleted_at` DATETIME DEFAULT NULL AFTER `created_at`;

INSERT INTO `app_config` (`cfg_key`, `cfg_value`, `remark`) VALUES
  ('report.daily_limit',          '20',   '单用户单日举报上限，防止恶意刷举报'),
  ('report.reason_required',      'false','是否强制要求填写补充描述'),
  ('moderation.dfa_enabled',      'true', '是否启用本地 DFA 敏感词'),
  ('moderation.cloud_enabled',    'false','是否启用腾讯云文本审核（TMS），需配置密钥'),
  ('moderation.cloud_on_unknown', 'false','本地未命中时是否仍送云审核（增加延迟与成本）'),
  ('moderation.appeal_enabled',   'true', '是否允许用户对被拦截内容申诉')
ON DUPLICATE KEY UPDATE `cfg_value` = VALUES(`cfg_value`), `remark` = VALUES(`remark`);
