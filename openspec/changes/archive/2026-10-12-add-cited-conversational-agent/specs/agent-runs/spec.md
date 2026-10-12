# Agent 执行回合

## Purpose

定义登录用户在本人会话中发起一次智能阅读回答的持久化执行契约，覆盖请求幂等、并发和消息额度、执行终态、显式取消及断线后恢复查询，使网络重试和执行者中断不会静默触发新的模型生成。

## ADDED Requirements

### Requirement: 回合接口只向本人开放

系统 SHALL 在认证与 Agent 会话开启时提供 `/api/v1/me/agent/conversations/{id}/runs` 的 POST 和 GET、`/{run_id}` 的 GET、`/{run_id}/cancel` 的 POST，以及独立规格定义的事件订阅。所有入口 SHALL 校验当前身份与会话所有权，管理员不得豁免；他人、删除或未知会话统一返回 `404 AGENT_CONVERSATION_NOT_FOUND`，本人会话内的未知回合返回 `404 AGENT_RUN_NOT_FOUND`。

#### Scenario: 他人读取或取消
- **WHEN** 用户或管理员读取、订阅或取消另一账户的回合
- **THEN** 系统 SHALL 返回会话不存在，不泄露回合状态、输入、回答、引用或用量

#### Scenario: 无效登录先于资源校验
- **WHEN** 无效身份请求任意回合入口
- **THEN** 系统 SHALL 返回 `401 AUTH_SESSION_INVALID`，不查询或暴露资源归属

### Requirement: 回合关联既有用户消息且输入严格

创建回合 SHALL 只接受必需 UUID `message_id`、可选正整数 `context_article_id`、可选 `constraints` 和可选 UUID `retry_of`。constraints 仅允许 `keyword,topic,source_id`，沿用搜索工具格式。消息 SHALL 属于本人该会话、角色为 user 且是当前最新消息；创建 SHALL 不重复追加用户消息。未知或重复字段、null、非法编码和类型 SHALL 返回 `400 VALIDATION_FAILED`。

#### Scenario: 保存后生成
- **WHEN** 用户先追加消息，再为该消息创建合法回合
- **THEN** 系统 SHALL 关联原消息并返回 202 与回合位置，不改变原消息正文、序号或 ID

#### Scenario: 旧消息或助手消息
- **WHEN** message_id 属于其它会话、角色为 assistant，或该消息之后已有其它消息
- **THEN** 系统 SHALL 分别按不可访问消息或 `409 AGENT_RUN_INPUT_CONFLICT` 拒绝，不开始工具或模型调用

### Requirement: 接受幂等与生成终态分开

创建回合 SHALL 要求 UUID `Idempotency-Key`，按用户与创建操作作用域提供固定 24 小时接受幂等。首次接受结果、回合和助手额度预留 SHALL 原子提交；同键同规范化命令重放原 202 接受快照，同键异命令返回 `409 IDEMPOTENCY_KEY_REUSED`。生成失败或取消 SHALL 不将已成功接受的键变为可重执行；当前状态只能通过回合读取获得。

#### Scenario: 接受响应丢失
- **WHEN** 接受事务成功但客户端未收到响应，随后以同键同输入重试
- **THEN** 系统 SHALL 返回原回合，模型执行次数不因 HTTP 重试增加，即使原回合已经进入终态

#### Scenario: 接受事务回滚
- **WHEN** 回合或额度预留写入失败
- **THEN** 系统 SHALL 回滚接受、幂等和预留，后续同键可重新尝试接受

### Requirement: 同一消息的再次执行必须明确授权

一个用户消息 SHALL 最多有一个成功回合。已有任何回合后，新键创建 SHALL 必须通过 retry_of 指向该消息最新的 failed、canceled 或 timed_out 回合，且上下文和约束保持原值；否则返回 `409 AGENT_RUN_INPUT_CONFLICT`。每条消息最多接受 3 个回合，超过返回 `409 AGENT_RUN_ATTEMPT_LIMIT_EXCEEDED`。幂等记录到期 SHALL 不绕过这些事实约束。

