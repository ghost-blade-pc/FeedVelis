# Spec Delta

## Purpose

为匿名与登录用户提供可分页的公开文章推荐入口，以现有搜索候选和本人反馈形成简单、可解释的发现顺序，并在候选或搜索依赖不足时保持 Feed 可读。

## ADDED Requirements

### Requirement: 推荐 Feed 提供清晰且隔离的公开读取契约

系统 SHALL 提供 `GET /api/v1/articles/recommend`，`limit` 默认为 20 且只接受 1 至 50，支持可选不透明 `cursor`。成功响应 SHALL 包含 `items`、可空 `next_cursor`、`has_more`、`mode`（`personalized`、`cold_start` 或 `latest_fallback`）及 `degraded` 布尔值；每个 item SHALL 复用公开 `ArticleItem` 并增加单个最小 `recommendation_reason` 代码。端点 SHALL 匿名可访问；有效登录身份 SHALL 仅使用本人的反馈，失效或非法身份凭证 SHALL 按既有认证契约拒绝，不得静默使用匿名身份。`auth.enabled=false` 时 SHALL 保持匿名推荐入口可用。

#### Scenario: 匿名读取
- **WHEN** 未携带身份的用户请求合法推荐页
- **THEN** 系统 SHALL 返回公开文章页，且不读取任何用户画像或创建阅读事实

#### Scenario: 已登录用户读取
- **WHEN** 用户 A 与 B 具有不同反馈并分别请求推荐
- **THEN** 排序与排除 SHALL 仅使用各自画像，响应与游标不得混用身份

#### Scenario: 非法参数与凭证
- **WHEN** `limit` 越界、参数重复或游标非法，或请求携带失效凭证
- **THEN** 系统 SHALL 返回相应 `400 VALIDATION_FAILED`、`400 INVALID_CURSOR` 或既有认证错误，不得把凭证错误降为匿名请求

### Requirement: 无信号用户获得可预测的冷启动结果

匿名用户及没有有效正向偏好证据的登录用户 SHALL 采用 PostgreSQL 公开 latest 的 `(effective_published_at,article_id)` 倒序作为冷启动基础；登录用户仍 SHALL 排除本人当前有效的文章级“不感兴趣”。冷启动 SHALL 不要求 AI 结果、OpenSearch、查询模型或 Redis 可用。

#### Scenario: 无历史用户
- **WHEN** 用户没有阅读或收藏，且公开文章池不变
- **THEN** 相同首查 SHALL 得到相同文章 ID 顺序，`mode=cold_start` 且 `degraded=false`

#### Scenario: 只有负反馈
- **WHEN** 登录用户仅把一篇文章标记为“不感兴趣”
- **THEN** 冷启动顺序 SHALL 与 latest 一致但跳过该文章，且不得排除同主题或同来源的其它文章

### Requirement: 个性化候选与排序使用有界信号和版本化规则

有正向画像时，系统 SHALL 从当前公开投影按有限的关键词、主题及可用的语义路径召回候选，以固定 RRF 常数 60 融合有效召回顺序；语义路径不可用时 SHALL 保留关键词/主题候选。最终排序 SHALL 采用显式版本的固定权重规则，结合 RRF、主题/关键词偏好、新鲜度、关键词/主题/来源负向证据，并对连续同一 RSS Source 施加有界打散惩罚。收藏与去重后的阅读 SHALL 通过画像影响排序；有效文章级负反馈 SHALL 无条件排除该文章。所有同分情况 SHALL 以有效发布时间倒序和文章 ID 倒序打破，不得依赖并发完成顺序。

#### Scenario: 正向信号改变顺序
- **WHEN** 两篇公开候选具有可比较的召回基础，登录用户收藏或阅读了与其中一篇共享关键词或主题的文章
- **THEN** 对应证据 SHALL 能提高该候选排序，重复阅读不得无限放大影响

#### Scenario: 负反馈与来源打散
- **WHEN** 用户标记候选 A 为“不感兴趣”，且高分候选 B、C 来自同一 RSS Source，另一来源也有可比较候选
- **THEN** A SHALL 不出现，B、C SHALL 受固定连续来源惩罚；同来源其它文章 SHALL 仍有推荐资格

#### Scenario: BM25 或 KNN 候选为空
- **WHEN** 某一路或两路召回返回零个有效候选
- **THEN** 请求 SHALL 正常完成，并由剩余候选或 latest 补足，不得因空候选返回错误

### Requirement: 推荐游标冻结顺序并绑定身份和排序版本

推荐 SHALL 使用与搜索游标相互隔离的版本化不透明游标。首查 SHALL 冻结有界候选的去重顺序、实际模式、排序版本、身份绑定、消费位置、latest 补位位置及绝对过期时间；有效续页 SHALL 不重新召回、生成向量、读取新偏好来重排尚未消费的候选。游标 SHALL 在身份切换、版本未知、篡改、过期或参数不匹配时返回 `400 INVALID_CURSOR`。分页 SHALL 不重复已返回文章；新文章与新反馈可影响新首查，但不得改变已有游标的冻结排序。每页仍 SHALL 检查当前可见性及有效文章级负反馈。

