# Velis Roadmap

> 路线基线：2026-09-20。本文是项目目标、技术决策、实施顺序与完成标准的唯一来源；当前已经可用的功能、运行方式和限制见 [README](README.md)。
>
> Velis 是个人后端与 Agent 工程实践项目。路线优先形成小而完整的产品闭环，再围绕真实问题引入中间件、微服务、容器编排、可观测性和性能优化；目录占位、依赖声明或服务启动不等同于能力完成。

## 1. 项目定位

Velis 是一个可自托管的图文 Feed 与智能阅读系统。平台管理员维护少量高质量 RSS/Atom/JSON Feed 来源，所有用户共享由这些来源和用户投稿组成的内容池。系统使用 AI 生成摘要、关键词、主题和 Embedding，通过推荐 Feed 与 Agent 对话帮助用户发现文章。

项目同时承担两个目标：

1. **产品目标**：完成内容采集、用户投稿、AI 增强、推荐 Feed、对话 Agent 和定时 Agent 收件箱的端到端闭环。
2. **学习与作品目标**：从零实践后端工程、Agent 工程、可靠异步、搜索与缓存、微服务演进、Docker/Kubernetes、可观测性、压测和 Go GC 优化，并留下可复现证据。

产品功能保持克制，工程实现追求有依据的深度。每个组件必须对应明确问题、故障边界和验收指标，不以堆叠技术名词作为完成标准。

## 2. 目标业务闭环

目标版本形成五条互相衔接的链路：

1. 管理员登记少量系统级 RSS 来源，系统定时抓取、清洗、去重并自动发布到共享内容池。
2. 登录用户创建 Markdown/图文草稿、上传图片并主动发布；发布后立即进入共享内容池，不经过管理员审核。
3. 已发布文章异步生成摘要、关键词、主题和 Embedding，并同步到 OpenSearch。
4. 用户通过 latest Feed、recommend Feed、搜索和对话 Agent 获取带来源引用的文章。
5. 后续允许用户创建定时 Agent 任务；任务匹配新文章后写入站内 Agent 收件箱，用户上线后查看。

### 首版必须覆盖

- 账户、会话和必要权限；管理员管理系统级 Source。
- RSS/Atom/JSON Feed 抓取、清洗、源内去重、自动发布和抓取状态可见性。
- 用户草稿、图片、直接发布、编辑、下架与软删除；管理员保留全局下架能力。
- latest 与 recommend 两类 Feed；阅读、收藏和“不感兴趣”等少量推荐信号。
- Eino 内容增强、OpenSearch 全文与语义召回、对话 Agent、引用校验和流式输出。
- RabbitMQ、Redis、MinIO、Docker Compose，以及随阶段建设的测试和观测。
- 在业务闭环稳定后进行有边界的微服务拆分、Kubernetes 部署和性能优化。

### 明确不做

- 投稿人工审核、审核队列、内容安全运营平台；不将基础 HTML 清洗、上传校验和管理员下架误删为“审核”。
- 用户自定义 RSS 订阅、OPML 导入和开放互联网爬虫；Source 由平台管理员统一配置。
- following、hot、评论、作者社交、私信、付费、广告、多租户和多地域。
- 首版的邮件、短信、移动系统通知；定时 Agent 仅写入站内收件箱。
- 视频、直播、自训练模型、通用 Agent/MCP 平台。
- 为展示技术而同时引入用途重叠的消息队列、向量库或 RPC 框架。

## 3. 实施原则

### 业务优先级

- 先保证内容能进入、发布、阅读，再建设异步 AI、检索、推荐和 Agent。
- AI 增强失败不能阻塞 RSS 或用户文章发布；无 AI 时 latest 和正文阅读仍可用。
- Agent 只通过受控 Application Service/Tool 访问文章和推荐，不直连数据库或绕过权限。
- 用户投稿不做审核，但必须保留鉴权、资源归属、输入清洗、限额、下架和软删除。

### 工程学习原则

