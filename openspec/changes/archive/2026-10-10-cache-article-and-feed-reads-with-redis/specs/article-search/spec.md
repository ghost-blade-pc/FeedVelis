# Spec Delta

## MODIFIED Requirements

### Requirement: 每批搜索命中回到 PostgreSQL 复核当前公开事实

系统 SHALL 把 OpenSearch 仅作为候选与排序来源，并在返回前按有界候选批次从 PostgreSQL 复核当前 `published` 状态、修订、来源、作者与当前增强结果选择；只有与该批当前事实匹配的版本化卡片数据才可从缓存装配，缺失、损坏或不匹配部分 SHALL 批量回源。系统 SHALL 按最终检索次序（纯 BM25 全序或混合 RRF 全序）重排 PostgreSQL 结果，丢弃不存在或当前不可见的文章；KNN 候选 SHALL 额外校验投影修订、generation、Embedding 选择和 profile 与当前事实一致。首次融合前 SHALL 移除无效 KNN 贡献，同篇文章若有 BM25 贡献 SHALL 保留其 BM25 资格。系统 SHALL 继续有界扫描候选直到收集请求页、候选耗尽或达到单请求扫描上限；不得逐命中执行 PostgreSQL 查询。

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

#### Scenario: Redis 故障时装配搜索结果
- **WHEN** OpenSearch 和 PostgreSQL 正常，但 Redis 不可用或卡片数据损坏
- **THEN** 系统 SHALL 批量回源装配当前公开结果并保持搜索排序，缓存故障不得导致 SEARCH_UNAVAILABLE 或返回未经事实复核的旧卡片

#### Scenario: 当前事实复核与卡片回填并发
- **WHEN** 文章编辑或增强选择变化发生于拆分后的事实读取与回源装配之间
- **THEN** 每批事实与卡片 SHALL 保持一致的读取视图，旧载荷不得被写入新版本键或冒充当前修订
