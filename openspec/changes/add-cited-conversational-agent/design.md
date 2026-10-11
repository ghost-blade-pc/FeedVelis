# 智能阅读助手执行与流式交付设计

## Context

动机和交付范围见 [proposal.md](proposal.md)。当前基础已归档：`agentconversation` 提供不可变 user/assistant 消息、用户状态锁、24 小时幂等、永久最小删除标记和加密历史分页；公共 Append 只写 user。`agenttools` 已提供三个严格 JSON 只读入口及当前文章修订身份；`bootstrap/agent.go` 的工具构造尚未接入生成消费者。

仓库锁定 Eino `v0.7.13`、Hertz `v0.10.6`。已读取本地 SDK：ADK 的 ChatModelAgent 支持 MaxIterations、BeforeChatModel、AfterChatModel 和 WrapToolCall，Runner 支持独立运行；Hertz 已含 `pkg/protocol/sse`。现有架构测试禁止 Domain/Application 导入 CloudWeGo、SQL、HTTP 等技术依赖。现有 Web 认证封装在刷新令牌后重新执行一次请求，生成接受必须可重放；`web/nginx.conf` 未为 SSE 关闭响应缓冲。

用户已确认断线不取消执行。智能阅读助手以后扩展笔记、外部搜索、写作与长期偏好；当前依然只启用站内阅读，参见 [编排规格](specs/agent-orchestration/spec.md)。现有 Roadmap 关于首版只读工具的约束仍成立。

## Goals / Non-Goals

**Goals:**

- 将回合事实、权限、预算、证据和用户事件放在应用契约中，将 Eino 编排适配留在 Infrastructure；单 Agent ReAct 作为本次执行策略。
- PostgreSQL 协调多个 API 实例，不依赖粘性路由；断线重连恢复的是已保存事件和结果，不是重新执行模型。
- 对部分输出、引用失效、取消竞争、进程中断和数据库故障给出可测试的结果，保留四层依赖方向及会话核心降级边界。
- 通过明确的数据及端口边界允许后续更换编排策略、增加工具、上下文来源及产物类型。

**Non-Goals:**

- 不建设通用 Agent 平台、动态工具安装、多 Agent 协作、开放网页爬虫或长期记忆数据库。
- 不实现写笔记、修改草稿、发布文章、外部搜索、历史自动摘要或成功回答重新生成；失败重试与成功再生成区分。
- 不持久化隐藏推理、完整 Prompt、Provider 原始响应或逐次原始工具输入输出，不实现模型执行检查点恢复。
- 不在此 change 交付 Web 对话页面和完整浏览器评测，也不声称验证了生产部署或外部模型计费的恰好一次。

## Decisions

### 1. 领域与应用契约独立于 Eino

新增 `domain/agentrun` 的状态、执行权、预算和重试规则，以及 `domain/agentevidence` 的来源、片段、引用和投影规则。新增 `application/agentrun` 处理接受/查询/取消/提交，`application/agentorchestration` 组织上下文、调用能力和验证结果。具体包拆分以领域职责为准，不把 SDK 的 AgentEvent/Message 直接作为应用端口。

应用端口覆盖回合仓储与事务、当前授权检查、证据读取及可见性复核、上下文来源、编排执行、回答片段和运行观测。Infrastructure 实现 Eino 模型、编排、PostgreSQL 和指标；Interfaces 实现 HTTP/SSE；bootstrap 构造并管理执行器生命周期。

工具定义包含 name/version、显式输入输出 schema、受控权限、read/write 副作用分类和预算。在本次注册表中只存在三个 read 工具，模型不能增删注册项；编排器调用应用提供的 ToolInvoker，而非直接连接仓库。后续写工具必须走对应应用服务、版本条件和业务授权；目前不实现一个空的审批系统。

备选为在 Application 直接创建 ADK Agent，或把所有流程塞入 Handler；两者均会破坏现有技术纯度或让执行绑定 HTTP 生命周期。本方案保留稳定业务端口，ADK 版本升级不直接改变 HTTP 契约。

### 2. 一轮执行采用 ReAct 取证与受控成文两阶段

