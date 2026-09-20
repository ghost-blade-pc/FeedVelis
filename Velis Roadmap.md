# Velis Roadmap

> 路线基线：2026-09-11；实施状态同步：2026-09-20。本文是项目目标、架构决策和实施顺序的唯一来源；当前可用功能与运行方式见 [README](README.md)。
>
> 本文吸收原 implementation、plan、旧开发文档和早期实践素材，以 implementation 的业务闭环与实施深度为主，结合已有代码增量推进。原文不再作为并行规范。GCFeed 仅提供设计参考，不要求功能、路径或技术栈逐项映射。

## 1. 产品目标与范围

Velis 是可自托管的图文 Feed 与智能阅读平台，面向有固定信息源的读者和轻量内容创作者。项目以后端工程实践为重点，Web 提供完整可演示的用户与管理流程。

目标版本形成五条链路：

1. 用户注册登录，创建 Markdown/图文草稿、上传图片、提交审核并发布。
2. 管理员维护 RSS/Atom/JSON Feed 来源，系统定时抓取、清洗、去重，按来源策略审核发布。
3. 已发布文章进入 latest、following、hot、recommend 四类 Feed；点赞、收藏和阅读行为参与分发。
4. Eino 异步生成摘要、关键词和 Embedding；OpenSearch 提供全文与语义混合检索。
5. Eino Agent 调用受控检索与推荐工具，以流式响应返回带来源引用的文章。

目标版本允许分阶段交付：投稿与基础阅读是第一条新增业务闭环，搜索、推荐和 Agent 不阻塞这一闭环。

### 首版范围与非目标

- 必须覆盖：账户权限、投稿/图片/审核、系统级来源管理、四类 Feed、关注作者、点赞收藏、曝光与站内阅读事件、AI 增强、混合搜索、对话推荐、基本管理与运行保障。
- RSS 来源登记与用户订阅是不同概念。首版来源由管理员维护；following 先围绕站内作者关注实现。用户订阅来源可在对应阶段提案中扩展，不把系统 Source 表伪装为个人订阅关系。
- 旧规划中的评论、通知、每日摘要、稍后读、OPML 等不作为本路线首版必交项，若新增则先调整范围。
- 不做视频链路、直播、私信、付费/广告、多租户平台、自训练模型、通用 Agent/MCP 平台或开放互联网爬虫。
- 首版不要求微服务、Kubernetes 或多地域；Kitex 仅在独立扩缩容或故障隔离有证据后评估。

## 2. 已确认技术决策

| 领域 | 方案 | 职责边界 |
| --- | --- | --- |
| 服务端 | Go + CloudWeGo Hertz | 保留现有 module 与 HTTP 适配；API 与 Worker 分进程 |
| 架构 | 模块化单体、四层依赖、显式 bootstrap | 业务规则不依赖 HTTP、数据库、MQ 或模型 SDK |
| 数据库 | PostgreSQL | 用户、文章、版本、关系、行为、任务、Outbox、AI 元数据和 Agent 会话的事实源 |
| 数据访问与迁移 | pgx + 显式 SQL、golang-migrate | 复用当前实现，不引入 MySQL/GORM 或生产 AutoMigrate |
| 搜索与向量 | OpenSearch | 全文、向量与混合召回；仅保存可重建的查询投影，不承担业务事务 |
| 缓存 | Redis | 页/卡片/统计、热榜、关注流索引与短期推荐快照；故障时回源或降级 |
| 异步事件 | RabbitMQ + PostgreSQL Outbox | 至少一次投递、消费幂等、有限重试与死信恢复 |
| 图片存储 | MinIO/S3 兼容端口 | 图片文件与文章引用分离；现有 MinIO 配置可复用 |
| AI | CloudWeGo Eino | 模型适配、内容增强 Workflow、Embedding 与 Agent；厂商 SDK 留在 Infrastructure |
| Web | Vue 3 + TypeScript + Vite + Pinia + Vue Router | 保留当前方案，重点服务后端闭环验证 |
| 观测 | 结构化日志、Prometheus/Grafana，逐步接入 Trace | 与每个业务阶段同步交付 |
| 协作 | OpenSpec | 后续规范、change 与归档统一使用 `openspec/`；`code_copilot/` 只读保留为迁移前历史证据 |

