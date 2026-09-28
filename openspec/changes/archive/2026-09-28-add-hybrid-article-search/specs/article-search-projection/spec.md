# 文章向量投影规格变更

## MODIFIED Requirements

### Requirement: 搜索文档是 PostgreSQL 当前事实的版本化投影

系统 SHALL 使用文章稳定 ID 作为搜索文档 ID，并只从 PostgreSQL 中该文章的当前状态、当前修订以及属于该修订的当前 generation 和 Embedding 选择构造文档；投影 SHALL 记录足以比较新旧目标的文章 `lock_version`、revision ID、generation result ID、embedding result ID、Embedding profile version 和索引 schema version，OpenSearch 不得反向修改业务事实。

#### Scenario: 当前公开修订具有完整 AI 结果
- **WHEN** 一篇 `published` 文章的当前修订同时选中了 generation 和匹配该 generation 的 Embedding
- **THEN** 投影 SHALL 包含文章公开检索字段、摘要、关键词、主题、固定维度向量及其版本身份；schema v2 SHALL 包含来自该 Embedding 事实的可精确过滤 profile 标识

#### Scenario: 当前公开修订尚无 AI 结果
- **WHEN** 一篇 `published` 文章的当前修订没有 generation 或 Embedding 当前选择
- **THEN** 系统 SHALL 仍建立不含相应可选字段的文本投影，不得沿用旧修订的增强结果或向量

#### Scenario: AI 当前选择不一致
- **WHEN** 当前 Embedding 不属于当前 revision 或不依赖当前 generation 选择
- **THEN** 投影读取 SHALL 忽略该向量并记录可观测的不一致，不得将其写入搜索文档

#### Scenario: 同维度 profile 升级
- **WHEN** 同一维度的 Embedding profile 升级而部分文章仍保存旧 profile 向量
- **THEN** schema v2 投影 SHALL 忠实保存每篇文章实际向量的 profile，不得用部署配置把旧向量标为新 profile；KNN SHALL 只召回与查询 profile 匹配的向量，旧文章仍可通过 BM25 命中

#### Scenario: 从 v1 升级与回滚
- **WHEN** 部署需要新增 profile 字段或回滚至不具备该字段的 v1 读索引
- **THEN** 系统 SHALL 使用新 schema 在线重建而不原地修改 v1；回滚到 v1 时 SHALL 退化到 BM25，继续允许文本搜索

