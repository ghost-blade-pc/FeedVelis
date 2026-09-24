# Design

## Context

现有文章发布事务已经通过 Outbox、RabbitMQ 和 `async-task-projector.v1` 收敛为每篇文章唯一的 `article.enrichment` 槽位。槽位固定 `revision_id`、`content_hash` 与单调 generation，但数据库和应用模型目前只有 `pending/canceled`，没有执行租约、阶段、退避或结果表。文章不可变修订已经保存标题、语言、纯文本和原始 `excerpt`；匿名 latest/详情直接读取当前修订，Web 只展示 `excerpt`。

本设计遵守现有四层依赖、版本化 SQL 迁移和核心路径降级约束。模型调用是不可事务化的外部副作用；生成与 Embedding 可能来自不同 OpenAI-compatible 服务，且兼容端点的 JSON Schema、usage 和错误体支持程度并不一致。

## Goals / Non-Goals

**Goals:**

- 在不延长文章写事务的前提下可靠执行 generation 与 Embedding，并用数据库 fencing 吸收重复调用和迟到完成。
- 让结构化生成结果可先于 Embedding 公开，同时让失败阶段独立恢复。
- 以不可变结果和显式当前指针支持同修订 last-known-good、profile 升级、审计和 I4 重建。
- 给所有模型调用设置确定性输入、有限资源边界、稳定错误分类和不含敏感内容的观测。
- 让默认 CI 经过真实 Eino 编排路径但不访问网络。

**Non-Goals:**

- 不实现 OpenSearch 索引、向量查询、BM25/KNN/RRF、recommend Feed 或 Agent。
- 不把模型变成 API/RSS 写路径、核心 readiness 或文章可见性的必要依赖。
- 不支持供应商专用协议；本 change 只装配 OpenAI-compatible Chat Completions 与 Embeddings。
- 不引入主题词表、人工审核、Prompt 在线编辑或自动全量重算。
- 不使用 PostgreSQL 向量运算，也不清理遗留 pgvector 扩展。

## Decisions

### 1. Application 管理执行语义，Eino 适配器管理模型 Workflow

新增内容增强应用模块，定义修订读取、任务认领、结果存储、生成 Workflow 与 Embedder 端口。Application 服务负责任务状态机、预算、错误决策和事务边界；Infrastructure 的 Eino 适配器实现生成与 Embedding 端口，PostgreSQL 适配器实现任务和结果存储。Worker bootstrap 只负责按配置装配受监管组件。

Eino Workflow 在启动时编译，内部使用确定性 Lambda 完成规范化、分段、检索文档构造和严格校验，并使用 ChatModel/Embedding 组件调用外部服务。模型调用期间不持有数据库事务或行锁。

选择该边界是为了让业务可靠性不依赖 Eino checkpoint 的具体实现，也不让 Eino 或供应商 SDK 类型进入 Domain/Application。备选方案是把认领、持久化和重试作为 Workflow 节点，但这会把数据库事务、租约语义和框架运行状态耦合在一起。

### 2. 生成与 Embedding 是两个独立 OpenAI-compatible profile

配置分别提供稳定 provider 标签、base URL、API Key、model、超时和阶段预算；Embedding 额外配置预期 dimensions。两者复用 Eino 的 OpenAI-compatible 组件类型，但创建独立客户端，允许 DeepSeek 等服务生成内容、另一兼容服务生成向量。

空 profile 表示阶段未启用；API Key 只从 `VELIS_*` 环境变量等既有配置入口注入。若一组 profile 出现“部分字段已填写但不足以安全调用”的情况，配置加载应明确报错，而不是静默连向默认公网端点。数据库只保存 provider、model 和版本身份，不保存密钥或完整 base URL；日志最多输出经过安全解析的协议与主机摘要。

选择独立 profile 而不是单一 provider，是因为 Chat Completions 兼容并不意味着同一服务提供 Embeddings。供应商专用 Eino 组件被排除，以缩小配置和错误映射矩阵。

### 3. 有界分层生成使用确定性输入和严格本地校验

生成输入由固定 revision 的标题、语言和 `plain_text` 按版本化规则规范化，并编码为确定性 JSON 后计算 SHA-256 `generation_input_hash`。短文走单次最终生成；长文按段落优先、Unicode 安全的确定性边界切分，在最大分块数和并发内生成中间摘要，再通过一次 reduce 调用产生最终对象。超出最大分块数时按版本化规则选择正文片段并设置 `input_truncated`。

