# Spec Delta

## ADDED Requirements

### Requirement: AI 当前选择切换可靠推进搜索投影

系统 SHALL 在当前 revision 的 generation 或 Embedding 结果成功选为当前结果时，于同一 PostgreSQL 事务推进该文章唯一的搜索投影目标；重复保存相同选择 SHALL 为 noop，事务任一步失败 SHALL 回滚 AI 当前选择与投影目标变化。该操作只持久化本地目标，不得在 AI 结果事务中同步调用 OpenSearch。

#### Scenario: generation 先于 Embedding 成功
- **WHEN** 当前 revision 的 generation 结果成功保存并切换，而 Embedding 尚未完成
- **THEN** 系统 SHALL 原子推进一个包含新 generation 身份且向量为空的投影目标，使文本增强可独立进入后续索引

#### Scenario: Embedding 随后成功
- **WHEN** 同一 revision 和 generation 的 Embedding 成功保存并切换
- **THEN** 系统 SHALL 原子推进更高 generation 的投影目标并包含新 Embedding 身份

#### Scenario: 迟到 AI 结果被 fencing 拒绝
- **WHEN** 旧 revision、旧任务 generation 或旧 profile 的迟到结果不能切换当前选择
- **THEN** 系统 SHALL 不推进搜索投影目标，既有当前投影目标保持不变

#### Scenario: 投影槽位持久化失败
- **WHEN** AI 结果或当前选择本可保存但搜索投影目标无法在同一事务持久化
- **THEN** 系统 SHALL 回滚该次结果发布，允许当前 AI 任务按既有重试与 fencing 语义安全恢复

