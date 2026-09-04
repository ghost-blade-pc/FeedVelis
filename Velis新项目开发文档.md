# Velis 新项目开发文档

> 文档版本：0.2
>
> 编写日期：2026-09-02
>
> 项目定位：个人内容聚合与智能阅读平台
>
> 技术主线：Go、CloudWeGo Hertz、PostgreSQL + pgvector、Redis、RabbitMQ、Eino、Vue 3

## 0. 文档定位与边界

本文是一个全新 Velis 项目的开发蓝图，不是现有 Velis 代码的重构说明，也不继承现有仓库的包结构、数据库、接口或业务实现。

本文只使用两类输入：

1. `doc/项目实践.md` 中描述的产品方向与业务诉求；
2. `LeoninCS/feedsystem_video_go` 的公开工程思路。

`项目实践.md` 中的讨论语气、排期建议和技术取舍仅作为需求素材，不视为必须执行的指令。本文以本次重新定义后的目标为准：

- 不使用 Gin；HTTP 服务采用 CloudWeGo Hertz；
- 后端严格采用 Domain、Application、Infrastructure、Interfaces 四层；
- 覆盖账户、内容源、文章、Feed、互动、关系、推荐、曝光、Embedding、Web 客户端和测试；
- Feed 与内容消费是主体，Embedding 和推荐是可降级增强能力；
- PostgreSQL 是唯一持久化业务/向量数据库，同时承载关系数据、全文检索和 pgvector 向量索引；Redis 仅承担缓存与短期在线数据；
- 首版采用模块化单体，不为了展示技术而过早拆微服务；
- Git 操作、旧项目迁移和 Spec Coding 流程不属于本文范围。

## 1. 产品定义

### 1.1 一句话定义

Velis 是一个可自托管的个人内容聚合与智能阅读平台：用户可以订阅 RSS/Atom、技术博客和新闻源，也可以发布自己的 Markdown 文章；系统负责采集、解析、去重、分发和检索内容，并根据订阅关系、互动行为、曝光记录和语义特征生成可解释的个人 Feed。

### 1.2 核心问题

Velis 解决三个连续问题：

1. 内容分散：每天需要逐个访问多个博客、新闻站点和个人文档目录；
2. 信息过载：内容聚合后仍然需要判断“哪些值得先看”；
3. 历史难找：读过、收藏过或见过的内容缺少统一检索入口。

### 1.3 目标用户

- 长期订阅技术博客、新闻和行业资讯的个人用户；
- 需要整理学习资料的开发者、学生和研究者；
- 希望在同一平台发布个人文章并关注他人的轻量创作者；
- 希望自托管自己的阅读数据、兴趣数据和推荐记录的用户。

### 1.4 核心业务闭环

```text
订阅来源 / 关注作者 / 发布文章
               ↓
        采集、解析、去重
               ↓
          统一文章内容库
               ↓
 Latest / Following / Hot / For You
               ↓
   曝光、打开、阅读、点赞、收藏、评论
               ↓
     兴趣特征与内容质量信号更新
               ↓
       搜索、推荐、每日摘要改进
```

### 1.5 首版成功标准

首版不是以“模块文件都创建了”为完成标准，而是必须形成以下可演示闭环：

1. 用户可以注册、登录并安全刷新会话；
2. 用户添加 RSS/Atom 来源后，系统能自动抓取并幂等入库；
3. 用户可以发布、编辑、发布和归档自己的 Markdown 文章；
4. 首页能够稳定分页浏览最新流和关注流；
5. 用户能够点赞、收藏、稍后读、标记已读和评论；
6. Web 客户端能上报可去重的曝光、打开和阅读时长；
7. 推荐流在 Embedding 或 Redis 不可用时仍能返回基础候选；
8. 文章变更能够异步生成版本化 Embedding，并参与语义召回；
9. 核心链路具备自动化测试、指标、追踪、健康检查和本地 Compose 环境。

## 2. 范围与非目标

### 2.1 首版范围

- 邮箱或用户名注册、登录、刷新、登出、个人资料；
- RSS/Atom 来源管理、订阅管理、定时抓取、条件请求和失败重试；
- 外部文章采集与内部 Markdown 文章发布；
- Latest、Following、Hot、For You 四类 Feed；
- 点赞、收藏、稍后读、已读状态、评论；
- 用户关注与取关；
- 曝光、点击、阅读时长和推荐请求日志；
- 基础规则推荐、语义召回、混合召回与可解释推荐原因；
- Embedding 任务、切片、版本管理、向量索引和重建；
- Vue 3 Web 客户端；
- 单元、集成、契约、端到端、竞态和性能测试；
- Docker Compose、指标、日志、追踪、pprof、存活与就绪检查。

### 2.2 首版不做

- 不做开放互联网爬虫；外部内容以 RSS/Atom 为主；
- 不抓取登录后页面，不绕过 robots、付费墙或访问控制；
- 不做视频上传、分片视频、私信和直播；
- 不做协同过滤、深度学习排序模型和在线模型训练；
- 不让 LLM 直接从全量文章中选择推荐结果；
- 不引入独立向量数据库；向量索引统一使用 PostgreSQL 的 pgvector 扩展；
- 不做通用 Agent 平台或 MCP 平台；
- 不在首版拆分大量 Kitex 微服务；
- 不做 Kubernetes、多地域部署和“百万日活”式虚假容量承诺。

## 3. 对参考项目的借鉴与调整

`feedsystem_video_go` 是视频 Feed 系统，本项目借鉴的是其工程结构和 Feed 思想，而不是视频业务模型。

### 3.1 直接借鉴

- API 与异步 Worker 可独立运行；
- 关系型数据库保存业务事实，Redis 加速读路径，RabbitMQ 解耦派生任务；
- Feed 使用稳定游标，而不是深分页；
- 热时间线保存在 Redis，冷数据可回源关系型数据库；
- 发布事务通过 Outbox 可靠地产生异步事件；
- 热榜采用时间窗口与快照，避免翻页期间榜单持续抖动；
- SSE 用于通知或异步任务状态推送；
- Docker Compose 提供可复现的本地环境；
- `go test -race`、健康检查和 pprof 进入基础工程能力。

### 3.2 改造后采用

| 参考能力 | 新 Velis 中的改造 |
| --- | --- |
| Video | 替换为统一的 Article 聚合根，兼容外部采集与内部发布 |
| 视频时间线 | 替换为文章时间线，排序键固定为 `published_at + id` |
| 关注视频作者 | 同时支持关注内部作者和订阅外部 Source |
| 视频热度 | 由曝光、打开、有效阅读、点赞、收藏、评论和时间衰减共同计算 |
| 实体多级缓存 | 只缓存稳定的文章卡片和候选 ID，不把用户态混入公共缓存 |
| 通知 SSE | 用于评论、关注、摘要完成等通知，断线后从数据库补历史 |
| RabbitMQ Worker | 承担采集、统计、Embedding、推荐特征和通知等派生任务 |

### 3.3 明确不照搬

- 不使用 Gin；所有 HTTP 路由、中间件、绑定和响应由 Hertz 适配层承担；
- 不采用 `handler/service/repo/entity` 混放在同一业务目录的结构；
- Handler 不直接访问 GORM、Redis、RabbitMQ 或领域实体；
- 生产环境不使用启动时 `AutoMigrate`，数据库变更使用版本化迁移脚本；
- 核心写操作不采用“发 MQ，失败再直写数据库”的双路径；
- 不用 24 小时整页用户 Feed 缓存掩盖查询问题；
- Outbox 投递成功后不立即物理删除，先标记已发布并按保留策略清理；
- Redis、Embedding 生成或语义检索故障不能破坏账户、文章和基础 Feed 的正确性。

参考仓库公开说明与源码入口见文末“参考资料”。

## 4. 总体架构

