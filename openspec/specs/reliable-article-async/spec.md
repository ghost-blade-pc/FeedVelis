# reliable-article-async Specification

## Purpose

定义公开文章事实从 PostgreSQL 原子进入 Outbox、经 RabbitMQ 至少一次投递并幂等收敛为异步任务的可靠性契约，使消息依赖故障、重复、乱序和进程崩溃均不破坏文章主链路或任务最终状态。

## Requirements

### Requirement: 公开文章事实与 Outbox 原子提交

系统 SHALL 在产生对异步下游有意义的文章公开生命周期变化时，于同一 PostgreSQL 事务提交文章事实和唯一的版本化 Outbox 事件；任一步骤失败 SHALL 回滚全部变化。

#### Scenario: 用户直接发布文章
- **WHEN** 用户创建一篇初始状态为 `published` 的有效文章
- **THEN** 系统 SHALL 在文章及首个修订提交的同一事务写入一个 `article.published.v1` 事件

#### Scenario: 草稿或离线文章恢复公开
- **WHEN** 草稿首次发布、作者重新发布自己下架的文章或管理员恢复管理员下架的文章
- **THEN** 系统 SHALL 写入一个引用当前最新修订的 `article.published.v1` 事件

#### Scenario: 已发布内容产生实质修订
- **WHEN** 已发布用户文章或 RSS 文章的规范化内容哈希发生变化并创建新修订
- **THEN** 系统 SHALL 在修订切换事务中写入一个 `article.revised.v1` 事件

#### Scenario: 公开文章下架或文章删除
- **WHEN** 已发布文章被作者或管理员下架，或任意用户文章被软删除
- **THEN** 系统 SHALL 分别在状态变更事务中写入一个 `article.offlined.v1` 或 `article.deleted.v1` 事件

#### Scenario: 私有编辑和无变化写入
- **WHEN** 调用者创建或编辑草稿、编辑已下架文章，或写入未产生内容哈希变化
- **THEN** 系统 SHALL 不写入文章集成事件

#### Scenario: Outbox 写入失败
- **WHEN** 文章业务变更成功但对应 Outbox 写入失败
- **THEN** 系统 SHALL 回滚文章、修订、资产绑定和 HTTP 幂等结果，并允许原命令安全重试

### Requirement: 文章事件使用版本化紧凑契约

每个文章事件 SHALL 包含唯一 `event_id`、版本化 `event_type`、UTC `occurred_at`、`producer`、可关联日志与 Trace 的 `trace_id`，以及文章聚合类型、稳定 ID、`lock_version` 和事件专用 payload；事件 SHALL 只携带稳定引用和版本信息，不得携带 Markdown、HTML、纯文本或数据库行快照。

#### Scenario: 发布或修订事件编码
- **WHEN** 系统创建 `article.published.v1` 或 `article.revised.v1`
- **THEN** payload SHALL 包含文章 ID、来源类型、当前修订 ID、修订号、内容哈希和 `published` 状态

#### Scenario: 下架或删除事件编码
- **WHEN** 系统创建 `article.offlined.v1` 或 `article.deleted.v1`
- **THEN** payload SHALL 包含文章 ID、当前修订 ID 和目标状态，且下架事件 SHALL 包含 `author` 或 `admin` 原因

#### Scenario: 后台任务没有入口 Trace
- **WHEN** RSS 调度等后台入口产生事件且上下文没有可复用的 Trace 标识
- **THEN** 系统 SHALL 生成新的有效根 `trace_id`，并在相关结构化日志中沿用它

#### Scenario: 不支持的事件契约
- **WHEN** Consumer 收到非法 JSON、缺失必需字段、未知事件类型或不支持的重大版本
- **THEN** 系统 SHALL 不修改消费去重与任务状态，并将消息作为永久失败交给 DLQ

### Requirement: RabbitMQ 缺失不阻塞文章主链路

系统 SHALL 无论 RabbitMQ 是否配置或可用都持久化应产生的 Outbox 事件；API 和 RSS 写路径不得同步连接 RabbitMQ，文章发布、latest 与详情 SHALL 只依赖原有核心依赖。

#### Scenario: RabbitMQ 未配置
- **WHEN** 服务未配置 RabbitMQ 连接
- **THEN** API 与 RSS Worker SHALL 正常处理核心内容，Relay 和 Consumer SHALL 保持禁用，未投递事件 SHALL 留在 Outbox

