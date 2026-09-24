# Spec Delta

## Purpose

定义公开文章基于固定修订生成摘要、关键词、自由主题和语义向量的行为，以及版本化结果、模型调用元数据、预算与失败降级契约，使内容增强可重试、可审计且不成为发布和阅读的必要依赖。

## ADDED Requirements

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

### Requirement: Embedding 基于版本化检索文档生成并持久化

系统 SHALL 在当前修订的结构化增强结果成功后，以固定顺序组合标题、摘要、关键词、主题和确定性选取的有界正文片段，形成由 `embedding_input_version` 标识的检索文档；系统 SHALL 保存其输入哈希、Embedding profile/version、维度和普通浮点向量，而不得在本阶段提供向量查询。

#### Scenario: 生成合法向量
- **WHEN** 当前增强结果有效且 Embedding 服务返回预期维度的有限浮点数向量
- **THEN** 系统 SHALL 保存不可变的版本化向量、输入哈希和调用元数据，并使其可供后续索引投影读取

#### Scenario: 向量维度或数值非法
- **WHEN** Embedding 返回空向量、维度不符或包含 `NaN`、正负无穷等非有限数值
- **THEN** 系统 SHALL 将其视为非法输出，不得保存或选为当前向量

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

系统 SHALL 为生成分块、生成汇总和 Embedding 调用记录阶段、attempt、provider、model、Prompt/Workflow/Embedding 版本、输入哈希、可用的输入/输出/总 Token、耗时、结果状态和稳定错误分类；记录与日志 SHALL 不包含 API 密钥、文章正文、完整 Prompt、模型原始输出或含凭据 URL。

#### Scenario: Provider 返回 Token usage
- **WHEN** OpenAI-compatible 响应包含合法 usage
- **THEN** 系统 SHALL 按调用阶段保存输入、输出和总 Token，并纳入任务级观测统计

#### Scenario: Provider 未返回 Token usage
- **WHEN** 兼容端点成功响应但没有可靠 usage
- **THEN** 系统 SHALL 以未知值记录 Token 字段而不得伪造精确计数，其他结果仍可通过校验后使用

#### Scenario: 调用发生错误
- **WHEN** 调用超时、限流、网络失败、服务端失败、鉴权失败、配置错误、预算耗尽或返回非法输出
- **THEN** 系统 SHALL 映射为稳定低基数错误分类，保存限长脱敏摘要，并据分类决定有限重试或终止

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

### Requirement: Profile 升级和存量补齐由显式有界命令触发

系统 SHALL 提供支持 dry-run、明确数量上限和阶段选择的管理 CLI，按 `missing-only` 或 `outdated-only` 为当前公开修订创建或推进目标任务；部署新 Prompt、model 或 Embedding profile SHALL 不自动重算所有文章。

#### Scenario: 预览缺失结果补齐
- **WHEN** 管理员以 dry-run 和数量上限请求预览 generation 或 Embedding 的 `missing-only` 候选
- **THEN** CLI SHALL 报告会处理、跳过和仍剩余的数量，不得修改任务或调用模型

#### Scenario: 推进落后 profile
- **WHEN** 管理员对 `outdated-only` 候选执行有界补录
- **THEN** CLI SHALL 锁定并复核当前公开修订，为指定阶段和当前 profile 推进 task generation，并由正常 Worker 执行而不得同步调用模型

#### Scenario: 重复执行相同补录
- **WHEN** 相同修订已经具有相同目标 profile 的待处理或成功结果
- **THEN** CLI SHALL 将其作为幂等跳过且不得无意义增加 generation

### Requirement: CI 使用可编程确定性模型替身

系统 SHALL 提供不访问网络的确定性 ChatModel 与 Embedder 替身，以相同 Workflow 边界产生固定输出和 usage，并可注入延迟、超时、非法结构、非法向量及稳定错误；默认 CI SHALL 不依赖真实模型账户。

#### Scenario: 默认 CI 验证成功路径
- **WHEN** 测试注入固定生成结果和固定向量
- **THEN** Workflow SHALL 产生可重复的摘要、关键词、主题、向量和调用元数据

#### Scenario: 默认 CI 验证失败路径
- **WHEN** 测试替身注入超时、预算耗尽、非法 JSON、错误维度或 generation 变化
- **THEN** 系统 SHALL 可重复地证明有限重试、错误分类、结果保留和迟到结果丢弃，且不得发出外部网络请求
