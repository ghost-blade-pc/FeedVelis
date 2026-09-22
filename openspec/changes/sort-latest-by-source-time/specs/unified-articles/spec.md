# Spec Delta

## MODIFIED Requirements

### Requirement: latest 使用有效发布时间稳定分页

`GET /api/v1/articles` SHALL 作为匿名 latest 且只返回 `published` 文章；RSS 文章的有效发布时间 SHALL 优先使用 `source_published_at`，缺失时回退 `published_at`，站内投稿 SHALL 使用 `published_at`。列表 SHALL 按固定 `(effective_published_at, article_id)` 倒序使用不透明游标分页；`GET /api/v1/articles/{id}` SHALL 只返回当前公开修订的清洗 HTML。

#### Scenario: RSS 按原站发布时间排序
- **WHEN** 多篇 RSS 文章具有不同的 `source_published_at`
- **THEN** latest SHALL 按 `source_published_at` 从新到旧排列，而不受同批抓取时间或插入顺序影响

#### Scenario: 缺失原站时间时回退
- **WHEN** RSS 文章没有 `source_published_at`，或文章来源为站内投稿
- **THEN** latest SHALL 使用该文章固定的 `published_at` 参与统一排序

#### Scenario: 编辑不改变 latest 位置
- **WHEN** 已发布文章被编辑并产生新修订
- **THEN** 其有效发布时间 SHALL 不变，后续 latest 排序不得将其顶回首页

#### Scenario: 同一发布时间稳定翻页
- **WHEN** 多篇文章具有相同的有效发布时间
- **THEN** 系统 SHALL 以文章 ID 作为确定性次序并避免正常翻页中的重复项

#### Scenario: 排序语义变更后使用旧游标
- **WHEN** 调用方提交按旧排序语义生成的不透明游标
- **THEN** 系统 SHALL 将其识别为无效游标，调用方可从第一页重新获取列表

#### Scenario: 私有文章不可匿名读取
- **WHEN** 匿名调用者请求草稿、下架或删除文章的详情
- **THEN** 系统 SHALL 返回 404，并从 latest 中排除该文章

## RENAMED Requirements

- FROM: `### Requirement: latest 使用稳定站内发布时间分页`
- TO: `### Requirement: latest 使用有效发布时间稳定分页`
