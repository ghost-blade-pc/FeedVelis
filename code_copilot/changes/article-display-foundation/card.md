---
schema: spec-copilot/change-v3
change_id: article-display-foundation
profile: high-risk
status: testing
fix_cycle: 0
failure_source: null
last_event_id: E000015
blocked_from: null
resume_to: null
review_verdict: pending
review_basis: null
created: 2026-09-07
updated: 2026-09-07
---

# Change Card — 文章展示基础能力与库表设计

本文件只保存当前状态；转换历史读取 `events.jsonl`，因果证据读取 `log.md`。

## 一句话目标

> 建立系统级 RSS 2.0、Atom 与 JSON Feed 文章的最小闭环：通过本地管理 CLI 添加来源、受控抓取、幂等保存到 PostgreSQL、在 Web 按时间稳定展示，点击标题跳转原站。

## 范围

- 包含：系统级 Source、本地管理 CLI、RSS 2.0/Atom/JSON Feed 抓取与解析、外部 Article 入库/更新、文章列表 API、Web 列表、条件请求、基础调度与失败退避。
- 不包含：用户/订阅关系、内部 Markdown 发布、站内文章详情、点赞/评论、推荐、曝光、搜索、Embedding、Redis、RabbitMQ/Outbox、图片代理。
- 涉及模块/目录：`domain/source`、`domain/article`、`application/source`、`application/article`、`infrastructure/fetcher/httpfeed`、`infrastructure/persistence/postgres`、`interfaces/scheduler`、Hertz、迁移、OpenAPI 与 Web。

## 行为与验收

- 当前行为/问题：业务目录均为占位，数据库只有 `velis` schema，Web 只有健康占位页。
- 期望行为：系统能周期性抓取已登记的 RSS 2.0、Atom 或 JSON Feed 来源，重复抓取不制造重复文章，Web 只展示安全的文本卡片并跳转可信的 HTTP(S) 原文 URL。
- [ ] 同一规范化 Feed URL 只能创建一个 Source。
- [ ] 同一 Source 的同一文章重复抓取只更新原记录；内容无变化不写入。
- [ ] 文章按稳定复合游标分页，列表不读取正文大字段。
- [ ] HTTP 304 不解析文章；抓取具备 SSRF、超时、重定向与响应大小限制。
- [ ] Web 展示标题、来源、作者、摘要和时间，标题跳转原站；不提供站内详情。
- targeted validation：Domain/Application 单测、真实 PostgreSQL 集成测试、Fetcher/Parser 测试、Hertz 测试、Vitest、迁移 round-trip、race/vet/build。

## 风险与阻塞

- 风险：SSRF、恶意 Feed/HTML、重复或错误更新、调度并发、无认证 Source 写入口、数据库迁移与回滚、游标兼容性。
- 当前阻塞：无。用例图、业务流程图、ER 图、流程时序图和后端四层实现架构图及分层改动清单均已确认。
- 解除条件：已满足；Proposal 可进入 `ready`。实现仍需开发者另行显式 `/apply`。