```text
Accept --> Claim --> Context --> ReAct discovery --> Evidence set
                                                       |
Commit <-- Validate finish <-- Persist/emit block <-- Stream answer
```

取证阶段使用 Eino ADK ChatModelAgent + Runner，关闭用户文本透传。模型根据工具观察结果继续决策；正常结束的自由文本只表示取证结束，不作为答案。工具调用通过可信身份、约束和预算拦截器执行，首版顺序执行一批工具，整批超过剩余次数时先拒绝。任意 Provider 并行工具输出也不能绕过这一入口。

成文阶段使用同一独立模型配置的无工具流式调用，只提供已经裁剪后的证据和有限上下文，要求输出受控 NDJSON。默认最多 4 次模型调用，包含取证和成文；取证最多 3 次并预留成文 1 次。到达取证次数而尚未正常结束时失败，不额外调用成文突破次数。所有补偿、格式重试和 Provider SDK 自动重试默认关闭。

无结果、缺少条件、指代不清和能力不支持通过受控决策及模板输出，必要时可以不调用成文模型。若存在必需工具故障，不能选择无结果成功模板。正常 answer 必须至少有一个有效来源。

备选为直接把 ReAct 最终 Token 转发到 SSE，或固定“搜索一次→回答”；前者无法在发送前保证引用结构，后者不能根据文章内容继续取证。独立成文增加一次调用，但能够在有限工具循环之后冻结本轮证据，并严格隔离中间推理与用户输出。

### 3. 公共 API 保留消息追加，新增五个回合操作

基路径为 `/api/v1/me/agent/conversations/{id}`：

| 方法与路径 | 输入/输出及语义 |
| --- | --- |
| POST `/runs` | `message_id` 必需；可选 `context_article_id`、`constraints:{keyword,topic,source_id}`、`retry_of`；UUID Idempotency-Key；202 + Location + 接受快照 |
| GET `/runs` | limit 1–50，默认 20；cursor 可选；按 created_at/id 倒序；items/next_cursor/has_more |
| GET `/runs/{run_id}` | 当前状态、消息关联、投影后的结构化回答或 partial、用量和安全错误 |
| POST `/runs/{run_id}/cancel` | 无正文和查询参数；200 当前回合摘要，终态不变；本身幂等，无需额外幂等键 |
| GET `/runs/{run_id}/events` | Bearer + 可选 Last-Event-ID；只订阅已有事件 |

POST runs 的请求上限为 16 KiB，沿用严格 JSON、重复键/编码/null 拒绝和 UUID 校验。message_id 必须指向会话当前最后一条消息且为 user；未知或不属于当前会话的 message_id 统一 `404 AGENT_MESSAGE_NOT_FOUND`。不允许成功后换键再次生成。最新失败、取消或超时回合可通过 retry_of 显式重试，同一消息最多接受 3 次，原上下文和约束不变；用户修改问题时追加新消息。

接受快照固定为 `id,conversation_id,user_message_id,status,created_at`，status 为 accepted。列表/取消摘要另含 `assistant_message_id,error_code,finished_at`（可空）和 partial。详情增加 `answer,usage`；内部执行权、登录会话、Prompt 版本细节和原始模型输出不暴露。相同请求始终重放首次接受快照；详情 GET 才代表当前状态。

列表游标复用独立 Agent 认证加密密钥，增加 run-list 用途及会话绑定，不接受其它用途游标；TTL 和 limit 规则沿用会话配置。未知/重复参数拒绝。202 表示接受成功，不表示生成成功。

备选为修改 POST messages 直接生成，或用新接口接受正文并再次存消息；本方案使既有接口继续可用，消息请求和生成请求各有稳定幂等键。下一 Web change 负责一次提问跨两个接受请求的重试协调。

### 4. 接受幂等、执行权与数据库事务分离

新增回合关联既有用户消息。使用已有 idempotency.Execute 在短事务中提交“接受成功”而非占用幂等 pending 等待整个模型执行；窗口固定 24 小时，规范摘要包括用户消息 ID、上下文、约束和 retry_of。回合创建操作关联 resource_type=`agent_conversation` 及会话 UUID，方便删除时将私密成功载荷转成原三字段删除终态。扩展删除清理操作集合，不能遗漏新操作。

