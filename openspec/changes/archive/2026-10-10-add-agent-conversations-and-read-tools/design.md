# Design

## Context

动机及交付范围见 [proposal.md](proposal.md)。当前没有 Agent 会话数据表、API 或工具执行入口；已有账户认证、PostgreSQL 事务与幂等服务、搜索、本人推荐及公开文章读取可以复用。

本次只读检查得到的实施约束：

| 现有位置 | 已有行为 | 本 change 的衔接点 |
| --- | --- | --- |
| `application/idempotency/service.go` | 规范化命令摘要、事务执行、完整成功 JSON 重放 | Agent 删除必须清掉快照中的标题与正文，不能只删除消息表 |
| `infrastructure/persistence/postgres/transaction.go` | 嵌套事务复用 context 中事务 | 外层先取得用户配额锁，再复用幂等执行，避免锁序倒置 |
| `application/articlesearch/service.go` | 分页有后续结果时保留 PIT，关闭 PIT 使用传入 context | 增加共享检索的一次查询模式；取消时也要有界清理，不能调用后丢弃 cursor |
| `application/recommendation/service.go` | 按用户画像推荐，支持 cold_start/latest_fallback | 内部工具保留真实模式、原因及排除集语义，不增加自由文本推荐入口 |
| `application/article/service.go`、`domain/article/article.go` | 公开详情为卡片加清洗 HTML；通用文本规范化会修复编码并 TrimSpace | 增加当前公开修订纯文本端口；聊天正文使用独立严格规范化 |
| `postgres/article_repository.go` | `article_versions.plain_text` 已存在 | 读取持久化纯文本，不重复做 HTML 转文本；新端口遵循事务读取视图 |
| `application/articleread/` | 当前事实、版本化卡片与短读取快照 | 工具复用当前修订/增强身份和批量装配，不能另读身份后拼接旧卡片 |
| `presenter/headers.go` | UUID 幂等键规范化和强 If-Match 解析 | 复用协议解析，If-Match 在本能力表示 title_version |
| `backend/migrations/` | 当前最大编号 000011 | 预计新增 000012，实施时若编号已占用使用下一个编号 |

行为依据是 [agent-conversations](specs/agent-conversations/spec.md) 与 [agent-read-tools](specs/agent-read-tools/spec.md)。所有权、五项会话操作、不可变消息、24 小时幂等、配额默认值、标题、删除、分页、错误顺序及工具身份/副作用沿用用户已确认决定。内部工具参数、执行预算、配置范围和实现拆分是用户授权继续分析后形成的本次提案选择，供本轮整体审阅。

## Goals / Non-Goals

**Goals:**

- 把配额、消息序号、标题版本、活跃时间、删除清理及成功重放放进可验证的 PostgreSQL 事务边界。
- 使用四层架构和现有应用端口，不让 HTTP、SQL、Redis 键、Eino 或第三方工具 SDK 类型进入领域模型。
- 对内部工具显式限定身份、输入、输出、deadline、资源清理及故障语义，允许后续编排适配而无需改写业务服务。

**Non-Goals:**

- 不新增通用工具注册平台、MCP 服务、工具 HTTP/CLI、生成模型调用或工具执行记录表。
- 不设计模型运行锁、Token 记忆预算、回答引用校验、SSE 事件恢复、Web 消息编辑及滚动锚点；本阶段的历史分页仅提供相应数据基础。
- 不修改既有公开搜索、推荐和文章 HTTP 契约，不把规划能力写成已实现功能。

## Decisions

### 1. 按能力增加领域与应用边界

建议新增 `domain/agentconversation`，定义 Conversation、Message、角色、标题/正文规则及领域错误；新增 `application/agentconversation` 承担身份、幂等、配额、排序和删除流程，仓储及事务端口由应用层定义。`application/agenttools` 提供三种具名强类型调用与只读结果，不直接依赖 Infrastructure。

PostgreSQL 仓储、游标密钥装配、Hertz Handler、配置和观测适配各留在现有层；bootstrap 注入已有搜索、推荐与文章应用能力。只有 API 且 auth/agent 同时开启时装配会话路由；Worker、迁移和管理 CLI 不新增 Agent 任务或密钥要求。

选择独立会话能力而不是复用登录 session 表，是因为账户历史必须跨设备与重新登录存续；选择强类型三个工具而不是动态平台，是因为当前只有三种已知读能力。后续 Eino 工具适配器留在 Infrastructure，本 change 不绑定其编排类型。

