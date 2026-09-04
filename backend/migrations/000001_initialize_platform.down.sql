-- 扩展可能被同一数据库中的其他 schema 使用，因此回退时只删除空的项目 schema。
DROP SCHEMA IF EXISTS velis;
