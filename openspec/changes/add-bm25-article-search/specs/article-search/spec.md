# Spec Delta

## Purpose

定义公开文章的匿名 BM25 搜索契约，包括最小过滤、确定性相关性排序、快照游标、PostgreSQL 当前可见性复核、局部故障语义和最小 Web 搜索体验。

## ADDED Requirements

### Requirement: 匿名文章搜索具有明确且有界的 HTTP 契约

系统 SHALL 提供匿名 `GET /api/v1/search/articles`，要求非空 `q`，并支持可选的精确 `keyword`、精确 `topic` 与正整数 `source_id` 过滤；`q` 规范化后 SHALL 为 1 至 200 个 Unicode 字符，`keyword` 与 `topic` 各为 1 至 64 个 Unicode 字符，`limit` SHALL 默认为 20 且只接受 1 至 50。成功响应 SHALL 返回 `items`、可空 `next_cursor` 与 `has_more`，其中每个 item 复用公开 `ArticleItem` 契约且不得公开搜索分数、索引内部字段或文章私有内容。

#### Scenario: 只按搜索词查询
- **WHEN** 匿名调用者以合法 `q` 请求第一页且不提供过滤条件
- **THEN** 系统 SHALL 返回按搜索相关性排列的公开文章页及分页状态

#### Scenario: 组合最小过滤条件
- **WHEN** 调用者同时提供合法 `q`、`keyword`、`topic` 和 `source_id`
- **THEN** 系统 SHALL 只保留同时满足关键词、主题和来源过滤的全文命中；来源过滤不得匹配没有该 Source 的站内投稿

#### Scenario: 查询参数非法
- **WHEN** `q` 为空或超过长度边界，过滤值为空或过长，`source_id` 非正整数，或 `limit` 超出范围
- **THEN** 系统 SHALL 返回 `400 VALIDATION_FAILED` 且不得向 OpenSearch 发出查询

#### Scenario: 没有匹配文章
- **WHEN** 合法查询在完成当前可见性复核后没有结果且候选已耗尽
- **THEN** 系统 SHALL 返回空 `items`、空 `next_cursor` 和 `has_more=false`，不得把 latest 内容冒充搜索结果

### Requirement: BM25 覆盖规定文本字段并具有确定性排序

系统 SHALL 使用现有文章搜索读投影执行 BM25 全文查询，并始终过滤 `visible=true`。标题、纯正文、AI 摘要、关键词和主题 SHALL 均可贡献匹配，标题、关键词、主题和摘要 SHALL 获得高于普通正文的显式固定权重；过滤条件不得改变为语义检索。结果 SHALL 按 BM25 分数倒序，再按有效发布时间倒序、文章 ID 倒序形成全序，且同一查询快照中的相同输入 SHALL 产生相同次序。

#### Scenario: 各规定字段能够召回
- **WHEN** 固定测试文章分别只在标题、纯正文、AI 摘要、关键词或主题中包含查询词
- **THEN** 每篇文章 SHALL 能被对应查询召回，且查询不得要求文章必须具有 AI 结果或向量

#### Scenario: 高权重字段优先
- **WHEN** 两篇其它条件等价的文章分别只在标题和普通正文中以等价频次匹配查询词
- **THEN** 标题命中的文章 SHALL 排在普通正文命中的文章之前

#### Scenario: 分数相同时稳定打破并列
- **WHEN** 多篇命中文章具有相同 BM25 分数
- **THEN** 系统 SHALL 依次使用有效发布时间和文章 ID 的固定倒序打破并列，不得依赖分片或返回顺序碰巧稳定

#### Scenario: 中英文混合查询
- **WHEN** 查询包含连续中文、ASCII 术语、URL 或专有名词样例
- **THEN** 系统 SHALL 使用当前索引 schema 声明的分析语义产生可重复结果；若现有 schema 无法满足固定验收样例，系统 MUST 通过新 schema 和在线重建升级，不得原地改变既有索引语义