### 2. 四张表与数据库不变量

| 表 | 主要字段及约束 | 用途 |
| --- | --- | --- |
| `agent_user_state` | user_id 主键并关联 users；conversation_count 非负 | 每用户短事务锁及会话计数；首次写入按需创建 |
| `agent_conversations` | UUID id；user_id；title；title_version>=1；message_count>=0；next_sequence>=1；created_at；last_activity_at | 现存会话与事务计数 |
| `agent_messages` | UUID id；conversation_id 外键 ON DELETE CASCADE；sequence>0；role 限 user/assistant；content；created_at；UNIQUE(conversation_id,sequence) | 不可变消息，应用层无更新/单删操作 |
| `agent_conversation_deletions` | conversation_id 主键；user_id；deleted_at，仅这三个业务字段 | 本人重复删除识别，不含标题正文，不自动过期 |

用户关联遵循现有用户生命周期，不引入新的账户删除 API；未来账户删除流程需同时处理 Agent 事实。建立 `(user_id,last_activity_at DESC,id DESC)` 列表索引、`(conversation_id,sequence)` 历史索引，以及删除标记的用户关联索引。消息不另存用户 ID，归属由会话唯一确定。

`message_count` 和 `next_sequence` 只在提交新消息时更新，未来受控 assistant 写入必须经过同一应用服务以计入总数。数据库保护外键、唯一性、合法角色和非负计数，配置上限由锁内业务校验；不靠 COUNT 后在另一事务 INSERT。只给现存表保存标题正文，删除表不复制元数据。UUID 由服务端生成并规范化，不复用已删除 ID。

选择事务计数加唯一约束而不是先查总数或依赖缓存，是为了让多 API 实例并发时仍不超限。不会重写旧迁移。

### 3. 所有会话写入采用固定锁序

对同一用户的创建、追加、改名和删除，使用短事务顺序：

1. 确保 `agent_user_state` 存在并锁定该用户状态行。
2. 按当前用户核对目标会话的所有权/删除状态；创建重试先查询该用户、创建操作和键在未到期窗口内已有的资源关联，若指向删除会话直接 404。已到期记录不能阻止键作为新操作使用。
3. 对需要幂等的操作调用现有 `idempotency.Execute`，使用同一事务；它取得对应幂等行锁并在成功时保存结果。
4. 新操作锁定会话行，检查标题版本或计数，写入事实、计数、序号及结果，然后一并提交。

删除在相同用户锁之后锁定会话并清理关联 Agent 幂等行，因此不会出现另一个会话写入先锁幂等行、再等待删除持有的用户锁的反向顺序。过期清理只能清理到期记录，不反向取得用户/会话锁。读操作不取得写锁；详情/历史在同一个短只读快照完成归属检查与读取，避免删除竞态被误读成“存在的空会话”。已经开始的读取按其数据库快照判定，后续请求不得从进程缓存返回被删除内容。

同用户的多个会话写入暂时串行，事务内只做 PostgreSQL 操作，不含搜索、Redis 或模型调用。这个代价小于引入跨配额与幂等资源的复杂锁图；实际瓶颈出现后再演进锁粒度。

追加从锁定的 `next_sequence` 分配序号，事务性递增；不承诺无间隙。活跃时间由服务端数据库时间生成，以 `max(当前时间,旧活跃时间)` 防止时钟回拨导致后退；相同时间用 UUID 排序打破并列。实际改名增加 title_version；无变化改名仍检查预期版本，但不改变版本和活跃时间。

### 4. Agent 幂等结果显式关联会话并支持删除终态

复用现有幂等表，新增独立操作名 `agent.conversation.create`、`agent.message.append`、`agent.conversation.rename`。Agent 使用固定 24 小时 retention 的独立 service 实例，不受其它文章操作可配置 retention 改变影响；使用服务端本次操作时间，重放不续期。创建摘要使用解析默认值后的规范化标题，追加使用会话 ID 和规范化正文，改名使用会话 ID、预期版本及规范化标题；不摘要原始 JSON 排版。

所有 Agent 成功记录统一关联 `resource_type=agent_conversation`、`resource_id=conversation UUID`。消息 UUID 和 sequence 在类型化内部结果中，不能把 resource_id 设置为 message UUID 导致删除无法查全。给 Agent 资源关联增加必要的局部索引。

