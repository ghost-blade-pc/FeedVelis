# 私有 Agent 会话与消息规格

## Purpose

定义属于用户账户的私有 Agent 会话与不可变消息基础，明确身份隔离、写入幂等、并发排序、资源上限、主动删除和游标历史读取，使后续对话编排及连续滚动界面获得一致且可验证的数据契约。

## ADDED Requirements

### Requirement: 会话只属于当前登录用户账户

系统 SHALL 将会话归属于用户账户而非登录会话，只允许当前有效登录用户操作本人会话；管理员身份不得绕过所有权。Agent 默认关闭，只有 `agent.enabled=true` 且 `auth.enabled=true` 时 SHALL 注册会话路由。

#### Scenario: 跨设备及重新登录
- **WHEN** 用户在另一设备登录，或注销后重新登录同一账户
- **THEN** 系统 SHALL 保留并允许读取本人既有会话，不因登录会话轮换、撤销或服务重启删除历史

#### Scenario: 他人和管理员访问
- **WHEN** 用户或管理员以合法格式访问另一账户的会话详情、消息、改名或删除接口
- **THEN** 系统 SHALL 返回 `404 AGENT_CONVERSATION_NOT_FOUND`，不泄露标题、正文、版本或存在状态

#### Scenario: 无效身份或功能关闭
- **WHEN** 已注册路由收到无效或失效登录身份，或认证/Agent 功能关闭
- **THEN** 前者 SHALL 返回 `401 AUTH_SESSION_INVALID`；后者 SHALL 不开放会话路由，不降级为匿名会话

### Requirement: 会话接口覆盖五项基本操作

系统 SHALL 在 `/api/v1/me/agent/conversations` 提供创建空会话和分页列表，在 `/{id}` 提供详情、改名及删除，在 `/{id}/messages` 提供历史与用户消息追加。创建和追加成功 SHALL 返回 201，列表、详情、历史和改名成功 SHALL 返回 200，删除成功 SHALL 返回 204。

#### Scenario: 创建后独立读取详情和消息
- **WHEN** 用户以有效幂等键 POST 创建会话后 GET 详情及消息历史
- **THEN** 系统 SHALL 返回空会话元数据及独立空消息页，详情不得隐式包含消息正文

#### Scenario: 当前阶段追加消息
- **WHEN** 用户 POST 合法正文到本人会话消息入口
- **THEN** 系统 SHALL 只持久化并返回该用户消息，不调用模型、不产生自动助手回复

### Requirement: 输入与输出字段具有明确格式

创建 SHALL 只接受可选 `title`，PATCH SHALL 只接受必需 `title`，消息 POST SHALL 只接受必需 `content`。成功单资源响应 SHALL 直接返回对象；会话字段为 `id,title,title_version,message_count,created_at,last_activity_at`，消息字段为 `id,conversation_id,sequence,role,content,created_at`。ID SHALL 为服务端 UUID，时间 SHALL 为 UTC RFC3339，版本、计数与序号 SHALL 为整数。

#### Scenario: 客户端伪造字段
- **WHEN** 请求包含角色、用户 ID、资源 ID、序号、时间、未知字段、重复 JSON 键、错误类型或不允许的 null
- **THEN** 系统 SHALL 返回 `400 VALIDATION_FAILED`，不忽略字段、不默认为其它含义且不写入事实

#### Scenario: 成功响应最小化
- **WHEN** 用户获取会话列表、详情或消息
- **THEN** 系统 SHALL 使用规定字段，不新增 `data` 包装；会话列表不得包含最后消息片段或消息正文

### Requirement: 消息角色及写入权限由服务端决定

持久化消息 SHALL 支持 `user` 和 `assistant` 两种角色。当前公共追加入口 SHALL 由服务端固定角色为 `user`；`assistant` 仅允许未来受控服务端编排写入。系统 Prompt、隐藏推理及原始工具调用 SHALL 不作为这两类会话消息保存。

