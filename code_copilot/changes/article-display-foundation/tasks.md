# 任务拆分 — 文章展示基础能力与库表设计

## 前置条件

- [x] D1–D5、A1–A4 和 E1–E11 已确认
- [x] Source 写入口、schema、抓取安全和回滚已锁定
- [x] 用例图、业务流程图、ER 图、流程时序图和后端四层实现架构图已逐张确认，`card.md` 恢复为 `ready`
- [x] Git touched files 与 Proposal 验证基线已记录

## Task 1 — 锁定契约、库表与设计图

- 状态：`completed`
- 目标：确认 D1–D5，冻结 Source/Article/Content 字段、API、信任边界及后端四层实现落点，并在 Apply 前逐张确认五类设计图。
- 验收映射：全部功能的编码前门禁。
- 涉及文件：本 change 文档、`diagrams.md`、OpenAPI 候选契约。
- 验证：`inspect_change.py` 与 workspace 机械校验。

### 设计图进度

- [x] 用例图：开发者已确认
- [x] 业务流程图：开发者已确认
- [x] ER 图：开发者已确认
- [x] 流程时序图：开发者已确认
- [x] 后端四层实现架构图：开发者已确认图、分层改动清单及包级颗粒度

## Task 2 — Source/Article Domain 与迁移

- 状态：`pending`
- 目标：实现外部 Article 身份/更新规则、Source 抓取状态，并创建三张表及索引。
- 验收映射：F1、F3、F4。
- 涉及包：`domain/source`、`domain/article`、`persistence/postgres`、`backend/migrations` 与 PostgreSQL tests；具体文件在 Apply 前按实现顺序细化。
- 风险：错误约束、数据丢失、未来 internal 扩展困难。
- 验证：Domain tests、真实 PostgreSQL up/down/up、约束和 `EXPLAIN`。

## Task 3 — 安全 Fetcher 与 Feed Parser

- 状态：`pending`
- 目标：实现条件请求、SSRF 防护、资源限制以及 RSS/Atom 解析/正文清理。
- 验收映射：F2。
- 涉及包：`infrastructure/fetcher/httpfeed`、`infrastructure/clock`、Application ports 与依赖清单；具体文件在 Apply 前按实现顺序细化。
- 风险：DNS rebinding、重定向绕过、压缩炸弹、恶意 XML/HTML。
- 验证：本地恶意 HTTP server、表驱动 parser、安全 corpus、取消/超时测试。

## Task 4 — 单 Source 抓取与幂等入库

- 状态：`pending`
- 目标：串起 Fetch → Parse → Normalize → Hash → Transactional Upsert。
- 验收映射：F2、F3。
- 涉及包：`application/source`、`application/article`、Domain repositories 与 PostgreSQL adapters；具体文件在 Apply 前按实现顺序细化。
- 风险：重复文章、无变化仍写、半事务状态。
- 验证：首次/重复/内容变化/304/并发集成测试。

## Task 5 — Worker 调度、租约与退避

- 状态：`pending`
- 目标：认领到期 Source，安全恢复过期租约，失败退避且不阻塞其他来源。
- 验收映射：F1、F2。
- 涉及包：`interfaces/scheduler`、`internal/bootstrap`、`application/source` 与 Source repository；具体文件在 Apply 前按实现顺序细化。
- 风险：重复抓取、饥饿、goroutine 泄漏、停机丢租约。
- 验证：并发/取消/race/优雅关闭测试。

## Task 6 — Source 管理入口与文章列表 API

- 状态：`pending`
- 目标：按 D1 提供 Source 添加入口，并提供稳定游标文章列表。
- 验收映射：F1、F4。
- 涉及包：`interfaces/cli`、`interfaces/http/hertz`、`internal/bootstrap`、`backend/cmd` 与 OpenAPI；具体文件在 Apply 前按实现顺序细化。
- 风险：无认证写入口、游标漂移、N+1。
- 验证：Hertz/CLI contract、错误映射、分页、批量 Source 查询。

## Task 7 — Web 最新文章列表

- 状态：`pending`
- 目标：将 `/latest` 占位页替换为真实文本卡片和安全外链。
- 验收映射：F5。
- 涉及文件：`web/src/api`、`types`、`features/article`、`views`、router、tests。
- 风险：上游 HTML/XSS、危险 URL、新旧请求竞态。
- 验证：Vitest、typecheck/build；具备条件时做 Playwright 最小闭环。

## Task 8 — 全链路与回归验证

- 状态：`pending`
- 目标：使用受控真实 Feed 完成 Source → Worker → PostgreSQL → API → Web，并执行完整回归。
- 验收映射：F1–F5。
- 验证：`test-spec.md`、`make check`、vet、Compose、迁移回滚、查询计划。

## 汇总

- 已完成：仓库研究、范围与契约确认、库表设计、五张设计图及后端四层分层改动清单。
- Deferred：用户、subscriptions、内部发布、详情、互动、搜索、推荐、Embedding、Redis、MQ、图片。
- 实际文件：仅 change 文档；未修改应用代码。
- 建议状态：五张设计图与分层改动清单均已确认，Proposal 可进入 `ready`；实现需另行显式 `/apply`。