锁顺序保持账户状态最先；接受走账户→幂等→会话→回合→事件，普通执行更新走账户→会话→回合→事件。取待执行候选时先非锁定读取 ID，再按此顺序条件抢占，不先锁回合后等待账户，以免与删除和 Append 形成逆序。每个写入都带活动状态、owner_token 及未过期租约条件；租约和总 deadline 使用数据库时间。

数据库约束包括每会话一个活动回合、每用户消息一个成功回合、消息/会话归属关联及正事件序号；账户最多两个活动回合在用户锁下保证。模型和工具网络调用绝不置于数据库事务内。最终事务重新检查所有权、活动状态、授权、配额及证据，追加唯一助手消息并提交状态与终态事件。提交响应丢失时查询原回合确认，不能重新运行模型来消除不确定性。

备选为用全程长事务或内存去重；前者占用连接并无法原子回滚外部调用，后者无法处理跨实例和进程丢失。此方案不承诺 Provider 恰好一次计费。

### 5. API 内执行器与 PostgreSQL 协调，断线只结束订阅

每个启用生成的 API 实例启动固定容量执行器，默认最多 4 个本实例执行任务，以 500ms 周期读取有界候选。通过短事务将 accepted 条件更新为 running 并分配 owner_token。执行租约固定 10 秒、每 2 秒续约；续约失败、读取终态或删除后立即取消本地上下文并停止动作，不能继续使用本地缓存身份。

生命周期上下文由 API supervisor 管理，独立于 SSE 请求。回合 deadline 从 created_at 起默认 60 秒，涵盖排队、取证和成文。取消接口原子提交 canceled 并释放预留；执行者以最多 1 秒周期检查共享状态，以及在每次工具/模型调用、片段提交和终态提交前检查，取消下游请求。即使取消未能立即阻止 Provider 处理，数据库执行权也阻止迟到写入。

会话开启时维护循环持续运行，默认每秒扫描有界批次：accepted 超过 deadline→timed_out；running deadline 到期→timed_out；租约失效→failed/AGENT_EXECUTOR_LOST。有效执行权过期后最多 5 秒内收敛。多实例竞争终态用条件更新保证唯一；恢复只重放状态和事件，不再次执行 running。已持久化而尚未执行的 accepted 可以在重启后、原 deadline 内被合法领取。

关闭生成配置时不启动新执行，维护循环仍收敛已有回合。优雅关闭先拒绝新接受，再取消本实例运行任务、提交 failed/AGENT_EXECUTOR_STOPPED 和释放预留；无法落库的 running 由租约过期兜底。关闭整个 Agent 时路由移除、历史保留，重新开启后维护循环先处理过期活动状态。与已有 bootstrap 关闭顺序整合，数据库连接池关闭前停止执行及维护。

备选为绑定请求上下文、添加 MQ 消费者或持久化 ADK 检查点。已确认断线继续执行排除了第一项；本阶段 60 秒内任务用 PostgreSQL 已可协调，后两项增加外部依赖或自动恢复费用语义，留给有依据的后续变更。

### 6. 助手消息额度提前预留

回合 active 记录包含一个 assistant_reserved 名额，用户锁下判断 `message_count + active reservations < max_messages`。预留不是消息，不增加公开 message_count，不占用序号。活动回合期间新 Append 返回冲突；合法旧 Append 成功重放在活动检查前完成。改名和读取仍可用。

成功事务将预留变为实际 assistant，按当前下一序号追加；失败、取消、超时与删除释放。降低配置不删除历史，成功提交仍按执行实例的当前配置复核；同一数据库的 API 副本必须使用一致配额配置，不支持热更新过程中不同副本各自采用不同上限。只有一个剩余消息名额时可先保存用户消息，但之后创建回合会因缺少助手名额拒绝，必须在操作文档中说明。

备选为执行后再检查额度或占位 assistant 消息后覆盖。前者可能发生付费后无法保存，后者违反不可变消息契约。

