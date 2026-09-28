# 文章搜索规格

## Purpose

定义公开文章的匿名 BM25 搜索契约，包括最小过滤、确定性相关性排序、快照游标、PostgreSQL 当前可见性复核、局部故障语义和最小 Web 搜索体验。

## Requirements

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

系统 SHALL 使用现有文章搜索读投影执行 BM25 全文查询，并始终过滤 `visible=true`。标题、纯正文、AI 摘要、关键词和主题 SHALL 均可贡献匹配，标题、关键词、主题和摘要 SHALL 获得高于普通正文的显式固定权重；过滤条件不得改变为语义检索。BM25 独立召回及纯 BM25 降级结果 SHALL 按 BM25 分数倒序，再按有效发布时间倒序、文章 ID 倒序形成全序，且同一查询快照中的相同输入 SHALL 产生相同次序。

启用混合搜索且两路可用时，最终结果 SHALL 使用应用层 RRF 次序，不得把 BM25 与 KNN 原始分数直接相加。

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

未启用混合搜索时，系统 SHALL 保留有限生命周期的 PIT 快照和 search-after 不透明游标，包含显式版本、规范化查询及过滤条件指纹、快照身份及完整排序值。启用混合搜索时，系统 SHALL 固定首查的有界候选列表与最终排序，并以经过认证加密的版本化游标携带列表、消费位置、查询指纹、实际检索模式、查询计划身份与绝对过期时间；后续请求 SHALL 验证条件一致，不再生成查询向量或重新召回/融合。混合搜索首查降级为 BM25 时 SHALL 同样固定有界列表，不得在翻页恢复为混合排序。正常翻页 SHALL 不重复或跳过已返回文章，且索引在翻页期间新增、删除或重新打分不得改变该游标所见候选集合。

#### Scenario: 正常继续下一页
- **WHEN** 调用者在游标有效期内使用相同 `q` 和过滤条件请求下一页
- **THEN** 系统 SHALL 从上一页最后消费位置之后继续同一快照，返回的文章 ID 不得与先前页面重复

#### Scenario: 翻页期间索引发生变化
- **WHEN** 第一页返回后当前读索引新增命中、更新文档或切换别名
- **THEN** 既有游标 SHALL 继续读取创建它时的快照次序，新查询第一页 SHALL 可观察新的索引状态；混合游标 SHALL 继续固定候选列表

#### Scenario: 游标与查询不匹配
- **WHEN** 调用者改变 `q`、`keyword`、`topic` 或 `source_id` 后复用旧游标
- **THEN** 系统 SHALL 返回 `400 INVALID_CURSOR`，调用者必须从新查询的第一页开始

#### Scenario: 旧版、篡改或过期游标
- **WHEN** 游标版本未知、编码或字段非法、超过长度边界、排序值无效、快照已过期或 BM25 快照身份不被 OpenSearch 接受、混合计划身份不兼容
- **THEN** 系统 SHALL 返回 `400 INVALID_CURSOR`，不得回退到不带快照的翻页或泄露 OpenSearch 原始错误

#### Scenario: 混合候选列表耗尽
- **WHEN** 首查最多两路各 100 个候选的去重列表已经消费完毕
- **THEN** 系统 SHALL 返回 `has_more=false` 和空 `next_cursor`，不得继续从更深候选补召回；开启混合搜索的该上限 SHALL 在 HTTP 契约说明中明确

#### Scenario: 混合翻页与模型波动
- **WHEN** 第一页后模型返回不同向量或 Embedding/KNN 服务失效
- **THEN** 后续页 SHALL 继续既有列表，模型调用数为零，排序、检索模式和绝对过期时间 SHALL 保持不变

#### Scenario: 旧 BM25 游标继续原计划
- **WHEN** 混合开关开启后客户端携带尚未过期的旧 BM25 游标
- **THEN** 系统 SHALL 继续 BM25/PIT 查询计划，不得把它解释为混合游标

### Requirement: 每批搜索命中回到 PostgreSQL 复核当前公开事实

系统 SHALL 把 OpenSearch 仅作为候选与排序来源，并在返回前按有界候选批次一次性从 PostgreSQL 读取当前 `published` 文章及其当前修订、来源、作者和当前增强结果。系统 SHALL 按最终检索次序（纯 BM25 全序或混合 RRF 全序）重排 PostgreSQL 结果，丢弃不存在或当前不可见的文章；KNN 候选 SHALL 额外校验投影修订、generation、Embedding 选择和 profile 与当前事实一致。首次融合前 SHALL 移除无效 KNN 贡献，同篇文章若有 BM25 贡献 SHALL 保留其 BM25 资格。系统 SHALL 继续有界扫描候选直到收集请求页、候选耗尽或达到单请求扫描上限；不得逐命中执行 PostgreSQL 查询。

