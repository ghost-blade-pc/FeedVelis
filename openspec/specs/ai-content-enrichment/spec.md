# ai-content-enrichment Specification

## Purpose

定义公开文章基于固定修订生成摘要、关键词、自由主题和语义向量的行为，以及版本化结果、模型调用元数据、预算与失败降级契约，使内容增强可重试、可审计且不成为发布和阅读的必要依赖。

## Requirements

### Requirement: 生成与 Embedding 使用独立的 OpenAI-compatible profile

系统 SHALL 将内容生成和 Embedding 作为两个可独立配置的 OpenAI-compatible profile；每个 profile SHALL 具有稳定 provider 名称、`base_url`、密钥、model、超时和阶段预算，密钥不得持久化到业务表、返回给客户端或写入日志。

#### Scenario: 两个阶段使用不同服务
- **WHEN** 生成与 Embedding 配置了不同的 `base_url`、密钥和 model
- **THEN** 系统 SHALL 分别调用对应端点，并在各自调用元数据中记录其 provider 与 model

#### Scenario: 只配置生成 profile
- **WHEN** 生成 profile 完整而 Embedding profile 未配置
- **THEN** 系统 SHALL 生成并公开摘要、关键词和主题，且不得因缺少 Embedding 配置把生成结果判为失败

#### Scenario: 两个 profile 均未配置
- **WHEN** 生成与 Embedding profile 均未配置
- **THEN** 系统 SHALL 不启动模型执行阶段，文章发布、latest、详情和核心 readiness SHALL 正常工作

### Requirement: 内容生成采用有界分层 Workflow

系统 SHALL 对固定文章修订的标题、语言和纯文本执行确定性规范化与分段；能在单次输入预算内处理的文章 SHALL 直接生成最终结果，超出单次预算的文章 SHALL 在最大分块数、最大调用数、并发和总超时约束内先生成分块摘要再汇总，且不得把无界正文发送给模型。

#### Scenario: 短文章单次生成
- **WHEN** 规范化文章输入未超过单次输入预算
- **THEN** 系统 SHALL 通过一次最终生成调用产生摘要、关键词和主题

#### Scenario: 长文章分层生成
- **WHEN** 规范化文章输入超过单次输入预算但可在任务总预算内分段
- **THEN** 系统 SHALL 按确定性边界生成有界分块摘要，并只以这些摘要及规定上下文汇总最终结果

#### Scenario: 分块以瞬时分类失败
- **WHEN** 某个分块调用以传输或 Provider 侧的瞬时分类失败，而其余分块已成功且仍有剩余调用数预算
- **THEN** 系统 SHALL 只对该分块就地重试，不得重新调用已成功的分块，也不得因此超出调用数或 Token 审计预算；阶段已被取消时 SHALL 不重试

#### Scenario: 文章超过最大分块数
- **WHEN** 文章分段数量超过配置的最大分块数
- **THEN** 系统 SHALL 使用版本化的确定性选段规则限制输入、记录 `input_truncated=true`，并不得通过增加无界调用绕过预算

#### Scenario: 任务总预算不足
- **WHEN** 执行前或执行中可判定剩余调用数、输入限制或总时限不足以完成 Workflow
- **THEN** 系统 SHALL 停止后续模型调用，以稳定的预算错误分类结束本次尝试且不得发布不完整结果

### Requirement: 结构化增强输出严格校验

最终生成结果 SHALL 是仅包含摘要、关键词和自由主题的结构化对象；摘要 SHALL 为非空且长度有界的纯文本，关键词和主题 SHALL 在 Unicode 规范化、去除首尾空白后满足配置的数量与单项长度上限并去重，主题语义 SHALL 由 `prompt_version` 标识。

#### Scenario: 合法结构化输出
- **WHEN** 模型返回满足 Schema、长度、数量和唯一性约束的摘要、关键词和主题
- **THEN** 系统 SHALL 接受并保存规范化结果，且保留产生该结果的 Prompt 与 Workflow 版本

