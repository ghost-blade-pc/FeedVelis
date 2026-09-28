# 混合文章搜索验收记录

验收日期：2026-09-27。变更：`add-hybrid-article-search`，schema：`spec-driven`。未调用真实收费模型，未部署、提交或操作生产。

## 环境与执行结果

本地 Docker Compose 的 `postgres`（PostgreSQL 17）与固定 `opensearchproject/opensearch:3.8.0`，OpenSearch `/` 返回版本 `3.8.0`、Lucene `10.5.0`。数据库为专用 `velis_test`，测试基座在迁移和清表前核对 `_test` 后缀。模型由本地 HTTP 桩返回人工向量。

在 `backend/` 执行：

```bash
VELIS_TEST_DATABASE_URL='postgres://velis:velis@localhost:5432/velis_test?sslmode=disable' \
VELIS_TEST_OPENSEARCH_URL='http://localhost:9200' \
go test -count=1 -v -run 'TestSearchProjection|TestSearchIndexRebuild|TestSearchRebuild|TestArticleSearch|TestHybrid' ./test/integration

VELIS_TEST_OPENSEARCH_URL='http://localhost:9200' \
go test -count=1 -v ./internal/infrastructure/search/opensearch

VELIS_TEST_DATABASE_URL='postgres://velis:velis@localhost:5432/velis_test?sslmode=disable' \
VELIS_TEST_OPENSEARCH_URL='http://localhost:9200' \
go test -count=2 -v -run 'TestHybrid' ./test/integration ./internal/infrastructure/search/opensearch
```

结果分别为 19、27、6 次测试通过，三组均为 **0 skip、0 fail**。第二组包含真实 Lucene KNN/PIT/显式排序、profile 和精确筛选；第三组重复运行同一固定集合，包含两次完整 HTTP 搜索/在线重建/双写/回滚。

根目录 `make check` 最终通过：Go 单测、race、架构检查、入口构建；Web ESLint、10 个 Vitest 文件/44 项测试、TypeScript 和 Vite 构建。`make check` 中未配置的其它真实依赖测试会跳过，**这些 skip 不作为本次真实搜索验收证据**。本变更的 PostgreSQL/OpenSearch 证据以上述显式配置命令为准。

`openspec validate add-hybrid-article-search --strict`：通过。`git diff --check`：通过。

## 五项关键验收

| 验收 | 证据与结论 |
| --- | --- |
| 两路独立召回与相同筛选 | `TestHybridRealKNNPITAndFilters`：BM25 独有文章 1，无向量仍命中；KNN 独有文章 2，排除旧 profile、关键词不符和来源不符。真实 OpenSearch 3.8.0 上同时使用 Lucene KNN、PIT、score/时间/ID 排序。 |
| 确定性融合 | `TestRRFExactScoresAndDedup`：`[A,B]` / `[B,C]`，B 两路贡献精确等于 `1/61+1/62`；`TestRRFDeterministicTiesAndWindow`：重复融合、时间/ID 并列与 200 条边界。真实 HTTP 集合重复执行次序一致。 |
| 失败隔离与有界在线调用 | `TestOnlineQueryEmbeddingSingleCall`、`TestHybridFallbacksAndRequiredFailures`、`TestHybridTotalDeadlineNeverReturnsDegradedPartialPage`：单次模型调用、限流/超时/取消/输出无效/KNN 故障降级，必需 BM25/数据库或总 deadline 失败没有部分页。 |
| 当前事实与向量身份隔离 | `TestHybridIdentityAndActualProfile`、`TestHybridRemovesStaleContributionsAndRevalidatesPages`、`TestHybridSkipsChangedOverlapAndEmptySnapshot`：实际 profile、旧修订、旧 generation/Embedding、同维度旧 profile 不贡献语义；BM25 资格保留，续页陈旧语义项整篇跳过。 |
| 冻结分页、兼容与安全 | `TestFrozenCursorRoundTripAndBoundaries`、`TestHybridFrozenPaginationNeverRecalls`、`TestSearchLargeEncryptedCursorOverHTTP`、`TestHybridArticleSearchHTTPRebuildAndRollback`：AES-GCM/HKDF、200 条游标小于 16 KiB、绝对 TTL、续页零模型/KNN、v1 游标、v2 在线升级/双写/v1 回滚及冻结页继续。 |

产品限制：开关默认关闭；开启后每路默认且最多 100 条，去重并集最多 200 条；BM25 降级首查也冻结最多 100 条，耗尽后不补召回。关闭开关恢复原 BM25/PIT 深分页并使 v2 游标失效。该限制已写入 OpenAPI、README 和 Roadmap；未宣称 recommend 已实现。

