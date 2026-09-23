# Design

## Context

当前文章写路径已经具备可复用的 PostgreSQL 事务基础：用户和管理员写命令在 HTTP 幂等事务中调用文章仓储，RSS Ingest 也可由 `TxManager` 包裹；仓储通过 context 复用同一 pgx transaction。文章具有不可变修订和单调递增的 `lock_version`，足以作为事件聚合版本与任务 fencing 依据。

RabbitMQ 目前只有 Compose 容器以及 Infrastructure/Interfaces 空包，没有连接配置、AMQP 客户端、拓扑、持久卷、Relay 或 Consumer。现有 `velis-worker` 同时运行 Feed 与清理调度，但没有通用组件监督、应用指标端点或 MQ 断连恢复。参见 proposal.md 的动机和 `specs/reliable-article-async/spec.md` 的行为契约。

```text
+------------------+       PostgreSQL transaction
| Article use case |-----------------------------------+
+------------------+                                   |
        |                                               v
        |                                      +----------------+
        |                                      | articles       |
        |                                      | article_versions|
        |                                      | outbox_events  |
        |                                      +-------+--------+
        |                                              |
        |                                       lease + confirm
        |                                              v
        |                                      +---------------+
        |                                      | Outbox Relay  |
        |                                      +-------+-------+
        |                                              |
        |                                              v
        |                                      +---------------+
        |                                      | RabbitMQ      |
        |                                      +-------+-------+
        |                                              |
        |                                       manual Ack
        |                                              v
        |                                      +---------------+
        +--------------------------------------| Task Consumer |
                                               +-------+-------+
                                                       |
                                                one DB transaction
                                                       v
                                          +-------------------------+
                                          | consumed_events         |
                                          | async_tasks             |
                                          +-------------------------+
```

## Goals / Non-Goals

**Goals:**

- 让文章事实、HTTP 幂等结果和 Outbox 事件共享一个提交边界，而不让 API/RSS 请求等待 MQ。
- 明确证明 Relay 与 Consumer 在断连、崩溃、重复、乱序和多实例下最终收敛。
- 形成可由后续 AI Worker 领取的最新文章增强任务，并为旧 generation 结果提供 fencing 基础。
- 让存量公开文章通过同一事件链路有界补录，而不是在 migration 或一次性脚本中绕过契约。
- 提供足以复现故障与判断积压的日志、指标、CLI 和真实依赖测试。

**Non-Goals:**

- 不领取或执行 `article.enrichment`，不增加模型、Prompt、Token、Embedding 或 AI 元数据。
- 不建设任务级 `running/retry_wait/succeeded/dead` 流程；本 change 的任务状态仅为 `pending/canceled`，后续 AI change 在 generation 条件下扩展状态机。
- 不引入 Redis、OpenSearch、多级 TTL retry queue、新 HTTP/Web 管理页面或第二个 Worker 二进制。
- 不保证事件全局有序、恰好一次投递或跨 PostgreSQL/RabbitMQ 的分布式事务。

## Decisions

### 1. Application 显式追加集成事件

在 Application 层定义与 AMQP 无关的事件信封、文章事件工厂和 `Outbox` 端口。用户文章、管理员状态变更和 RSS Ingest 在仓储返回最终文章/修订事实后显式调用 Outbox 端口；PostgreSQL 适配器通过现有 transaction context 写入同一事务。

RSS 仓储当前只返回 upsert 结果和文章 ID，需要把返回值扩展为足以构造事件的 mutation result，包括当前修订 ID/号、内容哈希、状态和 `lock_version`。用户与管理员用例已经取得 `StoredArticle`，只需根据变更前后状态和 `changed` 标志选择事件。事件选择规则集中在 Application 层测试，仓储不得自行发布或认识 RabbitMQ。

选择该方案而不是领域对象暂存 Domain Events，是因为当前文章聚合的主要写规则仍在 Application/Repository 协作中；为单一集成链路强行改造成完整 Domain Event 聚合会扩大 I3a。也不让 PostgreSQL trigger 构造 JSON，因为 trigger 难以复用应用契约校验、trace 上下文和单元测试。

