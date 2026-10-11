# 会话与生成回合的兼容调整

## MODIFIED Requirements

### Requirement: 会话接口覆盖五项基本操作

系统 SHALL 在 `/api/v1/me/agent/conversations` 提供创建空会话和分页列表，在 `/{id}` 提供详情、改名及删除，在 `/{id}/messages` 提供历史与用户消息追加。创建和追加成功 SHALL 返回 201，列表、详情、历史和改名成功 SHALL 返回 200，删除成功 SHALL 返回 204。存在活动生成回合时，新用户消息追加 SHALL 返回 `409 AGENT_RUN_IN_PROGRESS`；所有权及原成功幂等重放优先于此检查。消息追加仍不得隐式发起模型生成。

#### Scenario: 创建后独立读取详情和消息
- **WHEN** 用户以有效幂等键 POST 创建会话后 GET 详情及消息历史
- **THEN** 系统 SHALL 返回空会话元数据及独立空消息页，详情不得隐式包含消息正文

#### Scenario: 当前阶段追加消息
- **WHEN** 本人会话没有活动回合，用户 POST 合法正文到消息入口
- **THEN** 系统 SHALL 只持久化并返回该用户消息，不调用模型、不产生自动助手回复

#### Scenario: 活动回合期间的新追加
- **WHEN** 本人会话存在 accepted 或 running 回合，用户以新幂等键追加消息
- **THEN** 系统 SHALL 拒绝新追加且不占用键或消息额度，用户可先停止或等待回合终态

#### Scenario: 活动回合期间重放旧消息
- **WHEN** 该会话消息追加的原成功请求在有效窗口内原样重试
- **THEN** 系统 SHALL 重放原消息，不因活动回合拒绝，不触发生成或消耗预留

### Requirement: 输入与输出字段具有明确格式

创建 SHALL 只接受可选 `title`，PATCH SHALL 只接受必需 `title`，消息 POST SHALL 只接受必需 `content`。成功单资源响应 SHALL 直接返回对象；会话字段为 `id,title,title_version,message_count,created_at,last_activity_at`，消息字段为 `id,conversation_id,sequence,role,content,created_at`。由受控生成产生的助手历史 SHALL 额外包含 UUID `run_id` 和布尔 `content_redacted`，content 为当前可见性投影；用户消息字段不变。ID SHALL 为服务端 UUID，时间 SHALL 为 UTC RFC3339，版本、计数与序号 SHALL 为整数。

#### Scenario: 客户端伪造字段
- **WHEN** 请求包含角色、用户 ID、资源 ID、序号、时间、未知字段、重复 JSON 键、错误类型或不允许的 null
- **THEN** 系统 SHALL 返回 `400 VALIDATION_FAILED`，不忽略字段、不默认为其它含义且不写入事实

#### Scenario: 成功响应最小化
- **WHEN** 用户获取会话列表、详情或消息
- **THEN** 系统 SHALL 使用规定字段，不新增 `data` 包装；会话列表不得包含最后消息片段或消息正文

#### Scenario: 恢复结构化助手回答
- **WHEN** 用户读取受控生成的 assistant 历史消息
- **THEN** 系统 SHALL 返回对应 run_id 和投影标记，客户端可读取回合详情获得当前可见片段、引用和卡片，不从自由文本解析来源

### Requirement: 消息角色及写入权限由服务端决定

持久化消息 SHALL 支持 `user` 和 `assistant` 两种角色。当前公共追加入口 SHALL 由服务端固定角色为 `user`；`assistant` 仅允许受控服务端编排在完整回答成功提交时写入。系统 Prompt、隐藏推理及原始工具调用 SHALL 不作为这两类会话消息保存。

#### Scenario: 伪造助手消息
- **WHEN** 客户端提供 `role=assistant` 或其它服务端字段
- **THEN** 系统 SHALL 拒绝请求，不能通过客户端参数创建助手消息

#### Scenario: 工具执行与消息隔离
- **WHEN** 内部只读工具完成一次调用
- **THEN** 系统 SHALL 不因此追加用户、助手或工具角色消息

#### Scenario: 生成成功或中途失败
- **WHEN** 回合完整校验成功，或生成中途失败、取消、超时
- **THEN** 前者 SHALL 原子追加一条 assistant 消息，后者 SHALL 不追加成功助手消息；逐次工具调用和隐藏推理仍不进入会话消息

### Requirement: 消息排序使用服务端单调序号

每个会话的消息 SHALL 具有服务端分配、唯一且单调递增的正整数 `sequence`，允许间隙。历史 SHALL 按序号排序而非客户端时间。没有活动生成回合时，不同键的并发追加在配额允许时 SHALL 都可成功；活动回合期间新追加遵循回合冲突规则。成功助手消息 SHALL 取得当时的下一序号，额度预留不提前插入消息。幂等重放 SHALL 使用原 ID 和序号。

#### Scenario: 并发追加
- **WHEN** 没有活动回合时，多个不同键同时向本人会话追加合法消息且剩余额度充足
- **THEN** 系统 SHALL 按服务端串行提交次序产生唯一序号，不覆盖消息或生成重复序号

