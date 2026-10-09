# Design

## Context

见 proposal.md。现有 generation 任务最多尝试三次，长文 Map 阶段只就地重试传输类瞬时错误；模型非法摘要会使本轮已成功的分块失效。最终结果、任务状态及搜索投影在 PostgreSQL 的 fenced 事务中切换。

## Goals / Non-Goals

**Goals:** 在既有调用与 Token 边界内提高非法输出的恢复率；耗尽尝试后提供可审计、可替换的原文摘录；公开结果来源。

**Non-Goals:** 不承诺模型或数据库服务故障时任务必然成功；不把非法模型原文保存为结果；不无限增加模型调用。

## Decisions

1. Map 非法摘要只针对 `summary_empty` 与 `summary_too_long` 做一次纠正提示调用。传输类错误仍使用原 Prompt 就地重试。保留成功分块，所有调用计入原预算。这样避免整轮重复付费，并维持严格输出校验。
2. 摘录由当前修订的规范化纯文本前段确定性生成，最多 400 个 Unicode 字符且不超过摘要配置上限；标题在标签配置上限内作为最低限度的关键词与主题。空正文不摘录。选择纯提取是为了避免在模型失败时新增未经原文支持的事实。
3. 仅在最后一次 `invalid_output` 后执行摘录，复用 `SaveGeneration` 的租约、修订、可见性 fencing 和搜索投影原子切换。调用审计仍记录原模型错误，任务完成日志以 `fallback` 标识。其他错误保持失败。
4. `ai_generation_results.generation_method` 取 `model|extractive`，现有行默认 `model`；目标唯一约束加入此字段，使以后显式重算能保存同一输入的真实模型结果。API `enhancement.method` 与 Web 徽标同步暴露来源。

## Risks / Trade-offs

- [摘录概括质量低] → 在 API 与页面标注 `extractive`，限制摘录长度，后续显式重算可升级为模型结果。
- [纠正调用增加模型费用] → 每个失败分块至多一次，仍受调用数、Token 和时限约束。
- [旧应用与新迁移版本不匹配] → 先部署迁移，再滚动更新 API、Worker 和 Web；旧行默认 `model`。

## Migration Plan

先备份 `ai_generation_results` 与当前选择，再应用 000011 前滚迁移并发布代码。回滚代码前须确认不存在 `extractive` 行；降级迁移显式拒绝删除含摘录结果的数据。已失败的历史任务由管理员按文章 ID 使用现有补录命令显式重新排队，不自动批量重算。