最终输出只允许 `summary`、`keywords`、`topics`。本地严格 JSON 解码禁止额外字段和围栏修复；规范化后检查摘要非空和长度、数组数量、单项长度与去重。建议初始边界为摘要最多 1000 个 Unicode 字符、关键词 1–12 个、主题 1–5 个、标签各 1–64 个字符，具体 Prompt 和输入预算作为带默认值的部署配置记录在 README。

不把供应商的 `response_format` 当作可信边界：适配器可在兼容时请求结构化输出，但结果总要经过本地校验。首版不跨 generation attempt 持久化分块中间文本；一次失败可能重做 map 调用，但任务级尝试、调用数和 Token 审计预算会阻止无界成本。generation 最终结果一旦成功持久化，Embedding 重试不会重新运行 generation。

### 4. Embedding 输入是版本化的增强检索文档

检索文档按固定顺序组合标题、当前目标 generation 的摘要、关键词、主题和确定性选取的有界正文片段。字段名、分隔符、正文选段和最大长度由 `embedding_input_version` 定义；确定性编码后计算 `embedding_input_hash`。

Embedder 返回值在入库前检查：只接受一个非空向量，长度必须等于配置 dimensions，每个值必须是有限数。向量以 PostgreSQL `real[]` 保存，不创建向量索引或距离查询。相比只嵌入正文，该方案保留标题与模型提炼的主题；相比只嵌入摘要，又保留正文中的具体术语。代价是 generation 结果变化会产生新的 Embedding 目标，这是通过输入哈希和 profile 版本显式管理的。

### 5. 扩展单槽位任务为带 stage 的租约状态机

`async_tasks` 继续保持每篇文章唯一槽位，并扩展：

- `stage`: `generation` 或 `embedding`；
- `status`: `pending`、`running`、`retry_wait`、`succeeded`、`failed`、`canceled`；
- generation/embedding 目标 profile 版本；
- 分阶段 attempt、`next_attempt_at`、最后错误分类与限长摘要；
- `lease_owner`、UUID `lease_token`、`lease_expires_at`；
- 阶段完成和任务完成时间。

执行器使用短事务和 `FOR UPDATE SKIP LOCKED` 认领到期任务，提交事务后才调用模型。任何结果、重试或终态写入都要求同时匹配任务 ID、generation、lease token、当前公开 revision 和目标 profile。租约到期允许另一个 Worker 重新认领同一 generation；token 防止同 generation 的旧执行者提交。文章修订、下架、删除或显式升级会递增 generation 并清除租约，从而 fence 掉在途执行者。

generation 成功时在同一事务保存不可变结果、切换 generation 当前指针，并将任务推进到 Embedding；若 Embedding 未配置，则当前 generation 目标完成，任务可进入 `succeeded`。Embedding 失败只修改 Embedding 阶段状态。永久错误或尝试耗尽进入 `failed`，只有新文章事实或显式 CLI 推进才重新激活。

### 6. 不可变结果与独立当前指针承载 last-known-good

新增的逻辑存储分为：

- generation results：article/revision、provider/model、workflow/prompt/profile 版本、输入哈希、截断标志、摘要、关键词、主题和时间；
- embedding results：article/revision、所依赖的 generation result、provider/model/profile、输入版本与哈希、dimensions、`real[]` 向量和时间；
- current selection：每篇文章当前选中的 generation result 与 embedding result，可分别切换，但两者都必须属于文章当前 revision；
- model calls：task/generation/stage/attempt、provider/model/版本、输入哈希、nullable usage、耗时、状态、错误分类和脱敏摘要。

结果表用目标身份唯一键吸收同一目标的重复完成；当前指针只在 fenced 事务内更新。Prompt/model 升级时，旧指针保持不变直到新结果成功。新文章 revision 提交后，读取查询以 `current_revision_id` 再校验指针，因此旧 revision 结果无需同步删除也会立即不可见。generation 与 embedding 指针独立，使新摘要可以立即公开，而旧的同 revision 向量可作为 last-known-good 保留到新向量成功。

不使用“按 created_at 取最后一条”，因为迟到的旧 profile 可能完成得更晚并错误覆盖目标版本。

### 7. 错误分类、预算与调用审计位于应用边界

适配器把 HTTP/SDK 错误归一化为稳定分类：`timeout`、`canceled`、`rate_limited`、`provider_unavailable`、`network`、`invalid_output`、`budget_exceeded`、`input_unsupported`、`authentication`、`configuration`、`internal`。Application 根据分类和分阶段 attempt 决定退避重试或终止。`canceled` 表示调用被上层取消（同批分块的兄弟调用失败，或 Worker 停机取消整个阶段），不是 Provider 故障，必须与 `internal`、`network` 区分并保持可重试，否则会把一次可恢复的取消记成永久失败。

