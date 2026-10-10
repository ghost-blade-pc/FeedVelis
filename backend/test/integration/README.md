# PostgreSQL 集成测试

Agent 会话迁移、仓储、事务服务与认证HTTP全链路使用 `agent_conversation_*_test.go`，从仓库根运行 `make integration-agent-conversations`（必需 `VELIS_TEST_DATABASE_URL`）。工具使用 `agent_tools_test.go`，运行 `make integration-agent-tools`，额外必需 `VELIS_TEST_OPENSEARCH_URL` 和 `VELIS_TEST_REDIS_ADDRESS`。入口缺任意变量均非零退出，skip不得作为验收。表不变量、永久最小删除标记、历史保留和仅空业务事实允许的 down/up 命令见 [Agent 会话说明](../../../docs/agent-conversations.md)，工具契约见 [工具说明](../../../docs/agent-tools.md)。仍须设置专用 `_test` 库；不得通过删除历史让降级迁移通过。

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

## 推荐 Feed 与 AI 生成来源

`article_recommendation_e2e_test.go` 要求同时设置 `VELIS_TEST_DATABASE_URL` 和 `VELIS_TEST_OPENSEARCH_URL`，覆盖不同用户偏好、BM25/KNN 空候选、下架与新增负反馈、冻结分页及搜索连接故障转 latest。`make integration-search` 的用例筛选不包含推荐测试；在 `backend/` 中显式运行：

```bash
GOCACHE=/tmp/feedvelis-go-cache go test -count=1 -v \
  -run '^TestArticleRecommendationPostgresOpenSearch$' ./test/integration
```

`ai_generation_method_test.go` 只要求专用 PostgreSQL，覆盖同输入模型/摘录结果共存、公开读取来源、同 profile 摘录重排、dry-run 不创建任务及保留摘录数据的降级迁移保护：

```bash
GOCACHE=/tmp/feedvelis-go-cache go test -count=1 -v \
  -run '^TestGenerationMethod' ./test/integration
```

上述测试沿用共享基座，会清理测试数据和相关反馈、增强及投影事实。不得指向开发业务库或生产库，也不要让多个测试进程共用同一测试库。未配置依赖时的 skip 不能作为验收通过。

## Redis 与读取联合验收

从仓库根目录执行，Redis 8、PostgreSQL 17 与 OpenSearch 3.x 必须实际可达。测试用数据库应预先创建为专用 `_test` 库；连接串解析和服务端库名双重核对后，harness 自动迁移并清空测试数据。不要指向正在使用的数据库；基线及其他用例会重建测试语料，不可同时运行在同一个测试库。

```bash
VELIS_TEST_REDIS_ADDRESS=127.0.0.1:6379 make integration-redis

VELIS_TEST_DATABASE_URL='postgres://velis:velis@127.0.0.1:5432/velis_cache_reads_test?sslmode=disable' \
VELIS_TEST_REDIS_ADDRESS=127.0.0.1:6379 \
VELIS_TEST_OPENSEARCH_URL=http://127.0.0.1:9200 \
make integration-cache-reads
```

Redis 有认证时另注入 `VELIS_TEST_REDIS_USERNAME`、`VELIS_TEST_REDIS_PASSWORD`。专用 Make 入口缺少任何必需变量都先失败；普通 `go test` 中的 skip 不能算真实验收。联合入口先运行真实 Redis harness/适配器，再运行当前事实/只读快照/真实 Redis latest 分页/提交后失效测试，以及固定基线、已接入共享卡片的 PostgreSQL/OpenSearch 搜索、混合搜索与 `TestArticleRecommendationPostgresOpenSearch`，明确包含推荐回归。`make integration-all` 也包含此入口，因而额外要求 Redis 测试地址。

Redis harness 每次生成 128 位随机 namespace，只登记并 DEL 本次拥有的精确键，不执行 FLUSHALL、FLUSHDB、SCAN，不停止共享服务。测试进程自有 TCP 代理控制慢响应及断连，结束时关闭自有连接、监听器和 goroutine。其他 namespace 的 sentinel 不受清理影响；OpenSearch 创建并清理测试独占索引。测试结束保留专用数据库，Redis 键及索引按 cleanup 清理。

固定基线 `TestCacheReadBaseline` 使用 1000 篇固定语料、两名用户、limit=20、并发 5、每 worker 10 轮固定六请求，覆盖 latest 首查/续页、BM25/混合搜索装配、推荐首查/续页；真实 v2 索引配确定性三维查询向量桩，语料无向量使 KNN 确定性为空，不调用收费模型。可设置 `VELIS_TEST_CACHE_REPORT=/tmp/cache-report.md` 导出报告；语料摘要与计数应重复一致，P50/P95 随环境变化。数据库卡片端口返回的 JSON 字节是可比较载荷代理量，不是 PostgreSQL 网络字节，也不包含画像/排除 SQL。基线继续运行关闭缓存的原读取路径，确保可重复比较。共享卡片读取覆盖修订/AI 身份、动态事实、批次缺失装配、写事务绕过及数据库错误；latest 覆盖真实 Redis 清空、损坏、断连恢复及当前排序重查。提交后动作覆盖 PostgreSQL 真实提交失败、RSS 外层回滚、去重、用户/管理员命令、幂等重放及失效失败。搜索回归在 Redis 可达时接入共享卡片，并用进程自有代理验证 BM25/混合排序不因缓存断连改变。推荐回归在 Redis 配置后同时接入首查计划和专用 latest 共享装配，覆盖故障回退后恢复。`TestCacheReadRecommendationPlanIsolationExclusionsAndContinuation` 实测相同画像的真实身份隔离、绝对 30 秒寿命、无标签排除撤销/自然过期、独立首查与清缓存续页，以及专用 latest 的本人排除/skip/StartedAt/位置契约。

`TestCacheReadComparisonAndJointRecovery` 复用同一固定负载比较关闭、冷、热、损坏、断连、恢复，逐一断言各状态所有路径相同用户的输出 ID 序列一致；可设置 `VELIS_TEST_CACHE_COMPARISON_REPORT=/tmp/cache-comparison.md` 导出完整结果。冷与恢复都从本次空缓存开始，负载中自然逐渐回填；热与损坏在测量前预热，损坏仅改写已登记的精确键。未给每个请求强制清缓存，也没有 singleflight，故并发冷启动会重复回源。业务固定时钟保持原基线语料，测试包装器仅把计划 CreatedAt 对齐真实请求开始，以实际 Redis 绝对寿命测量。每次 BM25 搜索测量后关闭自身 PIT，清理耗时不计入请求延迟，避免多状态循环占满服务端上下文；不关闭其他客户端 PIT。

报告的 JSON 字节仍是应用端口序列化诊断量，结构不同（含空字段）不能视为 PostgreSQL 网络流量；事实、缺失片段、候选 ID 分开统计，画像/排除 SQL 不计入这些卡片端口数。快照端口耗时包含 begin/查询/缓存/commit 和连接池排队，是连接占用的近似上界，跨并发求和而非 wall time。召回及确定性 Embedding 单独计数，不以命中率或改善比例替代正确性。

`TestCacheReadJointFaultMatrixAndRealPostgresFailure` 在真实三依赖下覆盖三对象清空、错误类型、损坏、断连、慢响应、恢复、暖后编辑/AI 切换/下架/反馈、真实回填与提交后失效传输失败，以及跨对象共享预算。PostgreSQL 故障也通过自有 TCP 代理，仅断开本测试连接；暖卡片、latest、搜索装配及推荐计划均不得掩盖事实源故障。
