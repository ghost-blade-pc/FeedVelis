# Spec Delta

## ADDED Requirements

### Requirement: 公开文章事实原子推进搜索投影目标

系统 SHALL 在文章首次发布、公开修订、恢复、下架或删除时，于提交当前文章事实的同一 PostgreSQL 事务推进该文章唯一的搜索投影目标；公开状态 SHALL 产生 upsert 目标，不公开状态 SHALL 产生不可检索的 tombstone 目标。该推进只写入本地可重建任务，不得使文章写路径同步连接 OpenSearch 或 RabbitMQ。

#### Scenario: 文章发布或公开修订
- **WHEN** RSS 或用户文章首次发布、恢复公开，或已发布文章切换到新当前修订
- **THEN** 系统 SHALL 原子保存文章事实与指向当前 revision 的 upsert 投影目标

#### Scenario: 文章下架或删除
- **WHEN** 公开文章被作者或管理员下架，或文章被软删除
- **THEN** 系统 SHALL 原子保存不可见状态与带更高文章版本的 tombstone 投影目标

#### Scenario: 草稿或离线内容编辑
- **WHEN** 草稿或离线文章的内容发生变化但仍不公开
- **THEN** 系统 SHALL 保持或推进 tombstone 目标，不得建立可公开的 upsert 目标

#### Scenario: 投影槽位写入失败
- **WHEN** 文章业务变化成功但对应搜索投影目标无法写入
- **THEN** 系统 SHALL 回滚文章、修订、资产绑定、Outbox 和 HTTP 幂等结果，并允许原命令安全重试

#### Scenario: OpenSearch 和 RabbitMQ 均不可用
- **WHEN** OpenSearch 与 RabbitMQ 未配置或不可连接
- **THEN** 文章事务 SHALL 仍只依赖 PostgreSQL 提交业务事实与本地投影目标，文章发布、latest 和详情保持可用
