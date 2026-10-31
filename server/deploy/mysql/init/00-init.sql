-- 容器首次初始化时执行（仅当数据目录为空）
-- 主库由 MYSQL_DATABASE 环境变量创建，这里只做补充设置。
SET GLOBAL event_scheduler = ON;