### 7. 上下文组装与硬约束

ContextBuilder 分别组合系统策略、当前 user 消息、当前文章、最近成功回合及可选偏好来源。首版偏好来源返回空，不建偏好表；推荐画像仍只由 recommend_articles 使用。最多取最近 6 个成功回合，从最旧完整回合开始剔除以满足输入字节与 Token 预留；失败半成品不进入上下文。

当前文章通过 get_article 获取，计入工具调用次数。上一回合的第几篇文章按持久化引用次序解析，不能从正文正则猜 ID；已失效引用只保留安全占位，无法唯一定位则 clarification。当前问题不截断；必要输入超限返回 AGENT_CONTEXT_LIMIT_EXCEEDED。

结构化 constraints 在接受时固定，搜索参数取其与模型提议的兼容交集，冲突提议拒绝；source_id 等条件不能被模型覆盖。自然语言条件在取证第一轮抽取为同样严格类型的候选约束，经校验后锁定到本轮；显式输入优先，歧义或不支持条件澄清。带硬约束时不开放无约束推荐工具结果作为文章回答来源，get_article 也必须复核当前标签/来源。复核复用已有精确匹配语义，增加批量只读投影端口，不修改 get_article 的既有公共 JSON schema。

备选为给模型全部历史并仅提示其遵循条件；本方案把权限和显式约束交给服务端执行，但自然语言是否正确抽取仍属于独立评测项。

### 8. 证据、回答协议与不可变历史投影

证据记录以 evidence_id 标识本轮实际提供给成文模型的内容，kind 当前只允许 article，保存 article_id、revision_id、取得时间、summary/body 来源及截断。工作中的证据文本只用于当前输入，不另建永久全文镜像；成功保留来源身份、必要摘要与逐字引文核验信息，失败终态 24 小时后清除其工作证据和片段。当前公开事实从 PostgreSQL 复核，Redis/索引没有授权作用。

成文模型协议为有界 NDJSON：首记录为回答类型 header，随后每行一个完整 block，末记录 finish。block 包含非空 text、evidence_ids 和可选显式 quotes（文本及证据关联），不允许 URL、作者或卡片字段。answer 的每块至少一个有效引用，quotes 必须逐字匹配实际提供证据；未知/重复字段、乱序、重复片段 ID、非法编码、尾随垃圾或缺少 finish 均失败。单行缓冲最多 64 KiB、模型输出原始缓冲累计最多 256 KiB；总回答默认最多 8000 code point、32 块、10 个唯一来源，不对用户静默截断为成功。

无结果和 clarification 采用服务端模板，只有正确的工具观察事实可触发 no_results。Provider 流以工具结束或截断原因退出不能算 finish；任何非法尾部导致 failed，已发送的合法块保持 partial。

应用对外回答固定为 `version:1,kind,blocks,references,cards,redacted`。blocks 包含 id、text、evidence_ids、redacted；服务端生成展示编号。正常卡片来自可信 ArticleRef，复用工具上限、summary_source 和推荐 mode/degraded 标志，去重后按首次引用排序。工具中间答案或模型 metadata 永不成为卡片。

首次发块前做当前短快照批量复核，完整提交前再次复核；文章下架/删除与修订过时分开分类。无法复核时 fail closed，不假定暖缓存有效。复核与实际网络发送存在时间间隙，契约以当前读取快照为边界，不能撤回已送达内容。

历史助手消息保持原 ID/sequence/created_at/content，新增读取投影的 run_id/content_redacted。读取时按结构化片段生成安全 content，任一来源失效则隐藏整块及失效引用元数据；固定替代文本为“相关内容暂不可用”。run 详情和 SSE 重放使用同一投影器，不能有返回数据库原文的旁路。旧消息没有 run 关联时按旧格式读取；新生成助手必须有关联。

备选为只校验 article_id、整答完成后才发送、或下架时回写历史正文；前两者分别不足以检查引用及失去逐块展示，第三项违反消息不可变。来源/引文校验仍不能自动证明所有自由文本断言正确，因此评测独立检查语义支持度，不称为真实性证明。