- 从模块化单体出发，在业务闭环与测试稳定后再拆服务，保留拆分前后的架构和性能证据。
- PostgreSQL 保存业务事实；Redis、OpenSearch 和缓存投影均可重建；RabbitMQ 采用至少一次语义。
- 每项中间件都要覆盖正常路径、依赖故障、恢复、幂等和观测，不以“容器能启动”作为完成。
- 可观测性贯穿所有阶段；专门阶段负责补齐全链路、告警、压测、剖析与优化报告。
- GC 和性能参数只基于基准、负载和 pprof/trace 证据调整，记录优化前后数据与代价。

## 4. 已确认技术方向

| 领域 | 方案 | 项目中的用途 |
| --- | --- | --- |
| 服务端 | Go + CloudWeGo Hertz | HTTP API、管理接口与流式 Agent 入口 |
| 初始架构 | 模块化单体、四层依赖、显式 bootstrap | 先建立清晰领域边界，为后续服务拆分保留接口 |
| 微服务通信 | Kitex 或在拆分 change 中确认的单一 RPC 方案 | 服务拆分后的同步调用、超时、熔断和链路传播 |
| 事实存储 | PostgreSQL + pgx + 显式 SQL | 账户、来源、文章、行为、任务、Outbox、AI 和 Agent 事实 |
| 数据迁移 | golang-migrate | 版本化 SQL、前滚/回滚和升级验证；不使用生产 AutoMigrate |
| 缓存 | Redis | 文章/Feed 缓存、限流、短期推荐结果和可重建索引 |
| 异步消息 | RabbitMQ + PostgreSQL Outbox | AI、索引和定时 Agent 任务；至少一次、重试和 DLQ |
| 对象存储 | MinIO/S3 兼容接口 | 用户投稿图片及其归属、确认和生命周期 |
| 搜索与向量 | OpenSearch | BM25、KNN、混合召回和可重建查询投影 |
| AI/Agent | CloudWeGo Eino | 内容增强 Workflow、Embedding、Tool Calling 与 Agent 编排 |
| Web | Vue 3 + TypeScript + Vite + Pinia + Vue Router | 提供最小但完整的管理、投稿、Feed 和 Agent 演示流程 |
| 本地环境 | Docker Compose | 一键启动应用和真实依赖，支持故障与恢复实验 |
| 容器编排 | Kubernetes | 服务部署、探针、资源限制、滚动更新、弹性和故障恢复 |
| 可观测性 | OpenTelemetry、Prometheus、Grafana、Loki、Tempo | Metrics、Logs、Traces 的关联分析与告警 |
| 性能分析 | Go benchmark、pprof、trace、负载测试 | 定位 CPU、内存、锁、分配、GC 和端到端延迟瓶颈 |
| 协作 | OpenSpec | 后续 change 的提案、设计、任务、实施、同步与归档 |

不为简历覆盖面额外引入 Kafka、第二套向量数据库或第二套 RPC 框架。若以后替换组件，应由吞吐、语义、成本或运维证据驱动，并记录迁移和回滚方案。

### 工程组织与契约

- 保留 `backend/`、`web/`、`deploy/`、根 Makefile 和 Compose；没有真实多 module 需求时不增加 `go.work`。
- Domain 仅依赖 Domain；Application 依赖 Application/Domain；Infrastructure 依赖 Infrastructure/Application/Domain；Interfaces 依赖 Interfaces/Application/Domain；bootstrap 负责装配并保持豁免。
- 模块之间通过公开 Application 接口或事件协作，不直接修改其他模块负责的数据；服务拆分后继续沿用同一所有权边界。
- `/api/v1` 使用 snake_case、RFC 3339 时间与 UTC；保留 `/livez`、`/readyz`。列表返回 `items/next_cursor/has_more`，错误保持统一 `error` 信封和 `X-Request-ID`。
- Handler 负责绑定校验、鉴权、调用用例和响应映射；HTTP DTO、Application Input、Domain Entity、RPC/事件 Schema 分离。
- 写接口必须采用 Idempotency-Key 或清晰的资源状态幂等语义；相同键、调用者和接口下请求摘要不同时返回冲突。
- 事件包含 `event_id`、版本化 `event_type`、aggregate 类型/ID/版本、`occurred_at`、`trace_id`、producer 和显式 payload，不把数据库模型直接作为消息契约。
- 配置优先级保持默认值 < YAML < 环境变量；超时、并发、预算和资源限制配置化，真实凭据不提交、不记录日志。
- 当前 OpenAPI 只描述已实现接口；规划路径和字段在对应 change 中定稿后随实现更新。