#### Scenario: 明确重试失败
- **WHEN** 最新回合失败，用户以新键和正确 retry_of 显式重试，且消息仍为最新
- **THEN** 系统 SHALL 创建不同回合 ID，保留原失败状态，不新增用户消息

#### Scenario: 到期或换键绕过
- **WHEN** 原回合仍活动、已经成功、已达 3 次接受，或请求没有正确 retry_of
- **THEN** 系统 SHALL 拒绝重复生成，不因 24 小时幂等窗口结束而自动收费执行

### Requirement: 活动回合和助手额度有界

系统 SHALL 同时限制每会话最多一个、每账户最多两个 accepted/running 回合。接受 SHALL 在现存消息数外原子预留一个助手消息名额；没有名额返回 `409 AGENT_MESSAGE_LIMIT_EXCEEDED`，活动冲突返回 `409 AGENT_RUN_IN_PROGRESS`。预留 SHALL 不出现在消息历史或 message_count 中；成功转为实际消息，其他终态释放，不得突破账户并发或会话消息配置上限。

#### Scenario: 最后名额与并发接受
- **WHEN** 多个请求竞争同一会话或账户的最后活动名额、助手名额
- **THEN** 系统 SHALL 仅接受允许的请求，不重复预留，不先调用模型再发现无法保存助手消息

#### Scenario: 失败释放
- **WHEN** 已接受回合失败、取消或超时
- **THEN** 系统 SHALL 释放预留并保留原用户消息，允许符合显式重试规则的新回合

### Requirement: 回合具有唯一且可收敛的终态

回合 SHALL 使用 accepted、running、succeeded、failed、canceled、timed_out 六种状态。accepted 可进入 running 或非成功终态；running 可进入任意终态；终态 SHALL 不再变化。创建时间起的总 deadline SHALL 默认 60 秒且覆盖等待执行；超过期限进入 timed_out。任一回合 SHALL 至多由一个有效执行者提交结果，失效执行者不得写入迟到片段或助手消息。

#### Scenario: 取消与成功竞争
- **WHEN** 显式取消和完整回答提交并发发生
- **THEN** 先原子提交的终态 SHALL 生效；成功先提交时取消返回现有 succeeded，取消先提交时后续成功写入被拒绝

#### Scenario: 接受后无人执行
- **WHEN** 回合在 accepted 状态达到总 deadline
- **THEN** 系统 SHALL 收敛为 timed_out 并释放额度，不无限停留或重新计算期限

### Requirement: 成功提交原子保存完整回答

只有完整回答和所有片段通过证据及输出校验后，系统 SHALL 在一次原子提交中追加唯一不可变 assistant 消息、保存结构化回答、消费预留、写入 succeeded 和结束事件。失败或取消时 SHALL 不创建成功助手消息；已输出片段只能作为有期限的未完成回合结果，不进入后续成功历史上下文。

#### Scenario: 最后提交失败
- **WHEN** 保存助手消息、回答、状态或结束事件任一步失败
- **THEN** 系统 SHALL 不留下半成功消息或重复成功记录，客户端 SHALL 查询原回合确认最终事实

#### Scenario: 已显示片段后取消
- **WHEN** 回合已有校验片段但尚未完成，用户取消
- **THEN** 系统 SHALL 返回 canceled 与 partial 标记，不把片段伪装成完整助手回答

### Requirement: 断线不改变执行意图

SSE 断线、页面关闭或订阅请求主动中止 SHALL 只结束订阅，不取消已接受回合。本人显式 POST cancel SHALL 幂等返回 200 和当前回合摘要，对 accepted/running 原子提交 canceled 并请求终止依赖调用；对终态返回原终态。停止 SHALL 阻止后续业务动作和结果提交，不承诺撤销已发送的响应或外部服务已发生的费用。

#### Scenario: 断线后重连
- **WHEN** 用户在模型生成时断线并重新登录同一账户
- **THEN** 用户 SHALL 可查询和订阅原回合，不因重新订阅再次触发模型

