# Design

## Context

参见 `proposal.md` 的动机。I4.1 已建立 `velis-articles-read` 稳定读别名、schema v1 严格映射、每文章唯一文档与持久 tombstone；文档已有 `visible`、`source_id`、有效 `published_at`、标题、纯正文、excerpt、AI summary、keywords、topics 和向量。当前 OpenSearch Go 适配器只实现管理与 Bulk 写入，API 进程不创建 OpenSearch 客户端，公开读取仅有 PostgreSQL latest/详情，`ArticleRepository.ListPublished` 也只支持 latest 游标，Web 的文章卡片仍内嵌在 `ArticleList.vue`。

搜索索引是最终一致的可丢弃投影，不是可见性或响应内容的事实源。现有核心 readiness 只依赖 PostgreSQL；OpenSearch 配置与故障不能扩大到 latest、详情或文章写路径。OpenSearch 官方也明确指出裸 `search_after` 会受并发索引变化影响，而 PIT + `search_after` 是稳定分页的首选方式，因此本设计使用短生命周期 PIT，而不使用 `from/size` 或 scroll。

## Goals / Non-Goals

**Goals:**

- 在 Domain/Application 中定义不含 OpenSearch、SQL 或 Hertz 类型的搜索用例与端口。
- 让一个请求内的候选扫描、PostgreSQL 可见性复核、游标推进和 PIT 清理均有明确上限。
- 把查询计划、游标结构和排序语义显式版本化，使权重或排序变化不会误读旧游标。
- 复用现有 `ArticleItem` 作为唯一对外展示事实，OpenSearch 只返回候选 ID 与排序值。
- 以固定相关性夹具验证现有 CJK schema v1 是否足够，不把分析器升级偷偷混入查询实现。

**Non-Goals:**

- 不从向量字段读取或生成查询 Embedding，不引入 KNN、RRF、推荐、个性化或搜索缓存。
- 不提供高亮、自动补全、拼写纠正、分面聚合、总命中数或任意排序选项。
- 不保证长时间保存搜索会话；游标超过短 PIT 生命周期后由调用方重新查询第一页。
- 不在本 change 中增加公开 Source 目录；首版 Web 的来源过滤直接使用正整数 `source_id`。

## Decisions

### 1. 新建独立 `articlesearch` 应用模块，搜索路由始终存在

新增 `application/articlesearch`，定义 `QueryIndex`、`PublicArticleReader`、`Observer` 端口以及 `Service`。`QueryIndex` 只接收规范化查询、过滤、PIT 与排序位置并返回候选 ID；`PublicArticleReader` 按一组 ID 返回当前公开 `article.ListItem`。OpenSearch 与 PostgreSQL 分别在 Infrastructure 实现端口，Hertz 只解析参数和呈现结果。

API composition root 无论是否配置 OpenSearch都向路由提供搜索服务：配置完整时装配真实客户端，未配置或查询装配不可用时装配稳定的 unavailable 实现。这样 `GET /api/v1/search/articles` 不会因部署配置变成 404，而会明确返回 `503 SEARCH_UNAVAILABLE`；现有 `Articles`、`Assets`、`Auth` 装配不引用搜索服务。

备选方案是把查询加入 `searchprojection` 或 `article.Service`。前者会混合写侧收敛状态机与读侧请求生命周期，后者会让核心 PostgreSQL 阅读服务依赖 OpenSearch，均会扩大故障边界。

### 2. 查询计划 v1 使用固定 BM25 字段权重与精确过滤

查询适配器对读 PIT 执行 `_source=false` 的搜索，只读取文档 `_id` 和完整 `sort` 数组。查询计划 v1 为：

- `bool.filter` 固定包含 `term visible=true`；
- `keyword`、`topic` 分别使用 `keywords.keyword`、`topics.keyword` 的精确 term，`source_id` 使用数值 term；
- `bool.must` 使用 `multi_match`/`best_fields`，字段权重固定为 `title^5`、`keywords^4`、`topics^3`、`summary^2`、`plain_text^1`，不启用 fuzziness；
- 排序固定为 `_score DESC, published_at DESC, article_id DESC`，并要求 `track_scores=true`；文章 ID 全局唯一，使排序成为全序；
- 不请求 total hits、highlight、vector 或文档 `_source`，避免把旧索引正文带入 API 进程。

