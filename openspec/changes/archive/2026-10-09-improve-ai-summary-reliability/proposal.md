# Proposal

## Why

长文章的单个分块返回空摘要或超长摘要时，现有 Workflow 放弃整轮已成功分块；文章 366 已因此在三次任务尝试后失败。用户需要在模型反复输出非法内容时仍能看到可用、来源清楚的摘要，同时保留模型和基础设施故障的真实状态。

## What Changes

- 对空白或超长的分块摘要，在剩余调用、Token 和时间预算内仅重试失败分块，并使用明确的纠正提示。
- 生成阶段因 `invalid_output` 耗尽任务尝试后，以当前修订的有限原文摘录保存结果，继续既有 Embedding 流程；摘录明确标记为 `extractive`。
- 模型结果与摘录结果采用独立的不可变身份，同一输入以后仍可切换到模型结果。
- API 和 Web 展示结果来源；鉴权、网络、配置、数据库错误及无正文场景保持失败，不伪造成功。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `ai-content-enrichment`：增加分块非法输出的有限就地重试，以及生成最终失败后的可识别摘录降级。
- `unified-articles`：公开文章增强结果暴露来源类型，页面据此区分模型摘要与原文摘录。

## Impact

涉及 Eino Workflow、增强执行器、PostgreSQL 生成结果迁移与读取、OpenAPI、Vue 卡片和详情页。无需新增外部服务；旧生成结果迁移后标记为 `model`。