### PostgreSQL 与 OpenSearch 的边界

- PostgreSQL 决定文章当前公开版本、状态、权限和业务结果；索引滞后不能使下架内容重新可见。
- OpenSearch 不反向覆盖业务事实。API 搜索/推荐返回前批量检查 PostgreSQL 中的当前可见性。
- AI 元数据保存内容版本、模型、维度、Prompt/Workflow 版本、输入哈希及任务状态。向量查询与索引全部放在 OpenSearch。
- 索引可从 PostgreSQL 事实和版本化 AI 产物重建。是否额外保存向量生成结果作为重建缓存，在 I6/I7 提案中决定；若需重新调用模型，应明确预算和恢复时间，不能承诺无成本即时恢复。
- 现有 pgvector 镜像、vector 扩展和空适配目录属于遗留工程配置，没有已实现向量业务。I7 清理时同时验证新装与既有数据库升级；不重写已执行迁移，不执行无条件 DROP EXTENSION CASCADE。
- PostgreSQL 不可用时核心业务拒绝成功；Redis、RabbitMQ、OpenSearch 和模型故障的降级按实际能力定义，避免所有进程强制依赖全部服务。

## 3. 当前基线与复用范围

以下 RSS/文章基线来自 2026-09-11 静态核对；账户基线来自 2026-09-19 已归档的 [`account-access-foundation`](code_copilot/changes/account-access-foundation/card.md) change。持续更新的可用功能与运行边界以 README 为准。

| 能力 | 基线状态 | 后续处理 |
| --- | --- | --- |
| 工程、四入口、四层、迁移、CI、健康检查 | 已有实现 | 复用；补业务观测和集成环境 |
| Source 管理 CLI、调度租约、条件请求、失败退避 | 已有实现 | 复用；补管理 API、抓取历史与发布策略 |
| RSS/Atom/JSON Feed、正文清洗、源内去重 | 已有实现 | 复用；补跨源候选重复、版本与代理安全验证 |
| PostgreSQL Source/Article/Content 表 | 已有实现 | 增量扩展，保留已有文章 ID 与数据 |
| 文章列表与站内详情、Vue 阅读页面 | 已有实现 | 复用；补来源展示策略与 Feed 抽象 |
| 账户、会话、JWT、RBAC、登录限流与管理审计 | 已实现（I1 完成） | 复用为投稿、审核和后续用户功能的身份基础 |
| 投稿、审核、资产、Outbox/MQ | 未实现 | I2–I3 |
| Redis 业务、互动、画像、完整 Feed | 未实现 | I5 |
| Eino、OpenSearch、推荐、Agent | 未实现 | I6–I9 |
| 完整 E2E、压测、运维演练 | 未完成 | 各阶段建设，I10 收口 |

不因 I4 的 RSS 基础能力提前落地而重做它，也不据此宣称 I2–I4 已完成。当前文章表要求 Source 与外链，不能直接支持站内投稿；RSS 更新直接覆盖正文，接入审核前必须调整。I1 仍不包含改密、找回密码、注销账户、设备会话列表或管理员 HTTP 接口，这些限制以 README 为准。

## 4. 工程组织与契约

保留 `backend/`、`web/`、`deploy/` 和根目录 Compose/Makefile，不增加无实际多 module 需求的 go.work。

| 位置 | 职责 |
| --- | --- |
| `backend/internal/domain/<module>` | 聚合、值对象、领域错误、Repository 抽象 |
| `backend/internal/application/<module>` 与 `ports/` | 用例、跨聚合编排、事务与技术端口 |
| `backend/internal/infrastructure/` | pgx、Redis、MQ、存储、抓取、Eino、后续 OpenSearch 适配 |
| `backend/internal/interfaces/` | Hertz Handler/DTO、consumer、scheduler、CLI |
| `backend/internal/bootstrap/` | API、Worker、Admin 依赖装配；不承载业务规则 |
| `backend/api/openapi/velis.yaml` | 当前已实现 API 契约，随实现更新 |
| `backend/migrations/` | 增量迁移与可验证回滚/前滚策略 |
| `web/src/features/` | 按业务组织 Vue 功能 |

