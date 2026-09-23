# Tasks

## 1. 契约、迁移与配置基础

- [ ] 1.1 在 `backend/api/events/article/v1/` 定义四类文章事件的 JSON Schema、有效/无效 fixture 和契约说明，并以 Go 严格编解码测试验证必需字段、未知字段、类型、版本、UTC 时间与无正文约束。
- [ ] 1.2 新增 `000006_create_reliable_article_async` up/down migration，创建并约束 `outbox_events`、`consumed_events`、`async_tasks` 及所需 partial/unique 索引；用专用 PostgreSQL 测试库验证 up、空表 down、有数据时拒绝 down 和已有 I2 数据无损。
- [ ] 1.3 增加 RabbitMQ、Relay、Consumer、Outbox 清理、Worker shutdown 与 metrics 配置，补齐 YAML/`VELIS_*` 覆盖、范围校验和 URL 脱敏测试，并验证 MQ URL 为空时连接专用配置不阻止启动。
- [ ] 1.4 引入并锁定官方 AMQP 0-9-1 与 Prometheus Go 依赖，运行 `cd backend && go mod tidy && go test ./...` 验证依赖图和现有测试无回归。

## 2. 事件模型与文章事务集成

- [ ] 2.1 在 Application 层实现事件信封、四类 payload、UUIDv7 事件 ID、32 位十六进制 trace 上下文和文章事件工厂；用表驱动测试覆盖编码 fixture、后台根 trace 与非法输入。
- [ ] 2.2 定义不含 SQL/AMQP 类型的 Outbox Application 端口，并实现 PostgreSQL append/query 基础适配器；用集成测试验证 envelope 与扁平查询列一致、聚合事件唯一约束和事务外误用不会破坏数据。
- [ ] 2.3 扩展 RSS upsert mutation result，并在 RSS 插入、公开内容更新和无变化三条路径追加正确事件；用 Application/Repository 测试验证事件与文章修订同事务且 `last_seen_at` 更新不发事件。
- [ ] 2.4 将用户文章创建、编辑、发布、下架和删除接入 Outbox，覆盖直接发布、草稿发布、离线重发、published 修订、草稿/离线编辑、无变化和任意状态删除；用现有幂等测试及新增事件断言验证重放不重复写事件。
- [ ] 2.5 将管理员下架与恢复接入 Outbox，并验证管理员/作者下架原因、最新修订引用、`lock_version` 和既有权限行为保持正确。
- [ ] 2.6 增加真实 PostgreSQL 原子性测试，在 Outbox append、资产绑定或幂等 settle 故障下注入失败，验证文章、修订、资产引用、幂等结果和事件全部提交或全部回滚。

## 3. 消费 Inbox 与任务收敛

- [ ] 3.1 实现 `consumed_events` PostgreSQL 仓储，以固定逻辑消费者 `async-task-projector.v1` 执行 insert-once 与 `applied/noop` 记录；用集成测试验证同 consumer 去重、不同 consumer 独立和事务回滚后可重试。
- [ ] 3.2 实现包含 deleted 状态的文章当前事实查询和 `async_tasks` 任务槽位仓储，固定 article 后 task 的锁序，并用并发测试验证唯一槽位、generation 单调、状态约束与 expected-generation fencing。
- [ ] 3.3 实现文章任务投影 Application 服务，在一个事务中完成 Inbox、当前事实复核及 pending/canceled 收敛；用表驱动测试覆盖首次发布、修订合并、下架、删除、重新发布、无任务取消和相同目标 noop。
- [ ] 3.4 增加重复、逆序和版本间隔集成测试，按不同顺序提交 publish/revise/offline/delete 事件并验证最终任务始终匹配数据库当前状态，旧事件和旧 generation 不能回退结果。

## 4. Outbox Relay 与保留清理

- [ ] 4.1 实现基于数据库时间、`FOR UPDATE SKIP LOCKED`、owner/token/expiry 的批量认领，以及带 token 条件的 confirm/failure 回写；用双 Relay 集成测试验证不重复持有、租约过期恢复和旧 token 迟到回写失败。
- [ ] 4.2 实现 Relay Application 循环、bounded publish window、confirm timeout、错误分类及带 jitter 的 1–60 秒指数退避；用 publisher fake 验证 mandatory return、Nack、超时、取消和 confirm 后回写失败均保留可重试事件。
- [ ] 4.3 在 RabbitMQ Infrastructure 适配器中实现持久消息、`mandatory=true`、publisher confirm/return 配对、连接关闭通知和脱敏重连日志；以真实 RabbitMQ 测试验证可路由消息 confirm、不可路由 return 和 broker 重启后的恢复。
- [ ] 4.4 实现已发布 Outbox 的 7 天可配置批量清理与 pending/oldest-age 查询，接入独立 Worker 组件；用固定时间测试验证只删除过期已发布行且永不删除未发布事件。