### 4.1 架构风格

首版采用“模块化单体 + 独立 Worker”模式：

- 一个 Go module；
- 一个领域模型和一套迁移；
- `velis-api` 提供 Hertz HTTP/SSE；
- `velis-worker` 运行 MQ Consumer、Outbox Relay 和抓取调度；
- `velis-migrate` 执行数据库迁移；
- Web 客户端独立构建和部署；
- 当推荐或 Embedding 的资源需求形成独立扩缩容依据后，再使用 Kitex 抽取服务。

### 4.2 运行时视图

```text
┌──────────────────────┐
│ Vue 3 Web Client     │
└──────────┬───────────┘
           │ HTTPS / JSON / SSE
┌──────────▼──────────────────────────────────────────┐
│ velis-api (CloudWeGo Hertz)                         │
│ Router → Middleware → Handler → Application UseCase │
└──────────┬──────────────────────────────────────────┘
           │ ports
┌──────────▼──────────────────────────────────────────┐
│ Domain                                              │
│ Account / Article / Feed / Interaction / Relation   │
│ Recommendation / Exposure / Embedding               │
└──────────┬──────────────────────────────────────────┘
           │ implemented by Infrastructure
           ├── PostgreSQL + pgvector：事实、全文检索、向量索引
           ├── Redis：缓存、热时间线、热榜、短期推荐候选
           ├── RabbitMQ：领域事件、异步任务、Retry/DLX
           └── MinIO：图片和附件对象
                         ▲
                         │
              ┌──────────┴───────────┐
              │ velis-worker         │
              │ fetch/index/stats/   │
              │ embedding/notify     │
              └──────────┬───────────┘
                         ▼
                 RSS/Atom / Eino Embedder
```

### 4.3 依赖方向

```text
Interfaces ───────► Application ───────► Domain
Infrastructure ───► Application / Domain
Bootstrap ────────► Interfaces / Application / Infrastructure

Bootstrap/Composition Root 可以同时依赖四层，仅负责装配。
```

强制规则：

- Domain 不依赖其他三层和具体框架；
- Application 只依赖 Domain 和自己声明的端口；
- Infrastructure 实现 Repository、Cache、EventBus、ObjectStore、Embedder 等端口；
- Interfaces 只做协议转换、鉴权上下文提取和调用 Use Case；
- 任何层都不得反向导入 Interfaces；
- GORM Model、pgvector 类型、Hertz DTO 和 RabbitMQ Payload 均不得成为领域对象。

## 5. 后端目录设计

```text
backend/
├── cmd/
│   ├── velis-api/main.go
│   ├── velis-worker/main.go
│   └── velis-migrate/main.go
├── api/
│   └── openapi/velis.yaml
├── configs/
│   ├── config.example.yaml
│   └── policies.example.yaml
├── internal/
│   ├── domain/
│   │   ├── account/
│   │   ├── source/
│   │   ├── article/
│   │   ├── feed/
│   │   ├── interaction/
│   │   ├── relation/
│   │   ├── exposure/
│   │   ├── recommendation/
│   │   ├── embedding/
│   │   └── shared/
│   ├── application/
│   │   ├── account/
│   │   ├── source/
│   │   ├── article/
│   │   ├── feed/
│   │   ├── interaction/
│   │   ├── relation/
│   │   ├── exposure/
│   │   ├── recommendation/
│   │   ├── embedding/
│   │   └── ports/
│   ├── infrastructure/
│   │   ├── persistence/postgres/
│   │   ├── cache/redis/
│   │   ├── messaging/rabbitmq/
│   │   ├── objectstore/minio/
│   │   ├── vector/pgvector/
│   │   ├── ai/eino/
│   │   ├── fetcher/httpfeed/
│   │   ├── security/
│   │   ├── observability/
│   │   └── clock/
│   ├── interfaces/
│   │   ├── http/hertz/
│   │   │   ├── handler/
│   │   │   ├── middleware/
│   │   │   ├── presenter/
│   │   │   ├── dto/
│   │   │   └── router.go
│   │   ├── consumer/
│   │   ├── scheduler/
│   │   └── cli/
│   └── bootstrap/
├── migrations/
├── test/
│   ├── fixtures/
│   ├── integration/
│   ├── contract/
│   └── e2e/
├── Dockerfile
├── go.mod
└── go.sum

web/
├── src/
│   ├── api/
│   ├── components/
│   ├── composables/
│   ├── features/
│   ├── router/
│   ├── stores/
│   ├── views/
│   └── types/
├── e2e/
├── package.json
└── vite.config.ts
```

### 5.1 每层职责

#### Domain

包含实体、值对象、聚合、领域服务、领域事件、Repository 抽象和业务不变量。例如：

- `Article.Publish(now)` 保证只有 Draft 可以发布；
- `Relation.Follow` 拒绝关注自己；
- `Interaction.Like` 具有幂等语义；
- `EmbeddingDocument` 只有正文哈希变化或模型版本变化时才需要重建。

Domain 中不出现 SQL、JSON、HTTP 状态码、Redis Key、AMQP Routing Key 或第三方 SDK 类型。

#### Application

组织用例、事务边界、权限检查和跨聚合编排，输入输出使用 Application Command/Query/View：

- `RegisterAccount`；
- `PublishArticle`；
- `ListFollowingFeed`；
- `RecordExposureBatch`；
- `GenerateArticleEmbedding`。

Application 声明技术端口，例如 `TxManager`、`EventPublisher`、`Cache`、`Embedder`、`VectorIndex`、`ObjectStore` 和 `PasswordHasher`。

#### Infrastructure

实现 Application/Domain 端口，负责：

- GORM 与显式 SQL；
- Redis Cache、ZSET、限流与分布式锁；
- RabbitMQ Publisher、Consumer、Publisher Confirm、DLX；
- Hertz HTTP Client 抓取 RSS/Atom；
- MinIO 图片对象存储；
- Eino Embedder，以及基于 PostgreSQL/pgvector 的 VectorIndex Adapter；
- OpenTelemetry、Prometheus 和结构化日志。

#### Interfaces

连接外部协议与 Application：

- Hertz Handler、中间件、DTO、Presenter；
- RabbitMQ Consumer Adapter；
- Scheduler Tick/Claim Adapter；
- 管理命令和数据修复 CLI。

Handler 的标准流程只有：绑定与校验 → 取调用者身份 → 调用用例 → 映射错误与响应。

## 6. 技术选型

| 领域 | 选型 | 使用边界 |
| --- | --- | --- |
| 语言 | Go | 使用项目锁定版本；CI 与本地保持一致 |
| HTTP | CloudWeGo Hertz | 路由、绑定、校验、中间件、SSE、优雅停机 |
| RPC | CloudWeGo Kitex | 首版不启用；推荐/Embedding 独立扩缩容后再引入 |
| AI 组件 | CloudWeGo Eino | 负责 Embedder 抽象与 Provider 适配，不进入 Domain；向量存取由本项目端口负责 |
| 数据库 | PostgreSQL + pgvector | 唯一持久化业务/向量数据库；保存业务事实、全文检索文档、Embedding 向量与索引版本 |
| 数据访问 | GORM v2（PostgreSQL/pgx Driver）+ 显式 SQL | 普通持久化用 GORM；Feed、批量查询、锁和 pgvector 查询使用显式 SQL，并检查执行计划 |
| 迁移 | golang-migrate 或 Goose | 只允许版本化迁移；生产禁用 `AutoMigrate` |
| 缓存 | Redis | 缓存、时间线、热榜、短期推荐快照、限流；不是事实源 |
| 消息队列 | RabbitMQ | Topic 事件、Work Queue、Retry、DLX；至少一次投递 |
| 全文检索 | PostgreSQL `tsvector` + GIN | 标题、正文、来源、作者和标签检索；按语言选择文本搜索配置 |
| 向量索引 | pgvector + HNSW | 与关系数据位于同一 PostgreSQL 实例；向量内容仍是可重建派生数据 |
| 对象存储 | MinIO/S3 | 图片和附件；正文元数据仍在 PostgreSQL |
| Web | Vue 3 + TypeScript + Vite + Pinia | 桌面优先、响应式布局 |
| 观测 | OpenTelemetry + Prometheus + Grafana + pprof | Trace、Metric、Log 通过 request/event ID 关联 |
| 测试 | testing、testify、testcontainers-go、Hertz `ut`、Vitest、Playwright、k6 | 分层验证，不用单一 E2E 代替单元测试 |