#### Scenario: RabbitMQ 暂时中断
- **WHEN** RabbitMQ 在文章事务提交前、提交期间或提交后不可用
- **THEN** 文章事务 SHALL 不因 MQ 故障失败，事件 SHALL 保持未投递并在 MQ 恢复后继续追赶

#### Scenario: 长期未投递事件
- **WHEN** Outbox 事件长期未获得 RabbitMQ confirm
- **THEN** 系统 SHALL 保留事件而不得按年龄或尝试次数自动删除，并 SHALL 暴露待投递数量与最老事件年龄

### Requirement: 存量公开文章可经同一异步链路补录

系统 SHALL 提供有明确数量上限、可重复执行的管理 CLI，为升级前已有且当前仍为 `published`、尚未形成当前异步工作事实的文章补写引用当前修订的 `article.published.v1` Outbox 事件；补录 SHALL 使用正常事件、Relay 和 Consumer 链路，不得由 migration 构造消息或直接写入任务。

#### Scenario: 补录存量公开文章
- **WHEN** 管理员执行存量补录且发现符合条件的已发布文章
- **THEN** CLI SHALL 在指定上限内为每篇文章写入引用当前修订和 `lock_version` 的发布事件，并报告创建、跳过和失败数量

#### Scenario: 重复执行补录
- **WHEN** 管理员再次执行补录且文章已有匹配的未投递事件、已投递事件或当前任务
- **THEN** 系统 SHALL 跳过该文章或由既有幂等边界产生 `noop`，不得重复改变任务 generation

#### Scenario: 补录期间文章状态变化
- **WHEN** 候选文章在补录扫描后、事件写入前被下架、删除或修订
- **THEN** 补录事务 SHALL 锁定并复核当前事实，只为仍公开的当前修订写事件，否则安全跳过

#### Scenario: RabbitMQ 在补录时不可用
- **WHEN** 管理员补录存量文章但 RabbitMQ 未配置或不可用
- **THEN** 补录事件 SHALL 保存在 Outbox 并在 MQ 恢复后正常追赶，CLI 不得要求直接连接 RabbitMQ

### Requirement: Relay 提供至少一次投递和崩溃恢复

Relay SHALL 通过有时限、可竞争且带 fencing token 的认领权发布未投递事件，并仅在 RabbitMQ publisher confirm 且路由成功后标记已发布；系统 SHALL 允许重复和乱序投递，但不得静默丢弃未确认事件。

#### Scenario: 多个 Relay 并发认领
- **WHEN** 多个 Worker 同时扫描同一批可投递事件
- **THEN** 每个有效租约期内同一事件 SHALL 仅由一个当前认领者发布，其他 Relay SHALL 能继续认领不同事件

#### Scenario: confirm 前崩溃
- **WHEN** Relay 认领事件后在获得 publisher confirm 前退出
- **THEN** 租约到期后另一 Relay SHALL 能重新认领并发布该事件

#### Scenario: confirm 后回写前崩溃
- **WHEN** RabbitMQ 已确认消息但 Relay 在写入 `published_at` 前退出
- **THEN** 事件 SHALL 在租约恢复后允许再次发布，并由消费幂等吸收重复

#### Scenario: 过期认领者迟到回写
- **WHEN** 旧 Relay 的租约已被新认领替代后才收到 confirm
- **THEN** 旧 Relay SHALL 无法使用过期 fencing token 覆盖当前 Outbox 状态

#### Scenario: 发布失败
- **WHEN** 发布连接失败、消息不可路由或未获得 confirm
- **THEN** Relay SHALL 保存限长脱敏错误摘要，以有上限的指数退避安排重试，且不得把事件转入消费 DLQ

### Requirement: RabbitMQ 投递失败有限并可隔离

系统 SHALL 使用持久化 exchange、单个 durable quorum 主队列、显式有限 delivery limit 和 durable DLQ 投递文章事件；Consumer SHALL 手工 Ack，并限制每个实例的在途消息数量。

#### Scenario: 成功消费
- **WHEN** Consumer 完成数据库事务
- **THEN** Consumer SHALL 在事务提交后 Ack 对应 RabbitMQ delivery

