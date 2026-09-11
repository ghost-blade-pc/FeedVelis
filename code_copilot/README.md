# code_copilot — FeedVelis

本目录采用 `spec-copilot/workspace` v3，以严格阶段、内容寻址证据和事件链支持跨 Agent 交接。

## 最小导航

- 项目模式：`scaffolded`
- 应用/技术栈：`Velis Feed（API、Worker、迁移、管理 CLI 与 Web）` / `Go 1.26、CloudWeGo Hertz、PostgreSQL/pgx、Vue 3 + TypeScript`
- 构建与测试：`Make、Go toolchain、npm/Vite` / `Go testing、竞态检测、Vitest、架构依赖测试`
- 项目总览：[README](../README.md)；唯一目标与路线：[Velis Roadmap][roadmap]。本工作区管理执行与证据，不另立产品目标。
- 项目事实：`rules/project-context.md`
- 状态协议：`protocol/change-v3.json`
- 当前 change：`changes/<id>/card.md`
- 机器历史/人类证据：`events.jsonl` / `log.md`

任何阶段只读取 Runner 入口、当前阶段 reference、project-context、card 和当前阶段需要的文档。使用 Runner 的 `inspect_change.py` 生成最小交接视图，不创建易漂移的 `handoff.md`，也不默认加载完整历史日志。

## 阶段与权限

默认一次只执行 `propose / apply / test / review / fix / archive` 中的一个阶段；显式 `compact` 仅适用于符合条件的 lite change，并仍记录逐阶段事件。`ready` 表示提案完整可实施，显式 `/apply` 才构成实现授权。

Review 对应用代码严格只读；显式 `/review` 授权记录报告、verdict、card、log 和事件。状态转换必须使用 Runner 工具的 dry-run + CAS 写入，不手改 card/test/events/log 伪造历史。

不自动 commit、push、部署、采购或操作生产。机械校验不能证明业务、安全、Review 或外部测试语义真实。

## 项目检索词

- 根包/命名空间：`github.com/ghost-blade-pc/Velis_Feed/backend`
- 业务域：`account、source、article、feed、interaction、relation、exposure、recommendation、embedding`
- 入口：`backend/cmd/velis-api、backend/cmd/velis-worker、backend/cmd/velis-migrate、backend/cmd/velis-admin、web/src/main.ts`
- 依赖：`Hertz、pgx、golang-migrate、PostgreSQL、Vue、Pinia、Vue Router；中间件接入状态见项目 README`
- 风险：`认证授权、事务与 Outbox、幂等、并发、迁移、SSRF、隐私、缓存降级、消息重试、Embedding 降级`

[roadmap]: <../Velis Roadmap.md>
