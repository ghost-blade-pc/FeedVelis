# Proposal

## Why

I3 已将当前文章修订、AI 生成结果和 Embedding 作为 PostgreSQL 事实保存，但尚无可供搜索与推荐读取、且能从这些事实安全重建的 OpenSearch 投影。I4 后续的 BM25、KNN 和 recommend Feed 都依赖一个先解决乱序、迟到、部分失败与在线重建问题的索引基础，因此先独立交付文章搜索投影。

## What Changes

- 引入 OpenSearch 3.8.0 单节点开发依赖、受校验的连接配置，以及版本化文章索引模板、物理索引和读写别名；首版中文全文字段使用内置 `cjk` analyzer，避免额外分析插件的版本耦合。
- 新增每篇文章唯一的 `search_projection_jobs` 持久化收敛槽位。文章公开生命周期变化与 AI 当前 generation/Embedding 选择切换都会在各自 PostgreSQL 事务内推进该槽位的目标版本。
- 新增受租约、generation 和 fencing token 保护的搜索投影 Worker，从 PostgreSQL 当前事实构造文档，并向当前写索引执行幂等 upsert 或不可检索 tombstone；任何 OpenSearch 结果都不得反向覆盖 PostgreSQL。
- 对 Bulk 响应逐项分类并仅重试失败项；重复、乱序或迟到执行者必须收敛到当前文章状态，不能恢复旧修订、旧 AI 结果或已下架/删除文档。
- 新增有界、可恢复的索引重建 CLI：创建新物理索引，按稳定水位分页扫描 PostgreSQL，追赶重建期间增量，校验后原子切换别名，并保留显式回滚窗口。
- 增加索引延迟、任务状态、重试、Bulk 逐项错误、陈旧写入、重建进度与别名版本的日志和指标，以及真实 PostgreSQL/OpenSearch 集成测试。
- 本 change 不提供搜索 HTTP API、BM25 查询、查询 Embedding、KNN/RRF、recommend Feed 或 Redis 缓存；这些能力由后续 change 使用本投影交付。

## Capabilities

### New Capabilities

- `article-search-projection`: 定义文章搜索投影的版本化索引契约、持久化收敛任务、增量同步、迟到写防护、Bulk 部分失败处理和在线重建语义。

### Modified Capabilities

- `ai-content-enrichment`: 当前 generation 或 Embedding 选择成功切换时，必须在同一 PostgreSQL 事务推进对应文章的搜索投影目标，确保 AI 投影变化不会因进程崩溃而遗漏。
- `unified-articles`: 公开文章发布、修订、下架、恢复或删除时，必须在同一 PostgreSQL 事务推进对应文章的搜索投影目标；该本地持久化动作不得使文章写路径同步依赖 OpenSearch 或 RabbitMQ。

## Impact

- 后端新增搜索投影 Application/Domain 端口、PostgreSQL 任务与重建状态适配器、OpenSearch Infrastructure 适配器、Worker 组件和管理 CLI。
- 新增版本化 SQL migration；不改写既有迁移，也不使用 PostgreSQL `vector` 扩展进行检索。
- AI 结果保存事务以及文章发布、修订和状态变更事务会增加对搜索投影槽位的原子推进，但文章 API/RSS 写事务仍不连接 OpenSearch 或 RabbitMQ。
- `compose.yaml`、示例配置、Makefile、README 和观测指标将增加 OpenSearch 3.8.0 的本地开发与验证入口；未配置或不可用时不得影响文章发布、latest、详情和 AI 结果持久化。
- 本 change 不新增或修改面向 Web/HTTP 调用方的 API 契约。
