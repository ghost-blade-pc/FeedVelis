# Spec Delta

## MODIFIED Requirements

### Requirement: 用户稿件遵循明确的生命周期

用户文章 SHALL 支持 `draft`、`published`、`offline`、`deleted`，首次发布 SHALL 固定站内 `published_at`；编辑、下架后重新发布和管理员恢复均不得改变该时间，删除 SHALL 为不可恢复终态。公开状态提交后文章详情 SHALL 立即可读，启用缓存的共享 latest 候选发现 SHALL 遵循约定的短期新鲜度窗口。

#### Scenario: 创建草稿
- **WHEN** 登录用户创建 `initial_status=draft` 的文章
- **THEN** 系统 SHALL 创建仅作者可读的草稿，允许标题或正文暂时为空且 `published_at` 为空

#### Scenario: 直接发布
- **WHEN** 登录用户创建满足发布校验的 `initial_status=published` 文章
- **THEN** 系统 SHALL 在一次事务中创建并公开文章，设置首次 `published_at`，提交后匿名详情立即可用；启用缓存的 latest 允许正常情况下约 5 秒的候选新鲜度窗口

#### Scenario: 草稿首次发布
- **WHEN** 作者发布满足校验的草稿
- **THEN** 系统 SHALL 将其置为 `published`、设置固定 `published_at`，提交后详情立即可读并按 latest 新鲜度窗口进入适合其发布时间的候选位置

#### Scenario: 作者主动下架并重新发布
- **WHEN** 作者下架自己的已发布文章后再次发布
- **THEN** 系统 SHALL 恢复公开最新修订并保留首次 `published_at`，详情立即可读，latest 候选可按新鲜度窗口发现该文章

#### Scenario: 作者软删除文章
- **WHEN** 作者删除自己的草稿、已发布或下架文章
- **THEN** 系统 SHALL 将文章置为不可恢复的 `deleted`，立即从所有匿名读取中隐藏并保留数据库记录和内容修订；旧缓存不得绕过当前可见性复核

### Requirement: 管理员下架优先于作者发布

系统 SHALL 区分作者下架与管理员下架；管理员可下架任意已发布文章并恢复管理员下架的文章，但不得读取用户草稿、编辑或删除用户投稿，也不得恢复作者主动下架的文章。

#### Scenario: 管理员下架用户文章
- **WHEN** 管理员下架一篇已发布用户文章
- **THEN** 系统 SHALL 记录管理员级下架、操作者和时间，并立即从匿名读取隐藏文章；旧缓存不得绕过当前可见性复核

#### Scenario: 作者编辑管理员下架文章
- **WHEN** 作者编辑一篇由管理员下架的文章
- **THEN** 系统 SHALL 保存新的当前修订但保持文章离线，且作者发布请求 SHALL 返回 `403 ARTICLE_ADMIN_OFFLINE`

#### Scenario: 管理员恢复文章
- **WHEN** 管理员恢复由管理员下架且未删除的文章
- **THEN** 系统 SHALL 立即公开最新修订并保留首次 `published_at`，详情立即可读，启用缓存的 latest 候选可按新鲜度窗口发现该文章

#### Scenario: 管理员尝试访问草稿
- **WHEN** 管理员以非作者身份访问用户草稿或作者主动下架的私有详情
- **THEN** 系统 SHALL 返回 404，且不得因管理员角色扩大草稿读取权限

### Requirement: latest 使用有效发布时间稳定分页

`GET /api/v1/articles` SHALL 作为匿名 latest 且只返回 `published` 文章；RSS 文章的有效发布时间 SHALL 优先使用 `source_published_at`，缺失时回退 `published_at`，站内投稿 SHALL 使用 `published_at`。列表 SHALL 按固定 `(effective_published_at, article_id)` 倒序使用不透明游标分页；`GET /api/v1/articles/{id}` SHALL 只返回当前公开修订的清洗 HTML。启用缓存时新发布或恢复文章进入共享 latest 候选允许正常情况下约 5 秒的新鲜度窗口，卡片当前修订、当前增强结果和可见性 SHALL 仍由 PostgreSQL 复核；关闭或绕过缓存 SHALL 从当前事实读取候选。

#### Scenario: RSS 按原站发布时间排序
- **WHEN** 多篇 RSS 文章具有不同的 `source_published_at`
- **THEN** latest SHALL 按 `source_published_at` 从新到旧排列，而不受同批抓取时间或插入顺序影响

#### Scenario: 缺失原站时间时回退
- **WHEN** RSS 文章没有 `source_published_at`，或文章来源为站内投稿
- **THEN** latest SHALL 使用该文章固定的 `published_at` 参与统一排序

#### Scenario: 编辑不改变 latest 位置
- **WHEN** 已发布文章被编辑并产生新修订
- **THEN** 其有效发布时间 SHALL 不变，后续 latest 排序不得将其顶回首页；返回的卡片 SHALL 使用当前公开修订，不得等待列表或卡片缓存到期

#### Scenario: 同一发布时间稳定翻页
- **WHEN** 多篇文章具有相同的有效发布时间
- **THEN** 系统 SHALL 以文章 ID 作为确定性次序并避免正常翻页中的重复项

#### Scenario: 排序语义变更后使用旧游标
- **WHEN** 调用方提交按旧排序语义生成的不透明游标
- **THEN** 系统 SHALL 将其识别为无效游标，调用方可从第一页重新获取列表

#### Scenario: 私有文章不可匿名读取
- **WHEN** 匿名调用者请求草稿、下架或删除文章的详情
- **THEN** 系统 SHALL 返回 404，并从 latest 中排除该文章，即使缓存仍保留其 ID 或卡片

#### Scenario: 发布后读取缓存首页
- **WHEN** 新文章已提交且适合首页排序，但首页仍命中尚未过期的旧 ID 页
- **THEN** 详情 SHALL 立即可读；系统 SHALL 尽力失效首页，正常依赖下旧页的绝对有效期结束后新请求回源发现新文章，不保证当前已显示页面自动更新

#### Scenario: Redis 不可用或缓存清空
- **WHEN** Redis 不可用、ID 页损坏或缓存被清空，且 PostgreSQL 正常
- **THEN** latest SHALL 返回当前公开候选并保持原有键集分页语义，缓存问题不得使端点失败