#### Scenario: 模型返回非法 JSON 或额外字段
- **WHEN** 模型返回无法严格解析的 JSON、缺失必需字段、额外字段或错误字段类型
- **THEN** 系统 SHALL 将调用分类为 `invalid_output`，不得从 Markdown 围栏或解释文本中猜测并发布结果

#### Scenario: 输出违反内容边界
- **WHEN** 摘要为空或过长，或关键词、主题为空、过多、过长或规范化后重复
- **THEN** 系统 SHALL 拒绝整个最终生成结果并按有限非法输出策略处理

#### Scenario: 正文包含提示注入文本
- **WHEN** 标题、RSS 正文或用户正文包含要求改变任务、泄露 Prompt 或输出其它格式的指令性文本
- **THEN** 系统 SHALL 将其作为不可信文章数据而非系统指令处理，最终结果仍 SHALL 只接受规定的结构化对象

### Requirement: 结构化输出能力按 profile 显式选择且本地校验始终生效

generation profile SHALL 显式选择 `prompt`、`json_object` 或 `json_schema` 结构化输出模式并默认使用兼容性最高的 `prompt`；系统 SHALL 只对最终生成与格式纠正调用应用所选模式，分块摘要 SHALL 保持纯文本，并且任何模式的响应都 SHALL 经过同一严格本地校验。

#### Scenario: 默认使用 Prompt 模式
- **WHEN** 部署未显式选择结构化输出能力
- **THEN** 系统 SHALL 不向 Provider 发送可选的结构化输出参数，而 SHALL 通过包含完整字段、类型、数量和长度边界的 Prompt 请求结果

#### Scenario: Provider 支持 JSON Object
- **WHEN** profile 显式选择 `json_object`
- **THEN** 最终生成与格式纠正调用 SHALL 请求 JSON Object，分块摘要调用 SHALL 不请求 JSON，且本地严格校验 SHALL 保持不变

#### Scenario: Provider 支持 JSON Schema
- **WHEN** profile 显式选择 `json_schema`
- **THEN** 最终生成与格式纠正调用 SHALL 请求与当前输出边界一致的 Schema，分块摘要调用 SHALL 不请求 JSON，且本地严格校验 SHALL 保持不变

#### Scenario: 输出上限参数名按部署选择
- **WHEN** Provider 只接受 `max_tokens` 或 `max_completion_tokens` 中的一种作为输出上限参数名
- **THEN** 系统 SHALL 只发送部署显式选择的参数名，不得让输出上限静默失效；未显式配置时 SHALL 使用兼容性最高的 `max_tokens`，且发送的上限值 SHALL 覆盖当前真实输出长度

#### Scenario: Provider 不支持所选模式
- **WHEN** Provider 拒绝 profile 显式选择的结构化输出参数
- **THEN** 系统 SHALL 以稳定配置或输入错误终止该目标，不得静默回退到另一模式或在相同 profile 下混用执行语义

### Requirement: 最终非法输出具有一次性有界格式纠正机会

系统 SHALL 允许对 `generation_single` 或 `generation_reduce` 的最终非法输出执行格式纠正，但同一 `(task_id, task_generation)` 在全部重试期间至多调用一次；纠正 SHALL 在剩余调用数、Token 和阶段时限预算内执行，结果 SHALL 再次经过同一严格本地校验，模型原始输出不得持久化或写入日志。

#### Scenario: 首次最终输出可纠正
- **WHEN** 最终输出未通过严格校验、该 task generation 尚未使用纠正额度且剩余预算足够
- **THEN** 系统 SHALL 原子预占纠正额度，使用安全校验原因和有界的内存中原始输出执行一次 `generation_repair`，并仅在纠正结果通过严格校验后发布

#### Scenario: 纠正结果仍然非法
- **WHEN** `generation_repair` 的结果未通过严格校验
- **THEN** 当前 attempt SHALL 按 `invalid_output` 失败，后续普通 attempt MAY 在既有最大尝试次数内运行，但不得再次执行格式纠正

