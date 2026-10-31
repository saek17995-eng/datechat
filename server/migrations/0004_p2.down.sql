-- 回滚 P2 增量。
-- 只删 P2 新增的表；app_config 的种子行保留（删配置比留着更危险，
-- 回滚后旧版本代码读不到会走默认值，留着则完全无害）。
DROP TABLE IF EXISTS `upload_file`;
