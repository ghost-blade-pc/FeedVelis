# article-feedback Specification

## Purpose

定义登录用户对公开文章的阅读、收藏与“不感兴趣”反馈及本人状态读取，并提供有界、可解释且隔离的推荐画像读取契约，使后续推荐能够安全使用真实信号。

## Requirements

### Requirement: 反馈只属于经过认证的当前用户与公开文章

系统 SHALL 只接受当前有效登录身份对当前 `published` 文章写入反馈，用户 ID SHALL 从服务端认证上下文取得而不得由请求指定；本人状态读取和推荐画像读取 SHALL 严格按该用户 ID 隔离。匿名 latest、详情与搜索 SHALL 继续可用，匿名读取 SHALL 不产生阅读事实、跨会话身份或画像；`auth.enabled=false` 时 SHALL 不注册反馈路由。

#### Scenario: 登录用户操作公开文章
- **WHEN** 有效登录用户对公开 RSS 或站内投稿文章提交反馈
- **THEN** 系统 SHALL 只变更该用户与该文章对应的反馈事实

#### Scenario: 匿名或失效身份尝试写入
- **WHEN** 调用者未认证或令牌失效却请求任一反馈端点
- **THEN** 系统 SHALL 按现有认证错误契约拒绝，且不得写入任何反馈

#### Scenario: 非公开文章或非法 ID
- **WHEN** 调用者对不存在、草稿、下架或已删除文章提交反馈
- **THEN** 系统 SHALL 对合法但不可见的 ID 返回 `404 ARTICLE_NOT_FOUND`，对非正整数 ID 返回 `400 VALIDATION_FAILED`，不得暴露私有文章内容

#### Scenario: 用户之间隔离
- **WHEN** 用户 A 与 B 对同一文章提交不同反馈并分别读取状态或画像
- **THEN** 每人 SHALL 只看到自身状态与自身聚合，且请求不得接受可覆盖当前用户的 `user_id`

### Requirement: 阅读事实按固定窗口合并且画像权重有上限

系统 SHALL 提供 `POST /api/v1/me/articles/{article_id}/reads` 记录一次站内详情阅读；同一用户与文章在按 UTC 对齐的 30 分钟窗口内 SHALL 最多形成一个计权事实，重复请求 SHALL 返回成功而不增加计数。系统 SHALL 保留最近 90 天的窗口事实供画像使用；画像对同一用户、文章、UTC 自然日的阅读贡献 SHALL 最多计 1 次，超过 90 天的事实 SHALL 不贡献权重。

#### Scenario: 刷新或重试同一详情
- **WHEN** 同一登录用户在同一 30 分钟窗口内重复提交同一文章的阅读
- **THEN** 每次请求 SHALL 成功，但窗口事实与画像权重均 SHALL 不增长

#### Scenario: 跨窗口与单日上限
- **WHEN** 同一用户一天内跨多个窗口阅读同一文章
- **THEN** 系统 SHALL 可保留多个窗口事实，但当天画像阅读贡献 SHALL 仍为 1

#### Scenario: 匿名浏览详情
- **WHEN** 匿名用户打开公开文章详情
- **THEN** 正文 SHALL 正常显示，且前后端均 SHALL 不为该访问创建个性化阅读记录

### Requirement: 收藏是可读取且可取消的唯一状态

系统 SHALL 提供 `PUT` 与 `DELETE /api/v1/me/articles/{article_id}/favorite` 设置和取消本人收藏，并通过本人状态读取返回 `favorited`。同一用户与文章至多具有一个当前收藏状态；重复设置或取消 SHALL 成功且不增加推荐权重，取消后的状态 SHALL 立即可见。

#### Scenario: 收藏重试与取消
- **WHEN** 用户连续两次收藏同一文章再连续两次取消
- **THEN** 四次请求 SHALL 成功，最终 `favorited` SHALL 为 false，画像 SHALL 无该收藏贡献

#### Scenario: 两个用户收藏同一文章
- **WHEN** 用户 A 取消收藏而用户 B 保持收藏
- **THEN** A 的状态与画像 SHALL 无收藏贡献，B 的状态与画像 SHALL 保留收藏贡献

### Requirement: 不感兴趣只明确排除文章且具有有限保留期

系统 SHALL 提供 `PUT` 与 `DELETE /api/v1/me/articles/{article_id}/not-interested` 设置和撤销本人文章级负反馈，并通过本人状态读取返回 `not_interested` 与有效期。首次设置 SHALL 从服务端当前时间起有效 180 天；有效期内重复设置 SHALL 不延长有效期或放大权重；过期后状态与画像 SHALL 自动视为无效，重新设置 SHALL 建立新的 180 天有效期。撤销 SHALL 立即取消抑制。已有收藏与负反馈 SHALL 能同时保留，但有效负反馈 SHALL 对该文章的推荐资格优先于收藏与阅读。