#### Scenario: 纠正预算不足
- **WHEN** 最终输出非法但剩余调用数、Token 或阶段时限不足以完成纠正
- **THEN** 系统 SHALL 停止后续模型调用并按稳定预算分类结束本次 attempt，不得预占或透支纠正额度

#### Scenario: 纠正输入超过硬边界
- **WHEN** 待纠正的模型原始输出超过配置的纠正输入字符上限
- **THEN** 系统 SHALL 拒绝纠正调用并以稳定的非法输出或预算分类结束本次 attempt，不得把无界原始输出再次发送给模型

### Requirement: Embedding 基于版本化检索文档生成并持久化

系统 SHALL 在当前修订的结构化增强结果成功后，以固定顺序组合标题、摘要、关键词、主题和确定性选取的有界正文片段，形成由 `embedding_input_version` 标识的检索文档；Embedding profile SHALL 分别声明预期返回维度以及是否向 Provider 发送可选 `dimensions` 请求参数；系统 SHALL 保存输入哈希、Embedding profile/version、预期维度和普通浮点向量，而不得在本阶段提供向量查询。

#### Scenario: 生成合法向量
- **WHEN** 当前增强结果有效且 Embedding 服务返回预期维度的有限浮点数向量
- **THEN** 系统 SHALL 保存不可变的版本化向量、输入哈希和调用元数据，并使其可供后续索引投影读取

#### Scenario: 向量维度或数值非法
- **WHEN** Embedding 返回空向量、维度不符或包含 `NaN`、正负无穷等非有限数值
- **THEN** 系统 SHALL 将其视为非法输出，不得保存或选为当前向量

#### Scenario: 固定维度模型不接受请求维数
- **WHEN** profile 配置了预期返回维度但未启用发送 `dimensions`
- **THEN** 系统 SHALL 省略 Provider 请求中的 `dimensions` 参数，并仍以预期维度严格校验响应

#### Scenario: 可变维度模型显式请求维数
- **WHEN** profile 明确启用发送 `dimensions`
- **THEN** 系统 SHALL 将预期维度作为请求参数发送，并 SHALL 拒绝与该维度不一致的响应

#### Scenario: Embedding 阶段失败
- **WHEN** 摘要、关键词和主题已经成功但 Embedding 调用失败
- **THEN** 系统 SHALL 保留并公开生成结果，只重试或终结 Embedding 阶段且不得重复生成已经满足目标 profile 的内容

#### Scenario: 检索文档发生变化
- **WHEN** 标题、增强结果、正文选段规则、Embedding model、维度或输入版本中的任一目标身份发生变化
- **THEN** 系统 SHALL 产生不同的 Embedding 目标或输入哈希，不得把旧向量冒充为新目标结果

### Requirement: 增强结果按修订和 profile 版本化切换

系统 SHALL 将生成结果和 Embedding 结果作为绑定文章修订、输入哈希及对应 profile 的不可变版本保存；同一修订升级 profile 时 SHALL 继续提供最近成功结果，直到新目标完全校验并原子切换，而文章当前修订变化后 SHALL 立即停止提供旧修订结果。

#### Scenario: 同一修订升级 Prompt
- **WHEN** 当前修订已有成功的旧 Prompt 结果且新 Prompt 目标尚未完成或失败
- **THEN** 系统 SHALL 继续提供旧结果；新结果成功后 SHALL 原子切换且迟到旧 profile 结果不得反向覆盖

#### Scenario: 文章产生新修订
- **WHEN** 文章当前修订从已有增强结果的 revision 变为新 revision
- **THEN** 系统 SHALL 立即停止公开和选择旧 revision 的生成结果与向量，并在新结果可用前使用阅读降级

#### Scenario: 迟到执行者提交结果
- **WHEN** 模型调用结束时任务 generation、租约、文章可见性或当前 revision 已不再匹配执行开始时的目标
- **THEN** 系统 SHALL 丢弃产物内容，不得改变当前结果选择或任务的新 generation，并 SHALL 记录低基数 stale 结果

