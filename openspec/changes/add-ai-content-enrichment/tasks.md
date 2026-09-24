# Tasks

## 1. 依赖与配置边界

- [x] 1.1 引入并固定 CloudWeGo Eino 核心、OpenAI-compatible ChatModel 与 Embedding 组件版本，更新第三方声明，并以 `go mod tidy`、许可证核对和后端构建验证依赖闭包。
- [x] 1.2 为 generation 与 Embedding 增加独立 provider/base URL/API Key/model/profile/version/timeout/budget 配置、YAML 示例和 `VELIS_*` 环境变量覆盖，并以配置优先级、空配置禁用、部分配置拒绝和密钥脱敏测试验证。
- [x] 1.3 定稿并记录保守的分块、并发、调用数、尝试、退避、超时、输出长度和 Token 审计默认值，以配置边界单元测试及 README 配置表验证所有值有界。

## 2. 数据库迁移与持久化模型

- [x] 2.1 新增版本化 SQL migration，扩展 `async_tasks` 的 stage/status、目标 profile、attempt、退避、错误、租约和完成字段，并以空库 up/down、既有 pending/canceled 数据升级及约束测试验证不改写旧迁移。
- [x] 2.2 在 migration 中创建不可变 generation result、embedding result、current selection 和 model call 表，使用 `real[]` 向量、外键、目标唯一键和必要索引，并以 PostgreSQL 集成测试验证维度/状态约束、重复目标幂等及含数据 down 明确拒绝。
- [x] 2.3 扩展任务投影存储，使新修订、下架、删除、恢复和重复乱序事件正确重置 stage、递增 generation、清除租约或保持 noop，并运行现有 `asynctask` 单元测试及新增状态收敛测试。
- [x] 2.4 实现修订输入、版本化结果、独立 current generation/current embedding 指针和模型调用记录的 PostgreSQL Repository，并以事务测试验证同修订 last-known-good、跨修订不可见和迟到 profile 不覆盖。

## 3. 有界生成与 Embedding Workflow

- [x] 3.1 实现版本化文本规范化、确定性 JSON 输入哈希、段落优先 Unicode 安全分块、最大分块选段和预算计数，并以短文、超长文、多字节文本及重复运行一致性的纯单元测试验证。
- [x] 3.2 实现结构化输出模型及严格 JSON/Schema/长度/数量/去重校验，覆盖 summary、1–12 个关键词、1–5 个自由主题和 Prompt/Workflow 版本，并以围栏、额外字段、空值、过长和重复标签测试验证全部拒绝路径。
- [x] 3.3 使用 Eino 编译短文单次生成和长文 map-reduce Workflow，限制分块并发、调用数与总超时，并以可编程确定性 ChatModel 验证两条路径、截断标记、预算中止和上下文取消。
- [x] 3.4 实现“标题 + AI 增强结果 + 有界正文片段”的版本化检索文档与 `embedding_input_hash`，并以字段顺序、正文选段、输入版本和增强结果变化测试验证哈希语义。
- [x] 3.5 使用 Eino Embedding 组件实现向量生成及空向量、预期 dimensions、`NaN/Inf` 校验，并以确定性 Embedder 验证合法向量持久化输入和所有非法输出分类。
- [x] 3.6 实现两个独立 OpenAI-compatible Eino 适配器、每次调用隔离的 usage/耗时采集和 HTTP/SDK 错误归一化，并以 `httptest` 兼容端点验证不同 base URL/API Key、nullable usage、超时、限流、5xx、鉴权错误和日志脱敏，不调用公网。

## 4. 租约执行器与失败语义

