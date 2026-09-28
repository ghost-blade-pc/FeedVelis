# Design

## Context

动机见 proposal.md。现有 `application/articlesearch.Service` 通过 `QueryIndex` 执行 BM25/PIT/search-after，批量调用 `PublicArticleReader` 复核公开卡片；候选当前只有文章 ID 和排序值，没有向量身份。`CursorCodec` 使用 v1 HMAC 游标，上限 16 KiB。`bootstrap/api.go` 尚不装配模型；Eino 的 EmbeddingAdapter 已用于离线增强，可复用底层组件但不能把在线查询交给增强任务或继承其重试预算。

`articles.v1.json` 已有 Lucene/HNSW/cosinesimil 向量、revision 与两个结果 ID，缺少 profile。投影读取已检查 revision/generation，但查询返回前没有相应检查。Compose 固定 OpenSearch 3.8.0；v1 文本分析与字段权重继续沿用。

长期 article-search 规格目前要求所有结果遵循 BM25 全序和 search-after；本 change 显式修改对应要求，为开启混合搜索的首查定义有限列表分页，关闭时及旧游标继续原 BM25 语义。现有“旧投影返回当前卡片”只继续适用于纯文本贡献，不能授权旧向量参与融合。

## Goals / Non-Goals

**Goals:** 在线模型有独立预算，向量身份可验证，融合是可单测的纯函数，分页不依赖重跑 ANN 或模型，数据及游标资源有硬上限。

**Non-Goals:** 不扩展 `application/recommendation`、不写用户信号，不引入 Redis/搜索会话表，不进行在线生成、重排模型或实时模型评测；不承诺 ANN 在索引重建后重新查询仍返回完全相同集合。

## Decisions

### 1. 服务端开关与固定查询计划

新增 `search.query.hybrid` 配置，`enabled=false`。HTTP 的 q、筛选、limit 和响应不新增字段或模式参数。开启且第一页时尝试混合计划；关闭时保留现有 BM25 服务。旧 v1 游标总是继续原计划。混合 v2 游标绑定参数/profile/维度/算法版本的计划指纹；关闭开关或计划变化使既有混合游标失效并返回 INVALID_CURSOR。这样能独立回滚，避免公开一个仅用于测试的模式开关；两路验证通过应用端口/适配器测试完成。

| 参数 | 默认 | 合法边界 |
| --- | --- | --- |
| bm25_candidates | 100 | 1–100 |
| knn_candidates（即 KNN `k`） | 100 | 1–100 |
| embedding_timeout | 1s | 100ms–2s，且小于总预算 |
| knn_timeout | 1s | 100ms–2s，且小于总预算 |
| 搜索总预算 | 5s | 沿用搜索 timeout 配置，对整个编排生效 |
| RRF 常数 | 60 | 本版固定，不暴露动态配置 |
| BM25/KNN 权重 | 1 / 1 | 本版固定 |
| 在线 Embedding 重试 | 0 | 本版固定 |
| 混合快照 TTL | 2m | 本版固定、绝对期限，不因翻页延长 |
| 游标上限 | 16 KiB | 维持当前硬上限 |

新增字段提供对应 `VELIS_SEARCH_QUERY_HYBRID_*` 环境覆盖。模型未配置仅降级 BM25；明确启用却填写非法数值仍作为配置错误处理。模型调用的在线 timeout 独立于 AI 离线 `timeout/budget/max_attempts`。

### 2. 两路查询独立，BM25 必需

在输入/游标校验后创建 PIT；同一首查 PIT 上并发运行 BM25 与“查询 Embedding → KNN”。BM25 和语义子任务分别使用取消上下文，语义失败不得取消 BM25。BM25 失败则取消语义任务并返回现有错误；总 deadline 或调用方取消不伪装成语义降级。1 秒 Embedding 超时后立即使用已经成功的 BM25；KNN 完整响应才允许参与融合。PIT 在固定列表构造完成或失败时关闭，使用独立有界清理上下文并观测清理失败。