Domain 仅依赖 Domain；Application 依赖 Application/Domain；Infrastructure 依赖 Infrastructure/Application/Domain；Interfaces 依赖 Interfaces/Application/Domain；bootstrap 负责装配。现有依赖测试检查层级，不自动证明跨业务模块的数据所有权；后者由设计与 Review 验证。模块之间通过公开应用接口或事件协作，不直接改对方数据表。

配置沿用默认值 < YAML < 环境变量。随阶段新增认证、上传、MQ、Feed、AI、OpenSearch 与观测分组，启动校验超时、模型维度和必需配置；真实凭据不进入示例或日志。

### HTTP 与事件约定

- `/api/v1`、snake_case、RFC 3339 时间、数据库 UTC；保留 `/livez`、`/readyz`，不为路径形式改名。
- 列表为 items/next_cursor/has_more；错误为 `error: {code, message, request_id, details?}`，响应保留 X-Request-ID。
- Handler 绑定校验、鉴权、调用用例、映射响应；DTO、Application Input 与 Domain Entity 分离。
- 新增写接口定义 Idempotency-Key 或资源状态幂等语义。幂等记录绑定调用者、接口、请求摘要和结果；相同键不同参数返回冲突。
- 事件统一 event_id、带版本 event_type、aggregate_type/id/version、occurred_at、trace_id、producer、payload。事件 Schema 随生产者实现落地，不以数据库 Model 充当消息契约。
- 本文接口名称是实施建议，精确路径与字段在相应 change 中定稿；未实现接口不提前写成 OpenAPI 当前能力。

## 5. 统一文章与数据模型

### 模型演进

| 数据组 | 目标数据与约束 |
| --- | --- |
| 账户 | users、refresh_tokens；账号唯一、强密码散列、Token 哈希保存、轮换撤销、角色与状态校验 |
| 文章 | articles、article_contents、article_versions；来源类型、站内作者、公开版本、待处理版本、状态和乐观锁 |
| 来源 | 保留 sources；关联来源条目与文章，补抓取记录与来源展示/审核策略；站内稿件无需伪造 RSS Source |
| 资产与审核 | article_assets、article_audits/审核任务；图片归属、确认状态、文章版本、操作者、原因与审计时间 |
| 可靠异步 | outbox_events、consumed_events、async_tasks；事件唯一、consumer+event 唯一、task_type+biz_key+input_version 唯一 |
| 关系与互动 | follows、interactions、article_stats；禁止自关注、用户/文章/动作唯一、取消操作保持幂等 |
| 行为与画像 | article_view_events、exposures、user_interests；事件唯一、用户文章聚合、版本化可重建画像 |
| AI 与 Agent | article_ai_metadata、agent_sessions、agent_messages；产物版本、会话归属、工具结果引用与用量 |

表名为逻辑设计，迁移编号接续已有 000001/000002，不照搬从空库开始的编号。当前 ArticleContent 的正文分离继续保留；新增迁移要覆盖历史文章回填、约束建立、索引与回滚边界。

### 内容版本与审核规则

实施设计基线如下；它们是后续 change 的约束建议，尚未形成运行行为。第一个文章提案需定稿状态字段与权限矩阵。

- 文章公开状态与稿件处理状态分别表达，避免一份待审核稿使已公开版本意外消失。
- 稿件流程：DRAFT → PROCESSING_PENDING → REVIEW_PENDING → 审核通过；处理失败进入 PROCESS_FAILED，可显式重试；审核拒绝返回 DRAFT 并保留原因。
- 首次通过审核时文章成为 PUBLISHED；新版通过时原子切换 published_version。新版失败或被拒绝，不覆盖旧公开正文。
- PUBLISHED → OFFLINE；恢复发布需要重新审核。DELETED 为软删除终态，旧异步任务不得复活内容。
- 每次编辑/命令检查版本与权限；状态、审计和 Outbox 同事务提交。审核员审的是固定版本，不能误审核并发修改后的内容。
- RSS 内容变化形成候选新版本；相同内容不重复处理。未知源默认人工审核，可信源可以配置自动批准策略，但仍经过应用命令并记录审计。
- 站内稿件默认站内阅读。RSS 默认摘要与原文链接，允许站内正文的来源需有显式展示策略；迁移既有正文展示时明确历史默认值，不能在文档整理阶段改变现有页面行为。

