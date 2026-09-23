# PostgreSQL 集成测试

`harness_test.go` 是共享基座：读取 `VELIS_TEST_DATABASE_URL`（数据库名必须以 `_test` 结尾）、把 `migrations/` 应用到最新版本，并提供分区清表。`article_repository_test.go` 验证 Source/Article 仓储、租约、幂等、事务与并发；`account_repository_test.go` 验证账户唯一约束、会话撤销、刷新轮换与重放、登录失败限流、管理审计与清理、CLI 管理锁；可靠异步测试覆盖 Outbox/Inbox、任务收敛、补录和 RabbitMQ 端到端恢复。

基座提供 `resetArticles`（包括 Source、Article、资产、幂等、Outbox、Inbox 和异步任务）与 `resetAccounts`。测试会清空这些表，只能使用可丢弃的测试库；未设置环境变量时测试跳过。

在 `backend/` 中执行：

```bash
GOCACHE=/tmp/feedvelis-go-cache go test -count=1 ./test/integration
```

资产 API E2E 还要求真实 MinIO：设置 `VELIS_TEST_MINIO_ENDPOINT`（内部 `host:port`）、`VELIS_TEST_MINIO_UPLOAD_ENDPOINT`（测试进程可达且 authority 不同的完整 URL）、access key、secret key 和 bucket。未设置内部端点时测试会明确跳过。

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

可靠异步测试还要求 `VELIS_TEST_RABBITMQ_URL`，并可从仓库根目录执行 `make integration-async`。该入口会在缺少 PostgreSQL 或 RabbitMQ 测试 URL 时以非零状态明确失败，避免把 skip 误报为通过。真实 Broker 重启演练及故障矩阵见 [reliable_async.md](reliable_async.md)；该项默认 skip，必须显式执行并重启本地测试容器。