#### Scenario: 索引中暂留已下架文章
- **WHEN** OpenSearch 快照仍命中一篇已在 PostgreSQL 下架或删除的文章
- **THEN** 系统 SHALL 在响应前过滤该文章，且响应正文、数量和错误不得泄露其私有内容

#### Scenario: 批量复核保持命中次序
- **WHEN** 一个候选批次包含多篇仍公开文章且 PostgreSQL 以不同顺序返回它们
- **THEN** 系统 SHALL 按当前计划的 BM25 或冻结 RRF 全序返回文章，不得改为数据库行顺序或 latest 顺序

#### Scenario: 候选批次含不可见间隙
- **WHEN** 当前页中部分候选被 PostgreSQL 过滤但后面仍有候选
- **THEN** 系统 SHALL 在有界扫描范围内在 BM25/PIT 模式继续 search-after、在混合模式继续固定列表补充结果；若因扫描上限提前结束则游标 SHALL 越过已检查候选并允许后续请求继续，不得在正常翻页中重复已返回文章

#### Scenario: PostgreSQL 查询数量有界
- **WHEN** 一页需要复核 N 个 OpenSearch 命中
- **THEN** 系统 SHALL 按候选批次执行 PostgreSQL 查询，查询次数只随批次数增长而不得随 N 逐项增长

#### Scenario: 文章在复核时产生新公开修订
- **WHEN** 纯 BM25 贡献命中旧投影但 PostgreSQL 中该文章仍为 `published` 且已有新当前修订
- **THEN** 响应 SHALL 使用 PostgreSQL 的当前公开 ArticleItem；索引最终一致性不得让旧修订内容进入响应

#### Scenario: 旧修订向量尚未从索引移除
- **WHEN** 索引 KNN 命中旧修订而 PostgreSQL 当前修订已经变化
- **THEN** 系统 SHALL 在融合前移除该 KNN 贡献；仅语义命中的文章 SHALL 不返回，另有 BM25 命中的文章 SHALL 仅保留 BM25 排名贡献

#### Scenario: 同修订向量选择变化
- **WHEN** KNN 命中修订未变但 generation、Embedding result 或 profile 已不是当前选择
- **THEN** 系统 SHALL 同样移除该 KNN 贡献，不得使用同维度但不同 profile 的向量

#### Scenario: 混合翻页期间向量身份变化
- **WHEN** 固定列表中曾使用有效 KNN 贡献的候选在后续页复核时身份失效
- **THEN** 系统 SHALL 丢弃该候选并推进消费位置，即使它原先也有 BM25 贡献也不得沿用陈旧语义名次；重新查询第一页 SHALL 可以按新事实召回

### Requirement: 搜索故障被隔离并返回明确不可用错误

系统 SHALL 在必需的 BM25 路径的 OpenSearch 未配置、连接失败、请求超时、读别名缺失或查询响应无法验证时，为搜索端点返回 `503 SEARCH_UNAVAILABLE` 和可重试提示，不得回退到 PostgreSQL 模糊搜索或 latest。OpenSearch 状态 SHALL 不参与核心 readiness，且搜索故障不得改变 `GET /api/v1/articles`、`GET /api/v1/articles/{id}`、文章写入或投影任务持久化的可用性。

独立的 Embedding 或 KNN 失败 SHALL 仅禁用该次查询的语义贡献；BM25 成功且 PostgreSQL 复核成功时 SHALL 返回正常 BM25 搜索页，不得因为模型故障把关键词搜索一起置为不可用。

#### Scenario: OpenSearch 未配置
- **WHEN** API 进程未配置 OpenSearch 端点
- **THEN** 搜索路由 SHALL 仍存在并返回 `503 SEARCH_UNAVAILABLE`，latest 与文章详情 SHALL 按现有 PostgreSQL 路径正常工作

#### Scenario: OpenSearch 查询期间故障
- **WHEN** 必需的 BM25 查询路径 OpenSearch 不可达、超时或返回无法安全关联的响应
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

系统 SHALL 额外记录查询 Embedding/KNN/RRF 分段耗时、两路召回量、去重后候选量、无效向量身份过滤量、实际 BM25/混合模式及固定低基数降级原因；不得记录查询向量或把查询、profile 自由文本作为指标标签。

#### Scenario: 可见性过滤发生
- **WHEN** 一次搜索从候选中移除当前不可见文章
- **THEN** 系统 SHALL 增加受控的过滤计数并可按 request ID 诊断，不得在日志中写出被过滤文章内容或用户完整查询

