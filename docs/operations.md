# 运维

CLI 命令从仓库根目录进入 `backend/` 执行，默认读取 `configs/config.example.yaml`；该配置的数据库指向 `localhost:5432`，与 Compose 发布的端口一致。认证相关初始化见 [authentication.md](authentication.md)，配置项含义见 [configuration.md](configuration.md)。

## 登记与抓取来源

将示例 URL 和 ID 替换为实际来源：

```bash
cd backend
go run ./cmd/velis-admin -config configs/config.example.yaml source add -url https://example.com/feed.xml
go run ./cmd/velis-admin -config configs/config.example.yaml source list
go run ./cmd/velis-admin -config configs/config.example.yaml source fetch 1
go run ./cmd/velis-admin -config configs/config.example.yaml source pause 1
go run ./cmd/velis-admin -config configs/config.example.yaml source resume 1
```

`source fetch 1 --force` 可忽略条件请求头，重新拉取并清洗内容；它仍遵循来源认领规则。正常运行时 Worker 按每个 Source 的周期自动认领。CLI 与管理员 HTTP 复用应用规则。

## AI 内容增强补录

AI profile 升级不会自动触发全量费用。管理员必须在精确文章、有限批次、全量候选三种范围中恰好选择一种。可先精确重试单篇，或按有效发布时间从新到旧处理有限批次：

```bash
cd backend
go run ./cmd/velis-admin -config configs/config.example.yaml ai backfill -stage all -mode missing-only -article-id 324 -dry-run
go run ./cmd/velis-admin -config configs/config.example.yaml ai backfill -stage all -mode missing-only -article-id 324
go run ./cmd/velis-admin -config configs/config.example.yaml ai backfill -stage all -mode missing-only -limit 20 -order newest
```

AI 停用一段时间后，可先预览没有 AI 内容增强内容的全部文章，再显式确认全量推进：

```bash
go run ./cmd/velis-admin -config configs/config.example.yaml ai backfill -stage all -mode missing-only -all -dry-run
go run ./cmd/velis-admin -config configs/config.example.yaml ai backfill -stage all -mode missing-only -all -confirm-all
```

`-mode missing-only` 只为没有 AI 内容增强内容的文章补齐：当前没有生成结果的文章（`stage=all` 时连同 Embedding 一起补齐），以及已有摘要、关键词和主题但缺 Embedding 的文章；已有当前 generation 与 embedding 结果的文章不会被全量命令重算；`-article-id` 与 `-limit` 只改变范围，不绕过这一判断。要推进 profile 落后或来源为 `extractive` 的文章，请改用 `-mode outdated-only`。

模型连续返回非法摘要并耗尽 generation 尝试后，Worker 会保存最多 400 个字符的当前修订原文摘录；API 的 `enhancement.method` 为 `extractive`，Web 显示“原文摘录”。其他错误不会伪装为成功。升级代码和迁移后，可先对历史失败文章 366 做只读预览，再显式重排；摘录以后可用相同 profile 的 `outdated-only` 再试模型：

```bash
go run ./cmd/velis-admin -config configs/config.example.yaml ai backfill -stage all -mode missing-only -article-id 366 -dry-run
go run ./cmd/velis-admin -config configs/config.example.yaml ai backfill -stage all -mode missing-only -article-id 366
go run ./cmd/velis-admin -config configs/config.example.yaml ai backfill -stage generation -mode outdated-only -article-id 366 -dry-run
```

`-order oldest|newest` 只适用于 `-limit 1..1000`，默认 `oldest`。`-all` 固定命令开始时的文章 ID 快照并在内部短事务分页，只推进异步任务，不在 CLI 进程内调用模型；非 dry-run 必须带 `-confirm-all`。相同目标的 pending/running/retry_wait 任务会跳过，failed 任务可显式重推。全量执行可能产生大量模型费用，应先 dry-run，并检查 Token 预算、错误指标及 generation/Embedding 配置；`stage=all` 重新生成时会同时更新两个目标 profile。

推进 profile 升级后，用下面的查询核对是否仍有文章停留在旧 profile，把两个版本值换成当前部署的取值：