## 5. 当前基线

以下是路线调整时的仓库事实；持续更新的运行边界以 README 为准。

| 能力 | 当前状态 | 后续处理 |
| --- | --- | --- |
| API、Worker、迁移、管理 CLI、四层目录、CI、健康检查 | 已实现 | 复用并逐步增强观测和部署 |
| Source CLI、调度租约、条件请求、失败退避 | 已实现 | 增加管理 API/Web、抓取历史和按源配置 |
| RSS/Atom/JSON Feed、正文清洗、源内去重 | 已实现 | 接入统一内容模型和可靠异步 |
| 文章列表、站内详情、Vue 阅读页 | 已实现 | 演进为 latest Feed，并补稳定发布时间语义 |
| 账户、JWT、会话轮换、RBAC、限流和管理审计 | I1 已完成 | 作为投稿、管理和 Agent 会话身份基础 |
| 用户投稿、图片资产、Outbox/RabbitMQ 业务 | 未实现 | I2–I3 |
| Redis 业务缓存、推荐信号、recommend Feed | 未实现 | I4 |
| Eino、OpenSearch、对话/定时 Agent | 未实现 | I3–I6 |
| 微服务、Kubernetes、完整可观测性、压测和 GC 报告 | 未实现 | I7–I10 |

当前文章必须关联 Source 和外链，只支持 `published/hidden`；RSS 更新直接覆盖内容。目录中的 Redis、RabbitMQ、MinIO、Eino、向量适配器以及 Compose 服务不代表对应业务已经实现。现有 PostgreSQL `vector` 扩展和 pgvector 镜像是遗留配置，目标向量检索为 OpenSearch，后续迁移不得改写已执行迁移或无条件级联删除扩展。

## 6. 核心领域与数据边界

### 统一内容模型

RSS 内容和用户投稿共享公开文章读取模型，但保留不同来源身份：

- RSS 文章关联 `source` 和来源条目身份，保存原文 URL、原站时间和抓取时间。
- 用户文章关联站内作者，可使用 MinIO 图片，不伪造 Source 或外链。
- 文章至少区分 `draft`、`published`、`offline`、`deleted`；RSS 条目成功入库后自动发布。
- 用户主动发布后立即对所有用户可见；作者可以编辑、下架和软删除自己的文章，管理员可下架任意文章。
- 编辑已发布文章时不得因异步 AI 或索引失败使旧公开内容消失；具体采用版本表还是受控当前版本，在 I2 change 中定稿。
- AI 产物绑定文章内容版本；旧任务结果不得覆盖新内容，删除或下架不得被迟到事件复活。

目标数据组包括：

| 数据组 | 目标职责 |
| --- | --- |
| `users`、会话与审计 | 身份、角色、状态、令牌轮换和管理操作 |
| `sources`、抓取记录 | 系统级来源、租约、条件请求、失败和人工重试 |
| `articles`、内容/版本 | RSS 与用户文章、可见性、作者/来源和稳定发布时间 |
| `article_assets` | 图片归属、上传确认、引用和删除状态 |
| `outbox_events`、`consumed_events`、`async_tasks` | 可靠投递、消费幂等、任务状态、重试与重放 |
| `article_ai_metadata` | 摘要、关键词、主题、Embedding 版本和模型调用元数据 |
| 阅读/收藏/负反馈 | 服务推荐的最小显式与隐式信号，不扩展成社交系统 |
| Agent 会话、消息、定时任务和收件箱 | 对话归属、工具引用、调度条件、推荐结果与已读状态 |

### 去重与时间语义