Map 分块允许的就地重试只覆盖传输与 Provider 侧的瞬时分类（`timeout`、`canceled`、`rate_limited`、`provider_unavailable`、`network`）：同一 attempt 内只重试失败的分块，不重新调用已成功的分块，且受剩余调用数、Token 审计预算和阶段取消约束；`invalid_output` 等需要重新生成或纠正内容的分类仍交由任务级退避重试处理。

调用前硬限制使用可跨兼容供应商确定执行的单位：UTF-8/Unicode 输入边界、分块数、调用数、并发、单次输出 Token 和 wall-clock timeout。供应商返回的 usage 作为准确调用记录和调用后审计；未返回时保持 NULL，不用字符估算伪造 Token。若实际 usage 使任务超过审计预算，停止后续调用并分类为预算耗尽。所有日志错误均限长并脱敏，不记录文章文本、Prompt、原始模型输出或密钥。

### 8. Profile 升级只由显式选择范围的 CLI 推进

管理 CLI 支持 `generation|embedding|all`、`missing-only|outdated-only` 和 dry-run，并要求在三类范围选择器中恰好选择一种：`article-id` 精确处理单篇当前公开文章；`limit` 以 `oldest|newest` 顺序处理有限批次，默认 `oldest` 保持兼容；`all` 处理命令开始时符合条件的全部候选。非 dry-run 的全量操作还必须显式传入 `confirm-all`，避免拼写错误或部署脚本意外排入大量真实模型任务。CLI 只设置异步目标，不同步调用模型。

批次与全量模式在 Repository 层使用稳定游标和固定内部页大小选择候选，在逐篇短事务中锁定并复核文章仍公开且 revision 未变。具有相同 revision、stage 和目标 profile 的 `pending`、`running` 或 `retry_wait` 任务不再作为候选，避免重复命令或全量循环一直命中已排队任务；`failed` 任务仍是显式补录候选，可以递增 task generation、清除旧租约与纠正额度并重新执行。`article-id` 不依赖排序或分页，目标不存在、不公开或不符合 mode 时以脱敏统计表示跳过。

`stage=all` 且 generation 需要重新执行时，同时写入当前 generation 与 Embedding profile 目标，使 generation 成功后能够被当前 Embedding Worker 继续领取，不得遗留旧 embedding profile。若 generation 已满足而仅缺少或落后 Embedding，则只推进 Embedding 阶段。部署新 profile 本身不扫描或修改任务；重复对同一修订和目标 profile 执行命令为 noop。

全量模式虽然在数据库侧按固定小批次和短事务推进，但其业务范围是显式无上限的，可能最终产生与候选文章数相同量级的异步模型费用；因此只接受人工提供 `all + confirm-all`，输出累计 created/skipped/failed 和最终 has-more，不把内部页大小伪装成费用上限。该选择满足停用 AI 后重新开启时的一次性存量补齐，同时保持部署不会自动重算。

### 9. 公共读取通过轻量指针关联，绝不加载向量

文章列表和详情查询在 `articles.current_revision_id` 匹配时左连接 current generation 指针与结果，只读取 summary、keywords、topics 和 generated_at。响应始终包含 nullable `enhancement`；没有匹配结果时保持 `excerpt`。查询不连接 embedding result 的向量列，避免 latest 页把大数组带入内存。

Web 卡片和详情优先展示 AI summary，并分别展示关键词与主题以及“AI 生成”标识；为空则沿用 `excerpt`。所有字段经 Vue 文本插值渲染，不使用 `v-html`。AI 结果到达不会修改有效发布时间或游标。

### 10. 确定性替身经过同一 Eino 图

测试替身直接实现 Eino ChatModel 与 Embedder 接口，按输入哈希返回固定结构、固定 usage 和固定向量，并可注入延迟、错误、非法 JSON、非法维度及非有限数。单元和默认集成测试使用替身编译并运行同一 Workflow；真实端点测试必须显式提供环境配置且不属于默认 CI。

观测增加低基数指标：任务按 stage/status 的数量与年龄、认领/重试/成功/失败/stale 计数、调用耗时、Token、错误分类和组件 up。task/event/trace ID 只用于结构化日志关联，不作为 Prometheus label。

### 11. Prompt 是默认结构化边界，Provider 能力只按 profile 显式开启