### 去重与时间语义

- 保留源内 `(source_id, dedupe_key)` 唯一约束；external ID 优先，规范 URL 兜底。
- canonical URL、正文指纹用于跨源候选重复判断；不以相似标题直接自动合并。
- 来源映射允许一篇文章关联多个来源条目，避免“article_id 唯一”同时又要求合并多来源的冲突；是否自动合并由 I4 提案限定。
- 区分原站发布时间、首次发现时间、站内发布时间与更新时间。当前 sort_at 可随源内容更新而变动；I5 固定新的分页时间语义并提供兼容方案。

## 6. RSS 与可靠异步

### RSS 增量建设

复用 Subscription/Source Service、Scheduler、Fetcher、Parser、Sanitizer 与 Ingestion 的职责分离：认领到期源 → 抓取 → 解析清洗 → 去重/形成文章版本 → 按策略审核发布 → 更新来源记录。

- 租约保留 owner、过期时间和完成时 fencing 检查；暂停、过期或重新认领后，旧任务不能提交结果。
- 304 只更新检查与调度元数据；200 解析内容；失败分类退避。补来源级抓取周期、域名并发限速、完整抓取记录与人工重试。
- MQ 化抓取时补齐认领与任务持久化的一致性，消息丢失/积压不得导致永久失调度；重复投递、长抓取和租约续期需有测试。
- 仅 HTTP(S)，限制 DNS/重定向、目标地址、总时间、响应体；直连和环境代理分别验证 SSRF 边界。来源管理进入 HTTP 前先完成对应安全验收。
- XML/HTML 与图片都视为不可信输入；正文清洗版本可追踪，不执行源中指令，不任意去除可能改变资源身份的 URL 参数。

### Outbox 与 Worker

- I2 即建立 Outbox 表与同事务事件写入，保证首个投稿闭环事实和事件同时提交。
- I3 建 Relay、publisher confirm、队列与 Consumer。Broker 确认后才标记已投递，确认后进程崩溃允许重投。
- 消费去重记录与本地业务结果在同一事务提交，提交后 Ack；不能先独立提交去重记录再执行业务。
- AI、OpenSearch 等外部副作用不能靠数据库事务保证恰好一次，使用版本条件、幂等写、任务状态及可恢复重试。
- 处理、索引、AI、统计/兴趣、Feed fanout 消费者独立注册；重试有限、指数退避、DLQ 和人工重放可观测。
- API 基础写入不依赖 MQ 即时可用；Worker 按启用消费者声明 readiness/degraded，故障不能只记录日志并报告全部健康。

## 7. Feed、互动与推荐

### Feed Strategy

FeedService 负责选择场景、统一限制、批量组装卡片/统计/当前用户状态与指标。每个 Strategy 接收 viewer、cursor、limit 和 clientContext，返回轻量页项；不在 Handler 堆场景分支。

| 场景 | 候选与排序 | 失败处理 |
| --- | --- | --- |
| latest | PostgreSQL 已公开文章，固定发布时间与 ID | 数据库直接查询 |
| following | 先基于关注关系拉取；再按实测加入 inbox 与大作者 outbox 合并 | 回源 PostgreSQL 关系与文章 |
| hot | Redis 分钟桶聚合后形成固定窗口的分数快照 | PostgreSQL 热度快照，再退 latest |
| recommend | 多路召回、过滤、规则排序、来源/作者打散 | hot → latest |

关注流推拉是目标能力，先保证基础拉取正确，再验证扇出收益。阈值、批量大小、inbox 长度配置化；新关注回填，取消关注读取时二次过滤。

### 缓存与分页

- 页缓存仅存 ID、版本和排序字段；文章卡片、统计和用户动作分开缓存。用户相关缓存必须按真实身份隔离。
- 批量 MGET，缺失项批量回源，singleflight 合并同键请求；短首页 TTL、较长后续页 TTL、抖动与事件失效配合。
- 禁止逐项查询造成 N+1；Redis 数据丢失可以重建，不能丢失点赞、关注或发布事实。
- 新游标包含版本、场景、过滤条件绑定和完整排序元组，HMAC 校验；不以签名代替鉴权。当前 unsigned v1 游标的兼容/失效在 I5 中明确。
- latest/following 固定排序时间与首次请求上界；补历史文章新发布的边界测试。删除/下架可以缩短页，不能为了补齐而返回不可见内容。
- hot 固定窗口与已冻结分数，recommend 固定候选顺序/策略版本/随机种子及快照期限；仅固定 window_end 或 rank_score 字段不足以抵抗分数漂移。过期游标返回可识别结果，不静默换序。