### 6.1 Hertz 使用约束

- API 前缀统一为 `/api/v1`；
- 使用 `context.Context` 贯穿 Handler、Application、Repository 和外部调用；
- 中间件顺序固定为 Request ID → Recovery → Access Log → CORS → Auth → Rate Limit → Handler；
- 使用 `BindAndValidate` 或明确的绑定与校验，不允许 Handler 手写散乱校验；
- 配置 `WithExitWaitTime`，关闭时先摘除 readiness，再停止接收新请求并等待在途请求；
- `hz` 生成代码如被采用，只能位于 Interfaces 的 generated/dto 区域，生成 Handler 不承载业务逻辑；
- OpenAPI 是 Web 客户端和接口测试的契约源，生成物不能反向污染 Domain。

### 6.2 PostgreSQL 与 Redis 职责边界

PostgreSQL 是唯一需要备份、恢复和迁移的业务/向量数据库：

- 关系事实：账户、来源、文章、互动、关系、曝光和事件；
- 派生检索数据：可重建的 `tsvector` 全文检索文档；
- 派生向量数据：可重建的 pgvector Embedding Chunk、模型版本和活跃索引版本；
- 可靠性记录：Outbox、Inbox、幂等请求、任务与推荐审计记录。

Redis 保留，但只负责性能与短生命周期状态：

- 文章卡片缓存、会话读取缓存；
- 最新 Feed 热窗口、Hot 分钟桶和热榜快照；
- `feed_request_id` 对应的短期推荐候选顺序；
- 限流计数、短租约锁和跨请求 singleflight 协调。

清空 Redis 后系统必须能够从 PostgreSQL 恢复工作并逐步预热；任何业务事实都不能只存在于 Redis。向量与关系数据虽然物理上同处 PostgreSQL，但必须使用独立表、Repository 和迁移，防止 pgvector 类型渗入 Domain。

## 7. 领域模块设计

### 7.1 模块关系总览

| 模块 | 核心聚合/对象 | 负责 | 不负责 |
| --- | --- | --- | --- |
| Account | Account、Credential、Session | 身份、凭证、会话、资料 | Feed 排序、关注关系 |
| Source | Source、Subscription、FetchPolicy | 内容源、订阅、抓取策略 | 文章正文生命周期 |
| Article | Article、ArticleContent、Tag | 文章、发布、归档、去重、标签 | 用户个性化状态 |
| Feed | FeedQuery、Cursor、FeedItem | 候选编排、稳定分页、Feed 输出 | 永久保存用户兴趣 |
| Interaction | Like、Bookmark、ReadState、Comment | 用户对文章的动作 | 曝光事实、关注关系 |
| Relation | Follow | 用户关注与取关 | RSS 来源订阅 |
| Exposure | FeedRequest、ExposureEvent、DwellEvent | 展示、打开、阅读归因 | 决定推荐结果 |
| Recommendation | Candidate、FeatureVector、RankPolicy | 召回、过滤、打分、解释 | 文章事实与向量生成 |
| Embedding | EmbeddingDocument、Chunk、IndexVersion | 切片、向量生成、索引状态 | 文章生命周期 |

### 7.2 Account 模块

#### 能力

- 注册、登录、刷新、登出；
- 修改密码、昵称、头像和个人简介；
- 查看公开资料；
- 查询和撤销设备会话；
- 封禁或注销账户。

#### 关键规则

- 密码只保存 Argon2id 或 bcrypt 哈希；
- Access Token 短时有效，Refresh Token 每次刷新后轮换；
- Refresh Token 只以哈希形式保存在 `auth_sessions`；
- Web 端 Refresh Token 使用 `HttpOnly + Secure + SameSite` Cookie；
- 登出、改密、封禁时服务端可撤销会话；
- Redis 只是会话读取缓存，PostgreSQL 中的会话状态是最终依据。

#### 主要用例

`Register`、`Login`、`RefreshSession`、`Logout`、`LogoutAll`、`UpdateProfile`、`ChangePassword`、`GetProfile`。

### 7.3 Source 模块

#### 能力

- 创建、验证、启停和删除 RSS/Atom Source；
- 用户订阅、取消订阅和分类；
- 保存 ETag、Last-Modified、最近抓取结果和下一次抓取时间；
- 调度到期 Source，控制站点级并发、超时和退避；
- 支持 OPML 导入导出作为后续增强。

#### 关键规则

- URL 必须为 HTTP/HTTPS，禁止访问环回、链路本地、私网和云元数据地址，防止 SSRF；
- DNS 解析结果和重定向后的每一跳都要重新校验；
- 限制响应体大小、重定向次数、连接时间和总超时；
- 同一规范化 Feed URL 只创建一个 Source，不为每个用户重复抓取；
- Scheduler 使用数据库租约或 `FOR UPDATE SKIP LOCKED` 认领任务；
- HTTP 304 只更新检查时间，不重复解析文章；
- 连续失败采用带抖动的指数退避，超过阈值进入 `degraded`，但不自动删除来源。

#### 抓取状态

```text
active → fetching → active
   │         │
   │         ├── transient failure → retry_wait
   │         └── permanent failure → degraded
   └── user action → paused
```

### 7.4 Article 模块

#### 统一内容模型

外部文章与内部文章共享 Article 聚合：

```text
Article
├── origin: external | internal
├── source_id: 外部来源可用
├── author_user_id: 内部作者可用
├── title / excerpt / cover
├── canonical_url / language
├── status: draft | published | archived | deleted
├── published_at / updated_at
├── dedupe_key / content_hash
└── ArticleContent
    ├── raw_content
    ├── sanitized_html
    └── plain_text
```

#### 关键规则

- 外部文章通过 `(source_id, dedupe_key)` 唯一约束实现业务幂等；
- `dedupe_key` 优先取 GUID 哈希，否则取规范化 URL 哈希；
- `content_hash` 判断正文是否真正变化；
- 只有作者可以编辑内部草稿；已发布文章的实质变更产生 `article.updated.v1`；
- 外部 HTML 必须消毒，禁止脚本、事件属性和危险 URL；
- 删除采用软删除与可审计状态，异步清理缓存、搜索索引、向量和对象；
- 文章事实以 PostgreSQL 的 Article 表为准；Redis 缓存和 PostgreSQL 中的全文/向量派生索引均可从正文重建。

#### 主要用例

`CreateDraft`、`UpdateDraft`、`Publish`、`Archive`、`GetArticle`、`ListAuthorArticles`、`IngestExternalArticles`、`TagArticle`。

### 7.5 Feed 模块

#### Feed 类型

| Feed | 候选来源 | 默认排序 | 登录要求 |
| --- | --- | --- | --- |
| Latest | 全部已发布文章 | `published_at DESC, id DESC` | 否 |
| Following | 已订阅 Source + 已关注作者 | `published_at DESC, id DESC` | 是 |
| Hot | 时间窗口内的有效行为聚合 | `hot_score DESC, id DESC` | 否 |
| For You | 多路召回后排序 | `rank_score DESC, article_id DESC` | 是 |

#### 游标协议

游标是 Base64URL 编码的版本化 JSON，不让客户端拼 SQL 字段：