内部结果采用版本化 envelope，包含会话引用、成功 HTTP 状态及响应 snapshot；它只供应用/仓储识别，HTTP 仍返回原会话或消息对象。删除在同事务内将关联成功结果替换为不含标题正文的终态 envelope，只保留会话引用及 deleted 标识，维持原操作身份、摘要和到期时间。现有幂等表要求成功结果非 NULL，因此不能直接把 result_payload 设为 NULL，也不改全局 pending/succeeded 状态语义。

创建没有 URL 目标，需要 Agent 仓储提供按创建身份读取资源关联的锁内预检查；追加/改名先检查 URL 会话。随后重放也必须识别终态，不能把内部 envelope 直接写给 HTTP。对存活资源优先重放旧成功，再检查新操作的版本/配额；快照可比当前元数据旧，详情 GET 是当前事实。不同规范化命令的摘要冲突仍报 409，但目标已删除时优先 404。

删除同时物理删除会话/消息、减会话计数、写三字段标记并清除上述载荷，任一步失败全部回滚。业务表无软删除正文。删除标记随账户保留，用于原幂等记录到期后仍返回本人重复 DELETE 204；未知或他人 ID 为 404。窗口外重用创建键可以创建新的 UUID，会话不会用旧 ID 复活。

选择复用基础幂等协议并增加 Agent 局部清理，而不是另建一套重试协议，能保留既有事务能力又不改变其它模块。必须用真实 PostgreSQL 验证锁序、回滚及幂等清理，内存 fake 不足以证明。

### 5. HTTP 字段、解析与错误

基址 `B=/api/v1/me/agent/conversations`：

| 方法与路径 | 请求 | 成功 | 协议头 |
| --- | --- | --- | --- |
| POST B | `{}` 或 `{title}` | 201 Conversation | Idempotency-Key 必需 |
| GET B | limit、cursor 可选 | 200 ConversationPage | 登录 |
| GET B/{id} | 无 | 200 Conversation | 登录 |
| PATCH B/{id} | `{title}` | 200 Conversation | Idempotency-Key、If-Match 必需 |
| DELETE B/{id} | 无 | 204 无正文 | 不要求幂等键或版本 |
| GET B/{id}/messages | limit、cursor 可选 | 200 MessagePage | 登录 |
| POST B/{id}/messages | `{content}` | 201 Message | Idempotency-Key 必需 |

字段见会话规格；创建、详情及改名的单会话响应提供强 ETag，值为该响应快照的 `title_version`（例如 `"1"`），它只供标题 If-Match，不表示消息集合版本。成功重放保留首次状态、正文和对应版本。If-Match 复用强 ETag 正整数解析，拒绝弱版本、通配符、列表、前导零和溢出；重复关键协议头拒绝为相应 INVALID 错误。

JSON 采用严格解析：对象字段白名单、拒绝重复键、多余尾随值、非法 UTF-8、未配对 surrogate 转义及显式 null。UUID 复用现有解析并规范化为小写标准形式；Idempotency-Key 使用既有 UUID 契约。正文独立完成 CRLF/CR 到 LF 的转换，不调用会 TrimSpace/修复非法编码的文章 NormalizeText。标题拒绝换行和控制字符，再 TrimSpace 并检查 code point 数；客户端内容按纯文本保存。

消息 HTTP body 字节上限取 `max(64 KiB,12×max_message_chars+4 KiB)`，硬上限 256 KiB，确保最坏 JSON Unicode 转义仍可承载合法正文；创建/改名 body 上限 4 KiB。该限制只用于本组路由，超限统一 VALIDATION_FAILED。分页只允许 limit/cursor；未知或重复查询参数拒绝，省略 limit 用 20，显式 0 或空值拒绝；显式空 cursor 为 INVALID_CURSOR。cursor 编码长度最多 4 KiB。

