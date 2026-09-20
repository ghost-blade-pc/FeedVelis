---
schema: spec-copilot/change-v3
change_id: i1-status-documentation-sync
profile: lite
status: ready
fix_cycle: 0
failure_source: null
last_event_id: E000003
blocked_from: null
resume_to: null
review_verdict: pending
review_basis: null
created: 2026-09-20
updated: 2026-09-20
---


# Change Card — 同步 I1 完成状态与下一阶段文档

本文件只保存当前状态；转换历史读取 `events.jsonl`，因果证据读取 `log.md`。

## 一句话目标

依据已归档的 `account-access-foundation` 证据，同步 Roadmap、README 和产品上下文中的 I1 完成状态、当前边界与 I2 下一阶段指向。

## 范围

- 包含：更新 `Velis Roadmap.md` 的当前基线、I1 阶段状态、下一阶段及已定稿事项；更新 `README.md` 的项目摘要；更新 `code_copilot/rules/product-context.md` 的下一项业务。
- 不包含：业务代码、测试、配置、迁移、历史 change 证据、I2 设计或实现；不把 RSS 基础能力标记为 I4 完成。
- 涉及模块/目录：仓库根文档与 `code_copilot/rules/`。

## 行为与验收

- 当前行为/问题：`account-access-foundation` 已于 2026-09-19 通过测试与 Review 并归档，README 功能表也已记录账户能力；但 Roadmap 与 product-context 仍称 I1 待实施、下一项业务为 I1，README 首段未概括账户闭环。
- 期望行为：当前功能、阶段状态与下一步在三个入口中一致；I1 链接到归档 change，I2 被明确为下一项业务。
- [ ] `Velis Roadmap.md` 不再声称账户未实现或 I1 待实施。
- [ ] Roadmap 保留 I1 已接受限制，不扩大为改密、找回、注销、设备会话或管理员 HTTP 能力。
- [ ] README 摘要包含已实现的账户与会话闭环，与其功能表一致。
- [ ] `product-context.md` 把 I2 列为下一项业务，且不把本文档更新当作 I2 apply 授权。
- [ ] 全仓 Markdown 检索不再出现与当前事实冲突的“I1 待实施/下一阶段”描述（历史 change 除外）。
- targeted validation：对目标文件运行 `rg` 矛盾词检索，人工对照 `account-access-foundation/card.md` 与 README 功能表，并检查文档 diff。

## 风险与阻塞

- 风险：低。主要风险是把已实现的子能力误写为整体 I4 完成，或改写历史 Roadmap 技术决策；通过限定文档范围与证据链接避免。
- 当前阻塞：无
- 解除条件：不适用

`lite` 可只保留以上精简契约；复杂行为、外部契约或高风险 change 必须使用 `spec.md`、`tasks.md` 与所需测试规格。
