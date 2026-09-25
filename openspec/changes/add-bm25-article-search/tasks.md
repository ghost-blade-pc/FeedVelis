# Tasks

## 1. 查询配置与应用边界

- [x] 1.1 扩展 `SearchConfig.Query` 与 `HTTPConfig`，加入 3s 查询超时、2m PIT keep-alive、100 候选批次、500 单请求扫描上限、API metrics 地址及仅环境变量读取的 cursor key；补齐默认值、环境覆盖、范围/关系校验和脱敏测试，验证 development/test 可生成临时键而生产缺键只禁用搜索装配且不泄露键值。
- [x] 1.2 在 `application/articlesearch` 定义规范化请求、过滤、候选/排序位置、页面、受控错误、`QueryIndex`、`PublicArticleReader` 与 `Observer` 端口，运行架构测试确认 Application 不依赖 SQL、Hertz 或 OpenSearch SDK 类型。
- [x] 1.3 实现 `q`、`keyword`、`topic`、`source_id`、`limit` 的规范化与边界校验，使用 fake QueryIndex 验证空值、Unicode rune 上限、非法来源/limit 在任何 OpenSearch 调用前返回稳定校验错误。

## 2. 游标与有界搜索编排

- [x] 2.1 实现 query plan v1 的规范查询指纹，以及最大 16 KiB、严格 JSON、HMAC-SHA-256、常量时间验签和显式过期时间的 cursor v1 codec；单元测试覆盖正常往返、limit 改变、查询/过滤改变、未知字段、未知版本、篡改签名、非法排序值、过期和超长输入。
- [x] 2.2 实现第一页创建 PIT、后续页恢复 PIT/search-after、采纳最新 PIT ID 及终页/失败尽力关闭 PIT的生命周期；用可编程 fake 验证终页关闭、可继续页保留、依赖失败清理和清理失败不覆盖主错误。
- [x] 2.3 实现每批一次 PostgreSQL 复核、按 OpenSearch 顺序重排、过滤不可见 ID 并补扫到 `limit+1` 的分页算法；单元测试验证普通多页无重复、数据库乱序、下架间隙、当前修订替换、恰好耗尽和 lookahead 游标停在最后已返回命中。
- [x] 2.4 实现 500 候选扫描上限和短页推进语义；用全不可见/稀疏可见夹具验证扫描上限时 `has_more=true`、cursor 越过最后已检查候选、下一页不重复，并断言 PostgreSQL 调用次数只等于候选批次数而非命中数。
- [x] 2.5 接入 articlesearch Observer 与受控错误分类，测试 OpenSearch 未配置/超时/非法响应映射为 search unavailable、PIT-not-found 映射为 invalid cursor、PostgreSQL 失败映射为 dependency unavailable，且任何失败都不返回已累积的部分页面。

## 3. PostgreSQL 与 OpenSearch 适配器

- [x] 3.1 为 PostgreSQL 增加 `ListPublishedByIDs` 批量 reader，一条 SQL 连接当前 revision、Source/作者及匹配当前 revision 的 generation 选择；PostgreSQL 集成测试验证只返回 `published`、使用最新公开内容、增强结果不串 revision、空输入不查询以及任意数据库返回顺序均由应用层恢复。
- [x] 3.2 扩展 OpenSearch 客户端，分别实现读别名 PIT 创建、PIT 查询和精确 PIT 关闭，并复用现有 TLS、timeout 与错误脱敏；HTTP transport 单元测试验证 `allow_partial_pit_creation=false`、keep-alive、最新 PIT ID、请求 context 和关闭请求格式。
- [x] 3.3 实现 query plan v1 DSL：`visible=true`、可选 keyword/topic/source term、`best_fields` 权重 `5/4/3/2/1`、`_score/published_at/article_id` 倒序、`track_scores=true`、`_source=false` 且不追踪总数；请求序列化测试确认不包含 vector、highlight、全文 `_source` 或用户查询语法执行入口。
- [x] 3.4 严格解析 OpenSearch 搜索响应，只接受未超时、无分片失败、正文章 ID、唯一 hit 与三个有效 sort 值；适配器测试覆盖连接/超时、别名缺失、PIT 过期、OpenSearch 4xx/5xx、`timed_out`、部分分片、重复/缺字段/非法 JSON，并验证原始响应和凭据不会进入返回错误。
- [x] 3.5 使用真实 OpenSearch 3.8.0 和 schema v1 固定夹具验证标题、正文、摘要、关键词、主题独立召回，高权重字段优先，keyword/topic/source 组合过滤，同分全序、中英文/URL/专有名词及 PIT 期间增删文档和别名切换均稳定；若夹具证明 CJK/mapping 不满足验收，停止本 change 并提出 schema v2 重建而不修改 v1 资产。

