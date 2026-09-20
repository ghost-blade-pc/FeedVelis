# PostgreSQL 集成测试

`harness_test.go` 是共享基座：读取 `VELIS_TEST_DATABASE_URL`（数据库名必须以 `_test` 结尾）、把 `migrations/` 应用到最新版本，并提供分区清表。`article_repository_test.go` 验证 Source/Article 仓储、租约、幂等、事务与并发；`account_repository_test.go` 验证账户唯一约束、会话撤销、刷新轮换与重放、登录失败限流、管理审计与清理、CLI 管理锁。

基座提供 `resetArticles`（清 `velis.sources`、`velis.articles`、`velis.article_contents`）与 `resetAccounts`（清 `velis.users`、`velis.auth_sessions`、`velis.refresh_tokens`、`velis.login_failure_events`、`velis.login_blocks`、`velis.account_audit_logs`）。测试会清空这些表，只能使用可丢弃的测试库；未设置环境变量时测试跳过。

在 `backend/` 中执行：

```bash
GOCACHE=/tmp/feedvelis-go-cache go test -count=1 ./test/integration
```

需要临时实例时可用容器，用完即删：

```bash
docker run -d --name velis_it_pg -e POSTGRES_USER=velis -e POSTGRES_PASSWORD=velis \
  -e POSTGRES_DB=velis_test -p 55432:5432 pgvector/pgvector:pg17
VELIS_TEST_DATABASE_URL='postgres://velis:velis@localhost:55432/velis_test?sslmode=disable' \
  GOCACHE=/tmp/feedvelis-go-cache go test -count=1 ./test/integration
docker rm -f velis_it_pg
```

迁移回退/前滚用仓库命令验证，不依赖测试基座：

```bash
VELIS_DATABASE_URL="$VELIS_TEST_DATABASE_URL" go run ./cmd/velis-migrate -path migrations -steps 1 down
VELIS_DATABASE_URL="$VELIS_TEST_DATABASE_URL" go run ./cmd/velis-migrate -path migrations up
```

目前不是自动启动 Testcontainers 的测试套件。未来依赖集成范围见 [Velis Roadmap](<../../../Velis Roadmap.md>)，不以目录说明代替已实现测试。
