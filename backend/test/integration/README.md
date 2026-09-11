# PostgreSQL 集成测试

当前 `article_repository_test.go` 验证 Source/Article 仓储、租约、幂等、事务与并发行为。运行前准备已执行仓库迁移的专用数据库，并通过环境变量 `VELIS_TEST_DATABASE_URL` 注入连接地址；数据库名称必须以 `_test` 结尾。

测试会清空 `velis.sources`、`velis.articles`、`velis.article_contents` 及其关联数据，只能使用可丢弃的测试库。未设置环境变量时测试跳过。

在 `backend/` 中执行：

```bash
GOCACHE=/tmp/feedvelis-go-cache go test -count=1 ./test/integration
```

目前不是自动启动 Testcontainers 的测试套件。未来依赖集成范围见 [Velis Roadmap](<../../../Velis Roadmap.md>)，不以目录说明代替已实现测试。