- 保留源内 `(source_id, dedupe_key)` 唯一约束；external ID 优先，规范 URL 兜底。
- 首版不做复杂跨源自动合并；可记录 canonical URL/正文指纹供候选提示或后续演进。
- 区分原站发布时间、首次发现时间、站内发布时间和内容更新时间。
- latest 使用有效发布时间与 ID 分页：RSS 优先使用原站发布时间、缺失时回退固定站内发布时间，站内投稿使用固定站内发布时间；文章编辑不能把旧文章无条件顶回首页。
- 下架和删除文章必须从 Feed、搜索、推荐、Agent 引用和未读收件箱读取结果中隐藏。

## 7. 关键技术链路

### RSS 内容供给

管理员配置 Source 后，系统执行：认领到期源 → 安全抓取 → 解析/清洗 → 源内去重 → 自动发布 → 写入事件 → 更新抓取记录。

- 复用租约、fencing、ETag/Last-Modified、304 和失败退避。
- 增加来源级周期、域名并发限制、抓取历史和人工重试。
- 仅允许 HTTP(S)，限制 DNS/重定向、地址、总时间和响应体；分别验证直连与环境代理的 SSRF 边界。
- XML、HTML 与远程图片均视为不可信输入；保留来源与原文链接。

### 用户投稿

登录用户执行：创建草稿 → 编辑 Markdown/图文 → 上传并确认图片 → 主动发布 → 进入共享 latest Feed。

- 不存在提交审核、审核拒绝或审核员队列。
- 创建、发布、下架和删除采用 Idempotency-Key 或明确的资源状态幂等语义。
- 编辑使用乐观并发控制；资产必须校验所有者、类型、大小、引用和孤儿清理。
- 管理员下架是维护能力，不演进为内容审核平台。

### 可靠异步与 AI 增强

文章发布/更新与 Outbox 在同一 PostgreSQL 事务提交。Relay 经 RabbitMQ publisher confirm 发布；消费者以 consumer+event 去重，并在业务结果提交后 Ack。

内容增强顺序为：固定文章版本 → 输入规范化/分段 → Eino 生成摘要、关键词和主题 → Schema 校验 → Embedding → 保存版本化元数据 → 触发索引同步。

- 模型调用记录 provider、model/version、Prompt/Workflow 版本、输入哈希、Token、耗时和错误分类。
- 设置并发、总超时、有限重试、DLQ 和预算；CI 使用确定性模型桩。
- AI 失败保留原始摘要/正文并允许重试，不能回滚文章发布。
- 外部副作用采用版本条件和幂等写，不假设数据库与模型/OpenSearch 之间存在分布式事务。

### 搜索与推荐

OpenSearch 使用版本化物理索引和读写别名，保存可从 PostgreSQL 和 AI 元数据重建的文章投影。

- 先交付 BM25 与关键词过滤，再加入 KNN 与应用层 RRF 混合召回。
- 发布、更新、AI 完成触发 upsert；下架和删除触发移除；迟到事件不能复活旧文档。
- 查询结果返回前批量校验 PostgreSQL 可见性；OpenSearch 不反向覆盖业务事实。
- Bulk 逐项处理错误；索引重建覆盖分页读取、增量追赶、校验、别名切换与回滚窗口。
- OpenSearch 整体故障时搜索明确不可用，recommend 降级为 latest；无向量时退化到关键词/BM25。

Feed 只保留：

| 场景 | 候选与排序 | 降级 |
| --- | --- | --- |
| `latest` | PostgreSQL 已发布文章，有效发布时间（RSS 原站时间优先、固定站内时间回退）+ ID | 核心路径，不依赖 Redis、MQ、搜索或模型 |
| `recommend` | 关键词/主题、语义召回、少量用户反馈、新鲜度与来源打散 | BM25/关键词 → latest |

Redis 只保存可重建的文章卡片、Feed ID 页、短期推荐结果和必要限流状态；缓存失效或清空后必须能安全回源。用户相关键按真实身份隔离，批量读取避免 N+1。

### 两层 Agent

第一层是对话内即时推荐：

