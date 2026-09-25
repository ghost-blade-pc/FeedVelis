# article-search-projection Specification

## Purpose

定义从 PostgreSQL 当前文章、AI 增强与 Embedding 事实生成可丢弃、可验证、可在线重建的 OpenSearch 文章投影，并保证故障、重复、乱序和迟到执行不会泄露不可见内容或恢复旧版本。

## Requirements

### Requirement: 搜索文档是 PostgreSQL 当前事实的版本化投影

系统 SHALL 使用文章稳定 ID 作为搜索文档 ID，并只从 PostgreSQL 中该文章的当前状态、当前修订以及属于该修订的当前 generation 和 Embedding 选择构造文档；投影 SHALL 记录足以比较新旧目标的文章 `lock_version`、revision ID、generation result ID、embedding result ID 和索引 schema version，OpenSearch 不得反向修改业务事实。

#### Scenario: 当前公开修订具有完整 AI 结果
- **WHEN** 一篇 `published` 文章的当前修订同时选中了 generation 和匹配该 generation 的 Embedding
- **THEN** 投影 SHALL 包含文章公开检索字段、摘要、关键词、主题、固定维度向量及其版本身份

#### Scenario: 当前公开修订尚无 AI 结果
- **WHEN** 一篇 `published` 文章的当前修订没有 generation 或 Embedding 当前选择
- **THEN** 系统 SHALL 仍建立不含相应可选字段的文本投影，不得沿用旧修订的增强结果或向量

#### Scenario: AI 当前选择不一致
- **WHEN** 当前 Embedding 不属于当前 revision 或不依赖当前 generation 选择
- **THEN** 投影读取 SHALL 忽略该向量并记录可观测的不一致，不得将其写入搜索文档

### Requirement: 索引 schema 和别名具有显式版本

系统 SHALL 使用不可变 schema version 标识物理索引和映射，使用稳定读别名与写别名隔离调用方，并为中文及混合中英文全文字段配置可重复验证的分析器；向量字段维度 SHALL 与启用的 Embedding profile 一致，任何映射不兼容变更必须创建新物理索引而不得原地修改既有语义。

#### Scenario: 初始化空索引
- **WHEN** 管理员首次初始化当前 schema version
- **THEN** 系统 SHALL 创建匹配模板的版本化物理索引，并以原子别名操作建立唯一读索引和唯一写索引

#### Scenario: 重复初始化相同版本
- **WHEN** 当前物理索引和别名已经满足目标 schema version
- **THEN** 初始化 SHALL 返回幂等成功，不得创建重复索引或多写索引

#### Scenario: 中文与混合文本分析
- **WHEN** 固定分析样例包含连续中文、ASCII 术语和标点
- **THEN** 索引分析结果 SHALL 按声明的 schema version 产生确定性 token，且升级分析规则必须递增 schema version

#### Scenario: 向量维度不兼容
- **WHEN** 当前 Embedding profile 维度与目标索引映射不一致
- **THEN** 系统 SHALL 拒绝启动投影写入并暴露配置错误，不得截断、填充或静默丢弃向量

### Requirement: 每篇文章使用持久化收敛槽位

系统 SHALL 为每篇发生过公开投影变化的文章保存唯一 `search_projection_jobs` 槽位；每次目标变化 SHALL 单调增加 generation、替换完整目标身份并将任务推进为待处理，重复推进同一目标 SHALL 为 noop。槽位只表达“收敛到当前事实”，不得为每次中间变化无界追加任务。

#### Scenario: 首次公开文章
- **WHEN** 一篇文章首次产生可公开搜索投影
- **THEN** 系统 SHALL 创建 generation 为 1 的待处理 upsert 目标

#### Scenario: 多次变化在执行前合并
- **WHEN** 同一文章在 Worker 执行前连续发生修订和 AI 当前选择切换
- **THEN** 槽位 SHALL 收敛为最后一次当前事实的单一目标，并以更高 generation 使早先执行失效

#### Scenario: 相同目标重复推进
- **WHEN** 重复事务或恢复流程再次提交与槽位完全相同的目标身份
- **THEN** 槽位 generation 和已完成状态 SHALL 保持不变

#### Scenario: 文章失去公开可见性
- **WHEN** 文章从 `published` 变为 `offline` 或 `deleted`
- **THEN** 槽位 SHALL 收敛为不可检索的 tombstone 目标，即使当前 OpenSearch 中尚不存在该文档

### Requirement: 投影 Worker 以租约和 fencing 收敛当前事实

投影 Worker SHALL 以有界批次认领到期任务，使用租约所有者、租约 token 和 generation 保护完成、失败及重试写回；执行外部请求期间不得持有 PostgreSQL 行锁。发送 OpenSearch 操作前 SHALL 重新读取并验证目标对应的当前 PostgreSQL 事实，旧 generation 或过期租约的执行结果不得覆盖新任务状态。

#### Scenario: Worker 在请求前发现目标已过期
- **WHEN** 已认领任务对应的文章 revision、可见状态或 AI 当前选择已改变
- **THEN** Worker SHALL 不发送旧目标，重新推进槽位到当前事实并结束该次认领

#### Scenario: 租约期间进程退出
- **WHEN** Worker 认领任务后在完成写回前退出
- **THEN** 租约到期后另一 Worker SHALL 能重新认领同一 generation 并安全重试

#### Scenario: 旧执行者迟到完成
- **WHEN** 旧 Worker 的租约已被替代或任务 generation 已增加后才返回成功
- **THEN** 旧 Worker SHALL 无法把当前槽位标记为成功或覆盖新目标的诊断状态