```json
{
  "v": 1,
  "feed": "latest",
  "published_at": "2026-09-02T10:30:00Z",
  "article_id": 12345
}
```

规则：

- Latest/Following 使用 `(published_at, id)` 复合游标；
- Hot 使用 `(as_of, hot_score, id)`，所有后续页固定同一个 `as_of`；
- For You 首次返回 `feed_request_id`，候选顺序短期保存在 Redis，后续页按固定序列读取；
- 每次读取 `limit + 1` 条判断 `has_more`；
- 无效、过期或 Feed 类型不匹配的游标返回统一的 `INVALID_CURSOR`。

#### FeedItem

Feed 返回专用只读模型，不直接暴露 Article 聚合：

```text
FeedItem
├── article card
├── author/source card
├── viewer_state: liked/saved/read/read_later
├── stats: likes/comments/saves/reads
├── recommendation_reason
├── position
└── tracking: feed_request_id + impression_token
```

公共文章卡片和用户态必须分开批量查询，避免把某个用户的互动状态写入公共缓存。

### 7.6 Interaction 模块

#### 能力

- 点赞/取消点赞；
- 收藏/取消收藏；
- 稍后读、已读、阅读进度；
- 发布、删除和分页查看评论。

#### 一致性策略

- 用户能立即感知的动作先在 PostgreSQL 事务中提交；
- 唯一约束保证重复点赞、收藏和重复请求不会产生多行；
- 事务同时写 Outbox，异步更新计数、热榜、通知和推荐特征；
- `article_stats` 是派生读模型，可最终一致；
- API 的成功响应只代表事实写入成功，不把 MQ 投递完成作为响应条件；
- 请求可携带 `Idempotency-Key`，服务端保存有限时间的写入结果。

### 7.7 Relation 模块

#### 能力与规则

- 关注/取关内部作者；
- 粉丝列表、关注列表和计数；
- 禁止关注自己；
- `(follower_id, followee_id)` 唯一；
- 被封禁或已注销用户不可被新关注；
- 关注事实同步写 PostgreSQL，计数、通知和推荐特征异步更新。

注意：用户关注属于 Relation，RSS/Atom 订阅属于 Source。两者只在 Following Feed 的 Application Query 中被组合。

### 7.8 Exposure 模块

曝光不是普通访问日志，而是推荐和产品分析的业务事实。

#### 事件类型

- `impression`：卡片至少 50% 可见并持续 1 秒；
- `open`：用户打开文章详情；
- `engaged_read`：前台有效阅读达到阈值；
- `dwell`：离开详情页时上报有效停留时长；
- `dismiss`：用户明确表示不感兴趣；
- `finish`：阅读进度达到 90%。

#### 事件字段

```text
event_id             客户端 UUID，用于幂等
user_id              匿名时可空
anonymous_id         匿名设备会话
article_id
feed_type
feed_request_id
position
algorithm_version
impression_token     服务端签发，防伪造和串页
occurred_at
dwell_ms
client_context       viewport/app_version 等受控字段
```

#### 处理策略

- Web 端批量上报，每批最多 100 条，使用 `sendBeacon` 补发离页事件；
- `(event_id)` 唯一去重，服务端时间校正异常客户端时间；
- 原始事件按保留期分区或归档；
- 日级聚合用于推荐，原始明细不直接加入在线排序查询；
- 不采集正文选区、键盘输入等与推荐无关的敏感数据；
- 用户注销后按隐私策略删除或匿名化可识别记录。

### 7.9 Recommendation 模块

推荐采用“召回 → 过滤 → 特征 → 排序 → 解释”的确定性流水线，不把业务控制权交给 LLM。

#### 多路召回

1. Subscription Recall：用户订阅来源中的新文章；
2. Social Recall：用户关注作者的新文章；
3. Hot Recall：近期高质量、高参与文章；
4. Interest Recall：与用户标签偏好匹配的文章；
5. Semantic Recall：与最近有效阅读/收藏内容语义相近的文章；
6. Exploration Recall：少量新来源或长尾内容，避免信息茧房。

#### 过滤

- 已删除、未发布、被屏蔽来源；
- 用户明确“不感兴趣”的文章或来源；
- 最近已完成阅读内容；
- 同一来源连续出现过多；
- 同一文章在近期已曝光过多且无互动。

#### V1 规则排序

```text
score =
  0.30 * freshness
+ 0.20 * source_affinity
+ 0.20 * topic_affinity
+ 0.15 * semantic_similarity
+ 0.10 * quality_score
+ 0.05 * exploration_bonus
- exposure_fatigue
```

权重只是 V1 初始策略，必须保存为 `algorithm_version`，不能硬编码后失去可追溯性。离线评估使用曝光后的打开率、有效阅读率、收藏率和来源多样性，不能只优化点击率。

#### 降级顺序

```text
个性化混合推荐
    ↓ Embedding Provider、活跃向量索引或语义查询不可用
订阅 + 关系 + 标签 + 热度规则推荐
    ↓ Redis 不可用
PostgreSQL Following / Latest
```

任何降级都返回可用 Feed，并在响应元数据和指标中记录实际策略版本。

### 7.10 Embedding 模块

#### 职责

- 从 Article 读取已发布的纯文本快照；
- 按标题、段落和长度切片；
- 批量调用 Eino `Embedder`；
- 将向量写入 PostgreSQL 的 pgvector 列；
- 在 PostgreSQL 记录模型、维度、正文哈希、索引版本、切片状态和向量；
- 提供文章相似召回和查询语义召回；
- 支持全量重建、增量重建和删除补偿。

#### 切片策略

- 标题始终拼入每个正文块的上下文；
- 建议块长 500～800 tokens，重叠 50～100 tokens；
- 同一活跃索引版本使用固定模型和固定向量维度，启动时校验配置与 pgvector 列/索引定义；
- `chunk_id` 由 `article_id + content_hash + chunk_index + model_version` 确定生成；
- 同一模型版本重复消费使用 PostgreSQL `INSERT ... ON CONFLICT DO UPDATE`，而不是插入重复向量。

#### 状态机

```text
pending → processing → indexed
   │           │
   │           └── retryable error → retry_wait → processing
   └── superseded / article deleted → stale → deleting → deleted
```

#### 版本策略

- `embedding_model`、`dimension`、`chunk_policy_version` 和 `index_version` 必须入库；
- 正文哈希未变且模型版本未变时跳过；
- 更换模型但维度不变时创建新的 `index_version` 并后台重建；维度变化时创建版本化表/分区及新的 HNSW 索引；
- 读路径通过 PostgreSQL 中的活跃索引版本记录切换，不依赖外部向量数据库的集合别名；
- 切换前后都能回滚，不原地覆盖唯一可用索引；
- Embedding 失败不阻塞文章发布，只影响语义能力。

### 7.11 Search 与 Digest

Search 是跨 Article 与 Embedding 的 Application 查询能力：

- V1：使用 PostgreSQL `tsvector`、`tsquery` 和 GIN 索引检索标题、正文、来源、作者与标签；
- V1.1：使用 pgvector 余弦距离与 HNSW 索引进行语义召回；
- V1.2：关键词和向量结果用 RRF 或经过验证的归一化分数合并；
- 中文检索先基于 `simple` 文本配置、`pg_trgm` 或经基准测试确认的 PostgreSQL 分词扩展优化；首版不引入 OpenSearch。

Daily Digest 是推荐结果的持久化快照，不等于 Agent：

- 每天按用户时区生成；
- 先由 Recommendation 选出候选，再可选调用 LLM 生成摘要；
- LLM 不可用时仍能返回标题、来源和原文摘要；
- 每个条目保留文章引用和选择原因，禁止生成无来源内容。

## 8. 数据模型

### 8.1 核心表

