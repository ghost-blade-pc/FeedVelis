# agent-read-tools Specification

## Purpose

定义供后续对话编排使用的内部文章搜索、本人推荐及纯文本详情工具，以服务端身份、当前公开事实和有限资源预算约束工具调用，复用已有文章发现能力并避免把工具执行误当成用户反馈或会话写入。

## Requirements

### Requirement: 工具入口与身份由服务端控制

系统 SHALL 提供应用层内部 `search_articles`、`recommend_articles` 和 `get_article`，不新增工具 HTTP 路由或调试 CLI。调用者 SHALL 使用服务端已确认的用户账户身份，不接受工具参数中的 user_id；工具 SHALL 不依赖会话 ID，未来编排 SHALL 先校验会话所有权再传入真实用户。

#### Scenario: 参数伪造身份
- **WHEN** 工具输入包含 user_id、conversation_id 或其它未声明字段
- **THEN** 系统 SHALL 返回 `VALIDATION_FAILED`，不得改用模型或客户端指定的身份

#### Scenario: 不同用户调用推荐
- **WHEN** 服务端为两个合法用户分别调用推荐工具
- **THEN** 工具 SHALL 各自读取本人画像和排除事实，不能因工具复用或缓存混用另一用户数据

### Requirement: 工具输入具有严格的类型与上限

三个工具 SHALL 使用显式输入输出契约，拒绝未知字段、重复键、错误类型、null、非法 UTF-8、NUL 和未配对 Unicode 转义。搜索和推荐 `limit` SHALL 默认 5、只接受 1 至 10；文章 ID 和来源 ID SHALL 为现有正整数。工具参数 SHALL 不接受分页游标、任意 SQL、URL 抓取目标或动态工具定义。

#### Scenario: 非法调用不连接依赖
- **WHEN** 工具输入类型、长度、字符或数值范围非法
- **THEN** 工具 SHALL 返回 `VALIDATION_FAILED`，不执行 PostgreSQL、OpenSearch 或模型请求

#### Scenario: 省略和显式零值
- **WHEN** 调用者省略 limit 或显式提供 limit=0
- **THEN** 前者 SHALL 使用默认 5，后者 SHALL 拒绝，不能把显式非法值当作省略

### Requirement: 搜索工具复用当前检索语义且只返回一批

`search_articles` SHALL 接受必需 `q` 和可选 `keyword,topic,source_id,limit`；文本规范化后 q SHALL 为 1 至 200 个 code point，keyword/topic 各为 1 至 64。工具 SHALL 复用既有精确筛选、BM25 或已配置混合召回、确定排序和当前事实复核，只返回一次有界结果，不暴露分页游标。

#### Scenario: 合法过滤和空结果
- **WHEN** 用户搜索并组合合法过滤，或合法查询确实没有匹配
- **THEN** 工具 SHALL 只返回满足条件的当前公开结果；无匹配 SHALL 成功返回空 items，不能用 latest 代替搜索

#### Scenario: 必需检索路径故障
- **WHEN** OpenSearch/BM25 未配置、不可达、超时或返回无法验证的结果
- **THEN** 工具 SHALL 返回明确的 `SEARCH_UNAVAILABLE`；工具总 deadline 已耗尽时 SHALL 返回 `TOOL_TIMEOUT`，两者都不得伪装成空搜索

#### Scenario: 语义分支单独失败
- **WHEN** 已配置混合检索但 Embedding 或 KNN 单独失败，BM25 与 PostgreSQL 正常
- **THEN** 工具 SHALL 沿用现有 BM25 降级路径，不使可用关键词搜索整体失败

### Requirement: 一次搜索不遗留可续页资源

一次工具搜索 SHALL 在成功、错误、超时或取消后尝试释放其创建的全部临时搜索快照，不能仅丢弃分页游标而保留 PIT。清理 SHALL 使用独立且最多 250 毫秒的有界预算；清理失败 SHALL 可观测并由已有有限快照期限兜底，不把可用业务结果变为无限等待。

#### Scenario: 候选多于工具上限
- **WHEN** 结果超过 limit 且公开搜索接口通常会保留 PIT 供续页
- **THEN** 工具 SHALL 返回截断标记并释放自己的 PIT，既有公开 HTTP 搜索分页 SHALL 保持原行为

#### Scenario: 调用者取消
- **WHEN** 检索过程中调用者取消上下文
- **THEN** 工具 SHALL 停止业务读取，并在独立有界上下文中尝试释放已获得的 PIT

### Requirement: 推荐工具使用本人反馈并保留真实降级信息

`recommend_articles` SHALL 只接受可选 limit，按服务端用户身份复用本人推荐的排序、有效负反馈排除、冷启动及 latest 回退；不接受自由文本查询。结果 SHALL 包含真实 `mode,degraded,degrade_reason` 及每条最小 `recommendation_reason`，只返回一批且不暴露游标。

#### Scenario: 没有偏好或只有负反馈
- **WHEN** 当前用户没有有效正向信号
- **THEN** 工具 SHALL 采用当前 cold_start 路径，仍排除本人有效文章级负反馈，不生成新的阅读或偏好信号

#### Scenario: 搜索不可用但事实源正常
- **WHEN** 有正向偏好的推荐遇到 OpenSearch 故障且 PostgreSQL 可用
- **THEN** 工具 SHALL 成功返回 latest 回退并明确 `mode=latest_fallback,degraded=true`，不声称个性化检索成功

#### Scenario: 事实源或排除集失败
- **WHEN** PostgreSQL 当前公开事实或本人排除集无法读取
- **THEN** 工具 SHALL 返回 `DEPENDENCY_UNAVAILABLE`，不返回未经复核的缓存或索引内容，不忽略本人负反馈