#### Scenario: OpenSearch 未配置
- **WHEN** 未配置 OpenSearch 连接
- **THEN** 投影 Worker SHALL 保持禁用，文章发布、RSS 抓取、latest、详情和 AI 增强 SHALL 正常工作，持久化槽位 SHALL 保留待处理目标

### Requirement: Bulk 操作逐项判定并有限重试

系统 SHALL 检查每个 Bulk item 的真实结果，将成功、可重试失败、永久失败和陈旧冲突分别处理；一次 HTTP 成功不得被视为全部 item 成功。退避、最大尝试次数、单批文档数和请求体大小 SHALL 有配置上限，错误诊断 SHALL 限长并脱敏。

#### Scenario: Bulk 部分成功
- **WHEN** 一个 Bulk 响应中部分 item 成功而部分 item 返回可重试错误
- **THEN** 系统 SHALL 只完成成功目标，只为失败目标安排有上限退避重试，不得重发已成功项作为同批必要条件

#### Scenario: 永久映射错误
- **WHEN** 某 item 因映射或非法文档返回不可重试错误
- **THEN** 对应任务 SHALL 进入可诊断失败状态且其他 item 继续完成，不得无限重试整个批次

#### Scenario: 请求结果未知
- **WHEN** 连接在 OpenSearch 处理请求期间中断而客户端无法判断结果
- **THEN** 系统 SHALL 保留任务并允许幂等重试，最终以文章 ID 覆盖同一可见文档或 tombstone

#### Scenario: 重复写入 tombstone
- **WHEN** tombstone 目标已经以相同或更高投影版本存在
- **THEN** 系统 SHALL 将该目标视为已收敛成功

### Requirement: 迟到写不得恢复旧文档或不可见文章

系统 SHALL 使用单调投影版本执行有条件 upsert/tombstone，并在每次执行前以 PostgreSQL 当前事实校验可见性；低于搜索文档已观察版本的写入 SHALL 成为 noop。下架或删除事实的版本墓碑 SHALL 在正常增量同步和重建窗口内阻止旧 upsert 复活文档。

#### Scenario: 下架后迟到 upsert
- **WHEN** 下架 tombstone 已应用后，旧 revision 的 upsert 才到达 OpenSearch
- **THEN** 旧 upsert SHALL 被版本条件拒绝或成为 noop，文档不得重新可检索

#### Scenario: 修订事件乱序
- **WHEN** 新 revision 已写入后旧 revision 的任务迟到执行
- **THEN** OpenSearch SHALL 保持新 revision 投影，旧任务不得覆盖它

#### Scenario: 文章重新发布
- **WHEN** 下架文章以更高 `lock_version` 重新发布
- **THEN** 新 upsert SHALL 能越过旧墓碑恢复当前 revision，首次发布时间语义保持不变

### Requirement: 索引可在线重建并追赶增量

系统 SHALL 提供需要显式范围和目标 schema version 的管理命令，在不覆盖当前读索引的前提下创建新物理索引、按稳定 `(article_id)` 水位有界读取 PostgreSQL 当前事实、写入快照、追赶重建期间发生的增量、完成一致性校验并以单次原子操作切换读写别名。重建状态 SHALL 持久化并支持安全恢复，任一时刻同一逻辑索引只允许一个活动重建。

#### Scenario: 重建期间文章发生变化
- **WHEN** 快照扫描过某文章后，该文章又修订、下架或切换 AI 当前选择
- **THEN** 增量追赶 SHALL 在别名切换前把新索引收敛到该文章的切换前当前事实

#### Scenario: 重建进程中断
- **WHEN** 重建在快照或增量阶段退出
- **THEN** 管理员 SHALL 能从持久化水位继续同一目标索引，或显式放弃该未发布索引，不得影响当前读写别名

#### Scenario: 校验失败
- **WHEN** 新索引的可见文档数、抽样身份或待追赶任务水位不满足切换条件
- **THEN** 系统 SHALL 拒绝别名切换并保留当前索引服务

#### Scenario: 成功切换并回滚
- **WHEN** 新索引通过校验并完成别名切换
- **THEN** 系统 SHALL 记录前一索引与切换时间，在有限回滚窗口内支持原子切回，且不得自动删除前一索引

#### Scenario: 回滚窗口结束后的清理
- **WHEN** 管理员显式清理已超过回滚窗口且不被任何别名引用的旧索引
- **THEN** 系统 SHALL 仅删除精确匹配的旧物理索引，并拒绝删除当前读索引、当前写索引或未知索引

### Requirement: 投影运行状态可观测且不暴露敏感正文

系统 SHALL 暴露任务待处理数量和最老年龄、成功与失败计数、索引延迟、Bulk item 分类、重试、陈旧执行、重建阶段和别名版本；结构化日志 SHALL 使用 article ID、task ID、generation、index version 和可用 trace ID 关联，但不得记录正文、HTML、完整向量、模型密钥或 OpenSearch 凭据。

#### Scenario: 投影持续失败
- **WHEN** OpenSearch 不可用或任务连续失败
- **THEN** 指标和日志 SHALL 区分连接、超时、限流、映射、版本冲突和内部错误，并显示积压及最老任务年龄

#### Scenario: 记录失败诊断
- **WHEN** OpenSearch 返回包含请求细节的错误
- **THEN** 持久化与日志中的错误摘要 SHALL 限长、移除凭据及正文，不得保存完整 Bulk 请求
