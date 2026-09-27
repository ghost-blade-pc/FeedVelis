# Proposal

## Why

I4.1 已交付可重建的文章搜索投影，但用户仍无法查询它，当前 Web 也只有 latest 与正文阅读入口。现在需要先交付只依赖现有文本投影的 BM25 搜索，验证索引字段、分析器和可见性边界，再继续 KNN、RRF 与推荐能力。

## What Changes

- 新增匿名 `GET /api/v1/search/articles` 契约，支持搜索词以及关键词、主题、来源等最小过滤条件。
- 使用现有 OpenSearch 读别名执行 BM25 查询，对标题、正文、摘要、关键词和主题设置显式权重，并以固定次序稳定打破同分结果。
- 使用带版本、查询指纹和 OpenSearch PIT/search-after 状态的不透明游标提供稳定分页；拒绝旧版本、非法或与当前查询不匹配的游标。
- 对 OpenSearch 候选文章按批次回 PostgreSQL 读取当前公开事实，保持命中顺序并过滤已下架、删除或变更为不可见的文章，不进行逐篇查询。
- 新增最小搜索 Web 页面，提供查询、过滤、结果列表、继续加载、空结果与搜索不可用状态，并复用现有文章卡片和详情入口。
- OpenSearch 未配置、不可达、超时或读别名不可用时返回明确、可重试的搜索不可用错误；latest、正文阅读和核心 readiness 继续只依赖 PostgreSQL。
- 增加 BM25 查询、游标、批量可见性复核、OpenSearch 真实依赖、HTTP 契约及 Web 行为测试，并记录查询耗时、候选数、过滤数和失败分类等低基数观测数据。
- 不包含语义查询、查询 Embedding、KNN、RRF、个性化推荐、recommend Feed 或 Redis 搜索缓存。

## Capabilities

### New Capabilities

- `article-search`: 定义匿名文章 BM25 搜索的查询与过滤契约、确定性相关性排序、稳定游标、PostgreSQL 当前可见性复核、局部故障语义和最小 Web 工作流。

### Modified Capabilities

无。

## Impact

- 后端将新增独立搜索 Application 用例及端口、OpenSearch 查询适配器、PostgreSQL 批量公开文章读取、Hertz handler/DTO/错误映射和 API composition-root 装配。
- `backend/api/openapi/velis.yaml` 将新增搜索端点、查询参数、分页响应与 `SEARCH_UNAVAILABLE` 错误契约；现有文章详情 URL 和 `ArticleItem` 展示模型继续复用。
- Web 将新增搜索 API 客户端、类型、路由、导航入口和最小搜索页面，并复用现有文章卡片组件。
- Search 配置将补充查询/PIT/候选批次等有界参数；API 进程会在启用搜索时创建独立 OpenSearch 客户端，但 `/readyz` 不把 OpenSearch 作为核心依赖。
- 现有 `article-search-projection` schema v1 预期无需修改；若固定相关性样例证明当前 CJK 分析器或映射不满足验收，则必须另行递增 schema version 并通过既有重建流程发布，不能原地改变当前索引语义。