### 2. 只发公开生命周期事实

事件类型固定为：

| 事件 | 产生条件 |
| --- | --- |
| `article.published.v1` | 直接发布、草稿首次发布、作者重新发布、管理员恢复、存量补录 |
| `article.revised.v1` | 当前状态为 published 且内容哈希变化，含 RSS 更新 |
| `article.offlined.v1` | published 转为作者或管理员 offline |
| `article.deleted.v1` | 用户文章从任意非 deleted 状态软删除 |

草稿创建/编辑、offline 编辑、内容无变化和 RSS `last_seen_at` 更新不发事件。版本间隔因此合法；Consumer 只比较大小和当前事实，不要求连续。

每次业务提交至多为同一文章事实写一个事件。Outbox 使用 `(event_type, aggregate_type, aggregate_id, aggregate_version)` 唯一约束吸收意外重复追加；HTTP 幂等重放本身不会重新进入 execute callback。

### 3. 自定义紧凑 JSON 信封

信封采用项目契约而不引入 CloudEvents 依赖：

```json
{
  "event_id": "0199...",
  "event_type": "article.published.v1",
  "occurred_at": "2026-09-23T10:15:30.123456Z",
  "producer": "velis.content",
  "trace_id": "0123456789abcdef0123456789abcdef",
  "aggregate": {
    "type": "article",
    "id": "42",
    "version": 7
  },
  "payload": {
    "article_id": 42,
    "origin_type": "user",
    "revision_id": 81,
    "revision_no": 3,
    "content_hash": "...",
    "status": "published"
  }
}
```

顶层、aggregate 和各事件 payload 都严格解码并拒绝未知字段。`event_type` 后缀承担重大版本；v1 字段语义不原地改变。消息不携带正文，Consumer 通过 Application 查询端口读取当前文章事实，避免大消息和数据库模型泄漏。

版本化 JSON Schema 和正反例 fixture 放在 `backend/api/events/article/v1/`，作为仓库内事件契约；Go codec 的契约测试必须读取这些 fixture，防止文档与实际编码漂移。OpenAPI 不描述 AMQP 消息，因此不修改 `velis.yaml`。

`event_id` 使用 UUIDv7；存量补录的幂等性依靠当前任务/现存事件筛选与消费收敛，而不依赖可预测 ID。`trace_id` 使用 16 随机字节的小写十六进制格式：HTTP 入口建立后放入 context，调度和 CLI 没有上游值时生成新的根标识。它与 `X-Request-ID` 分离，未来接入 OpenTelemetry 时保持格式兼容。

### 4. 三张表分别承载投递、消费和当前工作

新增 `000006_create_reliable_article_async` migration，不修改已有 migration。

`outbox_events` 保存事件与 Relay 控制状态：

- `event_id uuid` 主键；事件类型、aggregate 类型/文本 ID/版本为独立受约束列，`envelope jsonb` 保存完整信封。
- `occurred_at/created_at/next_attempt_at/last_attempt_at/published_at` 使用 `timestamptz`。
- `publish_attempts`、限长的 `last_error_code/last_error_message`。
- 可为空且必须成组出现的 `lease_owner/lease_token/lease_expires_at`；token 为每次认领新 UUID。
- partial index 覆盖未发布且到期的扫描，另有已发布清理索引和聚合唯一约束。

`consumed_events` 使用 `(consumer_name, event_id)` 主键，并保存事件类型、聚合标识/版本、`applied|noop` 结果和处理时间。它不外键引用可清理的 Outbox，本阶段不清理。

`async_tasks` 使用 UUID 主键和 `(task_type, aggregate_type, aggregate_id)` 唯一键，保存：

- 固定任务类型 `article.enrichment`；状态只允许 `pending/canceled`。
- 单调递增 `generation`、`observed_aggregate_version`。
- 当前目标 `article_id/revision_id/revision_no/content_hash`。
- `created_at/updated_at/canceled_at`，并用 check constraint 保持状态与时间一致。

任务引用软删除后仍保留的文章与不可变修订。未来任何领取或完成更新必须携带 expected generation 并使用条件更新；本 change 先提供并测试该 fencing 存储边界，但不启动执行者。

