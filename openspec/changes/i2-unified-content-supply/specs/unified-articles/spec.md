# Spec Delta

## Purpose

定义 RSS 与用户投稿共享文章池所需的身份、版本、生命周期、权限、并发、幂等、历史迁移和匿名 latest 读取行为，使两类内容无需异步基础设施即可稳定发布和阅读。

## ADDED Requirements

### Requirement: 文章具有互斥且完整的来源身份

系统 SHALL 以 `rss` 或 `user` 标识文章来源类型，并确保 RSS 文章具有 Source、源内去重身份和原文 URL，用户文章具有站内作者且不得伪造 Source 或原文 URL。

#### Scenario: 匿名读取 RSS 文章
- **WHEN** 匿名调用者读取一篇已发布 RSS 文章
- **THEN** 响应的 `origin.type` SHALL 为 `rss`，并包含 Source 摘要、原文 URL 和可为空的原站发布时间

#### Scenario: 匿名读取用户文章
- **WHEN** 匿名调用者读取一篇已发布用户文章
- **THEN** 响应的 `origin.type` SHALL 为 `user`，只公开作者稳定 ID 和昵称，不公开登录用户名、Source 或伪造的原文 URL

### Requirement: 内容版本不可变且当前版本唯一

系统 SHALL 为每篇文章保存不可变、单调递增的内容修订，并以单一当前修订提供列表和详情内容；文章聚合 SHALL 使用独立的 `lock_version` 控制内容及状态并发。

#### Scenario: 编辑已发布文章立即公开
- **WHEN** 作者以当前 `If-Match` 编辑已发布文章且内容发生变化
- **THEN** 系统 SHALL 在同一 PostgreSQL 事务中创建新修订、切换当前修订并增加 `lock_version`，事务提交前匿名读取继续看到旧修订，提交后看到新修订

#### Scenario: 内容没有实质变化
- **WHEN** 编辑命令规范化后的内容哈希与当前修订相同
- **THEN** 系统 SHALL 返回当前结果且不得创建空修订或增加修订号

#### Scenario: 状态变更不制造内容修订
- **WHEN** 文章被发布、下架、恢复或删除但内容未变化
- **THEN** 系统 SHALL 增加 `lock_version`，且不得仅为状态变化创建内容修订

### Requirement: 用户稿件遵循明确的生命周期

用户文章 SHALL 支持 `draft`、`published`、`offline`、`deleted`，首次发布 SHALL 固定站内 `published_at`；编辑、下架后重新发布和管理员恢复均不得改变该时间，删除 SHALL 为不可恢复终态。

#### Scenario: 创建草稿
- **WHEN** 登录用户创建 `initial_status=draft` 的文章
- **THEN** 系统 SHALL 创建仅作者可读的草稿，允许标题或正文暂时为空且 `published_at` 为空

#### Scenario: 直接发布
- **WHEN** 登录用户创建满足发布校验的 `initial_status=published` 文章
- **THEN** 系统 SHALL 在一次事务中创建并公开文章，设置首次 `published_at`，且匿名读取立即可用

#### Scenario: 草稿首次发布
- **WHEN** 作者发布满足校验的草稿
- **THEN** 系统 SHALL 将其置为 `published`、设置固定 `published_at` 并纳入 latest

#### Scenario: 作者主动下架并重新发布
- **WHEN** 作者下架自己的已发布文章后再次发布
- **THEN** 系统 SHALL 恢复公开最新修订并保留首次 `published_at`

#### Scenario: 作者软删除文章
- **WHEN** 作者删除自己的草稿、已发布或下架文章
- **THEN** 系统 SHALL 将文章置为不可恢复的 `deleted`，立即从所有匿名读取中隐藏并保留数据库记录和内容修订

### Requirement: 管理员下架优先于作者发布

系统 SHALL 区分作者下架与管理员下架；管理员可下架任意已发布文章并恢复管理员下架的文章，但不得读取用户草稿、编辑或删除用户投稿，也不得恢复作者主动下架的文章。

#### Scenario: 管理员下架用户文章
- **WHEN** 管理员下架一篇已发布用户文章
- **THEN** 系统 SHALL 记录管理员级下架、操作者和时间，并立即从匿名读取隐藏文章

#### Scenario: 作者编辑管理员下架文章
- **WHEN** 作者编辑一篇由管理员下架的文章
- **THEN** 系统 SHALL 保存新的当前修订但保持文章离线，且作者发布请求 SHALL 返回 `403 ARTICLE_ADMIN_OFFLINE`

#### Scenario: 管理员恢复文章
- **WHEN** 管理员恢复由管理员下架且未删除的文章
- **THEN** 系统 SHALL 立即公开最新修订并保留首次 `published_at`

#### Scenario: 管理员尝试访问草稿
- **WHEN** 管理员以非作者身份访问用户草稿或作者主动下架的私有详情
- **THEN** 系统 SHALL 返回 404，且不得因管理员角色扩大草稿读取权限

### Requirement: 投稿正文受服务端 Markdown 策略约束

系统 SHALL 只接受 UTF-8 Markdown 用户正文，禁用原始 HTML，并由服务端渲染及白名单清洗；图片只可使用属于作者且已确认的站内资产引用。

#### Scenario: 保存未完成草稿
- **WHEN** 作者保存标题或正文为空的草稿
- **THEN** 系统 SHALL 允许保存，但不得因此绕过后续发布校验