#### Scenario: 时间相同
- **WHEN** 多条消息具有相同创建时间
- **THEN** 系统 SHALL 仍以 sequence 形成确定顺序

#### Scenario: 回合结束后的助手序号
- **WHEN** 回合成功提交唯一助手消息
- **THEN** 系统 SHALL 在原用户消息后分配服务端序号；失败预留不产生消息，序号和已存历史不被回写

### Requirement: 配额有默认上限且拒绝超限新写入

服务端 SHALL 默认限制每用户 50 个现存会话、每会话 200 条消息和单条用户正文 4000 个 Unicode code point，并允许有限范围配置。消息限额 SHALL 同时计入 user、assistant 及活动回合预留的助手名额，message_count 仍仅表示已保存消息数。接受回合 SHALL 预留一个名额，成功转为实际助手消息，失败、取消、超时或删除释放；并发和降低配置不得突破当前上限。超限 SHALL 拒绝新写入而不自动删除历史；并发不得突破配额，删除会话 SHALL 释放会话额度。

#### Scenario: 边界与并发
- **WHEN** 多个请求竞争最后一个会话或消息名额
- **THEN** 系统 SHALL 只提交剩余名额允许的请求，其余分别返回 `409 AGENT_CONVERSATION_LIMIT_EXCEEDED` 或 `409 AGENT_MESSAGE_LIMIT_EXCEEDED`

#### Scenario: 正文超长
- **WHEN** 规范化正文长度超过配置上限
- **THEN** 系统 SHALL 返回 `400 VALIDATION_FAILED` 并保留原历史，不截断用户内容

#### Scenario: 预留及降低配额
- **WHEN** 活动回合已有合法助手预留，而管理员降低消息上限
- **THEN** 新写入 SHALL 按当前配额拒绝；成功提交前重新检查上限，不能凭旧预留突破新上限，也不能自动删除原历史

### Requirement: 会话及关联内容被原子物理删除

本人会话 DELETE SHALL 在单事务中物理删除会话、全部消息、回合、回答、证据和事件，释放配额及助手预留，清除关联幂等快照的标题、正文和回合私密载荷，并生成原三字段最小删除标记。删除 SHALL 同时使所有执行权失效，执行者不能写回迟到结果。列表 SHALL 不再出现会话；后续详情、历史、追加、改名及旧成功重放 SHALL 返回 `404 AGENT_CONVERSATION_NOT_FOUND`，不得恢复资源。

#### Scenario: 三类旧成功结果
- **WHEN** 删除后分别重试该会话的创建、消息追加和改名成功键
- **THEN** 系统 SHALL 不返回旧标题或正文；24 小时窗口内 SHALL 不重新创建会话，原去重截止时间不改变

#### Scenario: 删除与追加或改名竞争
- **WHEN** 同一会话的删除与写入并发
- **THEN** 系统 SHALL 形成事务顺序；删除前提交的内容一并删除，删除后操作失败，不产生孤儿消息或会话复活

#### Scenario: 删除失败回滚
- **WHEN** 删除中任一事实或幂等清理步骤失败
- **THEN** 系统 SHALL 回滚全部删除、计数与标记变化，不能只删除部分内容

#### Scenario: 活动生成期间删除
- **WHEN** 本人删除仍有活动回合的会话
- **THEN** 系统 SHALL 原子清除回合和全部关联内容，后续订阅、查询、取消及接受重放返回会话不存在；模型迟到结果不得恢复消息或事件

### Requirement: 消息历史最近首查并向前加载

消息历史首查 SHALL 选最近消息，默认 20 条、`limit` 为 1 至 50，单批始终按 sequence 升序返回。`next_cursor` SHALL 指向当前批最早序号之前的历史，供未来界面向上连续加载。新追加 SHALL 不改变既有向前边界；历史未删除时 SHALL 不重复或跳过已有消息。生成助手历史正文 SHALL 按当前证据可见性投影，失效片段使用安全替代内容并设置 content_redacted；数据库中的不可变原文、ID、序号和时间不变。

#### Scenario: 多批历史
- **WHEN** 会话有 65 条消息，用户以默认批次读取首批及下一批
- **THEN** 系统 SHALL 依次返回序号 46–65 和 26–45，均批内升序；客户端可把第二批放在首批之前

#### Scenario: 加载期间追加
- **WHEN** 首批返回后又追加消息，用户继续使用历史游标
- **THEN** 系统 SHALL 只读取原边界之前消息，新消息不混入旧历史页；新首查 SHALL 包含最新消息

#### Scenario: 空历史和最早批次
- **WHEN** 会话为空，或已经加载到最早一批
- **THEN** 系统 SHALL 返回 `next_cursor:null,has_more:false`；空历史 items SHALL 为 `[]`

#### Scenario: 引用失效不改写消息
- **WHEN** 历史助手回答引用的文章下架或修订变化
- **THEN** 系统 SHALL 隐藏相关片段及失效来源，不改变分页边界和消息事实；事实复核失败时返回依赖错误，不直接返回旧正文
