# Spec Delta

## MODIFIED Requirements

### Requirement: 文章增强任务按当前事实收敛

系统 SHALL 为每个 `(task_type, aggregate_type, aggregate_id)` 维护至多一个当前 `article.enrichment` 任务槽位，并保存稳定任务 ID、单调递增 generation、目标修订、内容哈希、已观察文章版本、当前阶段及可执行状态；投影 SHALL 只依据事务内文章当前事实推进或取消槽位，执行器 SHALL 在该槽位上完成 generation 与 Embedding 阶段。

#### Scenario: 公开文章首次进入任务系统
- **WHEN** Consumer 处理文章事件且事务内读取的当前文章状态为 `published`，同时不存在任务槽位
- **THEN** 系统 SHALL 创建绑定当前修订的 `pending` 任务并设置首个 generation

#### Scenario: 公开文章修订更新任务
- **WHEN** 当前公开修订不同于任务目标
- **THEN** 系统 SHALL 把同一任务槽位更新为最新修订、递增 generation、重置为 generation 阶段的 `pending`，清除旧租约与重试状态，且不得留下需要执行的旧修订任务

#### Scenario: 重复事实不改变任务
- **WHEN** 任务已经绑定当前公开修订且已观察版本不旧于本次收敛结果
- **THEN** 系统 SHALL 记录合法 `noop`，不得递增 generation 或重置已完成阶段

#### Scenario: 文章当前不可见
- **WHEN** 任一合法文章事件触发收敛且事务内读取的文章当前状态为 `offline` 或 `deleted`
- **THEN** 系统 SHALL 对已有槽位递增 generation、置为 `canceled` 并清除执行租约；不存在任务时 SHALL 不创建空的 canceled 记录

#### Scenario: 下架后重新发布
- **WHEN** 已取消任务对应的文章重新公开且当前修订仍需处理
- **THEN** 系统 SHALL 递增同一任务槽位的 generation，并将最新修订重新置为 `pending`；执行器 MAY 复用该修订已有且满足目标 profile 的不可变结果而不得重复调用模型

### Requirement: 重复和乱序事件不能回退任务状态

Consumer SHALL 在任务事务中读取文章当前状态和修订，并使用已观察聚合版本及任务 generation 防止旧事件覆盖更新事实；执行器 SHALL 同时使用 task generation 和每次认领唯一的租约 token 防止旧执行者或租约过期执行者提交结果；系统不得要求事件版本连续或依赖 RabbitMQ 全局顺序。

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
- **WHEN** 后续任务执行者携带的 generation 已被新修订、状态变化或显式 profile 升级替代
- **THEN** 任务存储边界 SHALL 拒绝该旧 generation 更新任务、结果选择或当前向量

#### Scenario: 同一 generation 的旧租约迟到
- **WHEN** 原执行者租约到期且任务已由新 token 重新认领，原执行者随后尝试提交
- **THEN** 任务存储边界 SHALL 拒绝旧 token 的任务与结果更新，并保持新认领者状态不变

### Requirement: Worker 组件故障相互隔离并可优雅停止

Feed 调度、清理、Relay、Consumer 和已配置的内容增强执行器 SHALL 在现有 `velis-worker` 中作为独立受监管组件运行；MQ 或模型组件故障不得停止 RSS 抓取、清理或其他本地后台能力，未配置模型时不得构造需要外部模型的组件。

#### Scenario: RabbitMQ 断连
- **WHEN** Relay 或 Consumer 与 RabbitMQ 的连接中断
- **THEN** 该组件 SHALL 独立退避重连，Feed 调度、清理和不依赖该连接的组件 SHALL 继续运行

#### Scenario: 模型端点故障
- **WHEN** 生成或 Embedding 端点持续超时、限流或不可达
- **THEN** 内容增强执行器 SHALL 按任务策略退避或终止对应阶段，Feed、Relay、Consumer 和核心文章读取 SHALL 继续运行

#### Scenario: 模型未配置
- **WHEN** 生成或 Embedding profile 未配置
- **THEN** Worker SHALL 跳过对应外部模型组件的装配，保留可恢复任务事实且不把该可选能力计入核心 readiness

#### Scenario: Worker 收到关闭信号
- **WHEN** Worker 开始优雅关闭
- **THEN** Relay、Consumer 和内容增强执行器 SHALL 停止领取新工作、等待有界的在途数据库事务、confirm 与模型调用，并在关闭期限内释放连接与租约