## 固定语料与预期

- `opensearch/hybrid_test.go`：固定时间，文章 1 为 `独有文本 zebra` 且无向量；文章 2/3/4 均为 `语义文章` / `不同措辞` 和向量 `[1,0,0]`。文章 2 profile 为 `e-v2`，文章 3 为同维度 `e-old`，文章 4 关键词不同。查询 `zebra` 的 BM25 为 `[1]`；查询人工向量、profile `e-v2`、关键词 `关键词`、主题 `主题` 的 KNN 为 `[2]`；加不存在的来源条件为 `[]`。
- `integration/hybrid_article_search_test.go`：公开文章 7101 为 `distributed storage`，当前向量 `[0.25,0.5,0.75]` / profile `e-v1`；7102 为 `缓存 中文 Go` 且无向量。HTTP 模型桩只对规范化 `缓存` 返回相同向量。v1 或模型/KNN 故障只返回 7102；v2 正常返回 `[7102,7101]`（相同发布时间，ID 倒序打破等 RRF 分数）。续页不重复、回滚不重排，重新首查 v1 仍只返回 7102。
- `articlesearch/hybrid_service_test.go`：BM25 `[1,2]`，KNN `[2,3]`，精确期望 `[2,1,3]`；逐项改变修订、generation、Embedding、profile，验证移除语义贡献后 `[1,2]`。固定输入和人工向量不依赖真实模型结果。
- 现有 `TestBM25QueryPlanAndPITStability` 覆盖中文、ASCII 术语、URL、专有名词、所有文本字段及标题权重，继续使用不可变 cjk 分析语义。

## article-search 场景对照

测试路径前缀：应用测试在 `backend/internal/application/articlesearch/`，适配器测试在 `backend/internal/infrastructure/search/opensearch/`，数据库/完整链路在 `backend/test/integration/`。HTTP 和观测测试分别在 `internal/interfaces/http/hertz/`、`internal/infrastructure/observability/`。

