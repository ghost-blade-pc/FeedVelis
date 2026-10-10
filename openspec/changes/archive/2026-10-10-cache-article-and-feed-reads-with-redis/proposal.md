# Proposal

## Why

I4 搜索与推荐已具备 PostgreSQL 当前事实复核和稳定分页，但列表、搜索与推荐重复装配完整文章卡片，推荐新首查还会重复召回和排序。引入可丢弃 Redis 读取缓存完成 I4.6，并将新文章进入共享 latest 的新鲜度与当前内容、可见性和用户隔离分别约定。

## What Changes

- 新增版本化公开文章卡片缓存、latest 有序 ID 页缓存和按真实认证用户隔离的 recommend 首查候选计划缓存；采用 Cache-Aside、批量读取和批量回填。
- 默认卡片 TTL 5 分钟、latest ID 页 TTL 5 秒、推荐计划 TTL 30 秒；命中不续期。Redis 只保存可重建读取数据，不保存业务事实或分页必需状态。
- **BREAKING**：调整共享 latest 的新文章发现契约：发布、恢复后详情立即可读；缓存开启时进入列表允许正常情况下约 5 秒的新鲜度窗口。当前修订、AI 选择、下架/删除过滤和本人负反馈复核仍以 PostgreSQL 为准。
- PostgreSQL 事务提交后尽力失效首页缓存，失败不改变已提交业务结果；版本化卡片和短 TTL 负责安全收敛。
- 分离轻量事实读取与完整卡片装配，保持 BM25/KNN 身份检查和当前增强结果契约；推荐计划键绑定当前画像和配置，命中时检查候选排除集变化。
- 保留客户端冻结推荐游标和现有搜索游标；Redis 未配置、故障、清空或数据损坏时安全回源，不改变核心 readiness 或推荐业务降级含义。
- 增加命中、回源、损坏、超时、失效与回填失败指标，以及真实 PostgreSQL/Redis/OpenSearch 故障恢复和缓存前后基线证据。

## Capabilities

### New Capabilities

- `article-read-cache`：公开文章卡片、latest ID 页与用户隔离的短期推荐计划缓存，拥有 TTL、键版本、批量回源、资源预算、恢复与可观测性要求。

### Modified Capabilities

- `unified-articles`：明确发布与恢复的即时公开读取和 latest 候选新鲜度窗口，保留当前内容与可见性约束。
- `article-search`：允许在 PostgreSQL 当前事实批量复核后复用版本匹配的卡片缓存，保持搜索排序、语义身份与失败契约。
- `article-recommendation`：允许新首查短期复用候选计划，明确画像、排除集变化与当前复核规则，保留独立冻结游标与故障回退。

## Impact

- Application：文章列表读取、共享卡片装配端口、推荐首查计划、文章写入提交后失效端口；Domain 不引入 Redis 或第三方 SDK。
- Infrastructure/bootstrap：PostgreSQL 轻量批量读取、Redis 适配器与客户端生命周期、API/Worker 装配、缓存指标和配置；沿用现有 pgx 与当前事实表，首版不新增数据库事实表或迁移。
- 接口与运维：HTTP 字段和游标格式保持现有契约，OpenAPI/README/Roadmap 需说明列表新鲜度、配置与真实验收；Compose 接入可选 Redis，不把它加入核心启动健康门槛。
- 验证：Application 单元/架构测试、专用 `_test` PostgreSQL 与专用 Redis 测试命名空间、OpenSearch 联合验收；性能只报告同环境前后数据，不设置吞吐或提升比例门槛。
- 边界：不缓存正文、搜索召回结果、用户画像事实或反馈状态；不引入服务端分页会话、全局列表版本、分布式锁或缓存可靠事件链。