```sql
SELECT s.article_id, s.generation_profile_version, s.embedding_profile_version
FROM velis.ai_current_selections s
JOIN velis.articles a ON a.id = s.article_id AND a.current_revision_id = s.revision_id
WHERE a.status = 'published'
  AND (s.generation_profile_version <> 'generation-v2' OR s.embedding_profile_version <> 'embedding-v2');
```

Compose 部署中执行：`docker compose exec postgres psql -U velis -d velis -c "<上面的 SQL>"`。有结果说明该文章仍是旧 profile，用 `-mode outdated-only` 精确推进；没有结果表示所有公开文章的增强结果都与当前 profile 一致。

## 搜索投影

**OpenSearch 不是事实源。** 文章、修订、公开状态与 AI 当前选择都以 PostgreSQL 为准；索引只是可丢弃的派生投影。删除整个索引不会丢任何业务事实，重建即可恢复。投影写入永远不会反向修改 PostgreSQL。

一次完整的首次初始化与存量重建：

```bash
export VELIS_SEARCH_ENDPOINTS=http://localhost:9200   # Compose 只把 OpenSearch 发布到本机端口
ADMIN="go run ./cmd/velis-admin -config configs/config.example.yaml"

# 1. 建立首个物理索引、读写别名与 schema 身份（幂等，可重复执行）
$ADMIN search index init

# 2. 全量重建：创建候选索引、快照公开文章、追赶增量、校验后停在 validated
$ADMIN search rebuild start

# 3. 查看服务索引、活动重建与最近记录
$ADMIN search rebuild status

# 4. 校验通过后再原子切换读写别名，并打开 24h 回滚窗口
$ADMIN search rebuild cutover

# 5. 回滚窗口结束后清理旧索引（必须显式确认，且只删除精确目标）
$ADMIN search rebuild cleanup -index velis-articles-v1-20260925t120000z-aaaaaa -confirm
```

OpenSearch 端点必须显式给出，因为示例配置默认不启用投影。

运维边界：

- `rebuild start` 一次只允许一个活动重建；已有活动重建或回滚窗口未关闭时会明确拒绝。
- `rebuild resume` 从持久化的文章 ID 与 change sequence 水位继续，进程中途退出不会丢进度，也不会重写已完成的批次。
- 校验不通过（公开文档数与候选索引不一致、存在落后投递、抽样身份或内容指纹不符）时**拒绝切换**，当前索引继续服务；失败报告持久化在重建记录上。
- `rollback` 只能在回滚窗口内执行；窗口内 Worker 会同时写新旧两个索引，因此切回不会有落后文档。
- `cleanup` 拒绝删除当前读索引、回滚窗口内的索引、仍被投递引用或前缀未知的索引。回滚窗口到期后它会先停止该索引的投递再删除。
- 观察积压：`velis_search_projection_jobs`、`velis_search_oldest_pending_age_seconds`、`velis_search_lagging_deliveries`、`velis_search_bulk_items_total` 与 `velis_search_rebuild_phase`。
- 失败投递超过自动尝试上限后进入 `failed` 并停止自动重试；目标再次变化会自动重新激活，也可以用 `search retry -limit <1..1000> [-article-id <id>] [-index <物理索引>]` 有界重试，命令输出可审计报告。
- 单篇文章的永久失败（例如映射错误）只影响该篇，不会阻塞同批其它文档。
- 应用回滚时先停投影 Worker；旧应用忽略新增表，文章主链路继续可用。不要因应用回滚执行 down migration 或删除 OpenSearch 索引。

## 数据迁移与回滚

`000004_unify_content_supply` 会保留历史 RSS 文章 ID，把旧正文回填为 revision 1，并以原 `discovered_at` 固定 `published_at`。迁移前应备份并在专用环境核对文章数、ID、状态、修订和 latest 抽样。

`000005_sort_latest_by_source_time` 将公开文章的 latest 索引替换为 `(COALESCE(source_published_at, published_at), id)` 倒序表达式索引；down migration 只恢复旧索引，不修改文章数据。应用回滚时应先回滚 API，再回滚该迁移，避免查询排序与索引语义不一致。

