-- P7 LBS 与在线：给资料表补"最后上报的位置"。
-- 坐标只用于附近的人排序与距离展示：Redis GEO 负责检索，MySQL 这列负责兜底与审计。
ALTER TABLE `user_profile`
  ADD COLUMN `lng`         DOUBLE       DEFAULT NULL COMMENT '最后上报经度' AFTER `voice_intro`,
  ADD COLUMN `lat`         DOUBLE       DEFAULT NULL COMMENT '最后上报纬度' AFTER `lng`,
  ADD COLUMN `location_at` DATETIME     DEFAULT NULL COMMENT '位置上报时间' AFTER `lat`,
  ADD KEY `idx_lng_lat` (`lng`, `lat`);

-- "谁看过我"需要未读概念：read_at 为空即未读，看过列表后统一置位。
ALTER TABLE `user_visit`
  ADD COLUMN `read_at` DATETIME DEFAULT NULL COMMENT '被访问者已读时间，NULL 表示未读' AFTER `visited_at`,
  ADD KEY `idx_target_unread` (`target_id`, `read_at`, `visited_at`);

INSERT INTO `app_config` (`cfg_key`, `cfg_value`, `remark`) VALUES
  ('nearby.default_radius_km', '50',   '附近的人默认搜索半径（公里）'),
  ('nearby.max_radius_km',     '200',  '允许的最大搜索半径，防止一次扫全表'),
  ('nearby.page_size',         '20',   '附近的人单页条数'),
  ('nearby.distance_fuzzy',    'true', '对外展示距离是否按区间模糊（隐私）'),
  ('online.ttl_sec',           '120',  '在线状态 TTL：心跳间隔的 2 倍以上'),
  ('nearby.min_report_interval_sec', '30', '位置上报的最小间隔，防刷')
ON DUPLICATE KEY UPDATE `cfg_value` = VALUES(`cfg_value`), `remark` = VALUES(`remark`);