### 9. SSE 事件日志与代理

持久事件只包括 run.status、tool.status、answer.sources、answer.block、run.terminal。stream.error 是订阅级瞬态控制事件，不保存到回合日志，不能作为回合终态。事件 id 为 `v1:<run_uuid>:<positive_sequence>`，验证格式/回合/已提交上界，不能用来代替认证。单一 Last-Event-ID 是唯一续读输入，拒绝重复头和未知查询参数。

片段、对应新增来源和事件在短事务内一起保存，先来源后片段；成功助手及最后事件原子提交。GET 按序从 PostgreSQL 读取有界批次后投影，不占用长事务，最多 250ms 轮询；同实例通知只能优化等待，不影响正确性。重放可重复投递，消费端以 ID 去重；投影随公开性变化，相同 ID 的内容可能从原文变为 redacted。重连先用详情替换本地回答快照，再按 last_event_id 续读，不能仅跳过已消费 ID 而保留本地失效原文。

每次持久化先检查剩余额度，为终态预留一个事件及 4 KiB；每回合最多 128 事件和 512 KiB，超出以 AGENT_BUDGET_EXCEEDED 失败。心跳为 10 秒注释，不入库；订阅最长 90 秒，客户端可续订。每实例最多 64 条订阅、同账户最多 4 条，超过返回 429 AGENT_STREAM_LIMIT_EXCEEDED；本阶段不承诺跨副本总订阅上限，部署上限为实例数乘本实例上限。

Hertz writer 只在 Handler 生命周期使用，flush 失败或 5 秒写阻塞关闭订阅，不取消回合。响应使用 text/event-stream、Cache-Control:no-store、X-Accel-Buffering:no；nginx 为事件路由单独设置 proxy_buffering off、proxy_cache off、足够读超时（至少 90 秒），保留现有身份/IP 代理头。通过真实 TCP 和 nginx 测试心跳、分批可见、断流和慢客户端。

终态事件保留 24 小时，到期返回 410 AGENT_EVENTS_EXPIRED；客户端查询详情获得成功完整回答或无片段的失败摘要。Last-Event-ID 等于尚未清除的终态末位置时返回 204。流开始前用原错误信封；流开始后安全 stream.error 或 EOF，客户端查询原回合，不能以 EOF 推断成功。

前端下一 change 使用 fetch + Bearer + AbortSignal 读取 SSE；停止动作明确调用 cancel，AbortSignal 仅关闭订阅。访问令牌自然过期时关闭订阅，刷新后续订；回合绑定账户与登录会话且不存令牌。最迟每 10 秒复核订阅授权，执行器每次业务动作前及最多 1 秒维护周期检查原登录会话有效性；注销/禁用使运行回合 canceled，原因 AUTH_SESSION_INVALID。

### 10. 配置、预算与用量

配置优先级继续为默认值 < YAML < 环境变量。新增下表，所有上限需要范围校验与跨字段校验；当前 Agent 会话参数及独立游标密钥规则不变。