| 表 | 关键字段 | 关键约束/索引 |
| --- | --- | --- |
| `users` | id、username、email、status、profile | username/email 唯一 |
| `user_credentials` | user_id、password_hash、changed_at | user_id 唯一 |
| `auth_sessions` | id、user_id、refresh_hash、expires_at、revoked_at | refresh_hash 唯一；user_id + status |
| `sources` | id、feed_url、site_url、etag、last_modified、next_fetch_at、status | normalized_feed_url 唯一；status + next_fetch_at |
| `subscriptions` | user_id、source_id、category、status | user_id + source_id 唯一 |
| `source_fetch_logs` | source_id、status、http_status、latency、error_code | source_id + started_at |
| `articles` | id、origin、source_id、author_user_id、title、status、published_at、dedupe_key、content_hash | source_id + dedupe_key 唯一；status + published_at + id |
| `article_contents` | article_id、raw_content、sanitized_html、plain_text | article_id 唯一 |
| `article_search_documents` | article_id、language、search_vector、updated_at | article_id 唯一；`search_vector` GIN 索引 |
| `tags` | id、name、slug | slug 唯一 |
| `article_tags` | article_id、tag_id | 复合主键 |
| `article_stats` | article_id、impressions、opens、reads、likes、saves、comments、hot_score | article_id 唯一；hot_score + article_id |
| `article_likes` | user_id、article_id、created_at | user_id + article_id 唯一 |
| `article_bookmarks` | user_id、article_id、type、created_at | user_id + article_id + type 唯一 |
| `user_article_states` | user_id、article_id、read_progress、read_at、dismissed_at | user_id + article_id 唯一 |
| `comments` | id、article_id、author_id、parent_id、content、status | article_id + created_at + id |
| `user_follows` | follower_id、followee_id、created_at | follower_id + followee_id 唯一 |
| `feed_requests` | id、user_id、feed_type、algorithm_version、created_at | user_id + created_at |
| `exposure_events` | event_id、feed_request_id、article_id、user_id、type、position、occurred_at | event_id 唯一；按 occurred_at 分区/索引 |
| `recommendation_logs` | feed_request_id、article_id、recall_channels、features、score、position | feed_request_id + article_id 唯一 |
| `embedding_records` | article_id、content_hash、model、dimension、chunk_policy_version、index_version、status | article_id + model + index_version |
| `embedding_chunks` | chunk_id、article_id、chunk_index、content_hash、model、dimension、index_version、embedding、status | article_id + index_version + chunk_index 唯一；活跃版本建立 pgvector HNSW 索引 |
| `embedding_index_versions` | id、model、dimension、chunk_policy_version、table_or_partition、status、activated_at | 同时最多一个 active 版本 |
| `outbox_events` | id、event_id、type、version、aggregate_id、payload、status、available_at | event_id 唯一；status + available_at |
| `consumer_inbox` | consumer、event_id、processed_at | consumer + event_id 唯一 |
| `idempotency_records` | user_id、key、request_hash、response、expires_at | user_id + key 唯一 |
| `notifications` | id、recipient_id、type、payload、read_at | recipient_id + created_at + id |

### 8.2 数据库原则

- 时间统一以 UTC 保存，Interfaces 层按用户时区展示；
- 金额不存在时不要引入 Decimal；分数用明确精度或整数缩放，避免不可比较的浮点游标；
- 所有列表排序必须有唯一字段兜底；
- 所有外键的删除语义必须显式定义，不依赖默认级联；
- 大字段与高频列表字段分表，Feed 不读取正文；
- `JSONB` 只用于事件快照、可解释特征和非查询核心字段；
- 全文检索列使用可重复生成的 `tsvector`，并通过 GIN 索引加速；
- 向量列使用 pgvector；同一个可查询索引内不得混用不同维度；
- 初始化迁移显式启用 `vector` 扩展；如采用 trigram 检索，再显式启用 `pg_trgm`，并在启动检查中验证扩展版本；
- 每个高频查询都要有对应索引和 `EXPLAIN (ANALYZE, BUFFERS)` 证据；
- 测试和生产使用同一种数据库，不用 SQLite 模拟 PostgreSQL 的事务、锁、全文和向量特性。

## 9. HTTP API 设计

### 9.1 通用约定

- Base URL：`/api/v1`；
- JSON 字段使用 `snake_case`；
- 创建返回 201，删除成功返回 204，异步任务接受返回 202；
- 列表统一返回 `items`、`next_cursor`、`has_more`；
- 所有响应带 `X-Request-ID`；
- 写接口支持 `Idempotency-Key`；
- 错误返回稳定业务码，不向客户端泄露 SQL、堆栈和上游密钥。

```json
{
  "error": {
    "code": "ARTICLE_NOT_FOUND",
    "message": "文章不存在或不可见",
    "request_id": "01J...",
    "details": {}
  }
}
```

### 9.2 端点清单

#### Account

```text
POST   /auth/register
POST   /auth/login
POST   /auth/refresh
POST   /auth/logout
POST   /auth/logout-all
GET    /me
PATCH  /me
PATCH  /me/password
GET    /users/{user_id}
```

#### Source 与订阅

```text
POST   /sources/discover
POST   /sources
GET    /sources/{source_id}
PATCH  /sources/{source_id}
POST   /sources/{source_id}/refresh
POST   /subscriptions
GET    /subscriptions
PATCH  /subscriptions/{source_id}
DELETE /subscriptions/{source_id}
```

#### Article

```text
POST   /articles
GET    /articles/{article_id}
PATCH  /articles/{article_id}
POST   /articles/{article_id}/publish
POST   /articles/{article_id}/archive
GET    /users/{user_id}/articles
GET    /articles/{article_id}/similar
```

#### Feed

```text
GET    /feeds/latest?cursor=&limit=
GET    /feeds/following?cursor=&limit=
GET    /feeds/hot?cursor=&limit=
GET    /feeds/for-you?cursor=&limit=
```

#### Interaction 与 Relation

```text
PUT    /articles/{article_id}/like
DELETE /articles/{article_id}/like
PUT    /articles/{article_id}/bookmarks/{type}
DELETE /articles/{article_id}/bookmarks/{type}
PUT    /articles/{article_id}/read-state
GET    /articles/{article_id}/comments
POST   /articles/{article_id}/comments
DELETE /comments/{comment_id}
PUT    /users/{user_id}/follow
DELETE /users/{user_id}/follow
GET    /users/{user_id}/followers
GET    /users/{user_id}/following
```

#### Exposure、搜索、推荐和通知

```text
POST   /exposures/batch
GET    /search?q=&cursor=&source_id=&tag=&from=&to=
GET    /digests/daily?date=
GET    /notifications?cursor=&limit=
PUT    /notifications/{notification_id}/read
GET    /notifications/stream
```

## 10. 关键业务流程

### 10.1 外部文章采集

```text
Scheduler 扫描 next_fetch_at <= now
  → 数据库租约认领 Source
  → 投递 source.fetch.requested.v1
  → Fetch Worker 校验目标地址与重定向
  → 带 If-None-Match / If-Modified-Since 请求
  → 304：更新抓取状态并结束
  → 200：限制大小后解析 RSS/Atom
  → 规范化 URL、生成 dedupe_key/content_hash
  → 一个数据库事务批量 upsert Article + Outbox
  → 发布 article.published/updated
  → 缓存、Feed、Embedding、统计消费者各自处理
```

抓取响应只在内存或受控临时文件中处理，失败日志不保存完整隐私正文。

### 10.2 内部文章发布

```text
Hertz Handler 校验 DTO 和身份
  → PublishArticle Use Case
  → 加载 Article 聚合并校验作者与状态
  → 事务更新 Article + ArticleContent + Outbox
  → 返回已发布文章
  → Outbox Relay 发布 article.published.v1
  → 异步刷新 Feed、Embedding、通知和搜索读模型
```