#### Scenario: 发布有效图文稿件
- **WHEN** 标题为 1 至 200 个 Unicode 字符、Markdown 不超过 256 KiB，正文含可见文本或有效图片且图片不超过 20 张
- **THEN** 系统 SHALL 渲染并清洗 HTML、生成纯文本与服务端摘要，并允许发布

#### Scenario: 投稿包含原始 HTML 或外部图片
- **WHEN** Markdown 包含原始 HTML或非 `asset:<uuid>` 的图片地址
- **THEN** 系统 SHALL 禁止原始 HTML 生效并拒绝绕过站内资产协议的图片引用

#### Scenario: 投稿仅包含无效内容
- **WHEN** 发布请求没有可见文本且没有有效站内图片，或超过任一内容限制
- **THEN** 系统 SHALL 返回可识别的校验错误且文章不得公开

### Requirement: 投稿写命令同时满足幂等与乐观并发

全部 I2 文章写接口 SHALL 要求 `Idempotency-Key`；修改既有文章还 SHALL 要求 `If-Match` 对应当前 `lock_version`。成功幂等结果默认保存 24 小时，并与业务变更原子提交。

#### Scenario: 相同请求重试
- **WHEN** 同一调用者在保留期内以相同操作、幂等键和规范化请求重试
- **THEN** 系统 SHALL 重放首次成功业务结果，不得创建重复文章、修订或状态变更

#### Scenario: 幂等键被不同请求复用
- **WHEN** 同一调用者和操作使用已有幂等键但规范化请求不同
- **THEN** 系统 SHALL 返回 `409 IDEMPOTENCY_KEY_REUSED`

#### Scenario: 使用过期聚合版本写入
- **WHEN** 新幂等键携带的 `If-Match` 不等于当前 `lock_version`
- **THEN** 系统 SHALL 返回 `409 ARTICLE_VERSION_CONFLICT` 且不得修改文章

#### Scenario: 操作事务失败
- **WHEN** 文章写入或成功幂等记录中的任一步骤失败
- **THEN** 系统 SHALL 回滚二者且允许调用者使用同一幂等键安全重试

### Requirement: latest 使用稳定站内发布时间分页

`GET /api/v1/articles` SHALL 作为匿名 latest，且只返回 `published` 文章，按固定 `(published_at, article_id)` 倒序使用不透明游标分页；`GET /api/v1/articles/{id}` SHALL 只返回当前公开修订的清洗 HTML。

#### Scenario: 编辑不改变 latest 位置
- **WHEN** 已发布文章被编辑并产生新修订
- **THEN** 其 `published_at` SHALL 不变，后续 latest 排序不得将其顶回首页

#### Scenario: 同一发布时间稳定翻页
- **WHEN** 多篇文章具有相同 `published_at`
- **THEN** 系统 SHALL 以文章 ID 作为确定性次序并避免正常翻页中的重复项

#### Scenario: 私有文章不可匿名读取
- **WHEN** 匿名调用者请求草稿、下架或删除文章的详情
- **THEN** 系统 SHALL 返回 404，并从 latest 中排除该文章

### Requirement: 作者 API 与最小 Web 工作流一致

系统 SHALL 提供本人文章创建、列表、详情、编辑、发布、下架和删除 API，以及手动保存的 Markdown Web 工作流；首版不得自动合并并发内容。

#### Scenario: 手动保存已发布文章
- **WHEN** 作者在 Web 编辑已发布文章
- **THEN** 页面 SHALL 明确标示保存会立即公开，并只在用户主动提交时发送写请求

#### Scenario: Web 遇到版本冲突
- **WHEN** Web 收到 `ARTICLE_VERSION_CONFLICT`
- **THEN** 页面 SHALL 保留本地文本、提示刷新远端版本并允许复制内容，且不得自动覆盖或自动合并

#### Scenario: 身份功能关闭
- **WHEN** 服务以 `auth.enabled=false` 运行
- **THEN** 系统 SHALL 保持匿名 RSS/latest 可用且不注册 `/me/*` 与 `/admin/*` HTTP 写路由

### Requirement: 历史 RSS 数据无损迁入统一模型

迁移 SHALL 保留现有 RSS 文章 ID和内容，为每篇文章创建 revision 1，以 `discovered_at` 回填固定站内 `published_at`，并将 `published` 映射为 `published`、`hidden` 映射为无具体操作者的管理员级 `offline`。

#### Scenario: 从 I2 前数据库升级
- **WHEN** 对包含已发布和隐藏 RSS 文章的 I2 前数据库执行迁移
- **THEN** 文章数量、ID、Source 身份、源内去重键、原始正文、清洗正文和内容哈希 SHALL 保留，公开可见性 SHALL 按映射结果保持

#### Scenario: 迁移后读取历史文章
- **WHEN** 匿名调用者读取迁移后的已发布 RSS 文章
- **THEN** 系统 SHALL 通过联合 RSS 来源结构返回 version 1，并按回填的 `published_at` 分页

#### Scenario: 不安全降级被拒绝
- **WHEN** 数据库已有用户投稿、资产或第二个内容修订且执行 down migration 会丢失数据
- **THEN** down migration SHALL 明确失败而不得静默删除或折叠新模型数据

### Requirement: 核心内容路径不依赖后续中间件

RSS 自动发布、用户纯文本投稿、匿名 latest 和文章详情 SHALL 在 RabbitMQ、Redis、OpenSearch 和模型均未配置时正常工作。

#### Scenario: 后续依赖全部缺失
- **WHEN** 仅 PostgreSQL、API 和 Worker 的核心依赖可用且 MQ、Redis、搜索与模型未配置
- **THEN** RSS 和用户文章 SHALL 仍可发布并被匿名读取

