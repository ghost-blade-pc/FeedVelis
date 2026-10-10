# 实现与验收记录

验收日期：2026-10-10。范围为 I5 第 1–2 步：私有会话、不可变用户消息、标题版本、账户配额、24 小时幂等、原子删除、加密历史分页和三个内部文章只读工具。功能默认关闭；模型对话编排、最终引用校验、SSE、有限记忆、Web 和 Agent 评测仍由后续 change 实施。

## 实际环境

所有真实依赖均为本次创建的本机临时 Docker 容器，未连接外部账户或生产数据：

| 依赖 | 实测版本 | 本机地址 / 专用事实库 |
| --- | --- | --- |
| PostgreSQL | 17.11，镜像 `pgvector/pgvector:pg17` | `127.0.0.1:55434` / `velis_agent_test` |
| Redis | 8.10.1，镜像 `redis:8-alpine` | `127.0.0.1:56381` |
| OpenSearch | 3.8.0 | `http://127.0.0.1:59201` |

Go 按仓库 `go.mod` 使用 1.26.6 工具链；构建缓存为 `/tmp/feedvelis-go-cache`。OpenSearch 单节点、512 MiB 堆、测试环境关闭安全插件。查询 Embedding 使用确定性桩，没有调用外部付费模型。

## 执行命令与结果

下列命令在仓库根目录执行，均通过：

```bash
make check
cd backend
GOCACHE=/tmp/feedvelis-go-cache go test ./internal/architecture/...
GOCACHE=/tmp/feedvelis-go-cache go test -race -count=1 \
  ./internal/architecture/... ./internal/infrastructure/observability/... \
  ./internal/interfaces/http/hertz/...
GOCACHE=/tmp/feedvelis-go-cache go vet ./...
cd ..

export VELIS_TEST_DATABASE_URL='postgres://velis:velis@127.0.0.1:55434/velis_agent_test?sslmode=disable'
export VELIS_TEST_OPENSEARCH_URL='http://127.0.0.1:59201'
export VELIS_TEST_REDIS_ADDRESS='127.0.0.1:56381'
make integration-agent-conversations integration-agent-tools \
  integration-search integration-cache-reads

openspec validate add-agent-conversations-and-read-tools --strict
git diff --check
```

`make check` 完成 gofmt、全部 Go 单测、race、四入口构建、Web lint、Vitest（17 个文件 / 63 个测试）和 Web 构建。普通单测入口未设置真实依赖变量，不能用其跳过的集成用例代替真实验收；上述四个真实 Make 入口的必需用例实际执行，没有 skip。最终 PIT 清理补强后另重跑 `integration-agent-tools integration-search`；最终 request_id 日志关联补强后另重跑会话真实入口及 HTTP/观测 race 和架构检查。

两个新增 Make 入口均验证缺少必需环境变量时退出码为 2：会话缺 PostgreSQL；工具分别缺 PostgreSQL、OpenSearch、Redis。测试库检查继续拒绝非专用 `_test` 库。

## 会话事实、清除与回滚证据

| 实际执行的测试 | 数据库级断言及结果 |
| --- | --- |
| `TestAgentConversationMigrationConstraints` | 归属外键、孤儿消息、角色、正序号、序号唯一、标题长度与非负计数约束均拒绝非法行；级联删除不留消息。 |
| `TestAgentConversationMigrationSafeDown` | 在真实事务中执行 down/up SQL；空库和零计数用户状态可回退。非零计数、会话、消息、永久标记及已过期 Agent 幂等记录均阻止 down；恢复 savepoint 后原行和原正文仍完整。不改动迁移版本表。 |
| `TestAgentConversationRepositoryTransactions` | 注入事务错误后，会话、消息、配额状态均完整回滚；仓储支持服务端 assistant 角色但不提供消息修改/单条删除入口。 |
| `TestAgentConversationServiceOwnershipReplayDelete` | 删除后直接 SQL 查询：消息数 0、会话数 0、账户配额 0；删除标记表恰好 3 字段。4 条关联成功去重记录全部替换为 3 字段删除终态，无 title/content/snapshot。创建、改名、追加旧键不泄露原快照，窗口外创建新 UUID。 |
| `TestAgentConversationConcurrentQuotaAndSequences` | 最后一个会话名额竞争仅一个成功；并发追加按配额完成，序号唯一单调；65 条历史分批读取无重复跳过；同时间排序和并发标题版本正确。 |
| `TestAgentConversationIdempotencyIsolationFailureAndExpiry` | 固定 86400 秒窗口；用户/操作隔离、失败键复用、在飞冲突及到期重新执行通过。 |
| `TestAgentConversationDeleteFailureRollbackAndRaces` | 在删除最后的计数更新注入 PostgreSQL trigger 失败，消息与成功快照完整恢复，标记数 0、配额 1；随后 10 轮删除与追加/改名/重放竞争完成且无资源复活。 |
| `TestAgentConversationCleanupPreservesHistoryAndDeletionMarkers` | 过期去重清理仅移除到期去重行；现存历史和永久标记保持。 |
| `TestAgentConversationAPIAuthenticatedLifecycleAndValidation` | 真实注册、登录、JWT 认证、创建、追加、改名、分页、登出和重登录、删除全链路；管理员无归属豁免。合法最大转义正文、严格 JSON/头/分页、ETag/重放、401 优先、PG 断连 503 均通过。 |

