-- P7 回滚
DELETE FROM `app_config` WHERE `cfg_key` IN (
  'nearby.default_radius_km', 'nearby.max_radius_km', 'nearby.page_size',
  'nearby.distance_fuzzy', 'online.ttl_sec', 'nearby.min_report_interval_sec'
);

ALTER TABLE `user_visit`
  DROP KEY `idx_target_unread`,
  DROP COLUMN `read_at`;

ALTER TABLE `user_profile`
  DROP KEY `idx_lng_lat`,
  DROP COLUMN `location_at`,
  DROP COLUMN `lat`,
  DROP COLUMN `lng`;
