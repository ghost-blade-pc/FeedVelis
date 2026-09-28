# Design

## Context

见 [proposal.md](proposal.md)。当前公开文章详情和 latest 由 PostgreSQL 提供，搜索独立使用 OpenSearch 并在 PostgreSQL 复核可见性；认证开启时才装配 `/me/*` 路由。`domain/recommendation`、`application/recommendation` 尚为占位，Web 文章详情与复用卡片目前没有反馈状态。文章 ID 为 bigint，用户 ID 为 UUID，当前增强主题属于文章当前修订的可选结果。现有数据库迁移最大编号为 `000009`。

## Goals / Non-Goals

**Goals:**

- 写请求在并发、重试和网络重放下保持固定上限；用户身份只能由认证中间件提供。
- PostgreSQL 中的事实能够直接供未来 recommend 使用，且不把用户画像写进 OpenSearch 文档。
- 匿名阅读、latest、搜索和正文读取保持现有性能与可用性；反馈故障不阻塞正文展示。

**Non-Goals:**

- 本阶段不提供 recommend HTTP 端点、排序策略或对 latest/搜索结果的个性化过滤。
- 不做匿名设备识别、浏览器持久化用户 ID、公开收藏数或反馈时间线。

## Decisions

### 1. 使用独立反馈模块和 PostgreSQL 事实表

新增 `domain/articlefeedback`、`application/articlefeedback` 与 PostgreSQL 仓储，HTTP Handler 只做 ID/参数校验、读取认证身份和错误映射。`application/recommendation` 定义或复用一个只读画像端口，由反馈 Application 实现并通过 bootstrap 注入；Domain 不引入 SQL、HTTP 或 AI 类型。

新增 `000010_add_article_feedback.up.sql` 和 `.down.sql`：

- `article_read_windows(user_id uuid, article_id bigint, window_start timestamptz, first_seen_at timestamptz)`，以 `(user_id, article_id, window_start)` 为主键，并对 `first_seen_at` 建清理索引。
- `article_favorites(user_id, article_id, created_at)`，以 `(user_id, article_id)` 为主键。
- `article_not_interested(user_id, article_id, created_at, expires_at)`，以 `(user_id, article_id)` 为主键，并对 `expires_at` 建清理索引。

三表外键引用现有 `users` 与 `articles`，删除文章仍按现有软删除语义保留事实。与在 `articles` 增列或把个人状态放进搜索索引相比，独立表避免全局文章写放大和用户状态进入共享投影。与单个通用事件表相比，三表的唯一约束与保留期直接表达三种不同生命周期。

### 2. 服务端时间与原子状态写入

全部反馈写入先以当前事务内文章 `status='published'` 为条件验证；以服务端 UTC 时间决定窗口和有效期。阅读采用 30 分钟 UTC 固定桶及 `INSERT ... ON CONFLICT DO NOTHING`，每桶仅保留首次事实。画像再按 UTC 自然日聚合，每用户/文章/日最多 1 次；90 天以前的记录由读取过滤并由 Worker 定期小批量清理。此双层上限处理刷新、重试及一日内周期性刷请求。

收藏 `PUT` 使用唯一键插入并忽略冲突，`DELETE` 删除当前键且不存在也成功；负反馈 `PUT` 只在无记录或原记录已过期时写入 `now + 180 days`，有效期内不更新过期时间，`DELETE` 删除当前键。状态写入使用单条 SQL/事务约束处理并发，不依赖客户端 `Idempotency-Key` 或文章的 `If-Match`：这些接口本身具有资源状态幂等语义，且不修改文章聚合版本。负反馈重复设置无法通过刷新续期；过期后再次明确操作才开启新有效期。若收藏和负反馈同时存在，画像保留两种事实，但文章级排除优先，忽略该文章的正向排序贡献。

状态变更若与下架并发，事务中对文章行采用与可见性写入兼容的锁或等效条件写，确保提交时不会为已非公开文章接受新反馈。下架后既有事实不泄露，也不进入画像；恢复公开后仍可重新参与，负反馈须仍在有效期内。

### 3. API 采用本人资源与批量状态读取

在认证开启时注册：