### Requirement: 模型调用可审计且错误分类稳定

系统 SHALL 为生成分块、生成汇总、格式纠正和 Embedding 调用记录阶段、attempt、provider、model、Prompt/Workflow/Embedding 版本、结构化输出模式、输入哈希、可用的输入/输出/总 Token、耗时、结果状态和稳定错误分类；非法输出 SHALL 额外记录固定低基数的安全原因；记录与日志 SHALL 不包含 API 密钥、文章正文、完整 Prompt、模型原始输出、Provider 原始错误体或含凭据 URL。

#### Scenario: Provider 返回 Token usage
- **WHEN** OpenAI-compatible 响应包含合法 usage
- **THEN** 系统 SHALL 按调用阶段保存输入、输出和总 Token，并纳入任务级观测统计

#### Scenario: Provider 未返回 Token usage
- **WHEN** 兼容端点成功响应但没有可靠 usage
- **THEN** 系统 SHALL 以未知值记录 Token 字段而不得伪造精确计数，其他结果仍可通过校验后使用

#### Scenario: 调用发生错误
- **WHEN** 调用超时、限流、网络失败、服务端失败、鉴权失败、配置错误、预算耗尽或返回非法输出
- **THEN** 系统 SHALL 映射为稳定低基数错误分类，保存限长脱敏摘要，并据分类决定有限重试或终止

#### Scenario: 调用被上层取消
- **WHEN** 同批分块的兄弟调用失败导致并发取消，或 Worker 停机取消整个阶段
- **THEN** 系统 SHALL 把该调用记录为 `canceled` 分类并保持其可重试，不得归类为 `internal` 或 `network`，也不得由此把目标置为永久失败

#### Scenario: Chat 与 Embedding SDK 返回等价 HTTP 错误
- **WHEN** generation 或 Embedding 底层客户端返回相同的 HTTP 状态或网络错误但使用不同 SDK 错误类型
- **THEN** 系统 SHALL 将其归一化为相同的 `authentication`、`timeout`、`rate_limited`、`provider_unavailable`、`input_unsupported` 或 `network` 分类

#### Scenario: 严格校验拒绝最终输出
- **WHEN** 最终输出因 JSON 语法、额外内容、未知字段、摘要边界、标签数量、标签长度或重复标签而被拒绝
- **THEN** 调用审计 SHALL 保存对应固定安全原因且不得保存原始输出，指标标签 SHALL 保持低基数

### Requirement: 重试、超时和预算均有硬边界

系统 SHALL 为每个阶段设置调用超时、任务总超时、并发上限、最大尝试次数、退避上限、输入/分块/调用预算和最大输出 Token；暂时性错误 MAY 在边界内重试，永久错误或已耗尽预算 SHALL 终止该目标，且任何失败均不得回滚文章发布。

#### Scenario: 暂时性 Provider 故障
- **WHEN** 调用发生超时、限流、网络错误或可重试服务端错误且尚有尝试预算
- **THEN** 系统 SHALL 安排有上限退避的阶段重试，不得高速循环

#### Scenario: 永久配置或鉴权错误
- **WHEN** 调用因不完整配置、无效鉴权或不支持的响应契约失败
- **THEN** 系统 SHALL 停止该目标的自动重试并暴露诊断状态，不得影响文章发布和读取

#### Scenario: 实际 Token 超过审计预算
- **WHEN** Provider 返回的实际 usage 超过配置的任务审计预算
- **THEN** 系统 SHALL 记录 `budget_exceeded`，停止任何后续模型调用且不得发布未完成阶段的结果

### Requirement: Profile 升级和存量补齐由显式范围命令触发

系统 SHALL 提供支持 dry-run、阶段选择和显式范围选择的管理 CLI，按 `missing-only` 或 `outdated-only` 为当前公开修订创建或推进目标任务；范围 SHALL 是精确 `article-id`、带 `oldest|newest` 顺序的有限 `limit` 批次或经过二次确认的 `all` 全量操作之一，部署新 Prompt、model 或 Embedding profile SHALL 不自动重算所有文章。