#### Scenario: 伪造助手消息
- **WHEN** 客户端提供 `role=assistant` 或其它服务端字段
- **THEN** 系统 SHALL 拒绝请求，不能通过客户端参数创建助手消息

#### Scenario: 工具执行与消息隔离
- **WHEN** 内部只读工具完成一次调用
- **THEN** 系统 SHALL 不因此追加用户、助手或工具角色消息

### Requirement: 已保存消息不可变

系统 SHALL 不提供单条消息修改或删除能力。用户更正内容 SHALL 追加为新消息；删除历史 SHALL 通过删除整个会话完成。

#### Scenario: 改写一条消息
- **WHEN** 用户希望更正已有消息并提交新的合法正文
- **THEN** 系统 SHALL 追加新消息，保留原消息 ID、正文、角色、序号及创建时间

### Requirement: 正文严格规范化且保留格式

消息正文 SHALL 为有效 UTF-8 文本，拒绝 NUL、空字符串和全空白内容。服务端 SHALL 仅将 CRLF 和 CR 统一为 LF，保留其余首尾空白、缩进、换行与 Markdown 字符；长度和幂等摘要 SHALL 基于该规范化正文，长度按 Unicode code point 计数。服务端 SHALL 不把正文渲染成 HTML。

#### Scenario: 换行及缩进
- **WHEN** 用户提交带首尾空格、代码缩进和 CRLF 的非空正文
- **THEN** 保存正文 SHALL 仅改变换行格式；同一幂等键提交对应 LF 文本 SHALL 重放原消息

#### Scenario: 非法编码及字符边界
- **WHEN** 正文包含非法 UTF-8、未配对 Unicode 转义、NUL、全 Unicode 空白或超过配置的字符上限
- **THEN** 系统 SHALL 返回 `400 VALIDATION_FAILED`，不得先替换非法字符或截断正文后成功保存

#### Scenario: 组合字符
- **WHEN** 正文包含 emoji 或多个 code point 组成的视觉字符
- **THEN** 系统 SHALL 按 code point 计数，所有保留空格与换行也计入长度

### Requirement: 标题使用手动且有界的纯文本规则

创建省略标题 SHALL 使用“新对话”；显式标题 SHALL 去除首尾空白，随后为 1 至 100 个 Unicode code point 的有效 UTF-8 单行文本，不得包含换行或控制字符。空值、空串及全空白 SHALL 拒绝；相同标题 SHALL 允许。系统 SHALL 不根据消息自动命名。

#### Scenario: 默认标题与重复标题
- **WHEN** 用户以 `{}` 创建会话，或为不同会话提供相同合法标题
- **THEN** 前者 SHALL 使用“新对话”，后者 SHALL 全部成功并用不同 UUID 区分

#### Scenario: 标题格式错误
- **WHEN** 标题为 null、全空白、含换行或控制字符、非法编码或规范化后超过 100 字符
- **THEN** 系统 SHALL 返回 `400 VALIDATION_FAILED`，不改成默认标题、不静默截断

### Requirement: 标题版本控制只覆盖实际改名

会话 `title_version` SHALL 初始为 1。PATCH SHALL 要求 `Idempotency-Key` 和表示预期标题版本的强 `If-Match`；成功实际改名 SHALL 递增版本。相同规范化标题的有效版本请求 SHALL 成功而不改变版本或活跃时间；消息追加 SHALL 不改变标题版本。

#### Scenario: 并发改名
- **WHEN** 两个不同幂等键携带相同当前版本修改为不同标题
- **THEN** 系统 SHALL 至多让一个成功，另一个返回 `409 AGENT_TITLE_VERSION_CONFLICT`

#### Scenario: 重放旧改名
- **WHEN** 已成功改名的请求原样重试，但当前标题已被后续请求修改
- **THEN** 系统 SHALL 在所有权及删除检查后先重放原成功结果，不用当前版本拒绝原请求；读取详情 SHALL 返回最新事实

#### Scenario: 无变化请求仍需版本有效
- **WHEN** 用户提交与当前标题相同的规范化标题但预期版本已经过时
- **THEN** 新操作 SHALL 返回标题版本冲突，不能通过无变化内容绕过版本条件

