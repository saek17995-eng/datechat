-- =============================================================================
-- P2 增量迁移：资料、头像上传、他人主页
--
-- 新增：
--   1. upload_file  上传文件台账（本地盘 / COS 统一登记），用于：
--      - 头像/相册提交时校验"这个 URL 确实是我们签发出去的"，防止客户端伪造外链；
--      - 内容审核与运营后台复核（审核状态、审核人、审核时间）；
--      - 孤儿文件清理（上传了但从未提交的文件可被定期回收）。
--
-- 为什么不做 user_photo 独立表：
--   相册上限只有 9 张，且几乎总是随用户主页整体读取，
--   用 user_profile.photos 的 JSON 列能少一次 JOIN；扩容到"相册动态/评论"时再拆表。
-- =============================================================================

CREATE TABLE IF NOT EXISTS `upload_file` (
  `id`          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `user_id`     BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '上传者',
  `scene`       TINYINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '1头像 2相册 3语音自我介绍 4举报证据',
  `storage`     TINYINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '1本地盘 2腾讯云COS',
  `url`         VARCHAR(512)    NOT NULL DEFAULT '' COMMENT '可访问地址（本地为相对路径）',
  `object_key`  VARCHAR(255)    NOT NULL DEFAULT '' COMMENT 'COS 对象键 / 本地相对路径',
  `file_size`   INT UNSIGNED    NOT NULL DEFAULT 0 COMMENT '字节数',
  `mime_type`   VARCHAR(64)     NOT NULL DEFAULT '' COMMENT 'image/jpeg 等',
  `width`       SMALLINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '图片宽，非图片为 0',
  `height`      SMALLINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '图片高，非图片为 0',
  `checksum`    CHAR(64)        NOT NULL DEFAULT '' COMMENT '内容 SHA-256，用于去重与取证',
  `status`      TINYINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '1待审核 2已通过 3已拒绝',
  `used`        TINYINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '是否已被业务引用（头像/相册），0 未引用',
  `reviewed_by` BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '审核人 admin_user.id，0 未人工处理',
  `reviewed_at` DATETIME        DEFAULT NULL,
  `created_at`  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`  DATETIME        DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_object_key` (`object_key`),
  KEY `idx_user_scene_time` (`user_id`, `scene`, `created_at`),
  KEY `idx_status_time` (`status`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='上传文件台账';

-- -----------------------------------------------------------------------------
-- 运行时参数：上传与资料相关的开关，运营后台可直接改，不必重新发版
-- -----------------------------------------------------------------------------
INSERT INTO `app_config` (`cfg_key`, `cfg_value`, `remark`) VALUES
  ('upload.provider',              '"local"', '上传通道 local|cos，local 走本地盘便于自测'),
  ('upload.max_image_mb',          '5',     '单张图片大小上限（MB）'),
  ('upload.avatar_review',         'false', '头像是否需要人工/机审后才公开展示'),
  ('profile.nickname_max_len',     '16',    '昵称最大字符数'),
  ('profile.bio_max_len',          '200',   '签名最大字符数'),
  ('profile.max_tags',             '6',     '标签最多几个'),
  ('profile.max_photos',           '9',     '相册最多几张'),
  ('profile.min_age',              '18',    '注册最低年龄，生日校验用'),
  ('profile.default_avatar',        '""',    '未上传头像时的兜底地址，为空则客户端用首字母占位')
ON DUPLICATE KEY UPDATE `cfg_value` = VALUES(`cfg_value`), `remark` = VALUES(`remark`);