#### Scenario: PostgreSQL 整体不可用
- **WHEN** Consumer 因 PostgreSQL 连接故障无法处理消息
- **THEN** Consumer SHALL 停止继续领取并以进程级退避恢复，不得用无界高速 requeue 消耗投递次数

#### Scenario: 单消息处理反复失败
- **WHEN** 一条合法消息发生未分类处理错误并超过配置的 delivery limit
- **THEN** RabbitMQ SHALL 将其转入 DLQ，而不得无限循环或静默丢弃

#### Scenario: Consumer 实例异常退出
- **WHEN** Consumer 在 Ack 前断连或退出
- **THEN** RabbitMQ SHALL 把未确认消息重新交付给可用 Consumer

### Requirement: 消费去重与任务变化原子提交

每个逻辑 Consumer SHALL 以稳定、版本化的 `consumer_name` 和 `event_id` 作为消费唯一键，并在同一 PostgreSQL 事务提交消费记录与任务变化；`consumed_events` 本阶段 SHALL 不自动清理。

#### Scenario: 首次消费事件
- **WHEN** `async-task-projector.v1` 首次处理合法事件
- **THEN** 系统 SHALL 原子写入消费结果和对应任务变化，结果 SHALL 标记为 `applied` 或合法 `noop`

#### Scenario: 相同消息重复投递
- **WHEN** 同一逻辑 Consumer 再次收到已提交的 `event_id`
- **THEN** 系统 SHALL 不重复修改任务，并 SHALL 安全 Ack 该 delivery

#### Scenario: 事务提交前失败
- **WHEN** 消费记录插入或任务收敛任一步失败
- **THEN** 系统 SHALL 回滚二者且不 Ack 消息，使其可重新投递

#### Scenario: 提交后 Ack 前退出
- **WHEN** 消费事务已提交但 Consumer 在 Ack 前退出
- **THEN** 重投消息 SHALL 命中消费唯一键且不重复产生业务效果

#### Scenario: 新逻辑消费者处理相同事件
- **WHEN** 另一个稳定 `consumer_name` 收到相同 `event_id`
- **THEN** 系统 SHALL 允许该逻辑消费者独立处理一次

### Requirement: 文章增强任务按当前事实收敛

系统 SHALL 为每个 `(task_type, aggregate_type, aggregate_id)` 维护至多一个当前 `article.enrichment` 任务槽位，并保存稳定任务 ID、单调递增 generation、目标修订、内容哈希和已观察文章版本；本 change SHALL 只产生或取消任务，不执行 AI 工作。

#### Scenario: 公开文章首次进入任务系统
- **WHEN** Consumer 处理文章事件且事务内读取的当前文章状态为 `published`，同时不存在任务槽位
- **THEN** 系统 SHALL 创建绑定当前修订的 `pending` 任务并设置首个 generation

#### Scenario: 公开文章修订更新任务
- **WHEN** 当前公开修订不同于任务目标
- **THEN** 系统 SHALL 把同一任务槽位更新为最新修订、递增 generation、置为 `pending`，且不得留下需要执行的旧修订任务

#### Scenario: 重复事实不改变任务
- **WHEN** 任务已经绑定当前公开修订且已观察版本不旧于本次收敛结果
- **THEN** 系统 SHALL 记录合法 `noop`，不得递增 generation

#### Scenario: 文章当前不可见
- **WHEN** 任一合法文章事件触发收敛且事务内读取的文章当前状态为 `offline` 或 `deleted`
- **THEN** 系统 SHALL 取消已有的 `pending` 任务；不存在任务时 SHALL 不创建空的 canceled 记录

#### Scenario: 下架后重新发布
- **WHEN** 已取消任务对应的文章重新公开且当前修订仍需处理
- **THEN** 系统 SHALL 递增同一任务槽位的 generation，并将最新修订重新置为 `pending`

### Requirement: 重复和乱序事件不能回退任务状态

Consumer SHALL 在任务事务中读取文章当前状态和修订，并使用已观察聚合版本及任务 generation 防止旧事件或旧执行者覆盖更新事实；系统不得要求事件版本连续或依赖 RabbitMQ 全局顺序。

