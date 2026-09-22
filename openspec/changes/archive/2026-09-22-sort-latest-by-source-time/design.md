# Design

## Context

见 [proposal.md](proposal.md)。当前 PostgreSQL 查询按 `(articles.published_at, id)` 排序和过滤，应用层游标保存同一排序键；列表投影已经同时携带 `source_published_at`、`published_at` 和 `SortAt`。Web 对 RSS 优先展示原站时间，因此无需新增响应字段。

## Goals / Non-Goals

**Goals:**

- 让数据库排序、游标和页面可见时间采用同一口径。
- 保持 RSS、缺失原站时间的 RSS 与站内投稿能够稳定混合分页。
- 保持文章编辑、抓取更新和状态恢复不改变既定排序时间。
- 为新排序表达式保留可用的 PostgreSQL 索引路径。

**Non-Goals:**

- 不修改 Feed 提供的原站时间，也不对未来时间做截断。
- 不新增排序切换参数或用户偏好。
- 不改变本人文章列表、Source 历史或文章详情语义。

## Decisions

### 1. 在查询边界计算统一排序时间

latest 使用 `COALESCE(source_published_at, published_at)` 作为 `effective_published_at`。RSS 有原站时间时使用它；RSS 缺失原站时间以及所有站内投稿都自然回退到非空的 `published_at`。查询将同一表达式用于 SELECT、游标过滤和 ORDER BY，并把结果写入现有 `ListItem.SortAt`。

选择查询时计算可避免新增一列及历史数据回填。另一方案是持久化 `effective_published_at`，但它复制已有字段且增加写入一致性约束。

### 2. 使用版本化迁移替换 latest 索引

新增迁移，以 `COALESCE(source_published_at, published_at) DESC, id DESC` 为键创建仅覆盖公开文章的 latest 索引，并移除旧的 `published_at` latest 索引。down migration 恢复原索引，不修改已有文章数据。

保留旧索引会增加写放大且不服务新查询；直接改写既有迁移会破坏已运行环境的迁移历史，因此采用新版本迁移。

### 3. 将 latest 游标版本提升到 2

游标载荷结构继续保存时间和文章 ID，但编码版本改为 2，解码仅接受版本 2。这样部署后遗留的版本 1 游标明确返回 `INVALID_CURSOR`，不会把旧 `published_at` 当成新的有效时间造成缺项或重复。

兼容解析旧游标无法可靠恢复其对应文章的有效时间，还会让游标处理依赖数据库，因此选择让调用方从第一页重新请求。

### 4. 保持 Web 时间展示逻辑不变

Web 已对 RSS 展示 `source_published_at`，缺失时展示 `published_at`，这与新排序键一致。实现只需用测试固定该一致性，不新增客户端排序，避免破坏服务端游标顺序。

## Risks / Trade-offs

- [Feed 提供未来时间会把文章排到最前] → 这是按原站时间排序的直接语义；保留原值并通过 Source 信息让异常来源可追踪。
- [部署瞬间已有游标失效] → 版本 2 明确拒绝旧游标，前端当前刷新或重新进入 latest 即从第一页加载。
- [表达式排序可能退化为扫描] → 配套表达式部分索引，并在集成测试中核对排序与跨页结果。

## Migration Plan

1. 应用新数据库迁移，替换 latest 索引。
2. 部署使用有效时间查询和版本 2 游标的 API。
3. 验证混合来源首页按页面展示时间倒序，并验证旧游标返回 `INVALID_CURSOR`。
4. 回滚时先回滚 API，再执行 down migration 恢复旧索引；文章数据无需转换。
