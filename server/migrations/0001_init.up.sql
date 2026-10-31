-- =============================================================================
-- DateChat 初始化建表脚本（MySQL 8.0）
--
-- 约定：
--   1. 全站不使用外键约束：由应用层保证引用完整性，避免高并发下的锁竞争与级联删除风险；
--   2. 金额统一 BIGINT 存“分”，金币统一 BIGINT 存“整数金币”，禁止浮点；
--   3. 手机号/身份证等敏感字段加密存储（*_cipher），并冗余 HMAC 哈希列（*_hmac）做唯一索引；
--   4. 状态字段统一 TINYINT，取值含义写在列注释里，Go 侧用常量映射；
--   5. 所有表带 created_at / updated_at，软删用 deleted_at（仅对需要的表启用）。
-- =============================================================================

-- -----------------------------------------------------------------------------
-- 一、账号与实名
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS `user` (
  `id`             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '用户ID',
  `phone_hmac`     CHAR(64)        NOT NULL COMMENT '手机号 HMAC-SHA256(hex)，用于唯一索引与查询',
  `phone_cipher`   VARBINARY(128)  NOT NULL COMMENT '手机号密文 AES-256-GCM',
  `nickname`       VARCHAR(32)     NOT NULL DEFAULT '' COMMENT '昵称',
  `avatar`         VARCHAR(255)    NOT NULL DEFAULT '' COMMENT '头像 URL',
  `gender`         TINYINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '性别 0未知 1男 2女',
  `birthday`       DATE            DEFAULT NULL COMMENT '生日，用于年龄展示与筛选',
  `city_code`      VARCHAR(16)     NOT NULL DEFAULT '' COMMENT '城市编码，如 440300',
  `im_user_id`     VARCHAR(64)     DEFAULT NULL COMMENT '腾讯IM UserID，生成规则 u_{id}',
  `status`         TINYINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '状态 1正常 2冻结 3封禁 4注销',
  `register_ip`    VARCHAR(45)     NOT NULL DEFAULT '' COMMENT '注册IP（含IPv6）',
  `last_active_at` DATETIME        DEFAULT NULL COMMENT '最后活跃时间',
  `created_at`     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  `deleted_at`     DATETIME        DEFAULT NULL COMMENT '软删时间，NULL 表示未删除',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_phone_hmac` (`phone_hmac`),
  UNIQUE KEY `uk_im_user_id` (`im_user_id`),
  KEY `idx_status_active` (`status`, `last_active_at`),
  KEY `idx_city_gender` (`city_code`, `gender`, `status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='用户主表';

CREATE TABLE IF NOT EXISTS `user_auth` (
  `id`         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `user_id`    BIGINT UNSIGNED NOT NULL COMMENT '用户ID',
  `auth_type`  TINYINT UNSIGNED NOT NULL COMMENT '认证方式 1手机号 2微信 3Apple 4QQ',
  `identifier` VARCHAR(128)    NOT NULL COMMENT '第三方唯一标识；手机号为 phone_hmac',
  `credential` VARCHAR(255)    NOT NULL DEFAULT '' COMMENT '口令哈希（bcrypt），第三方登录为空',
  `status`     TINYINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '状态 1有效 2解绑',
  `created_at` DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_type_identifier` (`auth_type`, `identifier`),
  KEY `idx_user` (`user_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='用户登录方式';

CREATE TABLE IF NOT EXISTS `user_profile` (
  `user_id`         BIGINT UNSIGNED NOT NULL COMMENT '用户ID，与 user 一对一',
  `height`          SMALLINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '身高cm，0未填',
  `job`             VARCHAR(32)     NOT NULL DEFAULT '' COMMENT '职业',
  `tags`            JSON            DEFAULT NULL COMMENT '标签数组 ["温柔","爱旅行"]',
  `photos`          JSON            DEFAULT NULL COMMENT '相册 URL 数组，最多9张',
  `voice_intro`     VARCHAR(255)    NOT NULL DEFAULT '' COMMENT '语音自我介绍 URL',
  `bio`             VARCHAR(500)    NOT NULL DEFAULT '' COMMENT '个人签名',
  `price_audio`     INT UNSIGNED    NOT NULL DEFAULT 0 COMMENT '自定价：语音每分钟金币，0 表示跟随全局价',
  `price_video`     INT UNSIGNED    NOT NULL DEFAULT 0 COMMENT '自定价：视频每分钟金币，0 表示跟随全局价',
  `price_enabled`   TINYINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '是否开启自定价 0否 1是',
  `realname_status` TINYINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '实名状态冗余 0未认证 1审核中 2已认证 3未通过',
  `created_at`      DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`      DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`user_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='用户扩展资料与定价';

CREATE TABLE IF NOT EXISTS `user_realname` (
  `id`              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `user_id`         BIGINT UNSIGNED NOT NULL COMMENT '用户ID',
  `real_name_cipher` VARBINARY(128) NOT NULL COMMENT '姓名密文',
  `id_card_hmac`    CHAR(64)        NOT NULL COMMENT '身份证号 HMAC，用于查重（一人一号）',
  `id_card_cipher`  VARBINARY(255)  NOT NULL COMMENT '身份证号密文',
  `status`          TINYINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '0待提交 1审核中 2通过 3拒绝',
  `channel`         TINYINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '渠道 1腾讯云人脸核身 2阿里云实人认证 3人工',
  `provider_trace`  VARCHAR(64)     NOT NULL DEFAULT '' COMMENT '服务商流水号，用于对账与申诉',
  `fail_reason`     VARCHAR(128)    NOT NULL DEFAULT '' COMMENT '失败原因',
  `verified_at`     DATETIME        DEFAULT NULL COMMENT '通过时间',
  `created_at`      DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`      DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_user` (`user_id`),
  UNIQUE KEY `uk_id_card` (`id_card_hmac`),
  KEY `idx_status` (`status`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='实名认证记录';

CREATE TABLE IF NOT EXISTS `user_device` (
  `id`             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `user_id`        BIGINT UNSIGNED NOT NULL,
  `device_id`      VARCHAR(64)     NOT NULL COMMENT '客户端生成的设备唯一标识',
  `platform`       TINYINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '平台 1iOS 2Android 3Web',
  `push_token`     VARCHAR(255)    NOT NULL DEFAULT '' COMMENT '推送令牌',
  `app_version`    VARCHAR(32)     NOT NULL DEFAULT '',
  `last_ip`        VARCHAR(45)     NOT NULL DEFAULT '',
  `last_active_at` DATETIME        DEFAULT NULL,
  `created_at`     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_user_device` (`user_id`, `device_id`),
  KEY `idx_device` (`device_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='用户设备与推送令牌';

CREATE TABLE IF NOT EXISTS `user_login_log` (
  `id`         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `user_id`    BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '登录失败且用户不存在时为0',
  `phone_hmac` CHAR(64)        NOT NULL DEFAULT '' COMMENT '尝试登录的手机号哈希，便于风控',
  `device_id`  VARCHAR(64)     NOT NULL DEFAULT '',
  `ip`         VARCHAR(45)     NOT NULL DEFAULT '',
  `user_agent` VARCHAR(255)    NOT NULL DEFAULT '',
  `result`     TINYINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '1成功 2失败',
  `reason`     VARCHAR(64)     NOT NULL DEFAULT '' COMMENT '失败原因',
  `created_at` DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_user_time` (`user_id`, `created_at`),
  KEY `idx_time` (`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='登录日志（风控与审计）';

-- -----------------------------------------------------------------------------
-- 二、关系与举报
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS `user_block` (
  `id`              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `user_id`         BIGINT UNSIGNED NOT NULL COMMENT '发起拉黑的人',
  `blocked_user_id` BIGINT UNSIGNED NOT NULL COMMENT '被拉黑的人',
  `created_at`      DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_user_blocked` (`user_id`, `blocked_user_id`),
  KEY `idx_blocked` (`blocked_user_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='拉黑关系';

CREATE TABLE IF NOT EXISTS `user_report` (
  `id`             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `reporter_id`    BIGINT UNSIGNED NOT NULL COMMENT '举报人',
  `target_type`    TINYINT UNSIGNED NOT NULL COMMENT '1用户 2消息 3通话 4头像/相册',
  `target_id`      BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '被举报对象ID（用户/消息/通话）',
  `target_user_id` BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '被举报人，便于按人统计与封禁',
  `reason_code`    TINYINT UNSIGNED NOT NULL COMMENT '原因码 1色情 2诈骗 3广告 4辱骂 5未成年人 6其他',
  `description`    VARCHAR(500)    NOT NULL DEFAULT '' COMMENT '补充说明',
  `evidence`       JSON            DEFAULT NULL COMMENT '证据快照：截图URL、消息内容、通话ID',
  `status`         TINYINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '0待处理 1有效 2无效 3已封禁',
  `handled_by`     BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '处理人（admin_user.id）',
  `handled_at`     DATETIME        DEFAULT NULL,
  `handle_note`    VARCHAR(255)    NOT NULL DEFAULT '',
  `created_at`     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_status_time` (`status`, `created_at`),
  KEY `idx_target_user` (`target_user_id`, `created_at`),
  KEY `idx_reporter` (`reporter_id`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='举报工单';

-- -----------------------------------------------------------------------------
-- 三、会话与消息（腾讯 IM 的镜像库，仅用于审计、取证、后台检索）
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS `conversation` (
  `id`          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `user_a_id`   BIGINT UNSIGNED NOT NULL COMMENT '较小的 user_id',
  `user_b_id`   BIGINT UNSIGNED NOT NULL COMMENT '较大的 user_id',
  `last_msg_id` BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '最后一条消息ID（message.id）',
  `last_msg_at` DATETIME(3)     DEFAULT NULL COMMENT '最后消息时间，会话列表排序用',
  `msg_count`   INT UNSIGNED    NOT NULL DEFAULT 0,
  `created_at`  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_pair` (`user_a_id`, `user_b_id`),
  KEY `idx_last_msg` (`last_msg_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='单聊会话';

CREATE TABLE IF NOT EXISTS `conversation_member` (
  `id`            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `conv_id`       BIGINT UNSIGNED NOT NULL,
  `user_id`       BIGINT UNSIGNED NOT NULL,
  `last_read_seq` BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '已读游标（IM 已读回执的本地冗余）',
  `unread_count`  INT UNSIGNED    NOT NULL DEFAULT 0,
  `is_pinned`     TINYINT UNSIGNED NOT NULL DEFAULT 0,
  `is_muted`      TINYINT UNSIGNED NOT NULL DEFAULT 0,
  `is_deleted`    TINYINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '用户删除会话（单向隐藏）',
  `created_at`    DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`    DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_conv_user` (`conv_id`, `user_id`),
  KEY `idx_user_updated` (`user_id`, `updated_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='会话成员状态';

CREATE TABLE IF NOT EXISTS `message` (
  `id`          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `msg_id`      VARCHAR(64)     DEFAULT NULL COMMENT '腾讯IM 消息ID，回调幂等键',
  `conv_id`     BIGINT UNSIGNED NOT NULL,
  `from_uid`    BIGINT UNSIGNED NOT NULL,
  `to_uid`      BIGINT UNSIGNED NOT NULL,
  `msg_type`    TINYINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '1文本 2图片 3语音 4视频 5通话记录 6系统',
  `content`     JSON            DEFAULT NULL COMMENT '消息体，含文本/媒体URL/时长',
  `seq`         BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT 'IM 消息序列号，会话内递增',
  `status`      TINYINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '1正常 2已撤回 3已删除',
  `sent_at`     DATETIME(3)     DEFAULT NULL COMMENT '发送时间（毫秒精度）',
  `recalled_at` DATETIME        DEFAULT NULL COMMENT '撤回时间',
  `created_at`  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_msg_id` (`msg_id`),
  KEY `idx_conv_seq` (`conv_id`, `seq`),
  KEY `idx_from_time` (`from_uid`, `created_at`),
  KEY `idx_to_time` (`to_uid`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='消息镜像（审计用）';

CREATE TABLE IF NOT EXISTS `im_callback_log` (
  `id`            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `callback_type` VARCHAR(32)     NOT NULL COMMENT '回调类型：before_send / after_send / recall 等',
  `payload_hash`  CHAR(64)        NOT NULL COMMENT '原始报文 SHA256，用于幂等去重',
  `raw`           JSON            DEFAULT NULL COMMENT '原始报文',
  `processed`     TINYINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '0未处理 1已处理 2处理失败',
  `retry_count`   SMALLINT UNSIGNED NOT NULL DEFAULT 0,
  `error_msg`     VARCHAR(255)    NOT NULL DEFAULT '',
  `created_at`    DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_payload_hash` (`payload_hash`),
  KEY `idx_type_time` (`callback_type`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='IM 回调原始日志';

-- -----------------------------------------------------------------------------
-- 四、通话与计费
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS `call_session` (
  `id`               BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `call_no`          VARCHAR(32)     NOT NULL COMMENT '通话单号',
  `caller_id`        BIGINT UNSIGNED NOT NULL COMMENT '主叫',
  `callee_id`        BIGINT UNSIGNED NOT NULL COMMENT '被叫',
  `media_type`       TINYINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '1语音 2视频',
  `price_mode`       TINYINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '1全局价 2被叫自定价',
  `price_per_min`    INT UNSIGNED    NOT NULL DEFAULT 0 COMMENT '下单时快照的每分钟金币',
  `platform_rate`    SMALLINT UNSIGNED NOT NULL DEFAULT 1000 COMMENT '下单时快照的平台抽成‰，1000=全收',
  `payer_id`         BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '实际付费方（通常为主叫）',
  `status`           TINYINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '0初始化 1振铃 2通话中 3已结束 4已取消 5被拒接 6超时未接 7异常',
  `room_id`          VARCHAR(64)     NOT NULL DEFAULT '' COMMENT 'TRTC 房间号',
  `started_at`       DATETIME        DEFAULT NULL COMMENT '接通时间',
  `ended_at`         DATETIME        DEFAULT NULL COMMENT '结束时间',
  `duration_sec`     INT UNSIGNED    NOT NULL DEFAULT 0 COMMENT '通话时长（秒），服务端时钟计算',
  `billable_minutes` SMALLINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '计费分钟数（向上取整）',
  `total_coins`      BIGINT          NOT NULL DEFAULT 0 COMMENT '总扣费金币',
  `payee_income`     BIGINT          NOT NULL DEFAULT 0 COMMENT '被叫收益金币',
  `platform_fee`     BIGINT          NOT NULL DEFAULT 0 COMMENT '平台抽成金币',
  `end_reason`       TINYINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '1正常挂断 2余额不足 3心跳超时 4被叫拒接 5网络异常 6风控中断',
  `created_at`       DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`       DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_call_no` (`call_no`),
  KEY `idx_caller_time` (`caller_id`, `created_at`),
  KEY `idx_callee_time` (`callee_id`, `created_at`),
  KEY `idx_status_started` (`status`, `started_at`),
  KEY `idx_created` (`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='通话会话（计费主记录）';

CREATE TABLE IF NOT EXISTS `call_segment` (
  `id`              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `call_session_id` BIGINT UNSIGNED NOT NULL,
  `seq_no`          SMALLINT UNSIGNED NOT NULL COMMENT '第几分钟，从1开始',
  `start_at`        DATETIME        NOT NULL COMMENT '该分钟开始（服务端时间）',
  `end_at`          DATETIME        DEFAULT NULL COMMENT '该分钟结束，进行中为NULL',
  `seconds`         INT UNSIGNED    NOT NULL DEFAULT 0 COMMENT '实际秒数',
  `coins`           BIGINT          NOT NULL DEFAULT 0 COMMENT '该段扣费金币',
  `status`          TINYINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '1已预扣未结算 2已结算 3已退回',
  `created_at`      DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`      DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_session_seq` (`call_session_id`, `seq_no`),
  KEY `idx_status` (`status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='通话分钟切片（对账核心，天然幂等）';

CREATE TABLE IF NOT EXISTS `call_event_log` (
  `id`              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `call_session_id` BIGINT UNSIGNED NOT NULL,
  `event`           VARCHAR(32)     NOT NULL COMMENT 'create/ring/accept/hangup/timeout/settle 等',
  `from_status`     TINYINT UNSIGNED NOT NULL DEFAULT 0,
  `to_status`       TINYINT UNSIGNED NOT NULL DEFAULT 0,
  `operator_id`     BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '触发者：用户ID或0表示系统',
  `payload`         JSON            DEFAULT NULL,
  `created_at`      DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_session` (`call_session_id`, `id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='通话状态机事件流水';

-- -----------------------------------------------------------------------------
-- 五、钱包、订单与收益
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS `wallet` (
  `id`             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `user_id`        BIGINT UNSIGNED NOT NULL,
  `coin_balance`   BIGINT          NOT NULL DEFAULT 0 COMMENT '可用金币',
  `frozen_coin`    BIGINT          NOT NULL DEFAULT 0 COMMENT '预扣冻结金币（通话中）',
  `total_recharge` BIGINT          NOT NULL DEFAULT 0 COMMENT '累计充值金币',
  `total_consume`  BIGINT          NOT NULL DEFAULT 0 COMMENT '累计消费金币',
  `version`        INT UNSIGNED    NOT NULL DEFAULT 0 COMMENT '乐观锁版本号',
  `created_at`     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_user` (`user_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='用户钱包';

CREATE TABLE IF NOT EXISTS `wallet_transaction` (
  `id`             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `user_id`        BIGINT UNSIGNED NOT NULL,
  `biz_type`       TINYINT UNSIGNED NOT NULL COMMENT '1充值 2通话消费 3退款 4活动奖励 5人工调整',
  `biz_id`         VARCHAR(64)     NOT NULL COMMENT '业务单号：订单号/通话号#分钟序号',
  `direction`      TINYINT UNSIGNED NOT NULL COMMENT '1收入 2支出',
  `coins`          BIGINT          NOT NULL COMMENT '变动金币数（正数）',
  `balance_before` BIGINT          NOT NULL DEFAULT 0,
  `balance_after`  BIGINT          NOT NULL DEFAULT 0,
  `remark`         VARCHAR(255)    NOT NULL DEFAULT '',
  `created_at`     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_biz` (`biz_type`, `biz_id`, `user_id`),
  KEY `idx_user_id` (`user_id`, `id`),
  KEY `idx_created` (`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='金币流水（唯一账本，不允许改删）';

CREATE TABLE IF NOT EXISTS `recharge_package` (
  `id`          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `coins`       INT UNSIGNED    NOT NULL COMMENT '基础金币',
  `bonus_coins` INT UNSIGNED    NOT NULL DEFAULT 0 COMMENT '赠送金币',
  `price_fen`   INT UNSIGNED    NOT NULL COMMENT '售价（分）',
  `channel`     TINYINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '0全渠道 1微信 2支付宝',
  `sort`        SMALLINT        NOT NULL DEFAULT 0 COMMENT '排序，越大越靠前',
  `status`      TINYINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '1上架 2下架',
  `created_at`  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_status_sort` (`status`, `sort`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='充值档位';

CREATE TABLE IF NOT EXISTS `recharge_order` (
  `id`               BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `order_no`         VARCHAR(32)     NOT NULL COMMENT '商户订单号',
  `user_id`          BIGINT UNSIGNED NOT NULL,
  `package_id`       BIGINT UNSIGNED NOT NULL DEFAULT 0,
  `amount_fen`       INT UNSIGNED    NOT NULL COMMENT '实付金额（分）',
  `coins`            INT UNSIGNED    NOT NULL COMMENT '到账金币（含赠送）',
  `channel`          TINYINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '1微信 2支付宝 3Apple IAP',
  `channel_trade_no` VARCHAR(64)     DEFAULT NULL COMMENT '第三方交易号，回调幂等',
  `status`           TINYINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '0待支付 1支付中 2已支付 3已失败 4已退款 5已关闭',
  `client_ip`        VARCHAR(45)     NOT NULL DEFAULT '',
  `paid_at`          DATETIME        DEFAULT NULL,
  `expire_at`        DATETIME        DEFAULT NULL COMMENT '订单超时关闭时间',
  `created_at`       DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`       DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_order_no` (`order_no`),
  UNIQUE KEY `uk_channel_trade` (`channel`, `channel_trade_no`),
  KEY `idx_user_time` (`user_id`, `created_at`),
  KEY `idx_status_expire` (`status`, `expire_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='充值订单';

CREATE TABLE IF NOT EXISTS `call_price_config` (
  `id`             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `scope`          TINYINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '1全局 2按用户',
  `user_id`        BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT 'scope=2 时生效',
  `audio_per_min`  INT UNSIGNED    NOT NULL DEFAULT 0 COMMENT '语音每分钟金币',
  `video_per_min`  INT UNSIGNED    NOT NULL DEFAULT 0 COMMENT '视频每分钟金币',
  `platform_rate`  SMALLINT UNSIGNED NOT NULL DEFAULT 300 COMMENT '平台抽成‰，1000=全收（全局价模式）',
  `effective_at`   DATETIME        DEFAULT NULL COMMENT '生效时间，支持预约调价',
  `status`         TINYINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '1启用 2停用',
  `created_at`     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_scope_user` (`scope`, `user_id`),
  KEY `idx_status` (`status`, `effective_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='通话定价';

CREATE TABLE IF NOT EXISTS `user_income` (
  `id`               BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `user_id`          BIGINT UNSIGNED NOT NULL,
  `available_coins`  BIGINT          NOT NULL DEFAULT 0 COMMENT '可提现金币（已过T+7）',
  `frozen_coins`     BIGINT          NOT NULL DEFAULT 0 COMMENT '待解冻金币',
  `total_income`     BIGINT          NOT NULL DEFAULT 0 COMMENT '累计收益金币',
  `withdrawn_coins`  BIGINT          NOT NULL DEFAULT 0 COMMENT '累计已提现金币',
  `created_at`       DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`       DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_user` (`user_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='接听方收益账户';

CREATE TABLE IF NOT EXISTS `income_transaction` (
  `id`             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `user_id`        BIGINT UNSIGNED NOT NULL,
  `biz_type`       TINYINT UNSIGNED NOT NULL COMMENT '1通话收益 2提现 3退款 4人工调整',
  `biz_id`         VARCHAR(64)     NOT NULL,
  `direction`      TINYINT UNSIGNED NOT NULL COMMENT '1收入 2支出',
  `coins`          BIGINT          NOT NULL,
  `balance_before` BIGINT          NOT NULL DEFAULT 0,
  `balance_after`  BIGINT          NOT NULL DEFAULT 0,
  `available_at`   DATETIME        DEFAULT NULL COMMENT '解冻时间（T+7）',
  `status`         TINYINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '1冻结核算中 2可提现 3已提现 4已冲正',
  `created_at`     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_biz` (`biz_type`, `biz_id`, `user_id`),
  KEY `idx_user_status` (`user_id`, `status`),
  KEY `idx_available` (`status`, `available_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='收益流水';

CREATE TABLE IF NOT EXISTS `withdraw_order` (
  `id`          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `order_no`    VARCHAR(32)     NOT NULL,
  `user_id`     BIGINT UNSIGNED NOT NULL,
  `coins`       BIGINT          NOT NULL COMMENT '提现金币数',
  `amount_fen`  INT UNSIGNED    NOT NULL COMMENT '折算金额（分）',
  `channel`     TINYINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '1微信 2支付宝',
  `account_info` JSON           DEFAULT NULL COMMENT '收款账户信息（脱敏后存储）',
  `status`      TINYINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '0待审核 1通过 2打款中 3成功 4拒绝',
  `audit_by`    BIGINT UNSIGNED NOT NULL DEFAULT 0,
  `audit_at`    DATETIME        DEFAULT NULL,
  `audit_note`  VARCHAR(255)    NOT NULL DEFAULT '',
  `paid_at`     DATETIME        DEFAULT NULL,
  `created_at`  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_order_no` (`order_no`),
  KEY `idx_user_time` (`user_id`, `created_at`),
  KEY `idx_status_time` (`status`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='提现申请单（首期只记账不打款）';

-- -----------------------------------------------------------------------------
-- 六、LBS 与发现
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS `user_location` (
  `user_id`    BIGINT UNSIGNED NOT NULL,
  `lng`        DECIMAL(10, 7)  NOT NULL COMMENT '经度',
  `lat`        DECIMAL(10, 7)  NOT NULL COMMENT '纬度',
  `geohash`    VARCHAR(12)     NOT NULL DEFAULT '' COMMENT 'geohash，便于按网格批量查询',
  `city_code`  VARCHAR(16)     NOT NULL DEFAULT '',
  `updated_at` DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`user_id`),
  KEY `idx_city_time` (`city_code`, `updated_at`),
  KEY `idx_geohash` (`geohash`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='用户位置（热数据在 Redis GEO）';

CREATE TABLE IF NOT EXISTS `user_preference` (
  `user_id`         BIGINT UNSIGNED NOT NULL,
  `gender_pref`     TINYINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '期望性别 0不限 1男 2女',
  `age_min`         TINYINT UNSIGNED NOT NULL DEFAULT 18,
  `age_max`         TINYINT UNSIGNED NOT NULL DEFAULT 45,
  `max_distance_km` SMALLINT UNSIGNED NOT NULL DEFAULT 50,
  `only_online`     TINYINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '只看在线',
  `updated_at`      DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`user_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='匹配筛选偏好';

CREATE TABLE IF NOT EXISTS `user_visit` (
  `id`         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `visitor_id` BIGINT UNSIGNED NOT NULL,
  `target_id`  BIGINT UNSIGNED NOT NULL,
  `visited_at` DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '最近一次访问时间',
  `created_at` DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_pair` (`visitor_id`, `target_id`),
  KEY `idx_target_time` (`target_id`, `visited_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='谁看过我（按对去重，更新访问时间）';

-- -----------------------------------------------------------------------------
-- 七、内容安全与运营后台
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS `sensitive_word` (
  `id`         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `word`       VARCHAR(64)     NOT NULL,
  `category`   VARCHAR(32)     NOT NULL DEFAULT '' COMMENT '分类：涉黄/涉政/导流/辱骂…',
  `level`      TINYINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '1拦截 2转人工复审 3替换为***',
  `status`     TINYINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '1启用 2停用',
  `created_at` DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_word` (`word`),
  KEY `idx_level_status` (`level`, `status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='敏感词库（启动时加载进 DFA）';

CREATE TABLE IF NOT EXISTS `moderation_record` (
  `id`         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `biz_type`   TINYINT UNSIGNED NOT NULL COMMENT '1消息 2资料 3头像 4语音 5视频帧',
  `biz_id`     VARCHAR(64)     NOT NULL COMMENT '业务ID：消息ID/用户ID/通话号',
  `user_id`    BIGINT UNSIGNED NOT NULL DEFAULT 0,
  `provider`   TINYINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '1本地DFA 2腾讯云TMS 3TRTC截图 4人工',
  `scene`      VARCHAR(32)     NOT NULL DEFAULT '',
  `label`      VARCHAR(64)     NOT NULL DEFAULT '' COMMENT '命中标签：porn/ad/abuse…',
  `score`      DECIMAL(5, 4)   NOT NULL DEFAULT 0 COMMENT '置信度 0-1',
  `result`     TINYINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '1通过 2拒绝 3转人工',
  `raw`        JSON            DEFAULT NULL COMMENT '第三方原始返回',
  `created_at` DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_biz` (`biz_type`, `biz_id`),
  KEY `idx_user_time` (`user_id`, `created_at`),
  KEY `idx_result_time` (`result`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='内容审核记录';

CREATE TABLE IF NOT EXISTS `admin_user` (
  `id`            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `username`      VARCHAR(32)     NOT NULL,
  `password_hash` VARCHAR(255)    NOT NULL COMMENT 'bcrypt',
  `real_name`     VARCHAR(32)     NOT NULL DEFAULT '',
  `role`          TINYINT UNSIGNED NOT NULL DEFAULT 3 COMMENT '1超管 2运营 3审核 4客服',
  `status`        TINYINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '1启用 2禁用',
  `last_login_at` DATETIME        DEFAULT NULL,
  `last_login_ip` VARCHAR(45)     NOT NULL DEFAULT '',
  `created_at`    DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`    DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_username` (`username`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='后台管理员';

CREATE TABLE IF NOT EXISTS `admin_op_log` (
  `id`          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `admin_id`    BIGINT UNSIGNED NOT NULL,
  `module`      VARCHAR(32)     NOT NULL DEFAULT '' COMMENT '模块：user/report/order/config',
  `action`      VARCHAR(64)     NOT NULL DEFAULT '' COMMENT '动作：ban/unban/audit/refund',
  `target_type` VARCHAR(32)     NOT NULL DEFAULT '',
  `target_id`   VARCHAR(64)     NOT NULL DEFAULT '',
  `detail`      JSON            DEFAULT NULL,
  `ip`          VARCHAR(45)     NOT NULL DEFAULT '',
  `created_at`  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_admin_time` (`admin_id`, `created_at`),
  KEY `idx_target` (`target_type`, `target_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='后台操作审计';

CREATE TABLE IF NOT EXISTS `app_config` (
  `id`         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `cfg_key`    VARCHAR(64)     NOT NULL COMMENT '配置键，如 call.free_seconds',
  `cfg_value`  JSON            NOT NULL COMMENT '配置值，统一用 JSON 便于扩展类型',
  `remark`     VARCHAR(255)    NOT NULL DEFAULT '',
  `updated_by` BIGINT UNSIGNED NOT NULL DEFAULT 0,
  `created_at` DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_cfg_key` (`cfg_key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='运行时开关与参数';

CREATE TABLE IF NOT EXISTS `region_city` (
  `id`         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `city_code`  VARCHAR(16)     NOT NULL COMMENT '行政区划编码',
  `province`   VARCHAR(32)     NOT NULL DEFAULT '',
  `city`       VARCHAR(32)     NOT NULL DEFAULT '',
  `lng`        DECIMAL(10, 7)  NOT NULL DEFAULT 0 COMMENT '城市中心点经度',
  `lat`        DECIMAL(10, 7)  NOT NULL DEFAULT 0,
  `sort`       SMALLINT        NOT NULL DEFAULT 0,
  `status`     TINYINT UNSIGNED NOT NULL DEFAULT 1,
  `created_at` DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_city_code` (`city_code`),
  KEY `idx_status_sort` (`status`, `sort`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='城市字典';