- 受控工具首版为 `search_articles`、`recommend_articles`、`get_article`。
- Agent 校验会话归属，读取有限历史，提取约束，有界调用工具，以 SSE 返回回答和文章卡片。
- 标题、作者、链接和引用来自工具结果；无结果明确说明，不编造文章。
- 限制工具次数、总超时、Token、返回量；不保存隐藏推理过程。
- 覆盖工具失败、模型超时、断流、取消、越权、Prompt Injection 和无引用拦截。

第二层是定时 Agent：

- 用户创建、暂停、恢复和删除定时任务，描述主题、关键词、频率与结果数量。
- 调度任务只匹配任务上次成功运行后出现的新内容，并记录游标/水位。
- 结果写入站内 Agent 收件箱，包含文章引用、匹配理由、生成时间、任务来源和已读状态。
- 同一任务与文章的重复投递保持幂等；失败有限重试，用户能看到最近状态。
- 首版不发送邮件、短信或系统通知，也不允许 Agent 执行写业务数据的开放式工具。

## 8. 微服务、容器与运行平台

### 演进而不是预拆分

I2–I6 保持模块化单体代码库和清晰模块接口，API 与 Worker 可先作为独立进程部署。完成核心闭环并取得负载、资源和故障证据后，I7 至少实践以下服务边界：

1. **Content API**：账户、文章、Feed 与对外 HTTP；不直接执行模型和抓取任务。
2. **Ingestion Service**：Source 调度、RSS 抓取、解析和抓取记录，可独立限制网络与扩缩容。
3. **Intelligence Service**：AI 增强、检索编排和 Agent；承担模型延迟、Token 预算和计算型负载。

具体拆分数量由 I7 change 定稿，但必须满足：

- 同步调用有版本化契约、deadline、取消、熔断和错误映射；禁止无限重试。
- 异步事件有兼容策略、幂等和 DLQ；Trace Context 跨 HTTP/RPC/MQ 传播。
- 明确数据所有权；服务不直接写其他服务负责的表。若过渡期共享实例，也要按 schema/Repository 边界隔离并记录退出方案。
- 拆分前后使用同一负载验证延迟、资源、故障隔离和运维复杂度，允许得出“不应继续拆分”的结论。

### Docker 与 Kubernetes

- Docker 镜像采用可复现、多阶段、非 root 构建，区分迁移 Job 与常驻进程。
- Docker Compose 负责本地完整依赖、健康检查、持久卷、模型桩和故障实验。
- Kubernetes 提供 Deployment/Stateful 依赖边界、Service、Ingress、ConfigMap/Secret、迁移 Job、startup/liveness/readiness probe、requests/limits、滚动更新和优雅退出。
- 对可水平扩展的无状态服务验证 HPA；对 Worker 验证并发、重复消费和终止期间的任务处理。
- 使用 Helm 或 Kustomize 中的一种，不并行维护两套模板；选择在 I8 change 中确定。
- 不把单机学习环境描述成生产高可用；备份恢复、持久化和外部托管依赖的边界必须写清。

## 9. 分阶段实施与验收

每阶段都交付对应 OpenSpec change、OpenAPI/事件契约、迁移、测试、日志和最小 Web 状态。I0/I1 保留历史完成事实，后续按新目标执行。