| YAML / 环境变量 | 默认值 | 允许范围或要求 |
| --- | --- | --- |
| agent.generation.enabled / VELIS_AGENT_GENERATION_ENABLED | false | bool；接受生成另要求 auth 和 agent 开启 |
| agent.generation.provider / VELIS_AGENT_GENERATION_PROVIDER | 空 | 首版仅 openai-compatible |
| agent.generation.base_url / VELIS_AGENT_GENERATION_BASE_URL | 空 | 显式配置有效 HTTP(S) 地址，沿用模型配置网络约束 |
| agent.generation.model / VELIS_AGENT_GENERATION_MODEL | 空 | 显式指定支持工具调用和文本流的模型 |
| 仅环境 VELIS_AGENT_GENERATION_API_KEY | 无 | 凭据不写 YAML/日志，不继承 AI_GENERATION_API_KEY |
| agent.generation.profile_version / VELIS_AGENT_GENERATION_PROFILE_VERSION | agent-model-v1 | 非空版本 |
| agent.generation.prompt_version / VELIS_AGENT_GENERATION_PROMPT_VERSION | agent-reading-v1 | 非空版本 |
| agent.generation.timeout / VELIS_AGENT_GENERATION_TIMEOUT | 30s | 100ms–60s，实际受回合剩余期限限制 |
| agent.generation.max_tokens_parameter / VELIS_AGENT_GENERATION_MAX_TOKENS_PARAMETER | max_tokens | max_tokens 或 max_completion_tokens，只发所选字段 |
| agent.generation.max_output_tokens / VELIS_AGENT_GENERATION_MAX_OUTPUT_TOKENS | 2048 | 256–8192，每次调用上限 |
| agent.execution.timeout / VELIS_AGENT_RUN_TIMEOUT | 60s | 5s–120s，覆盖 accepted 起的全程 |
| agent.execution.max_model_calls / VELIS_AGENT_MAX_MODEL_CALLS | 4 | 2–8，含独立成文调用 |
| agent.execution.max_tool_calls / VELIS_AGENT_MAX_TOOL_CALLS | 6 | 1–12，每个工具尝试计数 |
| agent.execution.max_output_tokens / VELIS_AGENT_RUN_MAX_OUTPUT_TOKENS | 8192 | 512–32768，不小于单次上限 |
| agent.execution.audit_token_budget / VELIS_AGENT_RUN_AUDIT_TOKEN_BUDGET | 65536 | 8192–262144，必须大于单次最大输出预留 |
| agent.execution.max_input_bytes / VELIS_AGENT_MAX_INPUT_BYTES | 49152 | 8192–131072，含序列化上下文与工具 schema |
| agent.execution.max_answer_chars / VELIS_AGENT_MAX_ANSWER_CHARS | 8000 | 1000–16000，Unicode code point |
| agent.execution.concurrency / VELIS_AGENT_EXECUTION_CONCURRENCY | 4 | 1–16，每 API 实例 |

其余界限固定为协议/执行常量：每账户 2 活动回合、每会话 1、每消息 3 尝试、最近 6 成功回合、32 片段/10 来源、128 事件/512 KiB、终态事件 24h、租约 10s、续约 2s、状态扫描 1s、订阅 90s、心跳 10s、单次写阻塞 5s、每实例 64/每账户每实例 4 订阅。不把每个常量都变成运维开关。

每次模型调用前为输入估计及最大输出预留预算，完成后用 Provider usage 结算；没有完整 usage 时保留预留并设置 usage_complete=false。默认输入估计使用序列化 UTF-8 字节数加固定协议开销的保守估计，有已验证 TokenCounter 时可替换为准确计数；硬输入限制始终按实际字节执行。累计输出未报告时按已发送的最大输出上限计入，不能靠缺 usage 无限调用。

audit_token_budget 是准入与已知用量的运行保护，不保证未知 tokenizer/Provider 的精确实际费用。若 Provider 报告超过预算或忽略输出限制，立即停止后续调用并标记失败；已发生费用不能回滚。用量公开字段为 model_calls、tool_calls、input_tokens、output_tokens、total_tokens、usage_complete，未知项为 null，不能伪造零值；Prompt/策略/工具版本和配置摘要在私有回合元数据中保存，便于重现，不保存密钥或完整 Prompt。

### 11. 数据模型、保留及回退

新增下一编号迁移（当前预计 `000013`），包含 agent_runs、agent_run_evidence、agent_run_blocks、agent_run_events；允许用 JSONB 保存版本化回答元数据，领域类型不能包含 JSONB/SQL 类型。run 关联 conversation 和 user_message，成功时关联 assistant_message；关联约束与唯一索引保证一致性。证据/片段/事件级联依赖 run，run 级联依赖 conversation。失败保留摘要，但工作证据、片段和事件到期清理；成功证据与片段随会话保留，成功事件仍可清除。

删除会话沿原用户锁，在同一事务撤销运行权、清除关联事实及所有新幂等载荷、释放预留，生成原三字段删除标记。故障注入验证整事务回滚，不能只删事件而遗留正文。后台清理固定有界批次，并在最外层提交后才能发取消通知。