down migration 在三张表存在数据时明确失败，避免静默删除可靠性证据；仅空表允许移除。

### 5. Relay 使用短事务租约，Rabbit confirm 在事务外

每个 Relay 循环执行：

1. 在短事务中以数据库时间扫描 `published_at IS NULL`、`next_attempt_at <= now()` 且租约为空或过期的记录，通过 `FOR UPDATE SKIP LOCKED` 认领最多一个 batch，写入 owner、新 token、到期时间和本次尝试信息后提交。
2. 在事务外以 persistent delivery、JSON content type、事件类型 routing key、`mandatory=true` 发布，并等待 publisher confirm/return。
3. confirm 且已路由后以 `event_id + lease_token` 条件设置 `published_at` 并清除租约；失败则用同一条件记录分类错误、清除租约并设置带 jitter 的指数退避。

初始默认值均可由 `VELIS_*` 覆盖：batch 100、租约 30 秒、扫描间隔 1 秒、confirm 超时 10 秒、失败退避 1 至 60 秒、已发布保留 168 小时、清理 batch 500。未发布记录没有最大尝试或年龄淘汰。发布窗口使用有界并发，进程关闭时先停止认领，再在 Worker shutdown timeout 内等待 confirm；超时记录留给租约恢复。

不在数据库事务中等待 RabbitMQ，也不使用“发布后立即删除”的 Outbox。前者会长期占用连接和锁，后者无法诊断或安全处理 confirm 后崩溃窗口。

### 6. RabbitMQ 使用一个 quorum 主队列和一个 DLQ

固定逻辑拓扑如下，环境隔离使用 vhost 而不是随意改名：

```text
velis.events.v1 (durable topic)
    | bindings: article.*.v1
    v
velis.async-task-projector.v1 (durable quorum)
    |
    | reject / delivery-limit
    v
velis.dlx.v1 (durable topic)
    |
    v
velis.async-task-projector.dlq.v1 (durable quorum)
```

主队列 policy 明确设置 delivery limit 5、DLX/routing key、`dead-letter-strategy=at-least-once`、`overflow=reject-publish` 和 256 MiB byte limit。队列达到上限时让 Relay 发布失败并保留 Outbox，而不是丢弃最旧消息。Consumer 默认 prefetch 16、manual Ack。

应用使用官方维护的 `github.com/rabbitmq/amqp091-go` 并锁定 go.mod 版本。Compose 把浮动镜像改为 `rabbitmq:4.3.6-management-alpine`，增加持久化 named volume、非生产默认账户以及版本化 definitions/policy；Worker 启动时幂等声明 exchange、quorum queues 和 bindings，声明不兼容时只让 MQ 组件进入错误退避。非 Compose 环境必须先应用同等 policy，README 给出核验命令。

本 change 不增加延迟队列。Schema/版本错误直接 reject 到 DLQ；未分类单消息错误 Nack/requeue 并由 delivery limit 终止；PostgreSQL 整体不可用则暂停领取、关闭/恢复 Consumer 并以 1 至 30 秒 jitter 退避，避免消息热循环。消息级重投与后续 AI 任务级重试是两个独立机制。

### 7. Consumer 以稳定身份执行原子 Inbox 与任务收敛

逻辑消费者名硬编码为 `async-task-projector.v1`，不允许通过配置改变；Worker 实例 ID 只用于日志。处理步骤为：

1. 在事务外严格解析和校验信封；永久契约错误 reject，不创建成功消费记录。
2. 开启 PostgreSQL 事务，`INSERT consumed_events ... ON CONFLICT DO NOTHING`。
3. 已存在则提交空事务并 Ack；首次事件则锁定读取包含 deleted 状态的文章当前事实和现有任务槽位。
4. 当前为 published 时确保任务指向当前修订；不存在则 generation=1，目标或期望状态改变时 generation+1 并置 pending。
5. 当前为 offline/deleted 时只取消现有 pending；没有槽位则 noop。相同目标、相同状态和不旧的 observed version 不增加 generation。
6. 写入 `applied|noop` 结果并提交，然后 Ack。