#### Scenario: 跨设备停止
- **WHEN** 用户在另一设备或另一 API 实例取消本人活动回合
- **THEN** 系统 SHALL 以共享终态阻止迟到提交，并在有界检测周期内取消执行者的依赖上下文

### Requirement: 执行中持续检查授权与执行者存活

生成 SHALL 绑定接受时的真实账户及登录会话，不保存访问令牌。账户禁用、该登录会话撤销、会话删除、生成服务关闭或执行者失去有效执行权 SHALL 阻止后续动作。执行者丢失的 running 回合 SHALL 收敛为 failed，错误为 `AGENT_EXECUTOR_LOST`，不自动重新调用模型。访问令牌自然过期 SHALL 不单独取消账户和登录会话仍有效的回合。

#### Scenario: 进程崩溃后恢复
- **WHEN** API 重启或执行实例失联，持久化回合仍为 running
- **THEN** 系统 SHALL 在有效执行权过期后最多 5 秒内收敛失败并释放预留；客户端可显式重试

#### Scenario: 注销或删除期间调用
- **WHEN** 用户注销原登录会话或删除会话，而模型请求尚未返回
- **THEN** 系统 SHALL 停止新工具和模型动作并拒绝结果提交；重新登录只能读取未删除的既有事实

### Requirement: 回合可分页恢复查询且读取不生成

列表 SHALL 按 `(created_at,id)` 倒序提供默认 20、范围 1–50 的分页与 `items,next_cursor,has_more`；认证加密游标绑定用户、会话及回合列表用途，继承会话游标绝对有效期规则。详情 SHALL 返回状态、关联消息、当前可见回答、partial、用量及安全错误，不返回模型原文、内部 Prompt 或工具输入。任何列表、详情或事件读取 SHALL 不启动或重启生成。

#### Scenario: 刷新页面恢复
- **WHEN** 用户丢失本地回合 ID 后读取本人会话回合列表
- **THEN** 系统 SHALL 返回活动及历史回合摘要，可据此恢复查询和事件订阅

#### Scenario: 跨用途游标
- **WHEN** 列表使用其它账户、会话或消息历史用途的游标
- **THEN** 系统 SHALL 返回 `400 INVALID_CURSOR`，不静默重新首查

### Requirement: 回合错误与核心会话故障边界独立

生成默认 SHALL 关闭；关闭或未配置模型时新接受返回 `503 AGENT_GENERATION_UNAVAILABLE`，已接受键重放及回合读取继续可用。请求校验使用安全错误信封；接受后模型、工具、引用、预算和执行者故障 SHALL 保存为稳定终态分类。生成和维护 SHALL 不增加模型、搜索、Redis 或 MQ 为会话核心 readiness 依赖。

#### Scenario: 模型服务不可用
- **WHEN** 生成故障而 PostgreSQL 正常
- **THEN** 会话创建、历史、改名、删除及回合查询 SHALL 可用，故障不得伪装成空回答

#### Scenario: PostgreSQL 不可用
- **WHEN** 无法完成接受、归属读取或结果复核
- **THEN** 未开始流的请求 SHALL 返回 `503 DEPENDENCY_UNAVAILABLE`，不得从内存或缓存返回未经授权的私密结果

### Requirement: 删除和保留覆盖回合关联内容

成功回合与引用 SHALL 随会话保留；未完成片段及事件 SHALL 在终态 24 小时后清除，保留无正文的失败摘要和关联信息。删除会话 SHALL 原子清除所有回合、回答、证据、事件及相关幂等私密快照，并保留原三字段最小会话删除标记；旧请求重放不得返回回合或内容。清理 SHALL 不删除成功消息或延长幂等窗口。

#### Scenario: 事件保留期结束
- **WHEN** 终态超过 24 小时后执行有界清理
- **THEN** 成功助手历史和结构化引用 SHALL 保留，失败片段与事件正文 SHALL 消失，回合摘要仍可查询

#### Scenario: 删除后迟到回调
- **WHEN** 会话删除事务提交后执行者尝试持久化事件或最终回答
- **THEN** 写入 SHALL 被拒绝，不恢复会话、不留下孤儿记录、不泄露原幂等结果