| delta spec 场景 | 测试证据 |
| --- | --- |
| 两路分别验证召回 | `TestHybridRealKNNPITAndFilters`、`TestHybridArticleSearchHTTPRebuildAndRollback` |
| KNN 使用相同精确筛选 | `TestKNNBodyContainsInternalFilters`、`TestHybridRealKNNPITAndFilters` |
| 无向量文章 | `TestHybridRealKNNPITAndFilters`、`TestHybridArticleSearchHTTPRebuildAndRollback` |
| 查询 Embedding 失败或没有有效向量 | `TestOnlineQueryEmbeddingSingleCall`、`TestQueryEmbeddingRejectsNonfiniteAndEmpty`、`TestHybridFallbacksAndRequiredFailures` |
| KNN 单独失败 | `TestKNNRejectsInvalidResponses`、`TestHybridFallbacksAndRequiredFailures`、`TestHybridArticleSearchHTTPRebuildAndRollback` |
| 非法参数或续页请求 | `TestHybridVisibilityGapsAndCursorValidation`、`TestHybridFrozenPaginationNeverRecalls` |
| 融合重叠与独有候选 | `TestRRFExactScoresAndDedup` |
| 相同输入重复运行 | `TestRRFDeterministicTiesAndWindow`、`TestHybridArticleSearchHTTPRebuildAndRollback`（`-count=2`） |
| 候选数量达到边界 | `TestRRFDeterministicTiesAndWindow`、`TestFrozenCursorRoundTripAndBoundaries` |
| 各规定字段能够召回 | `TestBM25QueryPlanAndPITStability` |
| 高权重字段优先 | `TestBM25QueryPlanAndPITStability` |
| 分数相同时稳定打破并列 | `TestBM25QueryPlanAndPITStability`、`TestRRFDeterministicTiesAndWindow` |
| 中英文混合查询 | `TestBM25QueryPlanAndPITStability`、`TestIndexTemplateSchemaIdentityAndAnalyzer` |
| 正常继续下一页 | `TestHybridFrozenPaginationNeverRecalls`、`TestArticleSearchEndToEnd` |
| 翻页期间索引发生变化 | `TestHybridFrozenPaginationNeverRecalls`、`TestHybridArticleSearchHTTPRebuildAndRollback`、`TestBM25QueryPlanAndPITStability` |
| 游标与查询不匹配 | `TestFrozenCursorRoundTripAndBoundaries`、`TestHybridVisibilityGapsAndCursorValidation` |
| 旧版、篡改或过期游标 | `TestFrozenCursorRoundTripAndBoundaries`、`TestCursorCodecRejectsInvalidInputs`、`TestHybridLegacyV1CursorAndPlanChange`、`TestSearchLargeEncryptedCursorOverHTTP` |
| 混合候选列表耗尽 | `TestHybridFrozenPaginationNeverRecalls`、`TestHybridSkipsChangedOverlapAndEmptySnapshot` |
| 混合翻页与模型波动 | `TestHybridFrozenPaginationNeverRecalls`、`TestHybridFallbacksAndRequiredFailures` |
| 旧 BM25 游标继续原计划 | `TestHybridLegacyV1CursorAndPlanChange` |
| 索引中暂留已下架文章 | `TestArticleSearchEndToEnd`、`TestHybridVisibilityGapsAndCursorValidation` |
| 批量复核保持命中次序 | `TestHybridFrozenPaginationNeverRecalls`（读取桩故意倒序返回） |
| 候选批次含不可见间隙 | `TestHybridVisibilityGapsAndCursorValidation`、`TestServiceScansInBatchesAndAdvancesShortPageAtLimit` |
| PostgreSQL 查询数量有界 | `TestHybridFrozenPaginationNeverRecalls`（每页一次批量调用）、`TestHybridIdentityAndActualProfile`、`TestListPublishedByIDsEmptyInputDoesNotQuery` |
| 文章在复核时产生新公开修订 | `TestHybridRemovesStaleContributionsAndRevalidatesPages` |
| 旧修订向量尚未从索引移除 | `TestHybridRemovesStaleContributionsAndRevalidatesPages`、`TestHybridIdentityAndActualProfile` |
| 同修订向量选择变化 | `TestHybridRemovesStaleContributionsAndRevalidatesPages`、`TestHybridIdentityAndActualProfile` |
| 混合翻页期间向量身份变化 | `TestHybridSkipsChangedOverlapAndEmptySnapshot`、`TestHybridRemovesStaleContributionsAndRevalidatesPages` |
| OpenSearch 未配置 | `TestBuildArticleSearchDegradesWithoutConfigurationOrProductionKey`、`TestSearchRouteAlwaysExistsAndMapsControlledErrors` |
| OpenSearch 查询期间故障 | `TestHybridFallbacksAndRequiredFailures`、`TestSearchFailureDoesNotAffectLatestDetailOrReadiness` |
| PostgreSQL 复核失败 | `TestHybridFallbacksAndRequiredFailures`、`TestServiceFailuresReturnNoPartialPageAndClosePIT` |
| 可见性过滤发生 | `TestArticleSearchMetricsUseOnlyFixedLabels`、`TestArticleSearchLogsOnlyControlledDiagnosticFields` |
| 搜索依赖超时 | `TestOnlineQueryEmbeddingSingleCall`、`TestHybridFallbacksAndRequiredFailures`、`TestHybridTotalDeadlineNeverReturnsDegradedPartialPage` |
| 模型故障降级可诊断 | `TestArticleSearchMetricsUseOnlyFixedLabels`、`TestArticleSearchLogsOnlyControlledDiagnosticFields`、`TestHybridArticleSearchHTTPRebuildAndRollback` |

## article-search-projection 场景对照

| delta spec 场景 | 测试证据 |
| --- | --- |
| 当前公开修订具有完整 AI 结果 | `TestSearchProjectionReadsOnlyCurrentFacts`、`TestHybridIdentityAndActualProfile` |
| 当前公开修订尚无 AI 结果 | `TestSearchProjectionReadsOnlyCurrentFacts`、`TestProfileEncodingPreservesStrictV1` |
| AI 当前选择不一致 | `TestSearchProjectionReadsOnlyCurrentFacts`、`TestHybridIdentityAndActualProfile` |
| 同维度 profile 升级 | `TestHybridIdentityAndActualProfile`、`TestHybridRealKNNPITAndFilters`、`TestDocumentProfileFingerprintIsVersioned` |
| 从 v1 升级与回滚 | `TestHybridArticleSearchHTTPRebuildAndRollback`、`TestProfileEncodingPreservesStrictV1`、`TestSemanticSchemaDetection`、`TestIndexEnsureAliasesAndSchemaGuard` |

跨版本重建的实现同时修正了两处原流程限制：Start 校验当前实际 schema 与登记的当前 schema，而不是要求它等于目标 schema；Rollback 从实际回滚索引恢复 schema 身份。Bulk 对受管理物理索引按版本编码并计算该版指纹，使回滚窗口的 v1/v2 双写可以并存。未改写已执行数据库迁移或 v1 模板。