`000006_create_reliable_article_async` 创建 Outbox、消费 Inbox 与异步任务槽位。空表时可下迁移；只要存在事件、消费记录或任务，down 会拒绝执行。应用回滚前先停 Relay/Consumer、确认并备份这些表；不得用 force 或删数据绕过保护。已发布 Outbox 默认保留 7 天并由 Worker 分批清理，未发布事件不会被清理。

`000007_add_ai_content_enrichment` 扩展任务阶段、租约、重试与完成状态，并创建不可变 generation/Embedding 结果、独立 current 指针和模型调用审计表。回滚前先停 AI Worker；只要存在增强结果、调用记录或任务已进入新状态，down 会明确拒绝。向量保存为 `real[]`，此阶段没有向量查询或索引。

`000008_harden_ai_provider_acceptance` 增加同一任务 generation 跨重试共享的单次格式纠正额度，并为模型调用审计补充结构化输出模式与低基数安全原因。只要已经使用纠正额度或存在新调用类型/审计字段数据，down 会拒绝；应用回滚时应保留该迁移和审计事实。

`000009_add_search_projection` 创建搜索投影槽位、按物理索引的投递、索引服务状态与重建记录，并建立一个全局 change sequence。down 只在投影槽位、投递与重建记录全为空、且回滚窗口已关闭时允许执行，避免静默丢失诊断状态。应用回滚时先停投影 Worker；旧应用忽略这些表，不要用 force 绕过保护。

`000010_add_article_feedback` 创建阅读窗口、收藏和文章级负反馈表。只要任一表非空，down 就会拒绝删除反馈事实；应先备份并明确恢复方案，不得通过清表绕过保护。recommend 复用这些事实，不新增推荐结果表。

`000011_add_generation_method` 为生成结果增加 `model|extractive` 来源，旧行默认标记为 `model`，唯一身份加入来源以允许相同输入的模型与摘录结果共存。升级已有环境时先备份生成结果与当前选择、应用迁移，再更新 API、Worker 和 Web。存在 `extractive` 行时 down 会拒绝删除来源字段；历史失败任务不会随升级自动重排，须按前文补录命令先 dry-run 再显式推进。

down migration 受保护：只有数据库仍是“单修订 RSS、无投稿、无资产”的旧模型可表达状态时才允许回退。只要存在用户投稿、资产或第二修订，down 会在事务内明确失败。不要使用 force 或删除数据绕过保护；此时应保持数据库前滚并修复/回滚应用。

## 运行边界与故障降级

核心发布与阅读链路不依赖 MQ、Redis、搜索或模型：PostgreSQL 可用时，RSS、纯文本投稿、latest 和详情即可工作。OpenSearch 未配置或故障时搜索返回明确的 `503 SEARCH_UNAVAILABLE`，有正向画像的推荐在需要重新召回时按 latest 降级；已有有效缓存计划可在约 30 秒窗口内继续经当前事实复核；匿名或无正向画像仍按冷启动读取，不需要搜索。这些故障不会影响 latest、详情或 readiness。MQ 故障时事件留在 Outbox，恢复后 Relay 追赶；当前修订尚无成功增强结果时 `enhancement` 为 null，Web 回退到原始 excerpt。同一修订已选中的成功结果会保留至新结果成功切换；非法输出耗尽后的摘录标记为 `extractive`，不展示为模型摘要。

文章反馈事实仅依赖 PostgreSQL；阅读按 UTC 固定 30 分钟窗口去重，画像按 UTC 日最多计一次、单文章最多计三次，过期事实即使清理 Worker 暂停也不会参与画像。用户级 RSS 订阅、投稿审核、following/hot Feed 和社交功能不在当前范围；Web 导航中的占位页不代表对应能力已实现。

保留期一览：

| 数据 | 保留期 | 清理方 |
| --- | --- | --- |
| 阅读事实 | 90 天 | `velis-worker` |
| 有效负反馈 | 180 天 | `velis-worker` |
| 已发布 Outbox 事件 | 7 天 | `velis-worker` 分批清理，未发布事件不清理 |
| 登录失败事件 / 过期限制 | 30 分钟 / 24 小时 | 认证开启时由 Worker 分批清理 |
| 会话与刷新令牌 | 过期或撤销后 7 天 | 同上；用户与管理审计不清理 |

