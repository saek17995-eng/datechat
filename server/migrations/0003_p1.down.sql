-- 回滚 P1 增量：仅删除新增的审计表，账号相关表由 0001 管理。
DROP TABLE IF EXISTS `sms_send_log`;