查询词与标签先执行现有 UTF-8 修复、NUL/换行处理和 trim 语义，再按 Unicode rune 计数。参数规范化、字段集合、权重和排序共同构成 `query_plan_version=1`。未来调整任一语义时递增版本；旧游标因此失效，但索引 mapping 不必仅因权重调整而重建。

选择 `best_fields` 是因为标题、标签、摘要和正文表达同一相关性信号，首版只需让最强字段匹配主导并保留 BM25 长度归一化。`query_string` 会扩大语法与转义面，`simple_query_string` 仍引入用户可见操作符语义，均不适合首个最小契约。

固定真实 OpenSearch 数据集将覆盖中文 bigram、英文、URL、专有名词、字段单独命中、同分 tie-break 与标签过滤。若 schema v1 的 `cjk` analyzer 无法通过验收，停止本 change 的上线，另建 schema v2 模板并走 I4.1 重建/切换；不得修改已发布模板语义。

### 3. PIT + `search_after` 游标由 HMAC 绑定查询和过期时间

第一页在 `velis-articles-read` 上以 `allow_partial_pit_creation=false` 创建 PIT；查询和后续补扫都通过 `/_search` 使用该 PIT。每次搜索携带短 `keep_alive` 并采纳响应返回的最新 PIT ID。终页、请求失败或打开 PIT 后无法生成响应时尽力删除 PIT；用户放弃翻页时由 keep-alive 自动回收。

游标 v1 使用 base64url 编码的规范 payload 与 HMAC-SHA-256 签名，字段为：

- cursor version 与 query plan version；
- 规范化 `{q, keyword, topic, source_id}` 的 SHA-256 指纹（`limit` 不影响候选次序，不进入指纹）；
- 最新 PIT ID；
- 上次消费命中的三个原始排序值；
- 应用侧过期时间。

解码采用严格 JSON、未知字段拒绝、最大 16 KiB、常量时间签名比较，并验证分数为有限数、时间与文章 ID 排序值有效。HMAC 防止调用者替换 PIT ID或 search-after 位置，从而避免让匿名输入任意引用 OpenSearch 搜索上下文。PIT 不绑定查询，查询指纹则保证后续页必须重放同一规范化条件。

`search.query.cursor_key` 只从 `VELIS_SEARCH_CURSOR_KEY` 读取，Base64 解码后至少 32 字节；非 development/test 的 API 在启用搜索时缺少该键则拒绝装配搜索能力并报告配置错误。单进程开发环境可生成仅驻留内存的随机键并记录不含键值的警告，因此重启会令旧游标失效；多副本和生产必须共享显式密钥。密钥不得写入 YAML、示例值、日志或指标。

默认 `pit_keep_alive=2m`。它足以完成连续“加载更多”，同时限制匿名调用遗留的 PIT 资源。相比 `from/size`，PIT 保持并发写入下的候选快照；相比 scroll，PIT 可与应用自己的 search-after 位置组合且更适合用户分页。

### 4. 应用层以候选批次补扫，并把 PostgreSQL 结果恢复为命中顺序

默认每次从 OpenSearch 取 100 个候选，单个 HTTP 请求最多检查 500 个。Service 对每个候选批次只调用一次 `PublicArticleReader.ListPublishedByIDs`；PostgreSQL 使用 `WHERE a.id = ANY($1) AND a.status='published'`，一次连接当前 revision、Source/作者与当前 generation 选择，返回与现有 latest 相同的 `ListItem`。Application 建立 ID 到当前 ArticleItem 的 map，再按候选顺序迭代，完全不信任数据库返回顺序。

补扫算法如下：

1. 累积当前可见 item，并保存每个 item 对应的候选 sort position；
2. 不存在、下架或删除的 ID 计为 filtered，直接越过；仍公开但已修订的文章返回 PostgreSQL 当前内容；
3. 继续 search-after，直到得到 `limit+1` 个可见 item、OpenSearch 耗尽或达到 500 个已检查候选；
4. 找到第 `limit+1` 个可见 item 时，只返回前 `limit` 个并以最后一个已返回 item 的位置生成 next cursor，使预读 item 在下一页重新成为首个候选；
5. 未凑满一页但命中扫描上限时，保守设置 `has_more=true`，游标定位到最后一个已检查候选；这允许后续请求继续且不会反复检查同一批不可见文档；
6. 确认候选耗尽时返回 `has_more=false`、关闭 PIT，即使当前页恰好有 `limit` 个 item 也不制造空的下一页。