### Requirement: 搜索分页使用绑定查询快照的版本化游标

系统 SHALL 使用有限生命周期的搜索快照与 search-after 状态生成不透明游标。游标 SHALL 包含显式版本、规范化查询及过滤条件的指纹、快照身份和最后消费命中的完整排序值；后续请求 SHALL 继续同一快照并验证请求条件与指纹一致。正常翻页 SHALL 不重复或跳过已返回文章，且索引在翻页期间新增、删除或重新打分不得改变该游标所见候选集合。

#### Scenario: 正常继续下一页
- **WHEN** 调用者在游标有效期内使用相同 `q` 和过滤条件请求下一页
- **THEN** 系统 SHALL 从上一页最后消费位置之后继续同一快照，返回的文章 ID 不得与先前页面重复

#### Scenario: 翻页期间索引发生变化
- **WHEN** 第一页返回后当前读索引新增命中、更新文档或切换别名
- **THEN** 既有游标 SHALL 继续读取创建它时的快照次序，新查询第一页 SHALL 可观察新的索引状态

#### Scenario: 游标与查询不匹配
- **WHEN** 调用者改变 `q`、`keyword`、`topic` 或 `source_id` 后复用旧游标
- **THEN** 系统 SHALL 返回 `400 INVALID_CURSOR`，调用者必须从新查询的第一页开始

#### Scenario: 旧版、篡改或过期游标
- **WHEN** 游标版本未知、编码或字段非法、超过长度边界、排序值无效、快照已过期或快照身份不被 OpenSearch 接受
- **THEN** 系统 SHALL 返回 `400 INVALID_CURSOR`，不得回退到不带快照的翻页或泄露 OpenSearch 原始错误

### Requirement: 每批搜索命中回到 PostgreSQL 复核当前公开事实

系统 SHALL 把 OpenSearch 仅作为候选与排序来源，并在返回前按有界候选批次一次性从 PostgreSQL 读取当前 `published` 文章及其当前修订、来源、作者和当前增强结果。系统 SHALL 按 OpenSearch 命中顺序重排 PostgreSQL 结果，丢弃不存在或当前不可见的文章，并继续有界扫描候选直到收集请求页、候选耗尽或达到单请求扫描上限；不得逐命中执行 PostgreSQL 查询。

#### Scenario: 索引中暂留已下架文章
- **WHEN** OpenSearch 快照仍命中一篇已在 PostgreSQL 下架或删除的文章
- **THEN** 系统 SHALL 在响应前过滤该文章，且响应正文、数量和错误不得泄露其私有内容

#### Scenario: 批量复核保持命中次序
- **WHEN** 一个候选批次包含多篇仍公开文章且 PostgreSQL 以不同顺序返回它们
- **THEN** 系统 SHALL 按原始 BM25 全序返回文章，不得改为数据库行顺序或 latest 顺序

#### Scenario: 候选批次含不可见间隙
- **WHEN** 当前页中部分候选被 PostgreSQL 过滤但后面仍有候选
- **THEN** 系统 SHALL 在有界扫描范围内继续 search-after 补充结果；若因扫描上限提前结束则游标 SHALL 越过已检查候选并允许后续请求继续，不得在正常翻页中重复已返回文章

#### Scenario: PostgreSQL 查询数量有界
- **WHEN** 一页需要复核 N 个 OpenSearch 命中
- **THEN** 系统 SHALL 按候选批次执行 PostgreSQL 查询，查询次数只随批次数增长而不得随 N 逐项增长

#### Scenario: 文章在复核时产生新公开修订
- **WHEN** OpenSearch 命中旧投影但 PostgreSQL 中该文章仍为 `published` 且已有新当前修订
- **THEN** 响应 SHALL 使用 PostgreSQL 的当前公开 ArticleItem；索引最终一致性不得让旧修订内容进入响应

### Requirement: 搜索故障被隔离并返回明确不可用错误