#### Scenario: 重复负反馈
- **WHEN** 用户在有效期内对同一文章重复设置“不感兴趣”
- **THEN** 响应 SHALL 保持原有效期，画像 SHALL 只有一个文章级排除原因

#### Scenario: 过期或撤销
- **WHEN** 负反馈超过 180 天或用户撤销它
- **THEN** 状态 SHALL 为未标记，文章 SHALL 不再因该反馈进入排除集，收藏状态 SHALL 不受影响

#### Scenario: 文章级抑制
- **WHEN** 用户对一篇文章标记“不感兴趣”，同来源或同主题还有其他文章
- **THEN** 画像 SHALL 明确排除该文章；其他文章 SHALL 不被直接排除，来源与主题只作为有界降权证据

### Requirement: 本人反馈状态可批量读取且不泄露私有事实

系统 SHALL 提供 `GET /api/v1/me/article-feedback?article_ids=...`，接受 1 至 50 个互异正整数文章 ID，按请求顺序返回当前公开文章的 `article_id`、`favorited`、`not_interested` 和可空 `not_interested_expires_at`；非法、重复或超量参数 SHALL 返回 `400 VALIDATION_FAILED`，不可见文章 SHALL 从结果中省略。该响应 SHALL 不包含其他用户、阅读窗口事实或聚合权重。

#### Scenario: 文章卡片批量查询
- **WHEN** 登录用户请求同一页最多 50 篇公开文章的本人反馈状态
- **THEN** 系统 SHALL 一次返回对应状态，缺失反馈的文章 SHALL 返回 false 与空有效期

#### Scenario: 批次中出现下架文章
- **WHEN** 请求 ID 中一篇文章在读取前已下架
- **THEN** 响应 SHALL 省略该文章，其他公开文章的状态 SHALL 正常返回

### Requirement: 推荐画像端口聚合当前且有界的信号

系统 SHALL 提供仅供后端应用调用、以显式当前用户身份为参数的画像读取端口，返回最近 90 天按用户和文章/UTC 日去重的阅读贡献、当前收藏文章、有效负反馈文章排除集，以及由这些文章当前公开元数据推导的有界关键词、主题与 RSS 来源偏好证据。有效负反馈 SHALL 产生可解释的文章排除原因；关键词、主题和来源负向证据 SHALL 带样本量及上限，不能把整类内容硬排除。画像读取 SHALL 过滤当前不公开文章，且在缺少 AI 关键词或主题、RSS 来源或没有反馈时返回相应空证据，不得跨用户聚合。公开 latest 与搜索排序 SHALL 不使用该画像；recommend 消费该端口时 MUST 对有效文章级排除执行抑制并可解释其原因。

#### Scenario: 重复请求不放大画像
- **WHEN** 同一用户重复阅读、收藏或标记同一文章
- **THEN** 聚合后的文章贡献 SHALL 遵循单日阅读上限和唯一显式状态，不随请求次数增长

#### Scenario: 负反馈可解释
- **WHEN** 画像包含一篇有效“不感兴趣”文章
- **THEN** 端口 SHALL 返回该文章 ID 与明确反馈原因，供 recommend 排除；对应关键词、主题或来源只返回有界降权证据

#### Scenario: 文章被下架或增强结果缺失
- **WHEN** 已反馈文章不再公开，或当前修订没有关键词或主题结果
- **THEN** 不公开文章 SHALL 不贡献画像；缺少对应增强字段的公开文章 SHALL 不产生该字段证据，但文章级状态仍保持

#### Scenario: 用户之间隔离
- **WHEN** 两名用户对相同关键词关联文章提交不同反馈
- **THEN** 各自画像 SHALL 只包含本人信号形成的关键词证据，且任何用户不能读取另一用户的画像

### Requirement: Web 反馈交互与阅读保持一致

Web SHALL 仅在登录态文章详情成功展示公开正文后上报阅读；详情和复用文章卡片 SHALL 显示可访问的收藏与“不感兴趣”操作及本人状态。未登录用户 SHALL 继续正常浏览，并在尝试互动时被引导登录；状态写入失败 SHALL 明确提示且恢复服务端已确认状态，切换账户或退出登录 SHALL 清除上一个用户的反馈状态。

#### Scenario: 登录用户阅读和操作
- **WHEN** 登录用户打开公开文章并收藏或标记“不感兴趣”
- **THEN** 页面 SHALL 上报阅读、更新对应本人状态，刷新后 SHALL 从服务端恢复该状态

#### Scenario: 写入失败或切换账户
- **WHEN** 反馈请求失败，或用户 A 退出并由用户 B 登录
- **THEN** 页面 SHALL 不把未确认的操作当作成功，也不得向 B 展示 A 的反馈状态