### 行为与兴趣

曝光、卡片点击、站内详情打开、有效停留、滚动深度、读完、点赞、收藏与负反馈分开定义。事件携带 event_id、用户/匿名身份、article_id、scene、request_id、occurred_at；批量上报限流、校验时钟偏差并幂等。

外链只能可靠记录点击，不能推断原站阅读时长/读完。站内阅读信号应考虑页面可见性与有效停留，避免把后台停留计入阅读。

互动事实同步入 PostgreSQL，统计/热度/画像异步派生并可校准。兴趣权重、时间衰减、负反馈、来源集中度与推荐理由均版本化，以固定数据回放验证。

推荐逐步采用订阅/关注、主题关键词、热度/新鲜度与语义召回；过滤已不可见、近期曝光和负反馈内容，做作者/来源打散。匿名与新用户返回基础候选，AI 只是一条召回通道。

## 8. Eino、OpenSearch 与 Agent

### Eino 内容增强

固定文章版本 → 输入规范化/分段 → 结构化摘要与关键词 → 输出校验 → Embedding → 保存版本化元数据 → 发出增强完成事件。

- Application 定义 ContentEnhancer/Embedder 等端口，Eino 和 Provider 适配留在 Infrastructure。
- 摘要、关键词、主题、语言和可选安全标签有 Schema/长度/枚举校验；AI 不覆盖作者原文，也不独自决定业务审核权限。
- 记录 provider、model/version、dimension、prompt/workflow version、input hash、Token、耗时与错误分类。
- 任务绑定文章版本；旧任务可留档，不能覆盖 current metadata。模型维度不同使用新索引版本，禁止混写。
- 有并发、总超时、有限重试和预算；摘要失败回退原始摘要，Embedding 失败关闭语义通道。CI 与离线开发使用模型桩。

### OpenSearch

使用版本化物理索引和读写别名。文档包含 article_id/version、公开状态、标题、摘要/正文片段、关键词/主题、来源作者、语言、发布时间、热度、Embedding 和模型版本。

- 发布、新版本、AI 完成、统计快照触发增量同步；下架/删除触发移除。同步检查业务版本与派生版本，同一文章版本的旧 AI/统计事件也不能覆盖新产物。
- 删除保留必要的版本屏障或状态复核，防止迟到 upsert 复活旧索引；查询出站前再次验证事实源可见性。
- 全文 BM25 与向量 KNN 分别取候选，首版在应用层 RRF 融合，避免直接相加不同量纲分数；支持标题/来源/时间过滤。
- Bulk 逐项处理结果；索引重建从 PostgreSQL 分页读取，处理重建期间增量变化，校验数量/抽样/检索后切换别名，保留回滚窗口。
- 无向量时退全文；OpenSearch 整体故障时搜索返回明确不可用，推荐退 hot/latest，不将时间列表伪装成相关性搜索。
- 用固定中文与混合语言数据集验收分词、相关性、去重、过滤和延迟；服务版本、分析器、维度与资源预算在 I7 锁定。

### Eino Agent

首版只读工具：search_articles、recommend_articles、get_article。工具调用 Application Service，不直连数据库或绕过权限访问索引。

流程：校验会话归属 → 读取有限历史/记忆 → 提取查询约束 → 有界工具调用 → 校验引用 → 流式回答与文章卡片 → 保存消息、用量和必要记忆。

- 会话和每次工具调用均鉴权；RSS/文章/历史消息是数据，不能改变工具权限。
- 文章标题、作者、链接与引用来自工具结果；无结果明确说明，不编造文章。
- 限制工具次数、总超时、Token 与返回量；不保存隐藏推理过程。
- SSE 建议事件：session.started、message.delta、tool.started/completed、article.citation、message.completed、error、heartbeat。
- 用户消息幂等、服务端 message ID 去重；断流可恢复已完成回答，未完成回答显式重试并有取消/预算边界。