## 4. HTTP 契约、装配与故障隔离

- [x] 4.1 新增搜索 DTO、handler、`SEARCH_UNAVAILABLE` 错误码和 presenter 映射，并始终注册匿名 `GET /api/v1/search/articles`；Hertz 测试验证合法分页、全部参数边界、`Retry-After`、`INVALID_CURSOR`、搜索未配置时 503、统一 request_id 错误信封及响应不含 score/PIT/索引字段。
- [x] 4.2 更新 `backend/api/openapi/velis.yaml`，声明 q/keyword/topic/source_id/limit/cursor、`ArticleSearchPage`、400/503 语义与 `ArticleItem` 复用；运行 `go test ./api/openapi` 验证 YAML、引用、required/nullable 和错误响应契约。
- [x] 4.3 在 API composition root 装配只读 QueryIndex、PostgreSQL reader、cursor signer、real/unavailable service，并让客户端构造不执行启动 ping；bootstrap/路由测试验证 OpenSearch 未配置、启动时停机和恢复后搜索重试，同时 latest、详情、文章写入与 `/readyz` 始终走原有 PostgreSQL 路径。
- [x] 4.4 为 API 增加独立 Prometheus registry、非核心内部 metrics listener 和搜索 query observer，注册固定 result/stage 标签及 candidate/filtered/scan-limit/PIT 计数；指标测试验证标签全集有界且不含 q、过滤值、文章 ID、cursor/PIT、正文、物理索引名或凭据，metrics 监听失败不改变 API readiness。
- [x] 4.5 更新 `compose.yaml`、`.env.example` 与 `backend/configs/config.example.yaml`，把 OpenSearch 查询配置注入 API 而仍允许显式空 endpoints 禁用；配置/Compose 测试验证开发环境可直接搜索、生产 cursor key 只经环境提供，API 和 Worker 的禁用语义保持一致。
- [x] 4.6 增加跨适配器故障回归：构造索引暂留下架/删除文章、OpenSearch 停机/恢复、读别名缺失、PIT 过期及 PostgreSQL 复核失败，验证下架内容不返回、搜索错误明确，且相同进程中的 latest 与正文详情持续成功。

## 5. 最小搜索 Web 页面

- [x] 5.1 在 Web 类型和 API client 中加入 `ArticleSearchPage`、查询参数编码与分页调用；Vitest 覆盖 Unicode/空白编码、可选过滤、cursor 续页、AbortSignal 及 `SEARCH_UNAVAILABLE` 错误码保留。
- [x] 5.2 从 `ArticleList.vue` 提取无网络副作用的共享文章卡片/结果组件并让 latest 复用；组件测试验证 RSS/站内作者、AI 摘要与原 excerpt 降级、关键词/主题、时间和站内详情/原文链接行为不回归。
- [x] 5.3 新增 `/search` 路由、主导航入口和 SearchView 表单，支持 q、keyword、topic、source_id、提交后 URL 同步、结果列表与加载更多；组件/路由测试验证刷新从第一页恢复查询、cursor 不进入 URL、条件变化会取消旧请求并清空旧结果。
- [x] 5.4 实现 idle/loading/loading-more/ready/empty/unavailable/error 状态及重试交互，测试 `SEARCH_UNAVAILABLE` 不被显示为无结果、不混入 latest，非法输入在客户端提示且过期请求结果不会覆盖新查询。
- [x] 5.5 补齐搜索表单、筛选器、状态卡片和响应式结果布局样式，运行 Web lint、Vitest 与生产构建，并在桌面/窄屏手动检查导航、键盘提交、焦点与状态提示可访问性。

## 6. 文档与总体验收

- [x] 6.1 更新 README 的当前功能/API/降级矩阵与搜索运行说明，并更新 Roadmap 的 I4.2 进度和后续 I4 边界；文档审查确认不把 BM25 描述成语义、RRF、推荐或个性化能力，并记录 PIT 过期、精确标签/来源过滤、读权限和 cursor key 运维要求。
- [x] 6.2 更新 Make 真实依赖目标或测试筛选，使 PostgreSQL 批量 reader、OpenSearch BM25/PIT 与搜索故障链路可独立运行；执行 `make integration-postgres`、`make integration-opensearch`、`make integration-search` 并明确记录任何因依赖未配置而 skip 的项目不能作为通过。
- [x] 6.3 运行 `make check` 与 `openspec validate add-bm25-article-search --strict`，复核架构依赖、OpenAPI、Go race/build、Web lint/test/build，并保存全部命令结果；最后人工确认同一 PIT 正常翻页无重复、旧/非法游标拒绝、PostgreSQL 无 N+1、下架命中不泄露及搜索停机不影响 latest/正文。