| 阶段 | 状态 | 核心交付 | 验收门槛 |
| --- | --- | --- | --- |
| I0：目标与工程基线 | 已完成 | 四层架构、四入口、迁移、CI、健康检查、文档入口 | 当前能力与未来目标分离，基础命令可复现 |
| I1：账户与权限 | 已完成（2026-09-19） | 注册登录、JWT、会话轮换撤销、RBAC、限流、管理 CLI/审计、最小 Web | 正常/过期/撤销/轮换/重复账号/越权测试；管理员初始化可复现 |
| I2：统一内容供给 | 下一阶段 | 用户草稿/图片/直接发布/编辑/下架/删除；管理员 Source API/Web；RSS 与投稿统一公开读取；latest | RSS 自动发布与用户直接发布均可匿名阅读；并发、幂等、权限、资产和历史数据迁移正确；不依赖 MQ/Redis/模型 |
| I3：可靠异步与 AI 增强 | 待实施 | Outbox、RabbitMQ Relay、消费去重、任务状态、retry/DLQ；Eino 摘要/关键词/主题/Embedding | MQ 中断与重复消息可恢复；模型超时/非法输出/旧结果不破坏发布；无密钥仍可使用核心链路 |
| I4：搜索与 recommend Feed | 待实施 | Redis 缓存；OpenSearch BM25/KNN/RRF、索引同步与重建；最小阅读/收藏/负反馈信号 | latest/recommend 分页稳定并能降级；下架不泄露；缓存清空、迟到事件、Bulk 部分失败和重建增量可验证 |
| I5：对话 Agent | 待实施 | 会话/消息、只读工具、SSE、引用、有限记忆、Agent 页面与评测集 | 查询约束和引用正确；无结果、工具失败、超时、断流、取消、越权和注入场景有证据 |
| I6：定时 Agent 与收件箱 | 待实施 | 定时任务 CRUD/调度、水位、幂等匹配、站内收件箱与已读状态 | 新文章只投递一次；暂停/恢复/错过调度/重试正确；用户重新上线可查看结果 |
| I7：微服务演进 | 待实施 | 基于证据拆分 Content、Ingestion、Intelligence 边界；RPC/事件契约、服务级故障隔离与 Trace | 独立部署和扩缩容可演示；超时、熔断、重复事件和下游故障有测试；提交拆分前后对比报告 |
| I8：Kubernetes 交付 | 待实施 | 镜像、K8s 模板、配置/密钥、迁移 Job、探针、资源限制、滚动更新、HPA | 干净集群可部署；发布/回滚、扩缩容、Pod 终止和依赖故障实际演练 |
| I9：可观测与性能优化 | 持续建设并集中收口 | OTel、Prometheus/Grafana、Loki、Tempo、告警、压测、pprof/trace、GC 分析 | 一次请求跨 HTTP/RPC/MQ/模型可追踪；关键 SLI/告警可用；提交可复现的瓶颈与 GC 优化前后报告 |
| I10：作品集收口 | 待实施 | E2E、故障演练、备份恢复、ADR、架构图、Runbook、Agent 评测报告、演示脚本 | 干净环境完整演示两类内容供给、两类 Feed、搜索、两层 Agent、微服务与 K8s；文档不夸大生产能力 |

### 下一阶段的执行边界

下一项业务工作为 **I2：统一内容供给**。应先创建 OpenSpec change，定稿：

- RSS 与用户投稿的统一文章模型、历史数据回填和稳定发布时间；
- `draft/published/offline/deleted` 状态与作者/管理员权限矩阵；
- 已发布文章编辑时的版本策略和乐观锁；
- 图片上传方式、MinIO 对象键、确认协议、限额和孤儿清理；
- Source 管理 API/Web、SSRF 安全验收和抓取历史的本阶段范围；
- 创建、发布、编辑、下架、删除的幂等与失败路径；
- latest Feed、详情、投稿和来源管理的 OpenAPI、Web 页面与测试。

I2 不接入 RabbitMQ、Redis、OpenSearch 或真实模型，先证明两类内容都能稳定进入共享内容池。Outbox 可在 I2 设计中预留事务接口，但实际 Relay 与 AI 消费者统一在 I3 交付，避免为了未来组件阻塞当前闭环。

后续仍需在对应 change 中决定的主要细节：

| 待定项 | 最晚阶段 |
| --- | --- |
| 文章版本策略、图片限制、Source 管理范围、历史 RSS 回填 | I2 |
| RabbitMQ 拓扑、事件 Schema、重试预算、DLQ 重放、模型/Prompt/维度 | I3 |
| OpenSearch 版本/分析器/索引参数、推荐公式、信号权重和缓存 TTL | I4 |
| Agent 会话保留、记忆预算、SSE 恢复和评测数据集 | I5 |
| 定时任务频率限制、收件箱保留和水位语义 | I6 |
| 微服务精确边界、RPC 方案、数据拆分与迁移路径 | I7 |
| Helm/Kustomize 选择、集群环境和持久依赖策略 | I8 |
| 压测规模、SLI/SLO、告警阈值和 GC 优化目标 | I9 |

