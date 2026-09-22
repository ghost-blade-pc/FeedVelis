# Proposal

## Why

匿名 latest 当前按站内收录时间排序，却在 RSS 卡片上展示原站发布时间；批量抓取时多篇文章共享同一收录时间，页面因 ID 兜底而呈现与可见日期不一致的顺序。需要让排序键与读者看到的发布时间保持一致。

## What Changes

- RSS 文章使用原站发布时间作为 latest 排序时间；原站未提供时间时回退到固定站内发布时间。
- 站内投稿继续使用首次站内发布时间，与 RSS 文章共同按统一的有效发布时间倒序排列。
- latest 游标继续使用“有效发布时间 + 文章 ID”保证同一时间下的确定性分页，并更新游标版本以拒绝旧排序语义生成的游标。
- 更新查询测试与文档，覆盖混合来源、缺失原站时间、同一时间和跨页场景。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `unified-articles`: 将匿名 latest 从固定站内发布时间排序改为 RSS 原站时间优先、站内发布时间回退的有效时间排序。

## Impact

- 后端 PostgreSQL latest 查询、领域列表投影和应用层不透明游标。
- latest 查询相关单元测试、PostgreSQL 集成测试及 README 行为说明。
- API 响应字段保持兼容，但已有分页游标失效，调用方需要从第一页重新获取。