查询 Embedding 输入为规范化 q，查询输入版本 `search-query-v1`；不拼接文章摘要/正文标签，不请求 generation，不写模型结果表。Application 定义专用 QueryEmbedder 端口，Infrastructure 复用 Eino embedder 和统一错误分类，bootstrap 注入同一个文章 Embedding provider/model/profile/维度。API 的配置注入需与 Worker 对齐，但不以离线 Workflow 开关决定是否能调用查询 Embedding。除了有限值和维度，额外拒绝零范数。

KNN 使用现有 Lucene cosine 向量字段，将公开可见、keyword/topic/source 和 profile 条件放入 `knn.vector.filter`；BM25 保留 `title^5/keywords^4/topics^3/summary^2/plain_text^1`。两路排序均为 score、published_at、article_id 倒序。两路返回只含必要身份与排序元数据，不下载正文或向量。拒绝重复 ID、非有限分数、缺少身份、超量候选、timed_out 和失败分片；KNN 失败整路废弃。官方文档确认 Lucene 支持 KNN 内部过滤：[KNN 查询](https://docs.opensearch.org/latest/query-dsl/specialized/k-nn/index/)、[过滤语义](https://docs.opensearch.org/latest/vector-search/filter-search-knn/index/)。PIT+KNN+显式排序必须在仓库固定 3.8.0 上通过真实集成测试，不能以 HTTP 桩代替该证据。

### 3. profile 字段以 schema v2 发布

新增 `articles.v2.json` 和 `embedding_profile_version` keyword 字段，从 ai_embedding_results 的实际 profile 读取；不使用部署 profile 替代事实。更新投影编码、内容指纹、schema identity、Bulk 序列化和重建校验，保留 v1 编码兼容，不让新字段进入 strict v1。已有向量维度、引擎和分析器不变。

查询侧验证实际读索引 schema 身份；读别名仍在 v1 或 profile 元数据不可验证时，关闭语义子路径并记录 schema 降级，BM25 正常。不得为该字段原地修改 v1 映射。选择新 schema 而非仅在召回后过滤 profile，避免同维度旧模型向量挤占 KNN 有限候选。

### 4. 融合前和每页均复核向量身份

增加 PostgreSQL 批量读取端口，返回公开 ArticleItem 及当前 revision/generation/embedding/profile 身份，使用当前选择与结果关联验证向量归属，不把数据库或 SDK 类型带入 Domain。首次融合前批量复核两路并集；KNN 的索引身份必须完整匹配当前事实和查询 profile。移除陈旧 KNN 贡献，保留同篇 BM25 贡献；各路剩余候选重新从 1 编号后计算 RRF。没有有效 KNN 时使用原 BM25 次序。

纯 RRF 函数按固定通道顺序求和，并以快照有效时间、ID 打破同分；跨路使用同一 PIT 的文章排序时间。缺失一路是零贡献。身份校验失败数量独立计数。

每页复核当前可见性；曾含有效 KNN 贡献的候选还需匹配冻结的 revision/generation/embedding 身份。后续身份变化时整篇跳过（即使原先也有 BM25），不重新融合，不沿用旧语义排名。纯 BM25 候选继续允许返回当前公开卡片。该保守行为以重新查询恢复新修订召回，换取分页全序和旧向量隔离；它不提供跨请求的 PostgreSQL 事务快照。

### 5. 用认证加密游标固定有限列表

不尝试将融合分数映射到 OpenSearch search-after，不在每页重算 ANN，也不新增服务端搜索会话。首次开启混合搜索的查询（含 BM25 降级）最多冻结 200 条；携带实际模式、查询/计划指纹、绝对到期时间、消费偏移和有序候选。后续只做 PostgreSQL 批量复核，可跨 API 副本工作，模型故障和别名切换不改变已有列表。

v2 使用 AES-256-GCM，以现有 cursor key 经带专用上下文的 HKDF 派生独立加密键，使用随机 nonce；版本参与认证，不复用 v1 HMAC 构造。认证解密后严格校验长度、版本、模式、唯一 ID、偏移、过期及查询/计划指纹。候选紧凑编码：article ID 8 字节、revision ID 8 字节、generation UUID 16 字节、embedding UUID 16 字节、KNN 贡献标志 1 字节，共最多 49×200=9800 字节；列表已排序，无需存分数、发布时间或全文。固定长指纹和头部加入后 Base64 总长仍低于 16 KiB，以最坏数据编码测试验证。profile 用计划指纹绑定，不能明文携带查询或向量。无需压缩，避免解压资源风险。

响应消费偏移越过所有已经检查的候选；以 limit+1 的可见项确定 has_more，不能越过尚未返回的预读项。批量复核可沿用 100 条默认批次，单页最多扫描整个 200 条固定列表；耗尽即终止。首查降级 BM25 也最多 100 条，HTTP/OpenAPI 文档明确开启混合开关后的有限窗口。未启用路径保持既有深分页。

### 6. 观测与确定性验收

扩展现有 ArticleSearch Observer，记录 Embedding/KNN/RRF 时延、各路候选/并集/陈旧身份过滤量、实际模式与降级原因。原因限定 disabled、embedding_unconfigured、embedding_timeout、embedding_failed、invalid_vector、schema_incompatible、knn_timeout、knn_failed、no_valid_vector；错误细分由固定依赖错误分类处理，不输出原始响应、查询、PIT、游标、向量或凭据。

固定语料与非零人工向量覆盖：标题/正文/增强字段；语义独有、文本独有、双路重叠；无向量、旧 revision、旧 generation/embedding、同维度不同 profile；精确过滤、中英文查询、同分同时间、200 候选、所有候选下架。固定模型桩验证在线调用次数和失败隔离；真实 PostgreSQL/OpenSearch 验证 KNN/PIT、profile 过滤、陈旧投影、分页与 schema 切换。真实模型冒烟只作独立可选证据，不进入 CI 确定性判据。

## Risks / Trade-offs

- [有限窗口不能遍历全部匹配] → 默认关闭；契约明确每路 100 上限，关开关恢复原 BM25 深分页。
- [公开查询新增在线模型成本] → 仅首查一次、有界输入/超时、无重试、可关闭；本版不增加查询缓存或推荐预算系统。
- [ANN 结果随索引变化] → 仅要求相同固定候选输入的 RRF 确定性，冻结首查结果；真实测试采用小集合与明显相似度间隔。
- [陈旧向量挤占候选导致召回缩水] → profile 预过滤、身份复核和陈旧计数；保持 BM25，不扩大上限无限追赶。
- [模型 SDK 不尊重取消或预算] → 适配器与 HTTP 服务桩检查 deadline、取消及调用数；API 关闭时回收组件。
- [大游标触及 HTTP 入口限制] → 最坏编码验证低于 16 KiB，并测试真实 Hertz/Web 请求路径；部署入口必须支持现有游标长度，不记录 URL 查询串。
- [v2 投影更新影响回滚 v1] → 兼容两版编码与模板；先关语义再回滚别名，不自动删除旧索引或变更向量事实。

## Migration Plan

1. 实施代码与配置时默认关闭混合搜索，先验证旧 v1 BM25 与旧游标；本轮只规划。
2. 保留旧索引，按既有 CLI 初始化/在线重建 v2，追赶与校验 profile 身份，原子切换读写别名；不自动补录或重算文章向量。
3. 向 API 注入与 Worker 一致的 Embedding 配置和稳定游标密钥；验证默认 BM25、KNN 独立召回以及固定测试集后显式启用混合开关。
4. 观察降级、时延、候选及陈旧身份指标。回滚先关闭混合搜索，客户端对失效混合游标重查；必要时使用现有回滚流程切回 v1，继续 BM25。旧索引按现有保留窗口显式清理，不操作生产数据或降级数据库迁移。