down 只允许无新回合事实、无新预留、无新接受去重记录且无关联助手事实时执行；任何已过期但未清理的新事实仍阻止降级。不得通过删除用户数据让 down 成功；标准回退为关闭 generation、保留表和历史。老版本 API 可能忽略新预留并绕过生成历史投影，因此存在新助手事实时禁止直接回退到不理解本迁移的二进制。

### 12. 验收与演进边界

单元/契约测试按五份 delta 的场景实现，覆盖状态、预算、非法调用、注入、无结果及语义支持度。增加 `make integration-agent-runs`：必须提供专用 `_test` PostgreSQL、真实 OpenSearch、Redis 和本机确定性工具调用/流式模型桩。缺环境变量非零退出；不把 skip 当通过，不默认调用收费 Provider。使用两实例/两个执行器竞争及真实 TCP、nginx 验证恢复和分片可见，保留数据库与模型请求次数证据。

框架适配器测试固定本地模型协议，覆盖工具调用循环、成文 NDJSON 拆包/合包、Unicode、流式 usage、取消与缺 finish；运行现有架构测试，禁止 Application 依赖 Eino。新增固定阅读评测子集，包含多步取证、解释、追问、无结果、负反馈隔离、硬约束、伪造引用和 Prompt Injection；此子集为下一 Web/完整评测 change 的输入。

后续演进按独立变更进行：先接 Web 与完整评测，再讨论长期偏好、私人笔记、受控外部检索和写作产物。证据 kind、工具 manifest、上下文来源和版本化应用事件是扩展边界；没有启用对应能力前，助手不能声称已记住偏好或保存产物。实施阶段在 Roadmap 记录这些目标和依赖，不把规划写成当前能力。

## Risks / Trade-offs

- [ReAct 取证再成文增加一次延迟及模型费用] → 共享总预算，记录首个校验片段耗时；无结果和澄清使用模板，不能通过直接流出未校验文本省略成本。
- [模型引用合法仍可能产生错误解释] → 逐字引文匹配、正文取证要求与独立语义评测；不将结构校验描述成事实证明。
- [流中途失败时用户已见部分内容] → 保留 partial 状态、完整成功才写助手消息；下一 Web change 明确未完成展示。
- [文章状态在复核后到网络送达前发生变化] → 短快照复核、历史及重放再次投影，明确不能撤回已发送内容。
- [断线继续执行仍可能消耗费用] → 明确停止接口、回合总 deadline 和账户并发，不能把关闭连接当停止成功。
- [取消跨实例需要传播时间，Provider 也可能忽略取消] → 共享终态立即封禁写入、最多 1 秒检测及依赖 context，租约和总 deadline 兜底。
- [数据库事件轮询和长期成功引用增加开销] → 固定执行/订阅上限、短批量读取、终态事件清理与随会话删除，记录真实依赖测试数据。
- [错误版本的二进制可能绕过新额度或历史投影] → 上线/回退执行版本门槛；关闭生成保留支持投影的新代码，不直接降级旧二进制。
- [外部搜索和写工具扩大数据与权限范围] → 首版不启用，后续分别规定外部网络约束、引用来源和写入授权，不让工具注册自动授予权限。

## Migration Plan

1. 在专用 `_test` 库验证新迁移、约束、空事实 down/up 和有事实拒绝 down；确认没有改写 `000012`。真实环境操作前准备数据库备份与恢复方案。
2. 部署支持新结构及历史投影的代码，保持 generation=false；更新配置与 OpenAPI、nginx 路由，核对基础会话回归和 readiness。
3. 在隔离环境配置独立确定性模型桩并开启生成，执行完整联合验收、两实例和代理链路实验；生产启用另行授权，本 change 不部署。
4. 停用时关闭 generation 并收敛活动回合，保持回合/历史读取和投影；需要二进制回退时仅使用理解新数据的兼容版本。
5. 记录实现版本、配置、实际命令、skip、模型调用数、PIT 与业务反馈不变证据，以及未运行的测试范围；再更新 README/Roadmap 的实际状态。