任务更新以数据库当前文章 `lock_version` 写入 `observed_aggregate_version`。这样较旧事件也能提前收敛到更新事实；较低事件版本永远不能把槽位回退。文章行和任务行的锁序固定为 article 后 task，避免不同事件并发造成死锁。若提交后 Ack 前退出，重投命中主键后安全 Ack。

### 8. 存量补录通过 PostgreSQL 管理用例产生正常事件

新增命令：

```text
velis-admin async backfill-articles -limit <1..1000>
```

该命令只连接 PostgreSQL，按文章 ID 稳定扫描当前 published 文章，排除已经存在当前任务或已有文章异步事件的记录。在每篇文章的短事务中锁定并复核状态、当前修订和 `lock_version`，随后通过同一事件工厂与 Outbox 端口写入 `article.published.v1`。并发状态变化导致 skip，不尝试写旧快照。命令输出 created/skipped/failed 和剩余候选提示，可重复执行到 created=0；MQ 未配置不影响补录。

不在 migration 中插入事件，因为 migration 不具备 Application 契约、trace 上下文和可控批量，也会放大升级事务。不直接写 `async_tasks`，否则无法验证和复用真实可靠链路。

### 9. DLQ 重放只通过有界管理 CLI

新增命令：

```text
velis-admin async replay-dlq -limit <1..1000>
```

命令连接 RabbitMQ，从固定 DLQ 获取至多 limit 条消息，严格校验可恢复的原事件信封，以 `event_type` 作为原 routing key、保留 `event_id` 重新发布。只有 mandatory 路由成功并收到 confirm 才 Ack DLQ 原 delivery；失败时 Nack/requeue 并以非零状态结束。命令支持 context 取消并输出 confirmed/failed 数量与 event ID，不输出 envelope 正文或连接凭据。

不提供任意消息编辑、无限 drain 或 HTTP/Web 重放入口。需要修改坏数据时应修复生产者或发布新事件，不能让重放工具成为绕过契约的入口。

### 10. Worker 内部组件独立监督

`velis-worker` 继续作为唯一后台二进制。Feed、Cleanup、Relay、Consumer、Outbox Cleanup 和 Metrics Server 都实现统一的 `Run(context.Context) error` 生命周期，并由 bootstrap supervisor 管理：

- 构造期配置错误属于 fatal，Worker 不启动。
- MQ URL 为空时不构造 MQ 组件；Outbox 生产和已发布清理仍可运行。
- MQ 断连和短暂运行错误在 Relay/Consumer 内部重连；意外返回由 supervisor 记录并带退避重启，不能静默退出或停止 Feed。
- 根 context 取消后，先停止领取，等待统一 shutdown timeout，再关闭 channel、connection、指标 server 和数据库池。

现有 Feed 或 Cleanup 的行为不因 MQ 组件失败改变。后续若出现独立扩缩容证据，可把同一 Application/Infrastructure 组件装配到新二进制，无需改变事件或表契约。

### 11. 配置、安全与观测

增加 `rabbitmq` 和 Worker 异步配置，继续遵守默认值 < YAML < `VELIS_*`：URL 默认为空即关闭 MQ 组件；密码只能来自配置/环境且日志始终输出脱敏 host/vhost。拓扑名和逻辑 consumer 名是协议常量，不提供环境覆盖。数值范围在启动前校验，MQ 关闭时不强制校验连接专用字段。

Worker 增加内部 Prometheus endpoint，默认 `127.0.0.1:9091`，Compose 覆盖为 `0.0.0.0:9091` 且仅在容器网络供 Prometheus 抓取。应用指标至少包括：

- Outbox pending 数量、最老年龄、claim/publish/confirm/failure/cleanup；
- Consumer applied/noop/duplicate/requeue/permanent failure；
- task create/update/cancel、DLQ replay；
- MQ reconnect 与组件运行状态。

RabbitMQ 启用内置 Prometheus plugin，提供 queue depth、unacked、redelivery 和 DLQ depth；Compose Prometheus 配置同时抓取 Worker 与 RabbitMQ。高基数字段 `event_id/task_id/trace_id` 只进结构化日志，不作为 metric label。Outbox 错误字段和日志限制长度，不记录连接 URL、凭据或文章正文。