#### Scenario: 多 Worker 副本
- **WHEN** 多个 Worker 使用不同实例身份同时运行
- **THEN** Relay SHALL 通过租约协作，Consumer SHALL 通过队列竞争、消费去重和版本收敛保持正确结果，内容增强执行器 SHALL 通过任务租约与 fencing 防止重复提交

## ADDED Requirements

### Requirement: 内容增强任务具有可恢复的租约执行状态机

执行器 SHALL 仅认领到期的 `pending` 或 `retry_wait` 任务，将其置为带 owner、唯一 token 和到期时间的 `running`，并在事务中以 generation 与 token 条件推进阶段、安排有限重试、完成或终结任务；模型调用期间 SHALL 不持有数据库行锁或事务。

#### Scenario: 多执行器并发认领
- **WHEN** 多个 Worker 同时扫描可执行任务
- **THEN** 每个有效租约期内同一任务 generation SHALL 只有一个当前 token，其他执行器 SHALL 能继续认领不同任务

#### Scenario: 执行器在模型调用期间退出
- **WHEN** 任务处于 `running` 且执行器在提交结果前退出
- **THEN** 租约到期后另一执行器 SHALL 能重新认领同一 generation，并由结果幂等边界吸收可能的重复外部调用

#### Scenario: 暂时性阶段失败
- **WHEN** 当前 token 遇到可重试错误且仍有尝试预算
- **THEN** 系统 SHALL 条件更新为 `retry_wait`、保存下一次尝试时间和脱敏错误分类，并清除当前租约

#### Scenario: generation 阶段完成
- **WHEN** 当前 token 成功保存并选择结构化增强结果且目标还需要 Embedding
- **THEN** 系统 SHALL 在同一任务 generation 中推进到 Embedding 阶段并置为可执行状态，不得再次生成已满足目标的摘要

#### Scenario: 所需阶段全部完成
- **WHEN** 当前 generation 的所有已配置目标阶段均具有成功结果
- **THEN** 系统 SHALL 以 generation 与 token 条件将任务置为 `succeeded` 并清除租约与重试字段

#### Scenario: 永久失败或尝试耗尽
- **WHEN** 当前阶段发生永久错误或达到最大尝试次数
- **THEN** 系统 SHALL 以条件写将任务置为 `failed`、保留已成功阶段的结果和稳定错误分类，并停止自动领取直到目标被显式推进

### Requirement: 格式纠正额度跨重试持久且受 fencing 保护

系统 SHALL 为每个内容增强 task generation 持久化至多一次的最终输出格式纠正额度；额度 SHALL 只在当前租约、当前公开修订和当前目标 profile 仍匹配时以短事务原子预占，generation attempt 重试不得恢复额度，而新修订或显式 profile 推进形成的新 task generation SHALL 获得新的独立额度。

#### Scenario: 当前执行者首次预占纠正额度
- **WHEN** 当前 generation 的有效租约执行者遇到可纠正的最终非法输出且额度尚未使用
- **THEN** 系统 SHALL 原子标记额度已使用并在事务提交后才允许发起纠正模型调用

#### Scenario: 后续 attempt 再次遇到非法输出
- **WHEN** 同一 task generation 的后续 attempt 在纠正额度已使用后再次产生非法最终输出
- **THEN** 系统 SHALL 拒绝再次纠正并按普通有限重试或终态规则处理

#### Scenario: 预占后执行者退出
- **WHEN** 执行者在成功预占额度后、纠正调用中或调用审计写入前退出
- **THEN** 租约恢复后的执行者 SHALL 视额度为已使用，不得为保证成功而重复产生外部纠正调用

#### Scenario: 迟到租约尝试预占额度
- **WHEN** 租约已过期、token 已被替换、文章不再公开、当前修订已变化或目标 profile 已升级的执行者尝试预占
- **THEN** 存储边界 SHALL 拒绝预占且不得改变当前 task generation 的额度状态

#### Scenario: 新 task generation 重置额度
- **WHEN** 新修订、重新发布或显式 profile 推进递增任务 generation
- **THEN** 新 generation SHALL 获得一次独立纠正额度，旧 generation 的额度状态不得影响新目标