## 10. 测试、观测与性能证据

### 验证分层

| 类型 | 重点 |
| --- | --- |
| 单元测试 | 领域状态、权限、去重、游标、推荐规则、任务水位、AI 输出校验 |
| 接口/契约 | Hertz DTO、RPC、事件 Schema、错误信封、鉴权、幂等、限流和 SSE |
| 真实依赖集成 | PostgreSQL 事务/迁移、Redis 回源、RabbitMQ 重投、MinIO、OpenSearch 查询/重建 |
| Web/E2E | 登录、投稿直发、Source 管理、Feed、搜索、对话 Agent、定时任务与收件箱 |
| Agent 评测 | 检索命中、约束遵循、引用正确、无结果、注入、工具失败、成本和延迟 |
| 故障测试 | 数据库/缓存/MQ/搜索/模型不可用、Worker 中断、积压恢复、Pod 终止和回滚 |
| 性能测试 | API/Feed/搜索/SSE 延迟、Worker 吞吐、分配与 GC、资源上限和扩缩容 |

固定时间、随机种子、测试数据和模型桩；真实模型冒烟独立记录 provider、model、Prompt、日期、Token/费用与结果，不混入确定性 CI。未配置外部依赖而跳过的测试必须显式报告，不能等同于通过。

### 可观测性最低要求

- API/RPC：请求率、错误率、P50/P95/P99、超时、限流和依赖耗时。
- RSS：调度延迟、抓取耗时、304、条目数、重复数、失败分类和退避。
- MQ/Outbox：待投递年龄、队列深度、最老消息、重试、DLQ、消费吞吐和幂等命中。
- AI：首/总耗时、Token/费用、超时、格式错误、重试、版本过期和并发。
- OpenSearch：索引延迟、Bulk 逐项错误、查询耗时、召回量、重建进度和别名版本。
- Feed/推荐：缓存命中、回源、候选量、过滤量、降级、空结果和来源分布。
- Agent：首 Token、总耗时、工具次数/失败、引用拦截、取消和定时任务成功率。
- 运行时：CPU、RSS/Heap、goroutine、连接池、分配率、GC 次数/暂停/CPU 占比。

日志、指标和 Trace 使用 request_id、trace_id、event_id、task_id 关联并脱敏；禁止把 Token、Cookie、密码、模型密钥或完整私密对话写入日志。

### 性能与 GC 优化交付物

I9 至少选择一个实际瓶颈完成完整闭环：固定数据与负载 → 建立基线 → pprof/trace/指标定位 → 修改 → 相同环境复测 → 解释收益与代价。

报告至少包含吞吐、P95/P99、CPU、内存、分配次数/字节、Heap、goroutine、GC 次数、暂停和 GC CPU 占比。允许结论是“默认 GC 已足够”；不得只展示 `GOGC`/`GOMEMLIMIT` 参数而缺少证据，也不得用增加内存掩盖泄漏或无界缓存。

## 11. 完成定义与文档维护

一项能力完成必须同时具备：

- 明确的业务规则、权限、并发、幂等和降级边界；
- 更新后的 OpenAPI/RPC/事件契约与版本化迁移；
- 与风险匹配的单元、集成、E2E、故障或性能测试；
- 超时、取消、有限重试、资源上限和必要观测；
- 可用的 Web/CLI 操作入口及加载、空、失败、重试状态；
- 数据恢复或可重建边界，以及可复现的验证命令和证据。

文档职责：

- README 只描述当前功能、限制、启动和验证事实。
- 本 Roadmap 维护唯一项目目标、技术方向、阶段和完成标准。
- OpenSpec 保存具体 change 的 proposal、design、specs、tasks、实施状态与归档。
- OpenAPI、SQL、测试 fixtures 和局部说明是实现契约，不另立产品路线。
- `code_copilot/` 只读保留迁移前历史证据，不创建或推进新 change。

路线调整不会自动安装中间件、迁移数据库或产生已实现能力。README 与 Roadmap 的状态更新必须以当前代码和可复现验证为依据。