| HTTP | code | 原因 |
| --- | --- | --- |
| 401 | AUTH_SESSION_INVALID | 当前登录失效/缺失 |
| 400 | VALIDATION_FAILED | UUID、正文、标题、JSON、limit 或未知/重复参数错误 |
| 400 | IDEMPOTENCY_KEY_REQUIRED / IDEMPOTENCY_KEY_INVALID | 幂等头缺失/非法 |
| 400 | IF_MATCH_REQUIRED / IF_MATCH_INVALID | 改名版本头缺失/非法 |
| 400 | INVALID_CURSOR | 编码、认证、绑定、版本、长度或期限错误 |
| 404 | AGENT_CONVERSATION_NOT_FOUND | 不存在、已删除或非本人 |
| 409 | IDEMPOTENCY_KEY_REUSED / IDEMPOTENCY_IN_PROGRESS | 摘要冲突/在飞操作 |
| 409 | AGENT_TITLE_VERSION_CONFLICT | 新改名预期版本不匹配 |
| 409 | AGENT_CONVERSATION_LIMIT_EXCEEDED / AGENT_MESSAGE_LIMIT_EXCEEDED | 新操作超过配额 |
| 503 | DEPENDENCY_UNAVAILABLE | PostgreSQL 不可用或依赖 deadline |
| 500 | INTERNAL_ERROR | 预期外失败 |

统一中文 `error.message` 和 request_id，沿用 presenter。顺序为认证→基本格式→所有权/删除→幂等重放或冲突→新操作版本/配额；解密后的游标绑定检查在合法目标归属确认后进行。无会话路由时保留框架通常的不存在响应，不声称是会话业务 404。

### 6. 两种方向的无状态加密游标

采用独立 AES-256-GCM 密钥和随机 nonce，版本与用途纳入认证数据。密文只带用户、用途、排序边界、首次查询时间和绝对 expires_at；消息另带 conversation_id，不带标题/正文。编码大小固定有界，不保存 Redis 状态。

- 列表：首查按 `(last_activity_at,id) DESC` 取 limit+1，下一页使用小于末项复合边界。它是实时列表，不冻结跨页集合；未来 UI 按 ID 去重并允许刷新。
- 历史：首查按 sequence DESC 取 limit+1，选定批次后升序输出；续页条件为 `sequence < before_sequence`。下一游标取本批最小 sequence，批内升序。未来 UI 负责向上拼接及保持滚动锚点。
- 两者首查默认 1 小时绝对期限，续页继承，不因 limit 改变续期；limit 不写入固定绑定，允许 1–50 调整。
- 任意版本、格式、认证、用户/用途/会话绑定或到期失败统一 INVALID_CURSOR。密钥变化使旧游标失效，但不影响持久化历史。

选择认证加密而不是纯签名，是为了不暴露身份、时间边界及内部分页载荷；选择数据库 keyset 而不是 offset，是为了让消息追加不打乱向前读取。

### 7. 三个内部工具的契约

工具要求非空可信调用者身份，应用入口不接受匿名回退。业务身份作为单独参数而非可由模型填充的 JSON 字段；未来编排必须先通过会话服务取得本人会话。输入使用严格 schema，输出为强类型结构，工具/序列化适配通过同一校验入口，不能靠 map 任意转发。

| 工具 | 输入 | 结果及约束 |
| --- | --- | --- |
| search_articles | q 必需；keyword/topic/source_id/limit 可选 | q 规范化 1–200 字符，筛选词 1–64；limit 默认5、1–10；一次检索，保持当前 BM25/混合计划 |
| recommend_articles | 仅可选 limit | 默认5、1–10；本人画像与排除；mode、degraded、可空 degrade_reason 及每项 reason |
| get_article | article_id 必需；max_chars 可选 | article_id 为正 int64；max_chars 默认4000、1–8000；当前公开纯文本与截断状态 |

搜索文本先拒绝非法编码/NUL/null，随后复用现有搜索规范化、精确筛选及长度语义，不自行实现另一套检索规则。搜索/推荐结果没有 next_cursor 或续页输入；`truncated` 表示因候选/扫描、条数或字节预算未能完整返回，调用者不能据此假定检索已穷尽。

统一 ArticleRef 包含 `article_id,revision_id,title,origin_type,source,author,path,original_url,summary,summary_source,metadata_truncated`。source 只含公开来源 id/title，author 只含公开站内 id/nickname 或 RSS 作者显示名；path 使用现有站内详情路由；original_url 仅 RSS 可空；summary_source 为 `model|extractive|excerpt`。summary 最多 1000 code point，标题/来源/作者展示字段各最多 256；裁剪设置 metadata_truncated，不改变身份、顺序或链接。结果不带分数、画像权重或模型内部字段。get_article 在 ArticleRef 外加 `content` 与 `truncated`；列表外层为 `items,truncated`，推荐再带 mode/degraded/degrade_reason 和每项 recommendation_reason，原因值沿用现有固定集合。

