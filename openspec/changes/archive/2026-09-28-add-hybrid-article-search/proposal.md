# Proposal

## Why

I4.1/I4.2 已交付可重建的文章索引和 BM25 搜索，但无法召回措辞不同、语义相近的文章。I4.3 在已有文章向量之上加入查询 Embedding、KNN 与应用层 RRF，同时保证模型故障和向量缺失不破坏关键词搜索。

## What Changes

- 为现有匿名文章搜索增加可配置、默认关闭的混合搜索路径，复用查询与精确筛选、公开文章响应和 Web 搜索入口。
- 独立执行 BM25 与 KNN 召回，合并去重后在应用层执行确定性 RRF；定稿候选上限、超时、权重和参数。
- 查询向量不可用、模型失败、KNN 失败或没有有效文章向量时使用 BM25；BM25 或 PostgreSQL 失败仍明确失败。
- 校验向量的当前修订、generation、Embedding 选择和 profile，防止最终一致索引中的旧向量影响当前搜索。
- 为混合结果固定有限候选快照并生成加密游标，避免翻页重新调用模型或重新融合；混合搜索最多提供两路各 100 个候选的并集，耗尽后结束分页。
- 增加混合搜索指标、确定性模型桩与召回/排序/降级/旧修订回归集。
- 新增索引 schema v2 的 Embedding profile 标识，复用现有在线重建、切换与回滚流程；旧 v1 索引继续支持 BM25。
- 不包含 recommend、用户偏好、反馈信号、Redis 缓存、Agent 或新的 Web 排序控件。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `article-search`：增加有界混合召回、RRF、模型降级、当前向量身份复核与稳定混合分页，保留 BM25 独立召回及原有 HTTP 字段。
- `article-search-projection`：向量投影增加可过滤的 Embedding profile 身份，通过新 schema 和在线重建升级。

## Impact

- 后端：`application/articlesearch` 的端口、编排、游标和纯排序函数；Eino 查询 Embedding 适配器；OpenSearch 查询与 v2 模板；PostgreSQL 批量当前向量身份读取；API bootstrap 与 observability。
- 配置与契约：搜索混合配置、API 进程 Embedding 配置注入、示例 YAML、环境变量、Compose、OpenAPI 搜索说明及 README/Roadmap 的实施后状态。
- 兼容性：默认关闭时保留原 BM25/PIT 行为；开启后首查采用有限候选分页，游标显式区分查询计划。旧 BM25 游标在有效期内继续原计划。混合游标过期、密钥或计划失效时要求重查。
- 不新增数据库迁移或外部依赖，不执行模型补录、索引重建、部署或生产操作；实际实施需通过现有测试与真实 OpenSearch/PostgreSQL 验证。