## 观测

认证相关计数与耗时继续使用结构化日志；异步投影日志使用 `trace_id`、`event_id`、`task_id`、`worker_id` 串联，错误限长并脱敏。API 与 Worker 均提供独立的内部 `/metrics`；搜索指标使用固定结果、阶段、PIT、检索模式、通道、降级原因与依赖错误分类标签，记录请求、分段耗时、两路/并集候选、公开事实/陈旧身份过滤、扫描上限与 PIT 生命周期。Prometheus 同时抓取 API、Worker 与 RabbitMQ。Grafana 只有 Compose 入口，尚无正式仪表盘或告警规则。

浏览器 Playwright E2E、压测和完整监控告警尚未完成；可靠异步故障演练范围与证据见[可靠异步验证说明](../backend/test/integration/reliable_async.md)，不把它描述为生产级灾备验证。

## 集成测试与验证

```bash
make check
cd backend && GOCACHE=/tmp/feedvelis-go-cache go vet ./...

# 专用 _test PostgreSQL 与 RabbitMQ 均已启动时
VELIS_TEST_DATABASE_URL='postgres://velis:velis@localhost:5432/velis_test?sslmode=disable' \
VELIS_TEST_RABBITMQ_URL='amqp://velis:开发密码@localhost:5672/velis' \
make integration-async

# OpenSearch 模板/Bulk/别名、BM25/PIT 契约测试，以及 PostgreSQL + OpenSearch 搜索链路测试
VELIS_TEST_OPENSEARCH_URL='http://127.0.0.1:9200' make integration-opensearch
VELIS_TEST_DATABASE_URL='postgres://velis:velis@localhost:5432/velis_test?sslmode=disable' \
VELIS_TEST_OPENSEARCH_URL='http://127.0.0.1:9200' make integration-search
```

`make check` 包含 gofmt、Go 单测/竞态/构建、Vitest 与 Web 类型检查/构建；其中 gofmt 会修改未格式化的 Go 文件。独立命令见 [Makefile](../Makefile)。CI 另外执行 `go vet`，详见 [ci.yml](../.github/workflows/ci.yml)。

| 集成目标 | 必需变量 | 覆盖范围 |
| --- | --- | --- |
| `make integration-postgres` | `VELIS_TEST_DATABASE_URL` | `backend/test/integration` 的 PostgreSQL 用例 |
| `make integration-rabbitmq` | `VELIS_TEST_RABBITMQ_URL` | RabbitMQ 适配器 |
| `make integration-opensearch` | `VELIS_TEST_OPENSEARCH_URL` | 严格模板、Bulk/别名、BM25 与 PIT 契约 |
| `make integration-search` | `VELIS_TEST_DATABASE_URL`、`VELIS_TEST_OPENSEARCH_URL` | 候选批量复核、下架过滤与跨适配器链路 |
| `make integration-redis` | `VELIS_TEST_REDIS_ADDRESS` | Redis 测试基座与缓存适配器 |
| `make integration-cache-reads` | `VELIS_TEST_DATABASE_URL`、`VELIS_TEST_REDIS_ADDRESS`、`VELIS_TEST_OPENSEARCH_URL` | 缓存读取与推荐真实用例 |
| `make integration-all` | 上述全部 | 依次运行 PostgreSQL、RabbitMQ、OpenSearch、搜索与缓存读取 |

任一必需变量缺失时，对应目标的校验会以退出码 2 直接失败，不把 skip 当通过。