### Requirement: 写命令使用二十四小时幂等窗口

创建、追加和改名 SHALL 要求 UUID 格式的 `Idempotency-Key`，作用域为当前用户、操作类型及键。业务变更和成功结果 SHALL 同事务提交，成功记录 SHALL 在首次操作的 24 小时窗口内去重，重试不延长期限；到期后键 SHALL 可作为新操作。失败回滚 SHALL 不占用键。

#### Scenario: 相同语义请求重试
- **WHEN** 同一用户在窗口内以同键同规范化内容重试成功操作
- **THEN** 系统 SHALL 返回首次成功状态、资源 ID 及结果，不重复创建、追加、改名或消耗配额

#### Scenario: 同键内容不同
- **WHEN** 同一作用域键在窗口内被用于不同规范化命令
- **THEN** 系统 SHALL 返回 `409 IDEMPOTENCY_KEY_REUSED`；追加摘要 SHALL 包含会话 ID 与正文，改名摘要 SHALL 包含会话 ID、预期版本与标题

#### Scenario: 失败与到期
- **WHEN** 首次事务失败回滚，或成功记录已超过 24 小时
- **THEN** 后续请求 SHALL 可以重新执行业务规则；系统不承诺窗口外请求仍重放原资源

#### Scenario: 并发同键
- **WHEN** 同键同命令同时执行
- **THEN** 系统 SHALL 最多提交一次业务事实；另一请求 SHALL 重放已提交结果，或以 `409 IDEMPOTENCY_IN_PROGRESS` 明确表示尚在执行

### Requirement: 成功重放不改变当前业务状态

有效成功重放 SHALL 保留第一次响应，即使标题或计数已变化或当前配额已满，也不得再次检查配额以拒绝重放。重放 SHALL 不推进序号、标题版本、活跃时间或计数；资源已删除 SHALL 优先拒绝，不泄露原成功载荷。

#### Scenario: 满配额下重放
- **WHEN** 原资源仍存在且用户或会话随后达到配置上限
- **THEN** 系统 SHALL 重放原成功结果并维持当前计数；新键的新操作 SHALL 按当前配额处理

#### Scenario: 历史响应和最新事实
- **WHEN** 创建或改名结果的旧快照与当前元数据不同
- **THEN** 重放 SHALL 返回原快照，详情 GET SHALL 返回当前元数据，两者均不得隐式触发更新

### Requirement: 消息排序使用服务端单调序号

每个会话的消息 SHALL 具有服务端分配、唯一且单调递增的正整数 `sequence`，允许间隙。历史 SHALL 按序号排序而非客户端时间。不同键的并发追加在配额允许时 SHALL 都可成功；幂等重放 SHALL 使用原 ID 和序号。

#### Scenario: 并发追加
- **WHEN** 多个不同键同时向本人会话追加合法消息且剩余额度充足
- **THEN** 系统 SHALL 按服务端串行提交次序产生唯一序号，不覆盖消息或生成重复序号

#### Scenario: 时间相同
- **WHEN** 多条消息具有相同创建时间
- **THEN** 系统 SHALL 仍以 sequence 形成确定顺序

### Requirement: 配额有默认上限且拒绝超限新写入

服务端 SHALL 默认限制每用户 50 个现存会话、每会话 200 条消息和单条用户正文 4000 个 Unicode code point，并允许有限范围配置。消息限额 SHALL 同时计入 user 与 assistant。超限 SHALL 拒绝新写入而不自动删除历史；并发不得突破配额，删除会话 SHALL 释放会话额度。

#### Scenario: 边界与并发
- **WHEN** 多个请求竞争最后一个会话或消息名额
- **THEN** 系统 SHALL 只提交剩余名额允许的请求，其余分别返回 `409 AGENT_CONVERSATION_LIMIT_EXCEEDED` 或 `409 AGENT_MESSAGE_LIMIT_EXCEEDED`