### 12. 验证策略

- Application 单元测试覆盖所有文章状态/内容变化对应的事件选择、严格契约编解码、重复/noop、版本间隔和任务 generation。
- PostgreSQL 集成测试覆盖文章+幂等+Outbox 回滚、`SKIP LOCKED` 多认领者、lease token fencing、Inbox+任务原子性、行锁顺序、补录并发复核、清理和安全 down migration。
- RabbitMQ 真实依赖测试覆盖 mandatory return、publisher confirm、Consumer 断连重投、delivery limit 至 DLQ、人工重放和 broker 重启后的 durable 消息；缺少测试 URL时明确 skip 并单独报告。
- Compose 故障演练覆盖停 MQ 后继续发布、积压可见、恢复追赶、confirm 后模拟崩溃导致重复、乱序注入、多 Worker 竞争及优雅关闭。
- `make check` 保持单元/静态检查入口，另提供明确的真实依赖验证命令，不能把 skip 记为通过。

## Risks / Trade-offs

- **[Outbox 在 MQ 长期不可用时无界增长]** -> 未发布事件不丢弃，使用 pending/oldest-age 指标、告警边界和运行手册处理；已发布记录默认 7 天后批量清理。
- **[confirm 后回写前崩溃会重复发布]** -> 明确采用至少一次，依赖稳定 consumer+event 去重和任务版本收敛，不宣称恰好一次。
- **[紧凑事件要求 Consumer 回读 Content 数据库]** -> 当前模块化单体可保持强一致；I7 拆服务时用受控 Application/RPC 或新事件版本演进，不在 v1 塞正文。
- **[单任务槽位不保留逐次执行历史]** -> 当前只表达最新期望工作；后续 AI change 以独立 run/attempt 记录保存模型执行审计。
- **[严格拒绝未知字段降低同版本扩展弹性]** -> 任何新增字段通过新重大事件版本发布，换取明确契约和毒消息可诊断性。
- **[单节点 Compose 的 quorum queue 不提供节点级高可用]** -> 它仍验证持久化、confirm、delivery limit 和 DLQ 语义；多节点 HA 留给 Kubernetes/运行平台阶段。
- **[MQ policy 未在非 Compose 环境正确应用]** -> 版本化 definitions、启动日志、README 核验命令和真实依赖测试共同暴露偏差；不以应用硬编码可变 policy 掩盖运维配置。
- **[Worker 组件增加后生命周期更复杂]** -> 统一 supervisor、受控重启、固定关闭顺序和 goroutine/race 测试防止静默退出及泄漏。

## Migration Plan

1. 先备份专用测试数据库并验证 `000006` up/down；生产型环境只执行 up，不在有数据时 force/down。
2. 部署固定版本 RabbitMQ、持久卷、vhost/凭据、definitions/policy 和 Prometheus plugin，核验 quorum 主队列、DLQ、delivery limit 与持久化；此时旧应用不受影响。
3. 执行数据库 up migration，创建空的 Outbox/Inbox/任务表和索引。
4. 部署新 API/Worker/Admin。API 从此始终产生 Outbox；MQ URL 为空可先仅积压并验证核心内容路径。
5. 配置 MQ URL，启动 Relay/Consumer，验证新增文章从 Outbox 到 pending task 的闭环以及指标。
6. 运行有界 `async backfill-articles` 直到无候选，观察 backlog 清空并核对所有当前公开存量文章具有任务或在途事件。
7. 执行重复、乱序、broker 中断、Worker 中断和 DLQ 重放演练，保存验证命令与结果后更新 README/Roadmap。

回滚时先清空 MQ URL并停止 MQ 组件，保留三张表和 RabbitMQ 队列；旧版本 API 可继续使用原文章表。回滚应用会停止为新文章生成事件，因此只允许作为短期故障回退并记录缺口。数据库 down 仅在三张表确认无数据且已备份后执行；RabbitMQ 队列和持久卷不得随普通应用回滚删除。