会话 API 用例还执行关闭/重新开启演练：创建保留消息后关闭 Agent 路由，访问返回 404；重新开启后仍有 24 个未删除会话，保留消息可读取。关闭认证时 Agent 路由也不存在。该演练在 Handler 装配层执行，非部署或生产重启演练。推荐回退方式为关闭功能、保留数据库，不通过清除历史强行 down。

## 工具与既有读取回归证据

| 实际执行的测试 | 通过的行为 |
| --- | --- |
| `TestAgentToolsPublicTextCurrentRevisionAndVisibility` | RSS/投稿纯文本、Unicode 前缀、当前修订/标题/摘要一致；草稿、下架、删除和不存在统一不可读取，作者/管理员无公开性豁免。 |
| `TestAgentToolsPostgresOpenSearchRedisReadOnlyAndRecovery` | 两用户本人推荐与排除隔离；Redis 热缓存、清空、损坏、断连与恢复不改变结果顺序；陈旧索引/缓存不能绕过当前修订和公开性。搜索故障为错误、推荐故障保留 latest 回退、仅负反馈仍为 cold_start；PG 故障不能由暖缓存掩盖。 |
| 同上，业务事实直接比较 | 三工具调用前后，账户配额、会话、消息、删除标记、阅读、收藏、负反馈、文章、修订及当前增强选择表逐行 JSON 快照完全相同，画像读取结果保持；无业务写入。 |
| 同上，真实 PIT 列表 | 连续 12 轮工具调用后，`/_search/point_in_time/_all` 数量与调用前相同。 |
| `TestAgentToolsConcurrentRevisionProjection` | 40 次修订与三个并发读取者交错，返回修订身份、卡片和纯文本来自同一当前事实视图。 |
| `TestAgentToolsHybridAndCanceledSearchReleaseRealPIT` | 真实 v2 索引、确定性 Embedding、无有效向量 BM25 降级与主动取消均释放 PIT，重复调用无累积。 |
| 一次查询单测及工具单测 | 最新 PIT 身份在成功/失败/取消、KNN 返回错误或非法候选时仍清理；清理故障最多 250ms。严格输入不触发依赖；默认/上限、JSON 转义膨胀、尾项/正文截断、总 deadline 与调用者取消、稳定安全错误通过。 |
| `integration-search` 与 `integration-cache-reads` | 原 BM25/PIT 与混合冻结续页、公开事实复核、重建、推荐排序/降级、Redis 故障恢复和固定负载比较通过。 |

联合回归首次发现 latest 的数据库直读路径缺少新内部 `RevisionID`，导致它与共享缓存投影不一致。已在原 SQL 同步读取当前修订号，公开 JSON/presenter 不增加字段；修复后原失败测试和完整相关套件通过。混合一次查询还补强了 KNN 错误响应中轮换 PIT 的清理，并以回归测试覆盖。

## 契约、文档与交付边界

OpenAPI 已增加七个操作、对象/分页/输入 schema、协议头、错误和默认限制；YAML 解析与既有契约测试通过。示例配置、环境变量、Compose API 透传、README、Roadmap、配置/操作文档和集成测试说明已同步。

日志仅包含 request_id 和固定安全分类，指标标签不含用户/会话/文章 ID、正文、标题、查询、幂等键或游标。会话只依赖 PostgreSQL；可选 Redis、OpenSearch、RabbitMQ、模型不增加其 readiness 要求。三个工具仅为内部应用接口和装配点，本阶段未新增工具 HTTP/CLI 或模型编排消费者。

本次没有运行全部 RabbitMQ、MinIO、真实收费模型、浏览器端到端或生产备份恢复套件；它们不属于本 change 的必需验收。实施验收阶段没有提交、推送、部署、发布、同步长期规格或归档；后续规格同步与归档见下方记录。临时测试容器在验收结束后清理，不影响仓库或外部环境；会话保留/回退证据由上述可重复测试和此记录保存。

会话的活跃时间列表是实时 keyset，跨页移动不冻结集合；删除不能撤回已发出的响应或已有备份；游标密钥轮换会使旧游标失效；PIT 清理失败仍由有限 keep_alive 兜底。这些边界见 [会话操作说明](../../../../docs/agent-conversations.md) 和 [工具说明](../../../../docs/agent-tools.md)。

## 规格同步与归档记录

2026-10-10，经用户确认同步规格后归档。两份新增长期规格 `agent-conversations`（20 项要求）和 `agent-read-tools`（11 项要求）保留增量规格的 Purpose、全部要求及场景，仅调整标题和 Requirements 章节格式；同步后逐字核对通过，全部 12 份长期规格严格校验通过。

本 change 使用 `spec-driven` schema，规划产物全部完成，任务 35/35 完成，归档到 `openspec/changes/archive/2026-10-10-add-agent-conversations-and-read-tools/`，包含 `.openspec.yaml` 和原实施验收记录。Roadmap 状态与记录中的相对链接同步更新。本次归档仅调整规格与文档，没有重跑应用测试、执行数据库迁移、提交、推送或部署。