#### Scenario: 正文超长
- **WHEN** 规范化正文长度超过配置上限
- **THEN** 系统 SHALL 返回 `400 VALIDATION_FAILED` 并保留原历史，不截断用户内容

### Requirement: 会话及关联内容被原子物理删除

本人会话 DELETE SHALL 在单事务中物理删除会话及全部消息、释放配额、清除关联幂等快照的标题与正文，并生成最小删除标记。列表 SHALL 不再出现会话；后续详情、历史、追加、改名及旧成功重放 SHALL 返回 `404 AGENT_CONVERSATION_NOT_FOUND`，不得恢复资源。

#### Scenario: 三类旧成功结果
- **WHEN** 删除后分别重试该会话的创建、消息追加和改名成功键
- **THEN** 系统 SHALL 不返回旧标题或正文；24 小时窗口内 SHALL 不重新创建会话，原去重截止时间不改变

#### Scenario: 删除与追加或改名竞争
- **WHEN** 同一会话的删除与写入并发
- **THEN** 系统 SHALL 形成事务顺序；删除前提交的内容一并删除，删除后操作失败，不产生孤儿消息或会话复活

#### Scenario: 删除失败回滚
- **WHEN** 删除中任一事实或幂等清理步骤失败
- **THEN** 系统 SHALL 回滚全部删除、计数与标记变化，不能只删除部分内容

### Requirement: 最小删除标记只支持本人重复删除

删除标记 SHALL 仅含会话 UUID、所属用户 UUID 和删除时间，不含标题、正文或其它会话快照，随用户账户保留且不自动过期。标记 SHALL 不参与列表、消息、配额或历史。本人重复 DELETE SHALL 返回 204；从未存在或不属于本人的 ID SHALL 返回 404。

#### Scenario: 幂等窗口之外重复删除
- **WHEN** 用户删除会话超过 24 小时后再次 DELETE 同一 ID
- **THEN** 系统 SHALL 仍返回 204；其它用户及管理员 SHALL 返回 `404 AGENT_CONVERSATION_NOT_FOUND`

#### Scenario: 幂等载荷清理
- **WHEN** 成功幂等结果因删除转为终态去重记录
- **THEN** 系统 SHALL 只保留维持原窗口所需的身份、摘要、资源关联及截止信息，不保留原标题或消息正文

### Requirement: 历史由用户主动删除且不自动过期

系统 SHALL 保留现存会话与消息直到用户主动删除，不因闲置、重启、注销或自动任务清理历史。幂等结果与游标过期 SHALL 不代表历史过期；持久化历史 SHALL 不等于未来模型上下文全部输入。

#### Scenario: 长期闲置
- **WHEN** 用户超过幂等和游标期限后重新访问
- **THEN** 会话与消息 SHALL 仍存在，可从首查重新读取；系统不得因过期分页或去重元数据丢失消息

### Requirement: 会话列表按活跃时间实时分页

会话列表 SHALL 按 `(last_activity_at,id)` 倒序，创建、新消息提交和实际改名 SHALL 更新活跃时间；读取、重放和无变化改名 SHALL 不更新。首查默认 20 条，`limit` SHALL 为 1 至 50；响应 SHALL 为 `items,next_cursor,has_more`，不提供跨页冻结快照。

#### Scenario: 翻页期间会话活跃
- **WHEN** 未返回的会话因新消息移动到当前游标之前
- **THEN** 系统 SHALL 继续实时游标边界，不承诺跨页无遗漏或无重复；重新首查 SHALL 能获得当前次序

#### Scenario: 无更多会话
- **WHEN** 当前页之后没有符合边界的本人会话
- **THEN** 系统 SHALL 返回 `next_cursor:null,has_more:false`，空列表的 items SHALL 为 `[]`

### Requirement: 消息历史最近首查并向前加载

消息历史首查 SHALL 选最近消息，默认 20 条、`limit` 为 1 至 50，单批始终按 sequence 升序返回。`next_cursor` SHALL 指向当前批最早序号之前的历史，供未来界面向上连续加载。新追加 SHALL 不改变既有向前边界；历史未删除时 SHALL 不重复或跳过已有消息。

