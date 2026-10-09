# Spec Delta

## ADDED Requirements

### Requirement: 非法分块摘要在本轮有限纠正

系统 SHALL 在单个分块摘要为空或超过长度上限时，只针对失败分块执行至多一次有界纠正调用；此前已成功的分块 SHALL 保留。纠正仍须遵守任务调用数、Token 审计预算、总时限和同一严格摘要校验。

#### Scenario: 单个分块纠正成功
- **WHEN** 一个分块返回空或超长摘要，其他分块成功，且剩余预算允许再次调用
- **THEN** 系统 SHALL 使用明确的纠正提示仅重试该分块，保留原失败原因与两次调用审计，并继续汇总

#### Scenario: 纠正预算不足或仍然非法
- **WHEN** 剩余预算不足，或纠正输出再次违反摘要边界
- **THEN** 系统 SHALL 停止该分块的本轮调用，按 `invalid_output` 或预算错误结束本次尝试，不得发布不完整模型结果

### Requirement: 生成非法输出耗尽后可识别地摘录降级

系统 SHALL 仅在当前 generation 目标的 `invalid_output` 耗尽有限任务尝试后，从当前文章修订的规范化纯文本确定性提取有界摘要，并以 `extractive` 来源保存；模型成功结果 SHALL 标记为 `model`。摘录不得包含正文之外的新事实，并 SHALL 遵守现有摘要及标签边界。相同修订与 profile 的模型结果 SHALL 可在后续显式重算后独立保存并选为当前结果。

#### Scenario: 最终非法输出耗尽
- **WHEN** 当前修订有非空正文，generation 最后一次尝试仍因 `invalid_output` 失败
- **THEN** 系统 SHALL 保存来源为 `extractive` 的有界原文摘录，并按原有阶段规则继续 Embedding 或完成任务

#### Scenario: 仍有重试额度
- **WHEN** generation 因 `invalid_output` 失败但还有任务尝试额度
- **THEN** 系统 SHALL 保持有限退避重试，不得提前发布摘录

#### Scenario: 不能安全摘录
- **WHEN** 正文为空、生成 profile 未配置，或失败类别为鉴权、配置、传输、超时、预算与数据库错误
- **THEN** 系统 SHALL 保留真实失败或重试状态，不得生成摘录结果

#### Scenario: 后续模型重算成功
- **WHEN** 相同修订和 profile 已有摘录结果，管理员显式重算并获得通过严格校验的模型结果
- **THEN** 两种结果 SHALL 具有独立不可变身份，当前选择 SHALL 切换到来源为 `model` 的结果