- [x] 4.1 实现通过短事务和 `FOR UPDATE SKIP LOCKED` 认领到期任务、生成唯一 lease token、续接过期租约及停止领取，并以多执行器 PostgreSQL 测试验证同一 generation 单一当前 token 且模型调用期间无长事务。
- [x] 4.2 实现 generation 阶段执行、调用审计、不可变结果写入、current generation 条件切换以及向 Embedding 阶段推进，并以成功、重复完成、缺少 Embedding 配置和提交前 generation 变化测试验证。
- [x] 4.3 实现 Embedding 阶段独立重试、结果与 current embedding 条件切换和任务成功终态，并以“generation 已公开但 Embedding 失败”“重试不重复生成”“旧向量保留到新向量成功”测试验证。
- [x] 4.4 实现暂时性错误退避、非法输出有限重试、永久配置/鉴权失败、尝试耗尽、任务总预算和实际 usage 超预算处理，并以注入时钟/抖动的状态机测试验证无高速或无界重试。
- [x] 4.5 实现 task generation + lease token + 当前公开 revision + 目标 profile 的提交 fencing，并以编辑、下架、删除、profile 升级、租约过期后迟到完成的并发集成测试证明产物被丢弃且新状态不被覆盖。
- [x] 4.6 将内容增强执行器作为独立受监管 Worker 组件装配，实现有界关闭和模型故障隔离，并以 bootstrap/supervisor 测试验证空 profile 不装配、单阶段配置可运行、组件失败不停止 Feed/Relay/Consumer 且 AI 不进入核心 readiness。

## 5. 显式补录与升级 CLI

- [x] 5.1 实现按 `generation|embedding|all` 和 `missing-only|outdated-only` 分页选择候选、逐篇锁定复核并幂等推进 generation 的应用服务，以竞态、重复运行、非公开文章和 profile 已满足测试验证。
- [x] 5.2 为 `velis-admin` 增加必填 limit 与 dry-run 的 AI 补录命令，只输出 created/skipped/failed/has-more 等脱敏统计，并以 CLI 测试验证 dry-run 零写入、缺少上限拒绝、命令不直接调用模型且输出不含正文/密钥。

## 6. 公共 API 与读取降级

- [x] 6.1 扩展文章读取模型和 PostgreSQL latest/详情查询，仅左连接当前 revision 的 current generation 结果且不读取向量列，并以 Repository 测试验证有结果、null、旧修订、同修订 last-known-good 及排序/游标不变。
- [x] 6.2 更新 OpenAPI，为 `ArticleItem` 和详情增加 required-but-nullable `enhancement` 对象及 summary/keywords/topics/generated_at 边界，并运行 OpenAPI 校验测试确认不公开 provider、model、Prompt、Token、错误、任务状态或向量。
- [x] 6.3 更新 HTTP DTO、presenter 和路由契约测试，使 latest 与详情返回当前增强结果或显式 null，同时运行现有匿名读取、未配置模型和 Request ID/错误信封测试验证兼容降级。

## 7. Web 展示

- [x] 7.1 扩展 TypeScript 文章类型和 API fixture，加入 nullable enhancement，并以类型检查和客户端测试验证 null 与非空响应均可解析。
- [x] 7.2 更新 latest 卡片，优先展示 AI summary、区分关键词与主题并显示“AI 生成”标识，null 时回退 excerpt；以组件/模型测试验证切换和标签边界。
- [x] 7.3 更新文章详情展示 AI 摘要和标签，所有模型文本使用普通文本插值而非 `v-html`，并以包含 HTML 特殊字符的 Web 测试验证转义且正文阅读不受增强缺失影响。

## 8. 观测、集成验证与文档

- [x] 8.1 增加按 stage/status 的任务数量与年龄、认领/重试/成功/失败/stale、模型耗时、Token、错误分类和组件 up 指标及关联日志，并以指标测试验证低基数标签且日志不含正文、Prompt、原始输出、完整 URL 或密钥。
- [x] 8.2 扩展 PostgreSQL/RabbitMQ 真实依赖测试，覆盖事件到任务到 generation/Embedding 的成功闭环、Worker 崩溃租约恢复、重复投递、MQ/模型中断、旧修订丢弃和公开读取降级，并记录可复现测试命令与限制。
- [x] 8.3 增加使用确定性 ChatModel/Embedder 的 I3 可复现演示或 E2E，验证发布立即进入 latest、随后出现 AI 摘要/标签、向量已持久化而无检索 API、编辑后旧结果立即隐藏，以及无模型配置仍可发布阅读。
- [x] 8.4 更新 README、配置示例、迁移/回滚说明和 Roadmap 当前状态，明确已实现 AI 增强但 OpenSearch/检索仍未实现，并核对所有仓库文档、注释、日志和错误消息使用简体中文。
- [x] 8.5 运行 `openspec validate add-ai-content-enrichment --strict`、迁移测试、后端单元/竞态/构建、Web lint/test/build 和适用的真实依赖测试；记录任何因环境缺失而未执行的验证，且不得把跳过描述为通过。