#### Scenario: 正常翻页与偏好变化
- **WHEN** 第一页后用户增加收藏或索引切换，随后用有效游标加载下一页
- **THEN** 后续页 SHALL 延续首查顺序且不重复已返回文章，新收藏与新索引只影响新首查

#### Scenario: 身份或版本不匹配
- **WHEN** 匿名游标由登录用户使用、用户 A 的游标由 B 使用，或游标的排序版本已不受支持
- **THEN** 系统 SHALL 返回 `400 INVALID_CURSOR`，不得跨身份复用冻结结果

#### Scenario: 翻页时文章下架或新增负反馈
- **WHEN** 冻结候选在续页前下架，或当前用户随后将其标为“不感兴趣”
- **THEN** 系统 SHALL 跳过该文章并推进位置，继续有界补页，不得返回旧公开内容或重复项

### Requirement: 候选不足与搜索故障可降级到 latest

个性化候选不足时系统 SHALL 按 latest 顺序补位，排除已纳入推荐计划或已返回的文章；OpenSearch 未配置、连接失败、超时、读别名缺失或响应非法时 SHALL 返回可分页的 latest 内容，`mode=latest_fallback` 且 `degraded=true`，而不得使 Feed 整体失败。只有部分候选需要补位时 SHALL 保留 `mode=personalized` 并设置 `degraded=true`。语义路径单独失败但关键词/主题候选可用时 SHALL 仅降级语义贡献。PostgreSQL 当前公开读取或本人排除集读取失败 SHALL 返回依赖错误，不能返回未经复核的索引内容，也不能忽略负反馈。

#### Scenario: OpenSearch 整体不可用
- **WHEN** OpenSearch 未配置或首查不可用且 PostgreSQL 正常
- **THEN** 推荐 SHALL 返回公开 latest 顺序与明确降级状态，并允许使用推荐游标继续加载

#### Scenario: 候选数量小于页面需求
- **WHEN** 有效 BM25/KNN 候选少于请求页且 latest 还有文章
- **THEN** 系统 SHALL 用未重复的 latest 文章补页，候选耗尽不导致空白页或错误

#### Scenario: PostgreSQL 读取失败
- **WHEN** 当前公开事实复核或登录用户有效负反馈读取失败
- **THEN** 系统 SHALL 返回依赖不可用错误，不得直接返回 OpenSearch 投影或把错误解释为无信号

### Requirement: 推荐原因最小且仅解释实际结果

`recommendation_reason` SHALL 只取固定集合 `keyword_match`、`topic_match`、`similar_content`、`recent`、`latest_fallback` 中一个代码，并与该文章实际排序或降级路径一致；不得返回用户画像权重、查询文本、向量、内部得分或其它用户反馈。冷启动文章 SHALL 使用 `recent`，latest 补位与搜索故障降级文章 SHALL 使用 `latest_fallback`。

#### Scenario: 匹配主题与回退内容
- **WHEN** 一篇文章因用户主题偏好进入个性化结果，另一篇由 latest 补位
- **THEN** 两篇文章 SHALL 分别获得与实际依据一致的最小原因，不得把补位文章标成语义匹配

### Requirement: Web 支持 Feed 切换和降级提示

Web SHALL 在文章列表提供 latest/recommend 切换，默认维持现有 latest；切换或身份变化 SHALL 取消旧请求并重置当前列表及游标。推荐列表 SHALL 复用文章卡片、反馈操作和站内详情链接，展示最小推荐原因；`degraded=true` 时 SHALL 明确提示当前结果使用降级路径，并区分空内容与请求失败。游标失效时 SHALL 允许从推荐第一页重试。

#### Scenario: 切换 Feed 和账户
- **WHEN** 用户在 recommend 翻页后切至 latest，或退出并以另一账户登录
- **THEN** Web SHALL 清除旧结果和游标，不得把前一模式或前一身份的推荐继续拼接

#### Scenario: 降级与空内容
- **WHEN** recommend 成功但返回 `degraded=true`，或文章池确实为空
- **THEN** Web SHALL 分别显示降级提示或空内容状态，且 latest 与正文入口仍可使用

### Requirement: 推荐故障和分布可诊断

系统 SHALL 记录固定低基数的推荐模式、召回量、过滤量、latest 补位量、来源分布、降级原因与请求耗时；日志 SHALL 可用 request ID 关联，但不得记录用户画像、查询词、游标、向量、文章正文或原始搜索响应。

#### Scenario: 搜索故障转 latest
- **WHEN** 推荐因 OpenSearch 故障降级
- **THEN** 系统 SHALL 记录稳定错误分类与回退计数，响应仍为成功且指标标签不含用户或文章标识
