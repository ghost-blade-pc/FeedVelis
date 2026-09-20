---
schema: spec-copilot/change-v3
change_id: account-access-foundation
profile: high-risk
status: archived
fix_cycle: 4
failure_source: null
last_event_id: E000078
blocked_from: null
resume_to: null
review_verdict: passed
review_basis: sha256-v1:10d645390f315aa2238257230a37e43b76adc501d30a26de2a91dc582eed77dd
created: 2026-09-19
updated: 2026-09-19
---


# Change Card — I1 账户与权限基础

状态与事件仅由 Runner 生命周期工具维护。

## 一句话目标

建立注册、登录、会话恢复、本人资料与退出闭环，为后续投稿审核提供可信身份与权限基础。

## 范围与验收

- 包含：账户、会话轮换撤销、账号状态与角色字段、管理员 CLI、登录限流、管理审计、最小 Vue 闭环、迁移与验证；详见 spec.md 的 AC-01–AC-12。工程重点为后端，并为后续 Agent 提供可信身份基础。
- 不包含：投稿、Source HTTP 管理、外部身份服务、Feed 互动、复杂前端设计/管理后台、管理员 HTTP 接口，以及 AI/Agent 实现。
- 涉及 account 四层、bootstrap、Hertz/CLI、迁移、Vue；当前仅编写 change 文档。
- targeted validation：纯规则单测、HTTP/CLI、真实 PostgreSQL 并发与迁移、最小 Vue/Vitest 与人工浏览器冒烟记录。

## 风险与阻塞

- high-risk：权限、凭证、迁移、会话并发。
- 产品规则、会话、JWT/密钥轮换、Cookie/CSRF、端点校验矩阵、API、schema、CLI、审计、资源预算与回滚契约均已定稿（2026-09-19 修订）。
- 当前无 propose 阻塞项；实现风险集中在刷新并发、最后管理员并发保护、数据库迁移、反代拓扑下的来源 IP 判定和浏览器多标签协调。
- 已接受的限制：I1 不提供忘记密码自助或 CLI 改密路径，只能在授权、备份与留证前提下由运维例外处置；不引入 Prometheus 客户端与 `/metrics`，观测以低基数结构化日志字段承载；浏览器多标签行为以人工冒烟记录。
- 用户决策（2026-09-19，fix 轮次 4）：**无 Web Locks 时的跨标签刷新协调移出 I1**。原订的 localStorage 租约降级已移除（spec §4.3 与 AC-10 同步修订）；`localStorage` 没有 CAS，用它无法达成「同一时刻只有一个刷新者」这一不变量，该能力留待后续提案单独设计。检测不到 Web Locks 时各标签各自刷新，由服务端单次轮换与重放撤销兜底。
- 工作区配置变更（2026-09-19，用户授权）：`code_copilot/protocol/change-v3.json` 的 `loop_guard.max_completed_fix_cycles` 由 3 上调为 4，使第 4 轮 fix 可在协议内执行；这是对门禁阈值的显式上调，不是绕过。
- 只有用户显式授权 `/apply` 后才开始实现。