## 5. RabbitMQ 拓扑与 Consumer

- [ ] 5.1 将 Compose RabbitMQ 固定为 `4.3.6-management-alpine`，增加持久卷、开发凭据、Prometheus plugin 和版本化 definitions/policy，声明 topic exchange、主 quorum queue、at-least-once DLX、delivery-limit=5、256 MiB reject-publish 上限及 quorum DLQ；用 `docker compose config` 和 RabbitMQ 管理命令核验实际拓扑。
- [ ] 5.2 实现 Worker 启动时的幂等 exchange/queue/binding 声明与 policy 前置条件日志，验证重复声明成功、不可兼容拓扑只使 MQ 组件进入退避且 Feed 组件继续运行。
- [ ] 5.3 实现 manual-Ack Consumer 适配器与 prefetch=16，区分永久契约错误、PostgreSQL 全局故障和未分类单消息错误；用 fake delivery 测试验证提交后 Ack、失败不 Ack、永久 reject、数据库故障暂停领取和 context 取消。
- [ ] 5.4 增加真实 RabbitMQ Consumer 测试，验证断连前未 Ack 消息重投、重复 delivery 命中 Inbox、超过 delivery limit 进入 DLQ，且测试依赖缺失时明确报告 skip 而不计为通过。

## 6. Worker 监督、观测与安全

- [ ] 6.1 把 Feed、Cleanup、Relay、Consumer、Outbox Cleanup 和 Metrics Server 统一为受监管组件，重构 `RunWorker` 的启动、独立重启和有界关闭顺序；用 fake components 与 race test 验证 MQ 组件异常不停止 Feed、无静默退出和无 goroutine 泄漏。
- [ ] 6.2 将 Relay/Consumer 按 MQ URL 可选装配进现有 `velis-worker`，验证未配置 MQ 时只禁用 MQ 组件但仍产生 Outbox，配置 MQ 时多 Worker 可竞争认领和消费。
- [ ] 6.3 增加 Worker 内部 `/metrics` endpoint 及低基数 Outbox、发布、Consumer、任务、DLQ、重连和组件状态指标，并更新 Prometheus 抓取 Worker/RabbitMQ；用 handler 测试和 Compose 查询验证指标存在且不含 event/task/trace ID、URL、凭据或正文。
- [ ] 6.4 补齐关联结构化日志和错误截断/脱敏，运行日志测试验证 `trace_id/event_id/task_id/worker_id` 可串联正常与失败路径，且 MQ 凭据和事件正文不会输出。

## 7. 管理 CLI

- [ ] 7.1 实现 `velis-admin async backfill-articles -limit <1..1000>`，按 ID 有界扫描、逐篇锁定复核并通过正常事件工厂写 Outbox；用 PostgreSQL 集成测试验证存量 published 创建事件、已有事件/任务跳过、并发下架或修订安全跳过、重复执行 generation 不变和无 MQ 可运行。
- [ ] 7.2 实现 `velis-admin async replay-dlq -limit <1..1000>`，保留原 event ID、按 event type 路由并在 confirm 后 Ack 原消息；用 fake 与真实 RabbitMQ 测试验证 limit 必填、成功计数、失败保留、取消、已消费事件 noop 和非法事件再次进入 DLQ。
- [ ] 7.3 更新 CLI usage、bootstrap 装配和命令级输出/退出码测试，验证错误信息为简体中文、批量统计完整且不打印 envelope、正文或连接凭据。

## 8. 端到端验证与文档收口

- [ ] 8.1 增加可复现的 PostgreSQL+RabbitMQ 集成测试入口和 Makefile 命令，验证正常发布到 pending task、MQ 关闭时文章仍可读且 Outbox 积压、MQ 恢复后追赶，以及所有未运行的真实依赖测试被显式列出。
- [ ] 8.2 编写并执行 confirm 前/后 Worker 中断、重复消息、逆序注入、多 Worker 竞争、RabbitMQ 重启、DLQ 重放和优雅关闭故障演练，保存命令与关键日志/指标证据并核对没有事件丢失或旧状态复活。
- [ ] 8.3 更新配置示例、`.env.example`、README 和 Velis Roadmap，记录事件契约、拓扑、降级、补录、DLQ 重放、指标、保留/回滚和验证方式，并明确 I3 仅完成可靠异步底座、AI 增强仍未实现。
- [ ] 8.4 运行 `make check`、`cd backend && go vet ./...`、相关 race/真实依赖测试及 `openspec validate build-reliable-article-async-foundation --strict`，修复全部失败并记录 skip 与环境限制。