#### Scenario: 搜索依赖超时
- **WHEN** OpenSearch 或 PostgreSQL 复核超过其请求边界
- **THEN** 系统 SHALL 记录稳定的依赖与超时分类、分段耗时和失败计数，指标标签不得包含查询词、文章 ID、游标或原始错误文本


#### Scenario: 模型故障降级可诊断
- **WHEN** 查询 Embedding 失败而 BM25 正常返回
- **THEN** 请求 SHALL 计为成功，同时记录 Embedding 稳定错误分类和降级计数，不得将其统计为 BM25 故障

### Requirement: 查询语义召回具有独立且有界的资源预算

混合搜索 SHALL 默认为关闭；开启且配置可用时 SHALL 对规范化后的查询文本生成一次查询 Embedding，使用与文章向量匹配的 provider/model/profile 和维度，不调用内容生成模型、不持久化查询向量、不重试在线模型调用。每路召回 SHALL 默认且最多为 100 个候选，KNN 的近邻数量 SHALL 与其候选上限一致；单次搜索总预算 SHALL 默认为 5 秒，查询 Embedding 与 KNN 各 SHALL 默认最多 1 秒并受总 deadline 限制，超时或取消 SHALL 停止相应调用。空向量、非有限值、零范数或维度不一致 SHALL 被视为语义路径不可用。

#### Scenario: 两路分别验证召回
- **WHEN** 固定语料包含仅文本匹配文章及仅向量相近文章
- **THEN** BM25 独立召回 SHALL 命中文本文章，KNN 独立召回 SHALL 命中语义文章，最终混合结果 SHALL 能包含两者

#### Scenario: KNN 使用相同精确筛选
- **WHEN** 查询带关键词、主题或来源条件
- **THEN** 两路 SHALL 都执行 `visible=true` 及相同精确筛选；KNN SHALL 同时限制向量存在且 profile 匹配，不得通过语义相似绕过筛选

#### Scenario: 无向量文章
- **WHEN** 文章当前修订没有向量但文本匹配查询
- **THEN** 文章 SHALL 可以通过 BM25 进入混合候选并集，不得被全局向量存在条件排除

#### Scenario: 查询 Embedding 失败或没有有效向量
- **WHEN** Embedding 未配置、超时、限流、失败或输出无效，读索引不支持语义路径，或 KNN 返回零个有效候选
- **THEN** 在 BM25 和 PostgreSQL 正常时系统 SHALL 返回正常 BM25 排序页；没有有效查询向量时 SHALL 不调用 KNN

#### Scenario: KNN 单独失败
- **WHEN** KNN 超时、返回部分分片结果或响应非法，但 BM25 成功
- **THEN** 系统 SHALL 丢弃整路 KNN 结果，继续 BM25，并记录降级原因

#### Scenario: 非法参数或续页请求
- **WHEN** 查询校验失败或携带有效混合续页游标
- **THEN** 系统 SHALL 不调用查询 Embedding；非法请求 SHALL 保留原参数错误语义

### Requirement: 应用层 RRF 对候选并集产生确定性全序

系统 SHALL 对两路候选按文章 ID 去重，先移除不满足当前事实的 KNN 贡献，再按各路固定次序从 1 开始重新编号，计算 `score(d)=1/(60+rank_bm25(d))+1/(60+rank_knn(d))`，缺失一路贡献 SHALL 为零。最终 SHALL 按 RRF 分数倒序、快照有效发布时间倒序、文章 ID 倒序形成全序；不得依赖 map 遍历或并发完成顺序，不得用原始 BM25/KNN 分数跨路比较。只有一路有效时 SHALL 保持该路全序；BM25 是必需路径，失败不得返回 KNN 单路页。

#### Scenario: 融合重叠与独有候选
- **WHEN** 两路固定列表为 BM25 `[A,B]` 和 KNN `[B,C]`
- **THEN** B SHALL 获得两路贡献且只出现一次，A 和 C SHALL 各保留一路贡献，并按规定全序排序

#### Scenario: 相同输入重复运行
- **WHEN** 候选、身份、时间和参数相同而 map 插入顺序或两路完成顺序不同
- **THEN** RRF SHALL 产生完全相同的文章 ID 次序，并使用时间与 ID 打破同分

#### Scenario: 候选数量达到边界
- **WHEN** 两路各返回 100 个互不重叠的候选
- **THEN** 去重列表 SHALL 不超过 200 篇，后续分页 SHALL 只消费该有界列表