generation profile 增加 `structured_output_mode=prompt|json_object|json_schema`，默认 `prompt`。三种模式都使用由同一输出边界生成的完整 Prompt，明确字段、类型、摘要字符数、关键词/主题数量、单项长度、去重以及文章文本不可信；`json_object` 和 `json_schema` 只是在 Provider 明确兼容时额外发送对应 `response_format`，本地严格校验始终是最终可信边界。Provider 拒绝所选模式时不自动降级，避免同一 profile 混用不同执行语义；切换模式时运维必须升级 generation profile version。

分块摘要与最终结构化输出使用两个 ChatModel 实例：Map 实例固定文本响应，Final 实例按 profile 配置结构化能力，`generation_single`、`generation_reduce` 与 `generation_repair` 共用 Final 实例。两者使用相同端点、密钥和 model；`prompt` 模式可在内部安全复用底层实例。相比根据 Prompt 内容动态修改请求载荷，双实例不会依赖任务标记文本，也不会误把 JSON Schema 施加到纯文本 Map 调用。

Map Prompt 增加明确的摘要字符上限，返回后本地校验非空、控制字符和长度，防止多个无界分块输出使 Reduce 输入膨胀。标题、语言和正文继续使用确定性编码传入，并在高优先级指令中声明为不可信数据，降低 RSS 或投稿正文中的提示注入影响。

### 12. 最终非法输出只有一次跨重试持久化纠正额度

格式纠正只适用于 `generation_single` 或 `generation_reduce` 的最终输出，不用于 Map。Generator 首次调用返回经过分类的安全校验原因、仅驻留内存且有硬字符上限的原始候选以及调用审计；Application 在记录首次调用后检查剩余调用数、实际 Token 和共享阶段 deadline，预算足够时才通过短事务预占纠正额度，再调用独立的 Repair 端口。Repair 结果仍经过原严格解析器，不在本地剥离围栏、忽略字段或猜测 JSON。

`async_tasks` 为当前 task generation 保存 nullable `generation_repair_used_at`。预占条件同时匹配任务 ID、generation、lease token、当前公开 revision、generation profile 和未使用状态；generation attempt 重试不清除该字段，任务 generation 因新修订、重新发布或显式 profile 推进而递增时清空。预占提交后执行者即使在调用或审计前退出，额度也保持已使用，从而实现外部纠正调用的 at-most-once，而不是为了成功进行重复计费。

现有 Workflow 把首次调用、解析和结果封装在单个 Generator 方法中，无法在纠正前安全预占数据库额度。实现时将应用端口拆为首次生成与纠正两个操作，让 Application 持有短暂的内存候选并编排预占；原始输出不得进入数据库、日志或指标。整个 attempt 使用同一个阶段 deadline，Repair 不重新获得完整预算；它同时计入 `max_calls` 与 Token 审计预算。若剩余预算不足则不预占，直接以稳定预算分类结束。

### 13. Embedding 预期维度与可选请求参数分离

Embedding profile 保留必填 `dimensions` 作为响应向量的预期维度，并新增默认关闭的 `request_dimensions`。适配器只有在后者为 true 时才向 Provider 发送 `dimensions`，但无论是否发送都必须按预期维度、非空和有限数校验响应。固定维度模型因此无需接受其不支持的请求参数，可变维度模型则能显式请求目标维度；改变任一语义都要求升级 embedding profile version。

当前部署使用的 Cloudflare `@cf/baai/bge-m3` 应配置预期 1024 维且不发送 `dimensions`。这是部署值而非代码中的 provider 特判，通用适配器仍只依据 profile 行为。

### 14. Chat 与 Embedding SDK 错误统一归一化并保留安全原因

错误分类器同时识别 ChatModel 暴露的 API 错误、Embedding 底层客户端的 API/Request 错误以及标准 context/net 错误，再按 HTTP 状态映射：401/403 为 `authentication`，408 或 deadline 为 `timeout`，429 为 `rate_limited`，5xx 为 `provider_unavailable`，其它 4xx 为 `input_unsupported`，连接与 DNS 故障为 `network`，无法识别的错误才是 `internal`。context 取消映射为 `canceled`，且该判定必须先于 net 判定：SDK 客户端的 `url.Error` 同时满足 `net.Error`，否则取消会被错误地记成 `network`，或落到 `internal` 而被当作不可重试的永久失败。原始 Provider 错误体不进入业务表或日志。

严格输出校验额外产生固定低基数原因，如 `json_syntax`、`extra_content`、`unknown_field`、`summary_too_long`、`label_count`、`label_too_long` 和 `duplicate_label`。稳定错误大类仍为 `invalid_output` 并用于指标和重试决策；安全原因只用于调用审计与结构化诊断，模型原始输出不保存。

### 15. 输出上限参数名是部署值，且上限值必须覆盖真实输出

