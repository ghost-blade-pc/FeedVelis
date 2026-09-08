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

- 状态：`completed`
- 目标：实现外部 Article 身份/更新规则、Source 抓取状态，并创建三张表及索引。
- 验收映射：F1、F3、F4。
- 实际文件：`domain/shared/url.go`、`domain/source/source.go`、`domain/article/article.go` 及单测；`migrations/000002_*`；`persistence/postgres/{source_repository,article_repository,transaction}.go`；`cmd/velis-migrate/main.go` 及单测。
- 风险：错误约束、数据丢失、未来 internal 扩展困难。
- Apply 验证：Domain tests 通过；隔离 PostgreSQL `up/down/up` 后 `version=2 dirty=false`；Repository 集成测试通过；100 Source/100,000 Article 的 `EXPLAIN ANALYZE` 使用 `articles_list_idx`。

## Task 3 — 安全 Fetcher 与 Feed Parser

- 状态：`completed`
- 目标：实现条件请求、SSRF 防护、资源限制以及 RSS/Atom 解析/正文清理。
- 验收映射：F2。
- 实际文件：`application/ports/feed.go`、`infrastructure/fetcher/httpfeed/{fetcher,parser,sanitizer}.go` 及测试、`infrastructure/clock/clock.go`、三种固定 Feed fixture、`go.mod`、`go.sum`、`Dockerfile`。
- 风险：DNS rebinding、重定向绕过、压缩炸弹、恶意 XML/HTML。
- Apply 验证：RSS 2.0/Atom/JSON Feed fixture、500 条上限、畸形输入、SSRF 地址、条件请求、响应大小、重定向和 HTML 安全 corpus 测试通过；`govulncheck` 最终无可达漏洞。

## Task 4 — 单 Source 抓取与幂等入库

- 状态：`completed`
- 目标：串起 Fetch → Parse → Normalize → Hash → Transactional Upsert。
- 验收映射：F2、F3。
- 实际文件：`application/article/service.go`、`application/source/service.go` 及单测，`application/ports/transaction.go`、PostgreSQL Repository/TxManager、`bootstrap/feed.go`。
- 风险：重复文章、无变化仍写、半事务状态。
- Apply 验证：首次插入、hash 未变不改 `updated_at`、内容变化保留 ID、304 不解析、并发幂等和 Article/Content 事务集成测试通过。

## Task 5 — Worker 调度、租约与退避

- 状态：`completed`
- 目标：认领到期 Source，安全恢复过期租约，失败退避且不阻塞其他来源。
- 验收映射：F1、F2。
- 实际文件：`interfaces/scheduler/scheduler.go`、`bootstrap/worker.go`、`application/source/service.go`、`domain/source/source.go`、`persistence/postgres/source_repository.go`。
- 风险：重复抓取、饥饿、goroutine 泄漏、停机丢租约。
- Apply 验证：并发 `SKIP LOCKED` 认领无重复、人工抓取租约互斥、过期租约恢复路径、退避边界和全包 race 通过；Worker 使用进程 context 收敛抓取。

## Task 6 — Source 管理入口与文章列表 API

- 状态：`completed`
- 目标：按 D1 提供 Source 添加入口，并提供稳定游标文章列表。
- 验收映射：F1、F4。
- 实际文件：`interfaces/cli/source.go`、`cmd/velis-admin/main.go`、`bootstrap/admin.go`；Hertz `handler/article.go`、`dto/article.go`、`router.go` 及测试；`api/openapi/velis.yaml`、`bootstrap/api.go`、`README.md`。
- 风险：无认证写入口、游标漂移、N+1。
- Apply 验证：CLI add/list/pause/resume/fetch 契约、重复 add、Hertz 正常/非法游标/非法 limit、UTC 时间和真实 API 两页游标烟测通过。

## Task 7 — Web 最新文章列表

- 状态：`completed`
- 目标：将 `/latest` 占位页替换为真实文本卡片和安全外链。
- 验收映射：F5。
- 实际文件：`web/src/api/client.ts` 及测试、`types/article.ts`、`features/article/{ArticleList.vue,model.ts,model.test.ts}`、`views/LatestView.vue`、router 与样式。
- 风险：上游 HTML/XSS、危险 URL、新旧请求竞态。
- Apply 验证：Vitest、`vue-tsc` 和 Vite production build 通过；当前仓库仍无 Playwright 运行配置，未执行浏览器 E2E。

## Task 8 — 全链路与回归验证

- 状态：`completed`
- 目标：使用受控真实 Feed 完成 Source → Worker → PostgreSQL → API → Web，并执行完整回归。
- 验收映射：F1–F5。
- Apply 验证：`make check`、`go vet ./...`、真实 PostgreSQL integration、迁移回滚、100,000 Article 查询计划、三种公网 Feed、API 运行态、OpenAPI YAML 解析、`govulncheck` 与三个后端 Compose 镜像构建均通过。本项是开发者级 Apply 检查，正式 Test 阶段仍为 `not-run`。

## 汇总

- 已完成：Task 1–8；系统级 Source → 安全抓取 → 幂等文章入库 → API/Web 列表闭环已按确认契约实现，并完成 Apply 层级验证。
- Deferred：用户、subscriptions、内部发布、详情、互动、搜索、推荐、Embedding、Redis、MQ、图片。
- 实际文件：后端四层实现、Admin/API/Worker 入口、迁移、OpenAPI、Web、fixture、测试、README 与依赖清单；精确事实以 Git diff 和 Apply 事件证据为准。
- 建议状态：Apply 可完成；下一阶段仅建议 `/test`。