#### Scenario: 多批历史
- **WHEN** 会话有 65 条消息，用户以默认批次读取首批及下一批
- **THEN** 系统 SHALL 依次返回序号 46–65 和 26–45，均批内升序；客户端可把第二批放在首批之前

#### Scenario: 加载期间追加
- **WHEN** 首批返回后又追加消息，用户继续使用历史游标
- **THEN** 系统 SHALL 只读取原边界之前消息，新消息不混入旧历史页；新首查 SHALL 包含最新消息

#### Scenario: 空历史和最早批次
- **WHEN** 会话为空，或已经加载到最早一批
- **THEN** 系统 SHALL 返回 `next_cursor:null,has_more:false`；空历史 items SHALL 为 `[]`

### Requirement: 游标经过认证加密并绑定身份和用途

会话游标 SHALL 版本化并经过认证加密，使用独立于认证、搜索和推荐的密钥。列表游标 SHALL 绑定用户及列表用途，历史游标 SHALL 另绑定会话；不得携带消息正文或依赖 Redis 分页状态。首查默认绝对有效期 SHALL 为 1 小时，后续游标继承原期限，允许在 1 至 50 内改变批次大小。

#### Scenario: 篡改或跨作用域复用
- **WHEN** 游标被篡改、版本未知、过长、到期、密钥不匹配，或跨用户、跨会话、跨列表和历史用途复用
- **THEN** 系统 SHALL 返回 `400 INVALID_CURSOR`，不得静默首查或泄露游标明文及内部错误

#### Scenario: 翻页不续期
- **WHEN** 用户在原期限内改变 limit 并继续翻页
- **THEN** 系统 SHALL 沿原边界返回合法批次，新游标的绝对截止时间 SHALL 保持不变

### Requirement: 错误映射及校验顺序避免信息泄露

错误 SHALL 使用 `error.code,message,request_id`，消息为安全中文。校验 SHALL 依次处理登录、基本格式、所有权及删除、幂等和业务条件；标题版本与配额不得先于所有权泄露。系统 SHALL 对预期错误提供稳定代码，对 PostgreSQL 不可用或依赖 deadline 返回 503，对意外内部错误返回 500。

#### Scenario: 格式和协议头错误
- **WHEN** UUID、JSON、正文、标题、分页、未知或重复参数非法，或缺少/非法幂等键和 If-Match
- **THEN** 系统 SHALL 分别返回 400 的 `VALIDATION_FAILED`、`IDEMPOTENCY_KEY_REQUIRED`、`IDEMPOTENCY_KEY_INVALID`、`IF_MATCH_REQUIRED` 或 `IF_MATCH_INVALID`；无效游标 SHALL 为 `INVALID_CURSOR`

#### Scenario: 他人资源及多重错误
- **WHEN** 对他人会话的基本格式合法，但同时有旧版本、已满配额或可重放幂等记录
- **THEN** 系统 SHALL 优先返回 `404 AGENT_CONVERSATION_NOT_FOUND`，不执行重放或暴露业务状态

#### Scenario: 依赖与内部错误
- **WHEN** PostgreSQL 无法完成事实读取/事务，或发生未预期的内部失败
- **THEN** 系统 SHALL 分别返回 `503 DEPENDENCY_UNAVAILABLE` 或 `500 INTERNAL_ERROR`，不把故障伪装为空历史或 404

### Requirement: 会话运行不额外依赖中间件且不记录正文

会话持久化 SHALL 只依赖 PostgreSQL。系统 SHALL 以固定低基数的操作、结果和耗时观测会话行为，日志可通过 request ID 关联；不得记录标题、正文、幂等键、完整游标或凭据，也不得将用户和会话 ID 作为指标标签。

#### Scenario: 可选依赖不可用
- **WHEN** Redis、OpenSearch、RabbitMQ 或模型未配置或不可用而 PostgreSQL 正常
- **THEN** 已开启的会话接口 SHALL 继续工作；工具依赖故障不得改变会话路由与核心 readiness