每一候选批次对应至多一次 PostgreSQL 查询，因此复杂度是 O(batch count) 而不是 O(hit count)。若 PostgreSQL 查询失败，整个搜索请求失败，绝不直接返回 OpenSearch `_source` 或部分已复核页面。应用通过请求 context 同时约束 OpenSearch 与 PostgreSQL；不会在任何数据库事务或行锁期间等待 OpenSearch。

备选方案是只复核前 `limit` 个命中，它会因 tombstone 延迟制造大量短页；无限补扫则允许陈旧索引拖垮 API。固定批次与总扫描上限在完整页面和请求成本间提供可测试边界。

### 5. 错误分类区分客户端游标、搜索后端与核心数据库

错误映射固定为：

- 参数越界：`400 VALIDATION_FAILED`；
- 游标编码、签名、版本、指纹、排序值、应用过期或 OpenSearch PIT-not-found：`400 INVALID_CURSOR`；
- OpenSearch 未配置、连接/超时、读别名缺失、PIT 创建失败、查询超时、分片失败、`timed_out=true`、hit/排序响应非法：`503 SEARCH_UNAVAILABLE`，附短 `Retry-After`；
- PostgreSQL 批量复核失败：`503 DEPENDENCY_UNAVAILABLE`；
- 未识别内部错误：沿用统一安全错误信封，不透传 OpenSearch 响应体。

只有明确的 PIT 缺失/过期响应映射为 `INVALID_CURSOR`；其它 OpenSearch 4xx/5xx 不猜测为用户错误。失败后不自动回退 latest 或 PostgreSQL `ILIKE`，避免同一端点悄悄改变相关性、过滤与分页语义。

API `/readyz` 不 ping OpenSearch。客户端构造不在 API 启动时强制联网，OpenSearch 恢复后下一次搜索可直接成功；查询故障不会停止 Hertz、数据库池或文章服务。

### 6. 查询资源边界独立配置，API 进程只获得读权限

`SearchConfig` 新增 `Query` 子配置：`timeout` 默认 3 秒（100ms–30s）、`pit_keep_alive` 默认 2 分钟（30s–10m）、`candidate_batch_size` 默认 100（1–500）、`max_candidates_per_request` 默认 500（不小于 batch，最大 5000）；cursor key 单独从环境读取。HTTP `limit` 仍固定 1–50，不能绕过候选扫描上限。

API 复用现有 OpenSearch transport/TLS/脱敏逻辑，但 QueryIndex 只暴露创建/查询/关闭 PIT，不暴露模板、alias 切换、Bulk 或删除索引能力。生产凭据在 OpenSearch 侧只授予读别名 search/PIT 所需权限；Worker/管理 CLI 的写与管理凭据不应复用给 API。当前单一 `search.username/password` 先保持配置兼容，部署文档明确最小权限，并把拆分读写凭据列为后续加固而非本 change 的行为要求。

### 7. Web 提取无数据获取职责的文章卡片并用 URL 表达查询

把 `ArticleList.vue` 内的卡片展示提取为无网络副作用的 `ArticleCard.vue`（或等价结果列表组件），latest 与搜索共同复用现有来源、时间、AI 摘要/标签和详情链接逻辑。`SearchView.vue` 自己管理表单草稿、已提交查询、AbortController、items、cursor、has_more 与 `idle/loading/loading-more/ready/empty/unavailable/error` 状态。

成功提交后用规范化参数更新 `/search?q=...&keyword=...&topic=...&source_id=...`，但不把不透明 cursor 放入 URL；刷新页面从第一页重建结果。修改任何条件后提交会取消旧请求、清空旧结果和 cursor。`ApiError.code === "SEARCH_UNAVAILABLE"` 显示专用可重试提示，其它错误保留通用失败状态。主导航增加“搜索”，文章详情继续使用既有 `/articles/:id`。