### 10.3 Feed 读取与曝光

```text
请求 Feed
  → 解析版本化游标
  → 选择候选策略和降级策略
  → 批量获取 Article Card
  → 批量获取用户互动状态
  → 生成 feed_request_id、position、impression_token
  → 返回 FeedItem
  → Web 达到可见阈值后批量上报曝光
  → Exposure 聚合更新疲劳度、质量和离线指标
```

返回 Feed 不等于发生曝光；只有客户端满足可见阈值并上报后才计入 impression。

### 10.4 点赞与计数

```text
PUT like
  → PostgreSQL 事务 INSERT ... ON CONFLICT DO NOTHING/唯一约束
  → 写 interaction.liked.v1 Outbox
  → 立即返回 viewer_state=liked
  → Stats Consumer 幂等更新 article_stats
  → Hot Consumer 更新窗口 ZSET
  → Notification Consumer 写通知并尝试 SSE 推送
```

取消点赞使用对称事件。消费者不能通过“收到几条消息”直接累加，必须结合事件幂等或按事实表重算，防止重复投递造成计数漂移。

### 10.5 For You 推荐

```text
并行召回 subscription/social/hot/interest/semantic
  → 按 article_id 合并并记录 recall_channels
  → 过滤不可见、已屏蔽和过度曝光内容
  → 批量组装新鲜度、偏好、质量、语义等特征
  → 按 algorithm_version 计算 score
  → 加入来源多样性与探索约束
  → 固化候选顺序到短期 Redis Snapshot
  → 保存 recommendation_log
  → 返回首屏与可解释 reason
```

## 11. 事件与异步任务

### 11.1 事件信封

```json
{
  "event_id": "01J...",
  "event_type": "article.published",
  "event_version": 1,
  "aggregate_type": "article",
  "aggregate_id": "12345",
  "occurred_at": "2026-09-02T10:30:00Z",
  "trace_id": "...",
  "payload": {}
}
```

消费者只兼容自己声明的事件版本；破坏性变化升级版本，不静默改变 Payload 含义。

### 11.2 Topic 与 Queue

```text
velis.domain.events (topic exchange)
├── article.published.v1
├── article.updated.v1
├── article.archived.v1
├── interaction.liked.v1
├── interaction.unliked.v1
├── interaction.saved.v1
├── relation.followed.v1
├── relation.unfollowed.v1
└── exposure.recorded.v1

velis.tasks (direct/topic exchange)
├── source.fetch.v1
├── embedding.generate.v1
├── embedding.delete.v1
├── stats.aggregate.v1
├── feed.project.v1
└── digest.generate.v1
```

每类任务有独立 Queue、并发度、超时、Retry Queue 和 DLQ，避免 Embedding 积压阻塞抓取或统计。

### 11.3 Outbox 与 Inbox

- 业务事务写 `outbox_events`；
- Relay 用 `SKIP LOCKED` 并发认领，发送启用 Publisher Confirm；
- 成功后标记 `published_at`，失败记录次数和下次时间；
- 达到阈值转为 `failed` 并报警，不无限热循环；
- Consumer 在同一事务中写 `consumer_inbox` 和业务结果；
- `(consumer, event_id)` 唯一，重复消息直接 ACK；
- 只有不可重试格式错误进入 DLQ；临时基础设施错误进入带退避的 Retry Queue；
- 提供受审计的 DLQ 查看与重放命令。

## 12. Redis 设计

| Key | 类型 | 作用 | TTL/上限 |
| --- | --- | --- | --- |
| `article:card:v1:{id}` | String | 公共文章卡片 | 10 分钟 + 随机抖动 |
| `feed:latest:v1` | ZSET | 最新热窗口文章 ID | 最多 10,000 条 |
| `feed:hot:1m:{bucket}` | ZSET | 分钟热度增量 | 2 小时 |
| `feed:hot:snapshot:{as_of}` | ZSET | 稳定热榜快照 | 5 分钟 |
| `feed:rec:{feed_request_id}` | List/ZSET | 固化推荐候选 | 15 分钟 |
| `viewer:state:{user_id}:{article_id}` | Hash/String | 短期用户态 | 5 分钟，可选 |
| `ratelimit:{scope}:{subject}` | String | 限流 | 等于窗口 |
| `lock:{resource}` | String | 短租约协调 | 必须有随机 token 和安全解锁 |

原则：

- Redis Client 可空时 Application 仍能工作；
- Cache-Aside 回源使用进程内 singleflight，跨实例锁只用于确有价值的热点；
- 缓存内容带 schema version，避免结构升级误读旧值；
- TTL 加随机抖动，防止同批 Key 同时过期；
- 写成功后删除或更新相关 Key，事件消费者负责跨读模型刷新；
- 不缓存完整正文，不用 Key 扫描实现业务查询；
- 任何锁都不是业务正确性的唯一保障，最终由数据库约束兜底。

## 13. 安全与隐私

### 13.1 API 安全

- 登录、注册、评论、关注、发布和曝光分别限流；
- CORS 使用显式 Origin 列表，不允许带凭证的通配符；
- Refresh Cookie 模式下对状态变更接口实施 CSRF 防护；
- JWT 固定算法并校验 issuer、audience、exp、nbf、session id；
- 文件上传校验 MIME、扩展名、魔数、大小和图片解码结果；
- Markdown 渲染后统一 HTML 消毒；
- 错误日志不记录密码、Token、Cookie、完整正文和 Embedding Provider Key；
- 管理接口与 pprof 默认只监听 loopback 或内部管理端口。

### 13.2 抓取安全

- SSRF 防护覆盖首次解析、DNS 重绑定和每次重定向；
- User-Agent 明确标识 Velis 和联系入口；
- 尊重站点抓取策略和合理频率；
- 压缩响应设置解压后大小上限，防止压缩炸弹；
- 解析器对畸形 XML、超深嵌套和超大字段设置限制；
- 每 Source 和每 Host 有独立并发与退避策略。

### 13.3 隐私

- 曝光和推荐日志只收集实现功能所需字段；
- 支持导出订阅、文章和用户行为；
- 支持注销后的删除/匿名化流程；
- 原始曝光明细设置明确保留期，聚合数据去标识化；
- 推荐原因不得泄露其他用户的私密行为。

## 14. 可观测性与运维

### 14.1 日志

使用结构化日志，最少包含：

```text
timestamp level service env request_id trace_id
user_id(允许时) event_id consumer use_case error_code latency_ms
```

禁止把任意 `err.Error()` 原样返回客户端。日志中的外部 URL、标题和正文片段需要长度限制与换行清理。

### 14.2 指标

#### HTTP

- 请求数、状态码、延迟直方图、请求体大小；
- 当前连接数、SSE 连接数、限流拒绝数；
- 各 Feed 类型命中率和降级次数。

#### 抓取

- 到期 Source 数、认领延迟、HTTP 状态、304 比率；
- 解析失败、去重命中、每 Source 抓取时长；
- Host 级错误率和退避状态。

#### MQ

- Outbox backlog、最老事件年龄、发布失败；
- Queue depth、消费速率、Retry 和 DLQ 数量；
- Consumer 处理延迟和重复事件数量。

#### 推荐与 Embedding

- 各召回通道候选数、过滤率、排序耗时；
- 推荐策略版本、降级率、来源多样性；
- Embedding 调用耗时、批大小、失败率、Token/费用；
- 索引 pending/stale 数和新旧版本覆盖率。

### 14.3 健康检查

- `/livez`：进程事件循环仍可工作，不探测外部依赖；
- `/readyz`：按进程职责判断必要依赖；
- API：PostgreSQL 必须可用，Redis/RabbitMQ 故障可降级但返回明细状态；
- Worker：PostgreSQL 与 RabbitMQ 必须可用；Embedding Consumer 额外检查 pgvector 扩展、活跃索引版本和 Provider；
- `/metrics` 和 pprof 不暴露在公网业务端口。