### Requirement: 正文工具只读取当前公开修订的纯文本

`get_article` SHALL 接受必需 article_id 和可选 max_chars，后者默认 4000、只接受 1 至 8000 个 Unicode code point。结果 SHALL 来自同一读取视图中的当前公开修订元数据及已持久化纯文本，按前缀有界截取并返回 `truncated`；不得返回原始 HTML、草稿或旧修订正文。

#### Scenario: 不存在或非公开文章
- **WHEN** 请求文章不存在、是草稿、已下架或已删除，即使调用者是作者或管理员
- **THEN** 工具 SHALL 返回统一 `ARTICLE_NOT_FOUND`，不泄露非公开文章元数据或正文

#### Scenario: 正文达到字符边界
- **WHEN** 当前纯文本超过 max_chars 且包含多字节 Unicode 字符
- **THEN** 工具 SHALL 按 code point 截取前缀，不截断 UTF-8 字节，并准确设置 truncated

#### Scenario: 文章并发编辑
- **WHEN** 获取详情的同时文章切换当前修订
- **THEN** 返回的 revision_id、标题、摘要及纯文本 SHALL 来自同一有效读取视图，不能混合新旧修订

### Requirement: 工具输出保留文章身份但不泄露内部数据

文章结果 SHALL 包含 article_id、revision_id、标题、来源或作者、站内详情路径及有界摘要；RSS 原文链接 SHALL 为可选字段，摘要 SHALL 区分当前增强结果与原文摘录。结果 SHALL 不包含向量、Prompt、模型凭据、画像权重、搜索分数、私有资产凭据或内部任务状态；工具不负责最终回答的引用校验。

#### Scenario: 当前增强缺失
- **WHEN** 当前修订没有成功增强而旧修订有增强结果
- **THEN** 工具 SHALL 使用当前修订 excerpt 并标记为摘录，不沿用旧摘要或虚构 AI 来源

#### Scenario: 元数据过长
- **WHEN** 来源或作者文本超过展示预算
- **THEN** 工具 SHALL 明确标记展示截断，保留文章及修订身份、链接和排序，不编造替代来源

### Requirement: 当前公开事实优先于索引与缓存

搜索及推荐 SHALL 延续批量 PostgreSQL 当前事实复核和版本化卡片装配，保持实际检索顺序及缓存故障回源；正文 SHALL 读取当前公开修订纯文本。索引或 Redis SHALL 不能成为公开性及当前修订的权威来源。事实源失败 SHALL 返回依赖错误，不返回缓存中的陈旧内容。

#### Scenario: 缓存命中但文章已下架
- **WHEN** 索引或缓存中仍有已在 PostgreSQL 下架的文章
- **THEN** 工具 SHALL 过滤该结果，不能用旧命中泄露内容

#### Scenario: 缓存清空与错误
- **WHEN** Redis 被清空、不可用或载荷损坏而 PostgreSQL 正常
- **THEN** 工具 SHALL 批量回源当前事实，保持搜索/推荐顺序与身份隔离，不能因缓存故障扩大权限

### Requirement: 工具具有总时间及序列化输出预算

单次工具业务执行 SHALL 默认最多 5 秒，受调用者更短 deadline 限制，取消 SHALL 传递至依赖；工具层 SHALL 不额外重试。单个成功 JSON 输出 SHALL 默认不超过 128 KiB，预算耗尽时 SHALL 有界截断文本或移除尾部结果并明确标记，不改变已保留项身份及相对顺序。

#### Scenario: 总 deadline 与主动取消
- **WHEN** 工具总预算耗尽，或调用者主动取消
- **THEN** 工具 SHALL 分别返回 `TOOL_TIMEOUT` 或 `TOOL_CANCELED`，停止后续业务操作；资源清理仅使用独立清理预算

#### Scenario: JSON 转义扩大载荷
- **WHEN** 输入文章含大量需要 JSON 转义的文本导致编码后超出字节预算
- **THEN** 工具 SHALL 按最终序列化字节数裁剪并准确标记，不能仅按字符数估计或返回超预算载荷

### Requirement: 工具不写入会话或用户业务反馈

三个工具 SHALL 不创建或更改会话、消息、阅读、收藏、不感兴趣、用户画像及文章事实。既有缓存填充、临时 PIT 和低基数运行观测 SHALL 允许；本阶段 SHALL 不保存逐次工具调用记录，不调用生成模型产生回答。

#### Scenario: 连续调用三个工具
- **WHEN** 服务端先后搜索、推荐及读取正文
- **THEN** 调用前后会话、消息和用户反馈事实 SHALL 不变，工具调用不能算作本人阅读；已配置的查询 Embedding 仅可沿用既有检索预算

### Requirement: 工具错误可区分且观测不含原文

工具 SHALL 使用稳定类型错误 `VALIDATION_FAILED,ARTICLE_NOT_FOUND,SEARCH_UNAVAILABLE,DEPENDENCY_UNAVAILABLE,TOOL_TIMEOUT,TOOL_CANCELED,INTERNAL_ERROR`，区分成功空结果与失败。观测 SHALL 仅使用受控工具名、结果、降级、数量、截断及耗时；日志不得包含查询、正文、用户画像、游标、PIT 或凭据。

#### Scenario: 真空结果和依赖失败
- **WHEN** 合法搜索结果为空，或同一搜索的 PostgreSQL 复核失败
- **THEN** 前者 SHALL 成功返回空结果，后者 SHALL 返回依赖错误；错误不得附带底层响应或敏感文本

#### Scenario: 工具故障不影响会话
- **WHEN** 工具因搜索依赖失败或超时返回错误
- **THEN** 系统 SHALL 记录低基数分类，已保存会话及消息不受影响，核心 readiness 不增加搜索或模型依赖
