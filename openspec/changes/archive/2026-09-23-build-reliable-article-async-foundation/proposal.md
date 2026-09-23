# Proposal

## Why

I2 已能可靠地发布和读取 RSS 与用户文章，但文章事实尚不能跨进程可靠触发后续异步工作：RabbitMQ 中断、Worker 崩溃或重复投递都可能造成任务丢失、重复执行或旧状态覆盖新状态。I3 应先建立不依赖 AI 的可靠异步底座，证明文章事实能够原子地产生事件并最终收敛为可执行任务，同时保持现有发布与阅读主链路不依赖 MQ。

## What Changes

- 新增 PostgreSQL Outbox，使公开文章的发布、公开修订、下架和删除事实与文章业务变更原子提交；草稿及离线状态下的编辑不产生集成事件。
- 定义 `article.published.v1`、`article.revised.v1`、`article.offlined.v1`、`article.deleted.v1` 事件信封与紧凑载荷，使用文章 `lock_version` 作为聚合版本，正文仍以 PostgreSQL 当前事实为准。
- 在现有 `velis-worker` 中增加独立受监管的 Outbox Relay 和 RabbitMQ Consumer；Relay 使用租约认领、publisher confirm、失败退避和崩溃恢复，允许至少一次及乱序投递。
- 新增稳定逻辑消费者的消费去重与版本收敛：`consumed_events` 和 `async_tasks` 在同一事务更新，重复或过期事件不得重复产生效果。
- 为每篇文章维护一个带 generation 的 `article.enrichment` 任务槽位；发布或公开修订使最新修订进入 `pending`，下架或删除取消可取消任务。本 change 不执行 AI 增强。
- 提供有界、可重复执行的管理 CLI，为升级前已有且当前仍公开、尚未进入异步链路的文章补写 `article.published.v1` Outbox 事件，使存量内容也能形成任务。
- 建立单个 durable quorum 主队列、有限 delivery limit 和 DLQ，并提供有数量上限的管理 CLI 人工重放；暂不建设多级延迟队列。
- RabbitMQ 未配置或不可用时继续写入 Outbox 并保持文章发布、RSS 抓取、latest 与详情可用；恢复后从未投递事件继续追赶。
- 增加配置、迁移、日志/指标边界以及 PostgreSQL/RabbitMQ 正常、故障、重复、乱序、重放和优雅关闭验证。

## Capabilities

### New Capabilities

- `reliable-article-async`: 定义公开文章事件的原子产生、Outbox 至 RabbitMQ 的至少一次投递、幂等乱序消费、文章增强任务收敛、DLQ 重放及 MQ 缺失时的降级行为。

### Modified Capabilities

无。现有 `unified-articles` 已要求核心内容路径不依赖 RabbitMQ，本 change 在不改变该行为的前提下新增独立异步能力。

## Impact

- 数据库新增版本化迁移以及 `outbox_events`、`consumed_events`、`async_tasks` 数据与索引；不改写已有迁移。
- 后端新增事件/Application 端口、PostgreSQL 适配器、RabbitMQ 发布消费适配器和管理 CLI，并调整文章写用例、Worker 装配及生命周期管理。
- 后端新增 AMQP 客户端依赖；Compose 固定 RabbitMQ 版本并提供版本化拓扑/policy，新增非敏感 MQ 配置和环境变量。
- API/OpenAPI 不增加业务端点；Web、Redis、OpenSearch、Eino、模型调用、AI 元数据和 Embedding 均不在本 change 范围内。
- README 与 Roadmap 更新为 I3 可靠异步底座已交付、AI 增强仍待后续 change，且记录故障测试和可复现验证方式。