ArticleRef 的 revision_id、卡片及摘要必须来自同一最终事实视图。在现有搜索/推荐批量装配点增加内部类型化投影，携带当前修订身份；公开 HTTP presenter 保持原字段。不能返回卡片后再单独查 revision_id，把不同时刻的结果硬拼起来。get_article 新增最小 PublicArticleTextReader 应用端口，单次 SQL join 或短只读事务返回当前公开修订及其 plain_text；不扩展公共详情为原始正文下载，也不重新清洗 HTML。

搜索增加一次查询应用入口，与原 Search 共用召回、复核及排序逻辑，通过执行策略禁止保留游标/PIT；混合路径同样不编码续页载荷。任何创建成功的 PIT 都在退出时以最新 PIT 身份执行 ClosePIT，清理使用 `context.WithoutCancel` 派生且最长 250ms；创建失败时无资源则不清理。失败计数可观测，残留由现有有限 PIT keep_alive 兜底。不能把 HTTP 请求转调到自己的服务或丢弃 Search 返回的游标。

推荐增加一次结果投影入口，复用当前用户计划与最终排除复核，不改变已存在的缓存、候选上限及排序；没有需续页的 PIT 时不凭空建立分页资源。搜索/推荐不逐文章读取正文，正文只由 get_article 单独按需取得。

### 8. 时间、字节、错误与副作用预算

三工具执行默认 5s，总 deadline 取调用者与工具预算较早者，已有依赖预算继续取更小值；工具不增加重试。既有可选查询 Embedding 可随已配置搜索执行，它不是生成回答调用，不引入新的付费模型验收要求。超时及取消尽早停止业务工作，最多额外 250ms 只做 PIT 清理。

输出按最终紧凑 JSON 编码后计字节，默认 128 KiB；先限制文本字段，再缩减正文前缀或尾部列表项，保留项相对次序不变并设置截断标记。不能截断 article_id/revision_id/path 以凑预算。最小可配置字节预算仍能容纳一个经过字段上限约束的 ArticleRef；极端编码异常归 INTERNAL_ERROR，不能退化为假空结果。

工具错误是应用层类型，使用 VALIDATION_FAILED、ARTICLE_NOT_FOUND、SEARCH_UNAVAILABLE、DEPENDENCY_UNAVAILABLE、TOOL_TIMEOUT、TOOL_CANCELED、INTERNAL_ERROR；不含 HTTP 状态或底层错误原文。调用者明确取消优先归 TOOL_CANCELED，总预算耗尽归 TOOL_TIMEOUT；未耗尽总预算的搜索内部故障为 SEARCH_UNAVAILABLE，PostgreSQL 事实/排除集失败为 DEPENDENCY_UNAVAILABLE。成功空结果、冷启动、BM25 降级和推荐 latest 回退各保留真实含义。

不创建消息、阅读、收藏、负反馈或画像事实；缓存填充及 PIT 是允许的技术副作用。不增加逐次工具调用日志表，也不把查询/正文写入观测。必须验证调用前后的业务表和画像未变化，而不是仅断言返回值。

### 9. 配置、观测和启用策略

默认值 < YAML < VELIS 环境变量；专用密钥仅接受环境注入，不写入 YAML 实值或日志。

| 配置 | 默认值 | 支持范围/策略 | 环境变量 |
| --- | --- | --- | --- |
| agent.enabled | false | bool；有效路由同时要求 auth 开启 | VELIS_AGENT_ENABLED |
| agent.max_conversations_per_user | 50 | 1–500 | VELIS_AGENT_MAX_CONVERSATIONS_PER_USER |
| agent.max_messages_per_conversation | 200 | 1–2000 | VELIS_AGENT_MAX_MESSAGES_PER_CONVERSATION |
| agent.max_message_chars | 4000 | 1–16000 code point | VELIS_AGENT_MAX_MESSAGE_CHARS |
| agent.cursor_ttl | 1h | 1m–24h，首查绝对期限 | VELIS_AGENT_CURSOR_TTL |
| agent.tools.timeout | 5s | 100ms–10s | VELIS_AGENT_TOOL_TIMEOUT |
| agent.tools.max_output_bytes | 131072 | 65536–1048576 字节 | VELIS_AGENT_TOOL_MAX_OUTPUT_BYTES |
| Agent 游标密钥 | 无固定值 | base64 解码后恰好32字节 | VELIS_AGENT_CURSOR_KEY |

