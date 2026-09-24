# 可靠异步与 AI 内容增强验证说明

本文记录 I3 可靠异步底座与 AI 内容增强的可复现验证和最小运维流程。默认测试使用确定性 ChatModel/Embedder，不访问公网或真实模型账户。

## 契约与拓扑

- 事件 Schema 位于 `backend/api/events/article/v1/`，包含 `article.published.v1`、`article.revised.v1`、`article.offlined.v1`、`article.deleted.v1`。信封只携带版本事实与内容摘要，不携带正文。
- exchange 为 `velis.events.v1`，routing key 使用事件类型；主 quorum queue 为 `velis.async-task-projector.v1`，绑定 `article.*.v1`。
- 主队列 policy 使用 `delivery-limit=5`、`max-length-bytes=268435456`、`overflow=reject-publish` 和 at-least-once DLX；quorum DLQ 为 `velis.async-task-projector.dlq.v1`。
- RabbitMQ 4.3 中 `basic.nack(requeue=true)` 不增加 `delivery-count`。Consumer 对未分类单消息错误使用 `basic.reject(requeue=true)`，使 delivery limit 能终止毒消息循环；契约错误直接 reject、不 requeue。

文章写事务只依赖 PostgreSQL：公开状态变化与 Outbox 原子提交。MQ 未配置或不可用时，文章发布、latest 和详情仍可用；Relay 恢复后继续认领未发布事件。Consumer 每次都锁定并复核文章当前事实，因此重复、逆序和有版本间隔的旧事件不能复活旧状态。

`article.enrichment` 任务按 generation 与 Embedding 两阶段执行。Worker 使用短事务、`FOR UPDATE SKIP LOCKED`、generation 与 lease token 认领任务，模型调用期间不持有数据库事务。生成结果和向量均按修订及 profile 版本不可变保存；公共读取只关联当前修订的 generation 指针，不加载向量。模型未配置、模型故障或 Embedding 失败都不会阻止文章发布与阅读。

## 本地启动与验证

创建专用测试库并启动依赖后，在仓库根目录执行：

```bash
docker compose up -d postgres rabbitmq
docker exec -it velis-postgres-1 psql -U velis -d postgres -c 'CREATE DATABASE velis_test'

VELIS_TEST_DATABASE_URL='postgres://velis:velis@localhost:5432/velis_test?sslmode=disable' \
VELIS_TEST_RABBITMQ_URL='amqp://velis:velis-development-only@localhost:5672/velis' \
make integration-async
```

`make integration-async` 在任一测试 URL 缺失时以非零状态退出，避免把 Go 的 skip 当成真实依赖通过。测试会清空 `_test` 库中的业务表和固定 RabbitMQ 测试队列，不得指向生产或共享数据库。

不依赖 RabbitMQ 的确定性 AI E2E 可单独运行：

```bash
cd backend
VELIS_TEST_DATABASE_URL='postgres://velis:velis@localhost:5432/velis_test?sslmode=disable' \
go test -count=1 -v ./test/integration \
  -run 'TestDeterministicAIEnrichmentE2E|TestEnrichmentRepository|TestAIEnrichmentMigration'
```

Broker 重启演练有真实外部副作用，因此默认跳过。单独执行：

```bash
cd backend
VELIS_TEST_RABBITMQ_URL='amqp://velis:velis-development-only@localhost:5672/velis' \
VELIS_TEST_RABBITMQ_RESTART=1 \
go test -count=1 -v ./internal/infrastructure/messaging/rabbitmq \
  -run TestPublisherRecoversAfterBrokerRestart
# 测试输出 RABBITMQ_RESTART_READY 后，在另一终端执行：
docker restart velis-rabbitmq-1
```

## 补录、升级与 DLQ 重放

命令必须给出 `1..1000` 的批量上限：

```bash
cd backend
go run ./cmd/velis-admin -config configs/config.example.yaml \
  async backfill-articles -limit 100
go run ./cmd/velis-admin -config configs/config.example.yaml \
  async replay-dlq -limit 100
go run ./cmd/velis-admin -config configs/config.example.yaml \
  ai backfill -stage all -mode missing-only -limit 100 -dry-run
go run ./cmd/velis-admin -config configs/config.example.yaml \
  ai backfill -stage generation -mode outdated-only -limit 25
```

补录逐篇锁定并复核，只为仍为 published 且没有事件/任务的文章写正常 published 事件；无需 MQ 即可运行，重复执行不会推进任务 generation。DLQ 重放保留原 `event_id` 和事件类型，只有重新发布收到 confirm 后才 Ack 原消息；失败消息保留在 DLQ。命令输出只有批量统计，不输出事件信封、正文或连接凭据。

AI 补录必须显式指定阶段、模式和 `1..1000` 的上限。`dry-run` 只统计候选；正式执行只推进任务，不同步调用模型。部署新 Prompt、模型或 Embedding profile 不会自动全量重算，应先预览再以小批量执行并观察预算与错误指标。

## 指标、保留与回滚

