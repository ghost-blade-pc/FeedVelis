# 验收记录

- `make check`：通过；Go 单测、race、命令构建、Web lint、62 项 Vitest 与 Web 构建均成功。
- `VELIS_TEST_DATABASE_URL=.../velis_test go test -count=1 -v -run 'TestGenerationMethod' ./test/integration`：2 项真实 PostgreSQL 测试通过，覆盖结果共存、读取来源、同 profile 摘录重排、dry-run 不创建任务与降级迁移保护。
- `openspec validate improve-ai-summary-reliability --strict`：通过。
- 在当前开发数据库中以事务执行 000011 前滚迁移并回滚：通过，无持久修改。

当前运行中的 Compose 容器仍使用旧镜像与旧数据库迁移；本次未部署、未重排文章 366 或 356。部署后按 README 先 dry-run，再对目标文章显式补录。