- 已有 Domain/Application、抓取解析清洗、Hertz、CLI、配置、架构依赖与前端测试；账户领域、认证用例、HTTP 中间件、安全适配器与前端会话模块都有单测。
- PostgreSQL 集成测试通过 `VELIS_TEST_DATABASE_URL` 启用，要求已迁移的专用 `_test` 数据库（测试基座会自动应用迁移）；测试会清空 Source/Article 与账户相关表。未设置该变量时跳过，普通 CI 通过不能代替数据库集成验证。
- OpenSearch 集成测试通过 `VELIS_TEST_OPENSEARCH_URL` 启用，覆盖严格模板、Bulk/别名、BM25 字段权重、精确筛选、稳定排序和 PIT 快照；`make integration-search` 同时要求专用 PostgreSQL，覆盖候选批量复核、下架过滤和跨适配器链路。两个 Make 目标缺少对应变量时都会直接失败，不把 skip 视为通过。
- recommend 的 PostgreSQL/OpenSearch 链路与生成来源的 PostgreSQL 测试命令见[集成测试说明](../backend/test/integration/README.md)。`make integration-search` 的筛选不包含推荐用例，需显式运行 `TestArticleRecommendationPostgresOpenSearch`；相关依赖未配置时的 skip 不算验收通过。
- 可靠异步真实依赖测试还要求 `VELIS_TEST_RABBITMQ_URL`；`make integration-async` 在任一变量缺失时直接失败。Broker 重启演练会真实重启容器，默认跳过，需按[可靠异步验证说明](../backend/test/integration/reliable_async.md)单独执行。
- 推荐的新部署 profile 示例为 generation `generation-v2`（Workflow `hierarchical-v2`、Prompt `summary-v2`）与 Embedding `embedding-v2`（输入 `retrieval-document-v1`、固定维数模型示例为 1024 维）。profile 版本变化不会自动全量重算，应通过精确、有限批次或经确认的全量 backfill 显式推进。
- 真实 MinIO 测试通过 `VELIS_TEST_MINIO_ENDPOINT`、`VELIS_TEST_MINIO_UPLOAD_ENDPOINT`、`VELIS_TEST_MINIO_ACCESS_KEY`、`VELIS_TEST_MINIO_SECRET_KEY`、`VELIS_TEST_MINIO_BUCKET` 启用；Compose CORS 测试还要求 `VELIS_TEST_MINIO_WEB_ORIGIN`。内部与公共测试端点应使用不同 authority（例如 `127.0.0.1:9000` 与 `http://localhost:9000`）；未设置内部端点时测试会明确报告跳过，不能计作通过。
- [I2 可复现闭环脚本](../web/e2e/README.md)会在本地/`.test` API 创建临时用户、文章和 Source，验证用户直发、RSS 抓取、联合 latest、编辑冲突和作者/管理员下架；它不是生产脚本。

验证记录：

- 2026-09-22 使用专用 `_test` 数据库和真实 MinIO 完成 I2 全依赖回归：PostgreSQL 集成套件、MinIO 私有 Bucket/三种图片格式/流式读取与删除测试，以及上述 8 步 HTTP 闭环均通过；临时 API、数据库和测试对象已在验证后清理。
- 推荐 Feed 与 AI 摘要可靠性 change 已于 2026-10-09 同步长期规格并归档：[推荐验收记录](../openspec/changes/archive/2026-10-09-add-recommend-article-feed/tasks.md)、[摘要可靠性验收记录](../openspec/changes/archive/2026-10-09-improve-ai-summary-reliability/verification.md)。记录中的 `make check` 与真实依赖测试均通过；本次归档未部署应用、执行数据库迁移或重排历史任务。
- Redis 卡片/latest 读取缓存及用户隔离的推荐计划 change 已于 2026-10-10 同步长期规格并归档，见[缓存验收记录](../openspec/changes/archive/2026-10-10-cache-article-and-feed-reads-with-redis/verification.md)；后续顺序见 [Velis Roadmap](../Velis%20Roadmap.md)。

## Agent 会话基础操作

私有会话的启用、七个端点、幂等、永久最小删除标记、关闭功能保留数据和空事实回退命令见 [Agent 会话操作](agent-conversations.md)，三个内部只读工具及预算见 [工具说明](agent-tools.md)。`make integration-agent-conversations` 要求专用 PostgreSQL `_test` 库；`make integration-agent-tools` 额外要求真实OpenSearch和Redis，缺少变量均非零退出。关闭Agent不删除历史，不通过清空业务数据完成降级迁移。