- Worker 内部端点：`/metrics`，Compose 内为 `velis-worker:9091/metrics`。
- RabbitMQ 指标：`rabbitmq:15692/metrics`；Prometheus 配置同时抓取两个目标。
- 关键指标包括 Outbox pending/oldest age/操作结果、Consumer 结果、任务变化、DLQ 重放、MQ 重连和组件状态，以及 `velis_ai_tasks`、`velis_ai_oldest_task_age_seconds`、`velis_ai_operations_total`、模型耗时、Token 和稳定错误分类。标签不使用 event/task/trace ID、URL、正文或凭据。
- AI 结构化日志只使用 task ID、generation、stage、article/revision ID、结果和错误分类关联，不记录正文、Prompt、原始输出、完整端点或密钥；逐次调用元数据另存于 `ai_model_calls`。
- 已发布 Outbox 默认保留 7 天并批量清理；未发布事件永不由保留清理删除。
- `000006_create_reliable_article_async` 只有在 Outbox、Inbox、任务表均为空时允许 down。回滚前停止 Relay/Consumer、备份并核对积压；禁止 force 或删除事实来绕过保护。
- `000007_add_ai_content_enrichment` 只有在生成结果、向量、当前指针和调用审计均为空，且任务状态仍兼容旧 schema 时允许 down。应用回滚应保留该迁移及历史结果；执行 down 前先停止 AI Worker、备份并核对任务与审计数据。

## 2026-09-23 故障演练证据

| 场景 | 自动化证据 | 结果 |
| --- | --- | --- |
| confirm 前取消、confirm 后回写失败 | `TestRelayCancellationLeavesLeaseForRecovery`、`TestRelayConfirmedAndWritebackFailure` | 事件保留租约/待重试，不误标已发布 |
| 双 Relay 竞争、租约过期、旧 token fencing | `TestRelayLeaseCompetitionRecoveryAndFencing` | 不重复持有，过期可恢复，迟到写回被拒绝 |
| 重复消息、逆序与版本间隔 | `TestRabbitMQDuplicateDeliveryUsesInboxWithoutAdvancingTask`、`TestAsyncTaskProjectionConvergesUnderConcurrencyAndOutOfOrderEvents` | Inbox 去重；任务只匹配数据库当前事实 |
| 两个 Consumer 竞争 | `TestMultipleConsumersCompeteForDeliveries` | 两个 Worker 均收到投递并正常 Ack |
| 未 Ack 断连与毒消息 | `TestConsumerRedeliveryAndDeliveryLimitToDLQ` | 断连消息标记重投；第 5 次失败后进入 DLQ |
| RabbitMQ 重启 | `TestPublisherRecoversAfterBrokerRestart` 配合 `docker restart` | 连接关闭可感知，Broker 健康后重新声明拓扑并恢复 confirm |
| DLQ 重放 | `TestDLQReplayPreservesEventAndKeepsFailure` | event ID 保留；成功 Ack，失败留存 |
| MQ 不可达、恢复追赶与 AI 闭环 | `TestReliableAsyncPipelineAccumulatesWithoutMQAndCatchesUpAfterRecovery` | 文章立即可读、Outbox 积压；恢复后经任务、generation 与 Embedding 进入公开读取，编辑后旧结果立即隐藏 |
| 任务认领、租约恢复与旧 token | `TestEnrichmentRepositoryLeaseFencingAndStageCommits`、`TestEnrichmentRepositoryRejectsExpiredAndChangedGeneration` | 并发只认领一次；崩溃后租约可恢复；迟到执行者不能提交 |
| generation/Embedding 成功闭环 | `TestDeterministicAIEnrichmentE2E`、`TestReliableAsyncPipelineAccumulatesWithoutMQAndCatchesUpAfterRecovery` | 固定 Eino Workflow 生成公开摘要与标签，向量持久化且没有检索 API |
| 修订切换与读取降级 | `TestPublishedQueriesExposeOnlyCurrentRevisionGenerationWithoutVector` | 旧修订增强立即隐藏；无结果时显式 `null` 并继续使用 excerpt |
| profile 补录与幂等 | `TestAIBackfillDryRunAndPendingTargetAreIdempotent`、`TestAIBackfillConcurrentRunsAdvanceOnce` | dry-run 零写入；并发重复目标只推进一次；升级目标有界推进 |
| 模型中断、非法输出与预算 | `TestExecutorEmbeddingFailureKeepsGenerationAndBacksOff`、`TestWorkflowBudgetInvalidOutputAndCancellation` | 独立有限重试，不重复 generation，不发布不完整结果 |
| 有界关闭与组件隔离 | `TestSupervisorReportsBoundedShutdown`、`TestSupervisorRestartsFailedComponentWithoutStoppingOthers` | MQ 组件失败不停止 Feed，关闭超时显式报告 |

本轮还通过 RabbitMQ 管理命令核对了实际 exchange、binding、quorum queue、policy 和 Prometheus plugin；通过 Worker/RabbitMQ HTTP 指标端点核对运行时计数。这里的单节点 Compose 演练不等同于生产集群高可用、备份恢复或容量验证。