来源 ID 输入是首版在没有公开 Source 目录时的明确折中；不通过调用管理员 Source API填充匿名筛选器，也不扩大 Source 可见性契约。

### 8. API 侧搜索观测使用独立低基数指标

新增查询 observer，记录：

- `velis_article_search_requests_total{result}`，result 固定为 `success|validation|invalid_cursor|search_unavailable|dependency_unavailable|internal`；
- `velis_article_search_duration_seconds{stage}`，stage 固定为 `total|opensearch|postgres`；
- candidate、filtered、scan-limit 与 PIT create/delete/failure 计数，无动态标签。

API 使用独立 Prometheus registry 和内部 metrics listener（新增 `http.metrics_address`，默认仅回环），复用现有 metrics server；metrics listener 故障只记录告警，不改变 API readiness。结构化日志只记录 request ID、结果分类、耗时和计数，不记录 q、标签过滤值、cursor/PIT、正文、文章 ID列表或原始依赖错误体。

默认 CI 通过 observer fake 验证应用行为，不要求真实 Prometheus；指标注册与标签集合另做单元测试。真实 OpenSearch 集成测试是独立目标，未配置而 skip 不作为验收通过。

## Risks / Trade-offs

- [短 PIT 会在用户停留过久后过期] → 返回 `INVALID_CURSOR` 并让 Web 重新提交第一页；keep-alive 可在安全范围内配置，但不提供无限搜索会话。
- [匿名第一页会创建 PIT，恶意流量可能耗尽集群 PIT 上限] → 使用 2 分钟默认值、终页/失败主动关闭、HTTP 与候选硬上限及 PIT 指标；若实际流量需要再单独引入全局限流，不在本 change 中借用登录限流语义。
- [索引仍公开但正文已修订时，当前 PostgreSQL 内容可能不再包含查询词] → 可见性与当前内容正确性优先；投影 Worker最终收敛，新一轮搜索会消除陈旧匹配，不在请求中重新实现全文判断。
- [大量陈旧可见文档可能产生短页甚至空页且仍有 next cursor] → 有界补扫优先保护 API；游标越过已检查候选，指标暴露 filtered 与 scan-limit，运维应修复投影积压而不是放宽为无界扫描。
- [精确 keyword/topic 过滤对大小写和现有标签规范化敏感] → 首版严格按索引 keyword 值匹配并在固定夹具中记录语义；需要大小写折叠时升级 normalizer/schema，而不在查询端产生与索引不一致的猜测。
- [开发环境临时 cursor key 在重启后失效] → 游标本来就是短期资源；生产和多副本强制共享环境密钥。
- [BM25 权重是产品判断，初值可能不理想] → 权重纳入 query plan version 和确定性相关性夹具；以后可调权并使旧游标明确失效，无需改写业务事实。

## Migration Plan

1. 先交付应用端口、严格游标、批量 PostgreSQL reader 与确定性 fake 测试；不启用 API 搜索客户端时路由稳定返回 `SEARCH_UNAVAILABLE`。
2. 扩展 OpenSearch 适配器实现 PIT/query/close，使用现有 schema v1 的测试索引跑字段召回、权重、过滤、PIT 并发变更与响应校验集成测试。若相关性夹具失败，停止发布并单独走 schema 升级，不在此处修改线上索引。
3. 更新配置、OpenAPI、API composition root 和内部 metrics listener；开发 Compose 把 OpenSearch endpoint 注入 API，生产先创建最小读权限凭据和共享 cursor key。
4. 部署后先确认读别名存在且投影积压可接受，再开放 Web 导航；演练 OpenSearch 停机、别名缺失、PIT 过期、索引暂留下架文章和 PostgreSQL 复核失败，并同时验证 latest/详情仍可用。
5. 回滚时先回滚 Web 导航和 API 查询装配；搜索路由可降级为稳定 503，I4.1 投影 Worker、索引和 PostgreSQL 表无需回滚或删除。该 change 不新增数据库 migration，也不修改现有物理索引。

## Open Questions

无。字段权重、查询参数、游标安全、PIT 生命周期、补扫上限与错误语义均已在本 change 中固定；后续只能通过显式 query plan/schema 版本演进。