#### Scenario: 预览缺失结果补齐
- **WHEN** 管理员以 dry-run 和数量上限请求预览 generation 或 Embedding 的 `missing-only` 候选
- **THEN** CLI SHALL 报告会处理、跳过和仍剩余的数量，不得修改任务或调用模型

#### Scenario: 推进落后 profile
- **WHEN** 管理员对 `outdated-only` 候选执行有界补录
- **THEN** CLI SHALL 锁定并复核当前公开修订，为指定阶段和当前 profile 推进 task generation，并由正常 Worker 执行而不得同步调用模型

#### Scenario: 重复执行相同补录
- **WHEN** 相同修订已经具有相同目标 profile 的待处理或成功结果
- **THEN** CLI SHALL 将其作为幂等跳过且不得无意义增加 generation

#### Scenario: 精确重试单篇失败文章
- **WHEN** 管理员以 `article-id` 指定一篇仍公开且符合 mode 的失败文章
- **THEN** CLI SHALL 只推进该文章的当前修订，不得因其他候选的 ID 或发布时间影响目标选择

#### Scenario: 按最新顺序执行有限批次
- **WHEN** 管理员以 `limit` 和 `order=newest` 请求补录
- **THEN** CLI SHALL 按文章有效发布时间倒序及文章 ID 稳定打破并列选择不超过 limit 的候选，并准确报告是否仍有未排队候选

#### Scenario: AI 重新启用后全量补齐
- **WHEN** 管理员以 `all` 和 `confirm-all` 对 `missing-only` 执行非 dry-run 补录
- **THEN** CLI SHALL 使用固定内部页大小和逐篇短事务排入命令开始时符合条件的全部公开文章，输出累计脱敏统计，且不得同步调用模型

#### Scenario: 全量补录缺少二次确认
- **WHEN** 管理员对非 dry-run 请求使用 `all` 但未提供 `confirm-all`
- **THEN** CLI SHALL 在任何数据库写入前拒绝请求并提示全量操作可能产生大量模型费用

#### Scenario: 范围选择器冲突或缺失
- **WHEN** 管理员同时提供 `article-id`、`limit`、`all` 中多个选择器或一个都未提供
- **THEN** CLI SHALL 在任何数据库写入前拒绝请求；`order` 只允许与有限 `limit` 批次组合

#### Scenario: 已排队目标不阻塞后续候选
- **WHEN** 候选文章已经存在相同修订、阶段和目标 profile 的 `pending`、`running` 或 `retry_wait` 任务
- **THEN** 批次与全量扫描 SHALL 跳过该文章并继续后续候选，而 `failed` 任务 SHALL 仍可由显式命令重新推进

#### Scenario: 全阶段补录需要重新生成
- **WHEN** 管理员以 `stage=all` 推进缺失或落后的 generation
- **THEN** 任务 SHALL 同时绑定当前 generation 与 Embedding profile，使 generation 成功后由当前 Embedding Worker 继续处理，不得保留旧 embedding 目标

### Requirement: CI 使用可编程确定性模型替身

系统 SHALL 提供不访问网络的确定性 ChatModel 与 Embedder 替身，以相同 Workflow 边界产生固定输出和 usage，并可注入延迟、超时、非法结构、非法向量及稳定错误；默认 CI SHALL 不依赖真实模型账户。

#### Scenario: 默认 CI 验证成功路径
- **WHEN** 测试注入固定生成结果和固定向量
- **THEN** Workflow SHALL 产生可重复的摘要、关键词、主题、向量和调用元数据

#### Scenario: 默认 CI 验证失败路径
- **WHEN** 测试替身注入超时、预算耗尽、非法 JSON、错误维度或 generation 变化
- **THEN** 系统 SHALL 可重复地证明有限重试、错误分类、结果保留和迟到结果丢弃，且不得发出外部网络请求