#### Scenario: 下架事件先于旧发布事件处理
- **WHEN** `article.offlined.v1` 已收敛后又收到较低聚合版本的发布或修订事件
- **THEN** 系统 SHALL 依据文章当前不可见事实保持任务取消或不存在

#### Scenario: 旧下架事件晚于重新发布事件处理
- **WHEN** 当前文章已经以更高 `lock_version` 重新公开，但 Consumer 收到较低版本的下架事件
- **THEN** 系统 SHALL 保持或创建针对当前修订的 `pending` 任务，不得回退为 canceled

#### Scenario: 事件版本存在合法间隔
- **WHEN** 草稿或离线编辑增加了文章 `lock_version` 但未产生集成事件
- **THEN** Consumer SHALL 接受后续更高版本事件并按当前事实收敛，不得因版本不连续进入 DLQ

#### Scenario: 旧 generation 尝试更新任务
- **WHEN** 后续任务执行者携带的 generation 已被新修订或状态变化替代
- **THEN** 任务存储边界 SHALL 拒绝该旧 generation 更新当前任务

### Requirement: Worker 组件故障相互隔离并可优雅停止

Feed 调度、清理、Relay 和 Consumer SHALL 在现有 `velis-worker` 中作为独立受监管组件运行；MQ 组件故障不得停止 RSS 抓取或其他本地后台能力。

#### Scenario: RabbitMQ 断连
- **WHEN** Relay 或 Consumer 与 RabbitMQ 的连接中断
- **THEN** 该组件 SHALL 独立退避重连，Feed 调度和清理 SHALL 继续运行

#### Scenario: Worker 收到关闭信号
- **WHEN** Worker 开始优雅关闭
- **THEN** Relay 和 Consumer SHALL 停止领取新工作、等待有界的在途数据库事务和 confirm，并在关闭期限内释放连接与租约

#### Scenario: 多 Worker 副本
- **WHEN** 多个 Worker 使用不同实例身份同时运行
- **THEN** Relay SHALL 通过租约协作，Consumer SHALL 通过队列竞争、消费去重和版本收敛保持正确结果

### Requirement: DLQ 支持有界且安全的人工重放

系统 SHALL 通过管理 CLI 提供显式数量上限的 DLQ 重放，不得默认无限清空或允许编辑消息后绕过契约校验。

#### Scenario: 成功重放 DLQ 消息
- **WHEN** 管理员请求重放至多指定数量的 DLQ 消息
- **THEN** CLI SHALL 保留原事件信封和 `event_id` 重新发布，并仅在 publisher confirm 成功后 Ack DLQ 原消息

#### Scenario: 重发失败
- **WHEN** 重放消息未路由或未获得 publisher confirm
- **THEN** CLI SHALL 不 Ack DLQ 原消息，并 SHALL 返回非零结果及脱敏错误摘要

#### Scenario: 重放已经成功消费的事件
- **WHEN** DLQ 中的事件此前已由相同逻辑 Consumer 提交
- **THEN** 重发后的消息 SHALL 命中消费去重并安全结束，不得重复修改任务

#### Scenario: 重放仍然非法的事件
- **WHEN** 重放消息仍不满足当前支持的事件契约
- **THEN** Consumer SHALL 再次将其隔离到 DLQ，而不得写入任务或消费成功记录

### Requirement: 异步链路具有可诊断且脱敏的运行状态

系统 SHALL 以 `trace_id`、`event_id`、`task_id` 和 Worker 实例身份关联结构化日志，并 SHALL 提供足以判断积压、投递、重试、DLQ、消费吞吐和幂等命中的运行指标；日志和错误字段不得包含 MQ 凭据或文章正文。

#### Scenario: Outbox 发生积压
- **WHEN** 存在长时间未投递事件
- **THEN** 运维者 SHALL 能观察待投递数量、最老事件年龄、发布尝试和最近失败分类

#### Scenario: Consumer 处理消息
- **WHEN** 消息被应用、判定为 noop、重复命中、重投或转入 DLQ
- **THEN** 系统 SHALL 记录对应分类指标并输出包含关联标识的脱敏日志

#### Scenario: 已发布 Outbox 清理
- **WHEN** 已确认发布事件超过配置的保留期
- **THEN** Worker SHALL 分批清理已发布记录，且 SHALL 永不清理未发布事件或本阶段的 `consumed_events`