- `POST /api/v1/me/articles/{article_id}/reads`：成功返回 `204`，空正文；仅由 Web 详情成功展示后调用。
- `PUT|DELETE /api/v1/me/articles/{article_id}/favorite`：成功返回 `204`。
- `PUT|DELETE /api/v1/me/articles/{article_id}/not-interested`：成功返回 `204`。
- `GET /api/v1/me/article-feedback?article_ids=1,2,...`：成功返回 `items` 数组，顺序与请求一致，每项有 `article_id`、`favorited`、`not_interested`、`not_interested_expires_at`。

请求体为空；Handler 拒绝意外正文和无效 ID。所有写入必须携带 Bearer 认证；不接受请求中的用户 ID。GET 一次最多 50 个互异 ID，用单次批量 PostgreSQL 查询按当前公开事实关联状态；下架项省略。Web 详情请求单 ID，latest/搜索列表在已登录时批量请求其可见卡片 ID，避免 N+1。Web 对匿名状态不发本人读取或阅读上报请求，并在按钮处引导登录。与在公共 `ArticleItem` 上增加 `favorited` 相比，独立端点避免匿名缓存或共享搜索结果混入用户状态。

### 4. 画像端口先交付确定性规则和解释字段

画像读取接口接收当前用户 ID 与至多 200 个候选文章 ID，返回这些候选中的有效文章级排除及固定理由 `not_interested_article`，以及最多 500 篇最近有反馈的公开文章所形成的主题/RSS 来源证据。阅读按近 90 天的不同 UTC 日计数，单文章阅读分数封顶 3；当前收藏额外加 3；有效负反馈对文章直接排除，对其当前主题和 RSS 来源各产生一次负证据，单篇只计一次。主题和来源的正负总权重分别限制在 `[-20, 20]`，每个维度附正负样本篇数；该聚合只提供排序输入，不在本 change 决定推荐排名。缺少主题或 RSS 来源时该维度不生成证据。负反馈原因始终从当前有效的文章级事实读取，不能因 500 篇聚合上限漏排候选。

按当前公开文章及当前修订选中的增强结果推导维度；不把反馈时的主题或来源快照固化到反馈表。这样文章修改主题后画像反映当前内容，但画像随内容更新变化；后续 recommend 可用稳定解释字段展示其使用的证据。显式收藏与负反馈状态以 PostgreSQL 为唯一事实源，Redis 缓存留待 recommend change 设计。

### 5. Web 交互独立于正文读取

详情成功展示后异步上报阅读；失败只影响反馈提示，不使正文页变成失败态。详情与复用卡片显示收藏和“不感兴趣”按钮，按钮标识当前状态并提供撤销；匿名用户点击时跳转现有登录流程。列表批量拉取状态，详情重新拉取本人状态；请求取消、账户变化和反馈写失败时按已确认服务端状态渲染。对同一文章连续点击应串行化或等待前次写完成后重新读取，避免响应乱序覆盖最后意图。不在浏览器本地存储任何反馈画像或跨账户缓存。

## Risks / Trade-offs

- [热门文章被恶意重复上报] → 唯一窗口约束和单日权重上限；未来如观察到写流量问题，再加低成本限流，不改变语义。
- [画像聚合受反馈数量影响] → 候选集合上限 200、聚合样本上限 500、批量读取与索引；负反馈候选排除单独完整检查。
- [标签随文章修订变化] → 画像只用当前可见修订；解释返回实际使用的当前维度，不把旧标签当事实。
- [反馈表含用户行为数据] → 不写原文、IP、设备 ID 或自由文本；日志/指标不记用户 ID、文章 ID 或反馈详情，清理过期记录。
- [Web 列表与状态短暂不同步] → 状态批量请求失败时保留文章卡片、提示交互不可用；账户切换清空本地状态。

## Migration Plan

1. 先备份专用数据库并执行新编号迁移；新增表不改写旧表或旧迁移，旧 API 可继续运行。应用升级后启用反馈路由和 Web 交互，Worker 小批量清理 90 天前阅读事实与过期负反馈。
2. 以真实 PostgreSQL 验证迁移、并发幂等、账户隔离、下架竞争、到期清理和画像；以现有 `make check` 验证架构与 Web 行为。
3. 应用回滚时先停用反馈写入或回退 API/Web 版本；保留新表和已收集事实即可安全恢复。数据表非空时 down migration 明确拒绝以避免静默丢失用户反馈；若确需降级，先经备份、导出和人工确认，再执行数据清理及 down migration。