## 9. 分阶段实施与验收

每阶段都交付必要的 Vue 页面、OpenAPI、测试和日志；I10 是系统收口，不是首次建设质量能力。状态仅表示是否已有对应能力，阶段完成须由 change 证据确认。

| 阶段 | 基线状态 | 交付与依赖 | 验收门槛 |
| --- | --- | --- | --- |
| I0：统一目标与工程基线 | 工程已有，本次完成文档收敛 | README、Roadmap、协作入口一致；保留现有目录与流程 | 无并行目标文档；当前/未来分开；历史记录保留；新路线不冒充已部署 |
| I1：账户与权限 | 已完成（2026-09-19） | 用户、refresh token、注册登录退出、资料、RBAC、JWT、审计上下文与 Vue 登录；依赖 I0；证据见 [`account-access-foundation`](code_copilot/changes/account-access-foundation/card.md) | 正常/过期/撤销/轮换/重复账号/越权测试；管理员初始化方式可复现 |
| I2：投稿发布纵切片 | 下一阶段；RSS 模型/阅读可复用 | 统一 Article、版本、图片、稿件状态、审核、详情、latest；同事务 Outbox 表与写入；依赖 I1 | 投稿者创建→编辑→提交→审核员批准→匿名浏览；并发冲突/拒绝/下架/重复请求正确；不依赖 Redis/MQ/搜索/模型 |
| I3：可靠异步基础 | 待实施 | Relay、confirm、消费去重、async_tasks、retry/DLQ、处理/发布/下架事件；依赖 I2 | MQ 中断恢复、重复消息、Ack 前崩溃、旧版本任务、有限重试与重放有证据 |
| I4：RSS 接入统一发布 | 抓取基础已有 | 复用 Source/Fetcher；新增管理 API/页面、抓取记录、审核/展示策略、版本与去重；依赖 I2/I3 | RSS 与投稿进入同一公开模型；304/更新/重复/租约丢失/恶意源/代理路径可验证；现有数据可迁移 |
| I5：基础 Feed 与行为 | 时间列表可复用 | Strategy、following/hot、推拉、Redis 分层缓存、关注点赞收藏、曝光阅读、统计画像；依赖 I3/I4 | 三类 Feed、身份隔离、稳定分页、缓存清空回源、事实与计数幂等；形成压测基线 |
| I6：Eino 内容增强 | 待实施 | AI metadata、Workflow、Embedding、模型桩、版本保护、限流重试预算；依赖 I3/I4，主线在 I5 后 | 无密钥可演示非 AI 链路；超时/非法输出/维度变化/旧结果不破坏业务 |
| I7：OpenSearch 检索 | 待实施 | 索引/别名、Index Worker、BM25/KNN/RRF、重建回滚；清理 pgvector 遗留；依赖 I6 | 全文/语义/混合相关性、下架不泄露、迟到事件/Bulk 部分失败/重建增量正确；新装升级可验证 |
| I8：个性化推荐 | 待实施 | 多路召回、过滤、规则排序、打散、理由、快照与冷启动；依赖 I5/I7 | recommend 有稳定分页与降级，固定数据可回放，来源不过度集中 |
| I9：Eino Agent | 待实施 | 会话消息、只读工具、SSE、引用、记忆、Agent 页面和评测；依赖 I7/I8 | 无结果/工具失败/模型超时/断流/取消/越权/注入场景有证据，引用可跳转 |
| I10：完整交付 | 持续建设 | 管理页、E2E、指标告警、部署、数据保留、备份恢复、压测、Runbook 内容纳入 README；依赖 I1–I9 | 干净环境演示两条供给、四类 Feed、搜索与 Agent；依赖故障、DLQ 重放与索引重建实际演练 |
| I11：可选拆分 | 不作为首版门槛 | 收集 CPU/内存、延迟、故障与扩缩容证据；必要时评估 Kitex，优先资源密集 Worker 能力 | 无证据则记录保持单体的结论；拆分须有可量化收益、契约/超时测试与回滚 |

### 下一阶段的执行边界