### 14.4 告警

- Outbox 最老未投递事件超过阈值；
- 抓取成功率持续下降或单 Host 错误激增；
- Feed P95/P99 或错误率超阈值；
- DLQ 出现新消息；
- Embedding 新版本重建停滞；
- PostgreSQL 连接池耗尽、慢查询、HNSW 索引异常或 Redis 内存逼近上限。

## 15. Web 客户端

### 15.1 页面

```text
/login                 登录/注册
/onboarding            添加首批来源和兴趣标签
/                      For You
/latest                最新流
/following             关注流
/hot                   热榜
/article/:id           文章详情与评论
/editor/:id?           Markdown 编辑/预览/发布
/saved                 收藏与稍后读
/search                关键词/语义检索
/sources               来源、订阅和分类管理
/profile/:id           用户资料、文章与关系
/settings              会话、偏好、隐私和导出
```

### 15.2 前端架构

- `features/*` 按 account/feed/article/interaction/source/search 拆分；
- Pinia 只保存跨页面状态，服务端数据使用专门查询缓存层或轻量 composable；
- API 类型从 OpenAPI 生成或由契约检查保障，不手工维护两份同名 DTO；
- Access Token 仅保存在内存；Refresh Token 由 HttpOnly Cookie 管理；
- Feed 使用虚拟列表，但曝光观察器以实际 DOM 可见性为准；
- Optimistic UI 只用于可回滚的点赞/收藏，失败必须恢复并展示原因；
- SSE 断线使用带抖动退避重连，依靠 `Last-Event-ID` 或通知列表补偿；
- Markdown 编辑器自动保存草稿，但服务端仍校验版本号防止覆盖并发编辑。

### 15.3 Feed 曝光实现

1. FeedItem 渲染时保存服务端返回的 tracking 字段；
2. `IntersectionObserver` 检测 50% 可见；
3. 持续 1 秒后生成 impression event；
4. 同一 `feed_request_id + article_id` 在会话内只生成一次 impression；
5. 每 5 秒或累计 20 条批量上报；
6. 页面隐藏或卸载时通过 `sendBeacon` 尝试补发；
7. 服务端最终仍以 `event_id` 唯一约束去重。

### 15.4 可访问性与体验

- 键盘可完整操作 Feed、收藏、评论和编辑器；
- 图片包含替代文本并懒加载；
- 支持浅色/深色主题和阅读字号；
- 加载、空状态、部分降级和错误状态分别设计；
- 推荐卡片展示简短原因并提供“不感兴趣”；
- 不用无限骨架屏掩盖错误，超时后提供重试和降级入口。

## 16. 测试策略

### 16.1 测试金字塔

| 层次 | 重点 | 工具/方式 |
| --- | --- | --- |
| Domain 单元测试 | 状态机、不变量、值对象、打分策略 | Go `testing`，无 I/O |
| Application 单元测试 | 权限、事务编排、降级、端口调用 | Fake/Mock Repository、Clock、EventBus |
| Infrastructure 集成测试 | PostgreSQL/pgvector、Redis、RabbitMQ、MinIO | testcontainers-go + 真实依赖版本 |
| Interfaces 测试 | 绑定、鉴权、状态码、错误结构、游标 | Hertz `ut.PerformRequest` |
| 契约测试 | OpenAPI、事件 Schema、向后兼容 | Schema 校验 + Golden Files |
| Web 组件测试 | 状态、交互、错误恢复 | Vitest + Vue Test Utils |
| 端到端测试 | 用户闭环和跨进程链路 | Playwright + Compose |
| 并发/竞态测试 | 重复写、锁、Hub、缓存回源 | `go test -race -count=1 ./...` |
| 性能测试 | Feed、曝光批量、抓取消费 | k6 + 固定数据集 |

### 16.2 必测场景

#### Account

- 并发注册相同用户名只有一个成功；
- Refresh Token 轮换后旧 Token 不能再次使用；
- 改密和登出全部能撤销既有会话；
- Redis 不可用时安全语义不改变。

#### Source/Article

- 同一 Feed 连续抓取不会产生重复文章；
- HTTP 304 不触发解析和 Embedding；
- 同 GUID 内容变更按策略更新并产生一个新事件；
- SSRF、重定向到私网、超大响应和畸形 XML 被拒绝；
- 外部 HTML 清理后不能执行脚本。

#### Feed

- 相同发布时间的文章跨页不重不漏；
- 插入新文章不破坏已开始的热榜/推荐快照分页；
- 批量用户态没有 N+1 查询；
- Redis 故障时 Latest/Following 正确回源；
- 匿名 Feed 不泄露任何用户态字段。

#### Interaction/Relation

- 重复点赞、收藏、关注保持幂等；
- MQ 重复投递不会让计数翻倍；
- 取消与重复事件乱序时最终状态正确；
- 用户不能关注自己或修改他人文章。

#### Exposure/Recommendation

- 重复 event_id 只记一次；
- 伪造或跨请求 impression_token 被拒绝；
- Embedding Provider、pgvector 查询或活跃索引版本不可用时能降级；
- 同一算法版本和同一候选特征得到确定结果；
- 过度曝光文章受到疲劳惩罚；
- recommendation_log 能解释文章来自哪些召回通道。

#### Embedding

- 正文哈希未变不重复调用模型；
- 重复消息只 upsert 同一批 chunk；
- 模型维度与 pgvector 列/索引定义不一致时快速失败并报警；
- 新版本向量未达到完整性阈值前不会切换活跃版本；
- 删除文章最终删除向量，补偿任务可重试。

### 16.3 性能验收方法

性能数字必须绑定硬件、数据量、依赖版本和压测脚本，不能只写一个 QPS。初始基线建议：

- 数据集：10,000 用户、100,000 文章、每用户 100 个订阅/关注关系；
- 场景：70% Feed 读取、15% 详情、10% 曝光批量、5% 互动写入；
- 持续 10 分钟并包含 2 分钟预热；
- 比较 Redis 正常与 Redis 禁用两种结果；
- 记录吞吐、P50/P95/P99、错误率、DB QPS、缓存命中率、CPU、内存和 goroutine；
- 验收目标由第一次可复现基线确定，再针对具体瓶颈优化，不预先宣称生产容量。

## 17. 配置与部署

### 17.1 配置原则

- 配置文件提供非敏感默认值；
- 密码、Token Key、Provider Key 只通过环境变量或 Secret 注入；
- 启动时校验必填项、URL、超时、池大小和模型维度；
- 输出配置摘要时自动脱敏；
- 开发、测试、生产使用同一配置结构，不在代码里判断环境后偷偷改变语义。

### 17.2 本地 Compose

```text
postgres（包含 pgvector 扩展）
redis
rabbitmq
minio
velis-api
velis-worker
velis-web
prometheus
grafana
otel-collector（可选）
```

Embedding Provider 默认允许使用本地 Ollama 适配器；CI 中使用确定性 Fake Embedder，不能依赖真实收费 API。

### 17.3 启动与关闭

1. 执行 `velis-migrate up`；
2. 启动必要依赖；
3. API/Worker 完成连接和配置校验；
4. readiness 通过后接收流量；
5. 关闭时先置 readiness 失败；
6. API 等待在途请求和 SSE 清理；
7. Worker 停止拉取新消息，等待当前消息 ACK/NACK；
8. 最后关闭连接池、Tracing Exporter 和日志缓冲。

## 18. 开发阶段与验收

开发顺序按依赖关系组织，不按“先把每层目录建完”组织。

### M0：工程骨架

交付：

