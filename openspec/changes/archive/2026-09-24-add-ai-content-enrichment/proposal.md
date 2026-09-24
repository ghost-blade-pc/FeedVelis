# Proposal

## Why

现有可靠异步链路只能把公开文章收敛为待处理任务，尚不能生成真正的摘要、关键词、主题和语义向量，用户仍只能看到由正文开头截断得到的 `excerpt`。现在需要完成 I3 的 AI 内容增强，同时继续保证模型缺失、超时、非法输出或迟到结果不会阻塞文章发布、latest 与正文阅读。

## What Changes

- 在现有 `article.enrichment` 任务槽位上增加带租约、fencing、分阶段重试和预算限制的执行状态机，并用文章修订与 task generation 拒绝迟到结果。
- 使用 CloudWeGo Eino 编排有界分层摘要 Workflow，通过两套可独立配置的 OpenAI-compatible 生成与 Embedding 端点生成摘要、关键词、自由主题和向量。
- 持久化不可变、版本化的增强结果、普通浮点向量及调用元数据，包括 provider、model、Prompt/Workflow/Embedding 版本、输入哈希、Token、耗时和稳定错误分类。
- 对同一修订保留 last-known-good 结果，新 profile 成功后原子切换；文章修订变化后不再公开或投影旧修订结果。
- 在匿名 latest 与文章详情 API 中返回可选增强结果，并在 Web 中优先展示 AI 摘要、关键词和主题；缺失时继续使用原始 `excerpt`。
- 提供可按单篇文章、带顺序的有限批次或经过二次确认的全量范围执行、支持 dry-run 且可重复运行的管理 CLI，显式补齐缺失结果或升级落后 profile，而不在部署时自动产生全量模型费用。
- 提供确定性 ChatModel/Embedder 桩、故障注入测试和脱敏观测；未完整配置模型时不启动对应执行阶段，也不影响核心内容路径或 readiness。
- 本 change 不实现 OpenSearch、向量检索、recommend Feed、Agent，也不恢复 pgvector 查询能力；I4 直接使用本 change 持久化的当前向量构建检索投影。

## Capabilities

### New Capabilities

- `ai-content-enrichment`: 定义 Eino 内容增强 Workflow、生成与 Embedding 契约、版本化结果、模型调用元数据、预算/错误处理、升级补录与降级行为。

### Modified Capabilities

- `reliable-article-async`: 将现有只产生或取消任务的槽位扩展为可安全认领、分阶段执行、重试、完成和丢弃迟到结果的后台任务。
- `unified-articles`: 为匿名 latest 与文章详情增加当前修订的可选 AI 增强结果，并规定 Web 展示与原始 `excerpt` 降级语义。

## Impact

- 后端新增 Eino 与 OpenAI-compatible 组件依赖、AI 配置、应用端口、Worker 执行组件、PostgreSQL 迁移、管理 CLI、指标与测试桩。
- `async_tasks` 状态和认领字段扩展；新增版本化增强结果、向量、当前选择及调用记录等持久化结构，已执行迁移不改写。
- OpenAPI 的文章列表/详情响应新增向后兼容的可选增强对象；Vue latest 与详情页增加 AI 摘要和标签展示。
- API/RSS 核心写路径仍不调用模型；模型密钥不入库、不写日志，生成与 Embedding 可使用不同的 OpenAI-compatible `base_url`、`api_key` 和 `model`。