24 小时 Agent 幂等窗口、标题上限100、列表/历史批次20/50、工具结果5/10、正文工具4000/8000及清理250ms是本版固定协议/安全边界，不另增配置面。缩小配额不删除存量；已超新上限时拒绝新增，但允许读取、删除和合法重放。

开发环境 API 可生成进程临时游标密钥并说明重启失效；非开发环境只有 API 真正开启会话路由时才强制专用密钥，缺失时启动失败。不同 API 实例应注入相同密钥；Worker、CLI、迁移不因缺少 Agent 密钥失败。Agent 不增加 Redis/OpenSearch/模型 readiness 探测，关闭功能不删除数据。

指标记录受控 operation/tool、结果代码、降级原因、截断、计数/配额拒绝、延迟及 PIT 清理结果。日志保留 request_id 和安全错误分类，不含标题、正文、查询、画像、键、完整游标或凭据；用户/会话/文章 ID 不成为指标标签。

### 10. 五步实施及验收证据

建议 I5 分支 `feat/i5-conversational-agent`；本 change 的五步实施顺序为：领域与迁移→事务应用服务→HTTP 与加密分页→只读工具→联合验收与交付收口。每步包含相应成功、失败及边界测试和该步所需文档，细目见 [tasks.md](tasks.md)。后续独立 change `add-cited-conversational-agent` 承接编排/引用/SSE，再由 `add-agent-web-experience-and-evaluation` 承接界面和评测。

真实验收只使用专用 `_test` 库：数据库级配额竞争、同键/不同键并发、改名与追加、删除竞争、三类重放载荷清除、事务失败回滚、分页边界和迁移保护；真实 OpenSearch 验证一次搜索 PIT 在成功/失败/取消后的清理及可见性过滤；启用既有缓存的联合用例验证 Redis 清空/故障时的身份隔离和当前修订。查询模型使用确定性 stub，默认不调用外部付费模型。原搜索分页与推荐降级必须回归。依赖测试 skip 不是通过，证据记录实际服务、命令、结果和限制。

## Risks / Trade-offs

- 同用户会话写入串行 → 事务限定为短 PostgreSQL 工作，锁等待可观测；有证据后再细化，不能先牺牲配额和幂等正确性。
- 不自动过期的历史与删除标记持续增长 → 活跃会话/消息有配额，明确三字段标记永久保留策略；若未来变更保留策略，另提规格，不暗中清理。
- 物理删除不能撤回已经开始读取的响应，也不自动清除外部备份 → 业务删除保证数据库事实及幂等载荷原子清除，部署文档明确现有备份生命周期；不声称删除所有备份。
- 实时会话列表跨页移动 → API 明确不冻结，后续 UI 去重/刷新；消息历史采用稳定 sequence 向前边界。
- PIT 清理遇到搜索服务故障 → 最多250ms清理并记录失败，保留有限 keep_alive 兜底，不能无界重试。
- 搜索/推荐结果元数据可能在工具完成后被新修订替代 → 每次输出保证读取视图一致；后续最终引用校验需重新确认当前公开事实。
- 修改共享检索装配点可能影响公开分页 → 共用核心、分离资源保留策略，回归 BM25/混合排序、续页和缓存装配，不改变长期规格。
- 配置游标密钥轮换使已有游标失效 → 返回 INVALID_CURSOR 并从首查恢复，不影响消息；本版不扩展多密钥轮换平台。

## Migration Plan

1. 实施前确认编号，新增 `000012_add_agent_conversations.up.sql/.down.sql` 或当时下一个编号，补齐约束和必要索引；不修改已执行迁移。
2. 在专用空测试库执行 up/down/up，验证无数据可回退；带现存会话、消息、删除标记或 Agent 幂等记录时 down 必须拒绝，事务回滚不丢数据。只有空的用户状态行不应阻止安全回退。
3. 启用前按既有数据库备份流程创建并确认可恢复备份，记录恢复步骤；默认先保持 agent.enabled=false 部署并迁移，再在目标 API 配置认证、统一专用密钥及所需预算后显式开启。
4. 上线回退优先关闭 Agent 功能并保留表和历史；已有数据时不得强行执行 down 或手动清表。真正降级迁移仅在 Agent 数据为空且备份/恢复方案明确时执行。
5. apply 阶段更新 OpenAPI、示例配置、`.env.example`、README、Roadmap 和操作文档，区分本阶段已实现基础与仍待实现的完整 Agent。propose 阶段只提交本 change 规划文件，不触碰这些实现文件。