`max_output_tokens` 只有以 Provider 真正识别的参数名发送才会生效。OpenAI-compatible 实现对 `max_tokens` 与 `max_completion_tokens` 的支持并不一致，而发错参数名不会报错，只会让上限静默失效：输出无界、单次调用被拖到超时，审计预算与延迟估算同时失真。因此生成 profile 显式选择参数名，默认 `max_tokens`（兼容性最高），与结构化输出模式的处理保持一致；部署侧应按实测确认，不得依赖猜测。

参数名生效后，上限值必须覆盖真实输出，否则问题会从"输出无界"翻转为"合法结果被截断并判非法输出"。实测本部署的最终生成输出为 926–1604 Token、单次生成为 285–2424 Token，而原默认值 1200 落在该区间之内，一旦生效就会截断现在能成功的结果。默认值因此提高到 3072，并同时约束分块与最终调用；分块摘要的 800 字符边界仍由本地严格校验负责，不随上限提高而放宽。

## Risks / Trade-offs

- [OpenAI-compatible 实现细节不完全一致] → 默认使用 Prompt 模式，本地严格校验、nullable usage、可配置 base URL/模型和稳定错误映射；只有运维显式声明时才发送 JSON Object/Schema 或 Embedding dimensions。
- [模型调用已计费但执行者在提交前崩溃] → 接受至少一次外部调用，用租约与不可变结果唯一键保证至多一次业务选择，并通过调用/重试预算限制损失。
- [纠正额度预占后执行者在调用前崩溃] → 选择 at-most-once 成本语义，宁可失去该 generation 的纠正机会也不在租约恢复后重复计费；后续普通 attempt 仍可运行。
- [长文重试会重复分块调用] → 首版不持久化中间文本以控制数据模型复杂度；同一 attempt 内只对瞬时分类失败的分块就地重试，不重跑已成功的分块，任务级重试仍会重跑全部分块；限制总尝试和调用预算，generation 成功后不因 Embedding 失败重做。
- [正文提示注入影响模型服从性] → 将文章字段确定性编码为不可信数据、使用高优先级完整 Schema 指令并保持严格本地校验；不声称模型侧提示能构成绝对安全边界。
- [普通浮点数组占用 PostgreSQL 空间] → 本阶段只保存每个版本一个文章向量且公共查询不加载；I4 验证 OpenSearch 后再制定历史向量保留策略。
- [自由主题存在同义词和漂移] → 用数量/长度/去重约束和 prompt_version 隔离语义，受控词表留给后续 change。
- [current generation 与 current embedding 短时来自不同 profile] → 两者均绑定同一 revision 且独立标记版本；公共 API 只读 generation，I4 明确读取 current embedding。
- [新增任务状态使旧二进制不理解终态] → 数据库迁移先行；应用回滚保留新表和状态，不执行破坏性 down，旧 Worker 只停止 AI 执行且文章主链路继续可用。

## Migration Plan

1. 新增版本化 SQL migration，扩展 `async_tasks` 检查约束和租约/阶段字段，并创建 generation、embedding、current selection 与 model call 表及必要索引；迁移时把现有 `pending/canceled` 槽位映射到 generation 阶段且不改变 generation。
2. 已执行 `000007` 后通过新的 `000008` 增加 generation 纠正额度字段、`generation_repair` 调用类型和安全诊断所需元数据；不得改写 `000007`。先部署迁移，再部署理解新约束的 Worker。
3. 部署支持新 schema 的 API、Worker 和 CLI，但保持两个 AI profile 默认未配置；验证文章发布、latest、详情、Relay 与 Consumer 回归。
4. 为修复后的 Prompt/Workflow/结构化模式使用新的 generation profile version，先以 dry-run 查看 `missing-only` 与 `outdated-only`，再按小批 limit 补录并观察调用、预算、纠正额度、错误和 stale 指标；Web/API 在结果到达后自然显示增强内容。
5. 为固定维度 Embedding 配置正确预期维数、关闭请求维数并升级 embedding profile version；按 `missing-only` 补齐失败向量、按 `outdated-only` 升级旧向量，抽样验证维度、有限数、输入哈希和数据库体积。
6. 只有在 I4 change 中才读取 current embedding 建立 OpenSearch 投影。

回滚时先停内容增强执行器，再回滚应用；保留新增 migration 和已生成结果不会影响旧 API 的既有字段。down migration 仅允许在增强结果、调用记录为空且所有任务已转换回旧兼容状态时执行，否则明确拒绝；不得 force 或删除事实绕过保护。API 新字段为附加字段，旧 Web/客户端可忽略。