下一项业务工作为 I2 投稿发布纵切片，其 OpenSpec 提案需定稿统一文章模型、用例、接口、数据、权限、失败路径、测试与迁移方案。本次协作工作流与状态文档更新不构成 I2 实施。

已确定方向不重复征询；以下细节在对应阶段解决，不阻塞本 Roadmap：

| 待定细节 | 最晚定稿阶段 |
| --- | --- |
| 稿件拒绝/重审的精确状态、角色权限、图片限额、公开/待审核版本字段 | I2 |
| 事件 Schema、队列重试预算、任务保留与消费并发 | I3 |
| 来源展示权限、可信源策略、跨源合并与历史正文迁移 | I4 |
| 关注流阈值、曝光口径、快照有效期、排序公式、压测数据量与 SLO | I5 |
| 模型供应商/版本/维度、Prompt、预算、向量重建缓存策略 | I6 |
| OpenSearch 版本、中文分析器、索引参数、部署资源与迁移退出路径 | I7 |
| Agent 会话保留、匿名访问、记忆预算、流式恢复协议 | I9 |

可并行准备 Mock、fixtures、管理页面与评测数据，但不能绕开文章版本、事件契约、模型维度和权限边界的定稿。

## 10. 测试、观测与完成标准

### 验证分层

| 类型 | 重点 |
| --- | --- |
| 单元 | 状态机、权限规则、去重、排序、游标、重试、版本保护、AI 输出校验 |
| 接口 | Hertz DTO、错误信封、鉴权、幂等、限流与 SSE |
| 真实依赖集成 | PostgreSQL 约束/事务/迁移、Redis 清空、MQ 重投、OpenSearch 映射/查询/重建、对象存储 |
| Web/E2E | 登录投稿审核、RSS 管理、Feed、阅读互动、检索与 Agent；覆盖加载/空/错误/重试 |
| 故障与性能 | 依赖不可用、Worker 中断、积压恢复、Feed 批量回源、搜索、行为上报与 Agent 首 Token |

固定时间、随机种子、测试数据和模型桩；真实模型冒烟独立记录 provider/model/Prompt/日期/用量，不混入确定性 CI。未设置外部依赖而跳过的测试必须显式报告，不能等同集成通过。

### 指标与交付

- API：请求率、状态、P95/P99、超时与限流；Feed：场景延迟、空结果率、缓存命中/回源/singleflight、游标错误与降级。
- RSS：调度延迟、304、条目/重复/失败、单源退避；MQ：队列深度、最老消息年龄、重试与 DLQ；Outbox：待投递年龄。
- AI：耗时、Token/费用、超时、格式错误、版本过期；索引：同步延迟、Bulk 错误、重建进度与版本落后；推荐：召回量、过滤、多样性与阅读信号。
- Agent：首 Token、总耗时、工具失败、取消、无引用拦截；日志与事件通过 request/trace/event ID 关联并脱敏。
- Compose 随对应阶段加入真实依赖、健康检查、持久卷与模型桩；开发与部署资源参数分开。备份 PostgreSQL 和必要对象文件，验证索引/缓存恢复。
- 数据保留覆盖行为、抓取原文、审计、AI 和会话；用户删除同步处理事实、缓存、索引与迟到异步任务。

每项功能完成必须有：明确业务规则、权限/并发/幂等/故障测试、契约与迁移同步、超时取消和有限重试、必要指标、可用 Web 状态、数据恢复边界及可复现验证。目录创建或单次构建成功不算完成。

## 11. 文档维护规则

- README 是当前功能与运行事实入口；本 Roadmap 是唯一项目目标与实施路线。
- 技术选择、范围和阶段变化只在本文件维护，执行细节与证据写入具体 change；归档记录保留当时上下文，不追溯改写成当前目标。
- AGENTS/CLAUDE 只维护协作规则与导航；OpenSpec 维护后续规范、change 与归档，不复制另一份产品路线。
- `code_copilot/` 只读保留迁移前的历史证据，不再作为当前项目事实、规范或 change 状态入口。
- OpenAPI、SQL、测试 fixtures 和局部测试说明继续保留；它们是实现契约，不是额外项目路线。
- 路线与状态文档更新不安装 OpenSearch/Eino、不迁移数据库、不修改业务代码，也不以文档勾选代替可复现验证。