- 四层目录、Composition Root 和依赖检查；
- Hertz `/livez`、`/readyz`、统一错误和 Request ID；
- PostgreSQL/pgvector 迁移、配置加载、结构化日志；
- Compose、CI、基础测试和 `go test -race`。

验收：空业务也能一键启动、迁移、测试和优雅关闭。

### M1：账户与文章最小闭环

交付：

- Account 注册、登录、刷新和资料；
- 内部 Markdown 草稿、发布、详情与归档；
- 图片上传和安全 HTML 渲染；
- Web 登录、编辑器、详情页。

验收：新用户能从浏览器完成注册并发布一篇可阅读文章。

### M2：来源采集与基础 Feed

交付：

- Source/Subscription、Scheduler、RSS/Atom Fetcher；
- ETag、Last-Modified、去重、重试；
- Latest/Following 复合游标；
- Web 来源管理和 Feed 页面。

验收：添加真实 Feed 后可自动出现新文章，连续抓取不重复，分页不重不漏。

### M3：互动、关系与异步可靠性

交付：

- Like、Bookmark、ReadState、Comment、Follow；
- RabbitMQ、Outbox/Inbox、Retry/DLX；
- Article Stats、通知和 SSE；
- Redis 文章卡片与时间线缓存。

验收：重复请求与重复消息不会破坏事实或计数，MQ 短暂中断后可恢复。

### M4：曝光、Hot 与推荐 V1

交付：

- Web 曝光采集、批量上报和去重；
- Hot 时间窗口与稳定快照；
- 多路非语义召回、规则排序、推荐日志和原因；
- For You 降级链路。

验收：每个推荐项可追溯到请求、算法版本、召回通道和核心特征。

### M5：Embedding 与混合检索

交付：

- Eino Embedder Port/Adapter；
- 切片、pgvector HNSW 索引、版本和重建；
- Semantic Recall、相似文章和混合搜索；
- Embedding 指标、费用保护和降级。

验收：正文变更能异步更新向量；关闭 Embedding Provider 或禁用语义查询后，文章发布和基础 Feed 仍正常。

### M6：质量与交付加固

交付：

- 全链路追踪、Prometheus/Grafana、告警；
- 安全、契约、E2E、竞态和性能测试；
- 备份恢复演练、DLQ 重放和索引重建命令；
- 部署文档和演示数据。

验收：在干净环境可按文档部署，通过自动化测试并完成一次故障降级演练。

## 19. 完成定义

每个模块或用例只有同时满足以下条件才算完成：

- 业务规则写在 Domain/Application，而不是 Handler 或 GORM Hook；
- API 与事件契约已更新并通过兼容检查；
- 数据库迁移可向前执行，必要时有明确回滚/补偿策略；
- 正常、边界、权限、幂等和故障场景有测试；
- 日志、指标和 Trace 能定位失败位置；
- 所有外部调用有超时、取消、重试边界和错误分类；
- 缓存/MQ/Embedding 故障时的行为已定义并验证；
- 没有新增数据竞争、goroutine 泄漏、N+1 或无界队列；
- Web 具备加载、空、错误、重试和降级状态；
- 文档描述的是当前已实现能力，规划项明确标记为规划。

## 20. 关键架构决策

### ADR-001：先模块化单体，后按证据拆服务

选择：首版只使用 Hertz API + Worker，不立即用 Kitex 拆账户、文章、Feed 等服务。

原因：四层边界和异步任务已经能建立清晰所有权；过早拆分会引入服务发现、RPC 契约、分布式事务和部署成本，却没有独立扩缩容证据。

拆分触发条件：

- 推荐/Embedding 需要独立 GPU、连接池或发布周期；
- 单模块资源消耗持续影响 API SLO；
- 团队所有权和部署节奏真实分离；
- RPC 失败、重试、幂等和观测方案已能被自动化验证。

### ADR-002：PostgreSQL 是唯一持久化业务/向量数据库，Redis 只做缓存

选择：文章、行为、关系、事件、全文检索文档、Embedding 状态和向量均保存在 PostgreSQL；Redis 只保存可丢弃、可重建的缓存、热时间线、热榜和短期推荐候选。

结果：不再维护两套持久化数据库之间的数据同步。关系事实仍是权威数据；同库中的 `tsvector` 和 pgvector 向量属于可重建派生数据，Redis 故障时可以回源 PostgreSQL。

### ADR-003：核心写同步提交，派生效果异步处理

选择：点赞、关注、发布等用户事实同步落 PostgreSQL，并在同一事务写 Outbox；计数、通知、热榜和特征异步更新。

结果：API 成功语义清楚，MQ 短暂故障不会产生“返回成功但事实未保存”或“双路径重复写”。

### ADR-004：推荐可解释且可降级

选择：V1 使用版本化规则排序；Embedding 只是一个召回/特征通道。

结果：可以逐条解释推荐来源，离线重放排序，并在 AI 基础设施故障时维持产品核心功能。

### ADR-005：四层按依赖组织，不按名称装饰

选择：领域对象与 GORM/Hertz/Eino/RabbitMQ 类型完全隔离，通过端口和映射连接。

结果：四层不是目录命名练习；核心业务能在无数据库、无网络、无框架的测试中运行。

## 21. 实施时首先回答的问题

在正式编码前，团队只需补齐以下项目级选择，不应重新讨论本文已经确定的主线：

1. Go module 路径与许可证；
2. 用户登录使用邮箱、用户名，还是两者都支持；
3. 默认 Embedding Provider 与模型、维度、预算上限；
4. 首个 pgvector 索引版本使用的模型、维度、距离函数和 HNSW 参数；
5. 内部文章是否允许公开访问，默认可见性是什么；
6. 评论是否允许嵌套一层回复；
7. 原始曝光事件和抓取日志的保留期；
8. 首次性能基线使用的硬件和数据生成规模。

这些决定应形成短小 ADR，并锁定相应契约、迁移和测试，不把模糊选择散落到实现代码中。

## 22. 参考资料

- [feedsystem_video_go 仓库](https://github.com/LeoninCS/feedsystem_video_go)：模块闭环、API/Worker、Redis、RabbitMQ、SSE、Compose 等工程思路来源。
- [feedsystem_video_go 项目设计](https://github.com/LeoninCS/feedsystem_video_go/blob/main/feedsystem_video_go%E9%A1%B9%E7%9B%AE%E8%AE%BE%E8%AE%A1.md)：Feed 游标、缓存、事件和运维能力说明。
- [CloudWeGo Hertz 文档](https://www.cloudwego.io/docs/hertz/)：HTTP 框架能力与入口。
- [Hertz 参数绑定与校验](https://www.cloudwego.io/docs/hertz/tutorials/basic-feature/binding-and-validate/)：Interfaces 层 DTO 绑定与校验依据。
- [Hertz 优雅关闭](https://www.cloudwego.io/docs/hertz/tutorials/basic-feature/graceful-shutdown/)：API 停机和在途请求处理依据。
- [Hertz 单元测试](https://www.cloudwego.io/docs/hertz/tutorials/basic-feature/unit-test/)：Handler/Router 测试工具依据。
- [CloudWeGo biz-demo](https://github.com/cloudwego/biz-demo)：Hertz、Kitex 与 Clean Architecture 的官方业务示例集合。
- [CloudWeGo Eino Components](https://www.cloudwego.io/docs/eino/core_modules/components/)：Embedder、Indexer、Retriever 抽象依据。
- [CloudWeGo Eino Embedding 集成](https://www.cloudwego.io/docs/eino/ecosystem_integration/embedding/)：Embedding Provider 适配范围。
- [PostgreSQL 全文检索](https://www.postgresql.org/docs/current/textsearch.html)：`tsvector`、`tsquery`、排名和索引设计依据。
- [pgvector](https://github.com/pgvector/pgvector)：PostgreSQL 向量类型、距离函数、HNSW/IVFFlat 和混合检索依据。