系统 SHALL 在 OpenSearch 未配置、连接失败、请求超时、读别名缺失或查询响应无法验证时，为搜索端点返回 `503 SEARCH_UNAVAILABLE` 和可重试提示，不得回退到 PostgreSQL 模糊搜索或 latest。OpenSearch 状态 SHALL 不参与核心 readiness，且搜索故障不得改变 `GET /api/v1/articles`、`GET /api/v1/articles/{id}`、文章写入或投影任务持久化的可用性。

#### Scenario: OpenSearch 未配置
- **WHEN** API 进程未配置 OpenSearch 端点
- **THEN** 搜索路由 SHALL 仍存在并返回 `503 SEARCH_UNAVAILABLE`，latest 与文章详情 SHALL 按现有 PostgreSQL 路径正常工作

#### Scenario: OpenSearch 查询期间故障
- **WHEN** OpenSearch 不可达、超时或返回无法安全关联的响应
- **THEN** 当前搜索请求 SHALL 返回 `503 SEARCH_UNAVAILABLE`，不得返回未经过完整处理的部分搜索页

#### Scenario: PostgreSQL 复核失败
- **WHEN** OpenSearch 已返回候选但 PostgreSQL 批量可见性复核失败
- **THEN** 搜索请求 SHALL 按依赖不可用失败且不得直接返回 OpenSearch 文档；该失败不得改变其它读取端点的路由或健康语义

### Requirement: Web 提供最小且可恢复的搜索工作流

Web SHALL 提供独立搜索页面与主导航入口，允许用户输入 `q` 和可选关键词、主题、来源过滤，提交后展示与现有公开文章卡片一致的结果并链接到站内详情。页面 SHALL 支持继续加载、重新查询、空结果、参数校验、请求取消和搜索不可用状态；新查询 SHALL 清空旧游标与旧结果，不得在失败时混入 latest 内容。

#### Scenario: 提交查询并打开结果
- **WHEN** 用户提交合法查询且服务返回命中
- **THEN** 页面 SHALL 展示文章卡片、来源或作者、摘要或增强结果，并允许用户进入既有站内正文页

#### Scenario: 修改条件后重新查询
- **WHEN** 用户在已翻页后修改搜索词或任一过滤条件并再次提交
- **THEN** 页面 SHALL 取消过期请求、清空旧结果和游标并从新查询第一页开始

#### Scenario: 搜索不可用
- **WHEN** API 返回 `SEARCH_UNAVAILABLE`
- **THEN** 页面 SHALL 明确提示搜索暂不可用并允许重试，同时保留 latest 与正文导航，不得把故障显示为空结果

#### Scenario: 无结果
- **WHEN** API 成功返回空页且 `has_more=false`
- **THEN** 页面 SHALL 显示无匹配结果状态，并允许用户调整查询条件

### Requirement: 搜索运行状态可观测且不记录查询正文

系统 SHALL 记录搜索请求成功、不可用、非法游标与依赖失败计数，查询总耗时及 OpenSearch/PostgreSQL 分段耗时、候选数量、可见性过滤数量和候选扫描上限命中次数；指标标签 SHALL 为固定低基数。结构化日志 SHALL 使用 request ID 和稳定错误分类关联诊断，但不得记录完整查询词、游标、PIT 身份、文章正文、OpenSearch 原始响应或凭据。

#### Scenario: 可见性过滤发生
- **WHEN** 一次搜索从候选中移除当前不可见文章
- **THEN** 系统 SHALL 增加受控的过滤计数并可按 request ID 诊断，不得在日志中写出被过滤文章内容或用户完整查询

#### Scenario: 搜索依赖超时
- **WHEN** OpenSearch 或 PostgreSQL 复核超过其请求边界
- **THEN** 系统 SHALL 记录稳定的依赖与超时分类、分段耗时和失败计数，指标标签不得包含查询词、文章 ID、游标或原始错误文本
