# Review 报告 — article-display-foundation

## R1（Fix cycle 1 之后）

- Reviewer：当前上下文（未参与 Apply/Fix 实现；限制为与正式 Test 同一上下文，测试证据按层区分、不因同源升级）
- 审查对象：工作区（HEAD `9fe8a14` + Fix cycle 1 未提交修改，12 个后端文件）
- Review basis：内容寻址清单存于 `evidence/review/`（以 `sha256-v1:<hash>.json` 命名），结论形成时重算并通过 `verify valid=true`；因本报告内容参与清单，最终哈希以 `complete-r1.json` 的 `review_basis` 字段与 `card.md` frontmatter 为准（不自引用）
- Verdict：`passed`
- 审查边界：全程只读，未修改应用代码、测试或 Spec，未执行 formatter、生成器、自动修复、Git 或发布操作。

### R0-F1 至 R0-F4 修复核验（全部通过）

- R0-F1 租约 fencing：`Lease{Owner, ExpiresAt}` 由认领原样返回，MarkSuccess/MarkNotModified/MarkFailure 均以 `status IN ('active','degraded') AND lease_owner=$n AND lease_expires_at=$n AND lease_expires_at > checkedAt` 为条件，`RowsAffected()==0` 返回 `ErrLeaseLost`；pause 清租约后旧任务提交被拒。集成测试覆盖 stale 租约失败、paused 拒绝成功写入、过期租约 304 写入、同事务原子回滚（Article 零残留）。租约过期自愈：过期后新 Worker 可重新认领。
- R0-F2 URL 规范化：`removeDotSegments` 与 RFC 3986 §5.2.4 逐条对应；重复斜杠、尾斜杠、percent-encoding 保留；dot-segment 仅作用于路径、不触碰 host。单测覆盖尾斜杠、`//`、`/./`、`/../` 与 `%2e` 保留。
- R0-F3 hidden 保留：冲突 UPDATE 与 unchanged 分支均不再触碰 `status`；插入路径仍默认 published。集成测试验证 hidden 文章更新后状态不变。
- R0-F4 content_hash：`raw_description_truncated`/`raw_content_truncated` 已纳入可重算 payload，单测验证任一标志变化改变 hash。

### 本轮新增审查结论

- 嵌套事务正确性：`TxManager.WithinTransaction` 检测已存在事务时直接复用；fetch 成功路径以单事务包裹 Ingest + MarkSuccess，MarkSuccess fencing 失败时文章回滚（集成测试证实）。Ingest 对条目错误是 fail-fast 而非 continue，不存在 pgx 事务中止后继续执行的路径。
- 手动抓取语义：`FetchByID` 经 `ClaimByID`（仅 active/degraded 且租约空闲；paused 返回 `ErrInvalidStatus`），degraded 手动抓取成功后 MarkSuccess 恢复 active，符合 Spec。
- 无新 must-fix。SQL 全部参数化；事务上下文使用私有 key；fencing 为单语句条件更新，无 TOCTOU。

### Findings（本轮）

- Must-fix：无。
- Important：无新增。R0 遗留 I1、I3 继续 deferred；I2（Test 文档同步）已在正式 Test 阶段实质关闭（test-spec.md 已更新 passed 证据），剩余子项降级为 Suggestion。
- Suggestion：
  - `application/source` 编排层缺 fetch 成功路径与 ErrLeaseLost 传播的直接单测（当前由集成层间接覆盖），fake repository 已具备补测条件。
  - `url_test` 可补 `/a/b/..`→`/a/` 尾段回退与首段 `..` 用例。
  - R0 遗留：Web 加载更多 pending/序号防护；`RunWorker` 占位注释。

### 测试证据分层（R1）

- Mock/本地：targeted Go 单测全包、Vitest 5 项、vet/gofmt/diff --check（E000023）。
- 集成：真实 PostgreSQL 迁移 round-trip（version=2 dirty=false ×2）、`-race` 集成测试、约束/索引 `\d` 与 `EXPLAIN` 核对。
- 构建回归：`make check` 退出码 0。
- 真实环境/浏览器：未运行（公网 Feed 矩阵、Playwright E2E 仍为未覆盖）。

---

## R0（Apply 之后，9fe8a14）

- Reviewer：独立子 Agent `/root/independent_review`（未参与 Apply/Test）
- Proposal 基线：`f6d7ab73c1b4c236272d5b11fa9711b69816365c`
- 实现 HEAD：`9fe8a14f970d98deb33f35bb38a87784187816d6`
- Review basis：`sha256-v1:6122620cebad2a797403154e49e64de00946d8bd6183863fb6de1a36deffca23`
- Verdict：`changes-requested`
- 审查边界：全程只读，未修改应用代码、测试或 Spec，未执行 formatter、生成器、自动修复、Git 或发布操作。

## Must-fix

### R0-F1 — Source 完成写入缺少租约 fencing

- 触发条件：Source 抓取期间执行 pause；或租约过期后被新 Worker 认领，而旧抓取随后完成。
- 影响：旧任务可以无条件清空新租约并把 `paused` 改回 `active/degraded`，破坏单 Source 串行、人工 pause 和租约恢复语义。
- 证据：`backend/internal/infrastructure/persistence/postgres/source_repository.go:67-70` 认领只保存 owner/expiry；`:87-88` pause 清空租约；`:131-142` 完成写入只按 Source ID 更新，未校验 owner/token/status，也未检查 `RowsAffected`。对应契约见 `spec.md:148-152`。
- 修正要求：引入不可复用的 lease token/generation；完成写入必须以 token 和允许状态作 SQL 条件，失去租约或已 paused 时拒绝旧任务提交。

### R0-F2 — URL 规范化改变非 dot-segment 路径语义

- 触发条件：合法 URL 仅在尾斜杠或重复斜杠上不同，例如 `/feed` 与 `/feed/`、`/a/b` 与 `/a//b`。
- 影响：`path.Clean` 会错误合并可能不同的 Feed/Article URL，造成 Source 唯一键碰撞、canonical URL 改写和 URL 兜底去重错误。
- 证据：`backend/internal/domain/shared/url.go:48-62` 对完整 escaped path 使用 `path.Clean`；`spec.md:146` 只授权移除 dot-segment 并要求避免激进路径重写。
- 修正要求：采用 RFC 3986 remove-dot-segments 语义，保留非 dot 的空 segment 和有意义的尾部斜杠。

### R0-F3 — 上游内容更新会把 hidden 文章重新发布

- 触发条件：文章已由受控数据修复设为 `hidden`，随后同一 identity 的上游内容发生变化。
- 影响：本地隐藏决策被 Feed 覆盖，文章重新出现在匿名列表。
- 证据：`backend/internal/infrastructure/persistence/postgres/article_repository.go:68-71` 的冲突更新强制设置 `status='published'`；`spec.md:117,150` 规定只有新文章默认为 published，hidden 属于受控本地状态。
- 修正要求：冲突更新保留现有 status，仅插入新文章时设置 published。

### R0-F4 — content_hash 未包含截断标志

- 触发条件：第一次正文恰好达到 1 MiB 且未截断；第二次正文具有相同 1 MiB 前缀但追加内容。摘要 256 KiB 边界同理。
- 影响：持久化文本相同导致 hash 不变，Repository 走 unchanged 分支，`raw_*_truncated` 无法从 false 更新为 true。
- 证据：`backend/internal/domain/article/article.go:170-184` 的 hash payload 不含两个截断标志；`backend/internal/infrastructure/persistence/postgres/article_repository.go:60-65` 在 hash 相同时不更新正文记录；`spec.md:43,158-160` 要求正确记录截断状态。
- 修正要求：把截断标志纳入可重算 hash，或在 unchanged 判定中独立比较并更新这些持久化字段。

## Important（Deferred）

### I1 — OpenAPI 未完整声明 Request ID 契约

`backend/api/openapi/velis.yaml:42-52` 未声明文章响应的 `X-Request-ID` header，错误对象的 `request_id` 也没有列为 required；与 Spec 的“所有响应带 X-Request-ID”不完全一致。

### I2 — 正式 Test 文档与事件证据覆盖范围不一致

`test-spec.md:14-29` 的证据列仍是 `TODO`，执行结果仍保留 Proposal 的“未执行”；完整 Test 证据实际只存在于 E000017/log。源码测试也未直接覆盖 DNS 变址、重定向到受限地址、超时、gzip 解压后大小、pause/在途竞态、hidden 更新和截断边界转换。

### I3 — 本地手工抓取错误可能泄露 Feed URL query

`backend/internal/infrastructure/fetcher/httpfeed/fetcher.go:29` 与 `backend/internal/application/source/service.go:35` 保留底层网络错误全文，`backend/cmd/velis-admin/main.go:17-19` 直接输出到 stderr。`http.Client` 错误通常包含请求 URL，可能泄露 query token，违反 `spec.md:166`。

## Suggestions

- 为 R0-F1 至 R0-F4 增加回归测试，尤其覆盖 pause/租约过期期间的并发完成写入。
- Web 加载更多时增加 pending/禁用状态，并以请求序号防御不遵守 AbortSignal 的旧响应。
- 更新 `RunWorker` 的占位注释。
- 后续清理早期事件中仓库根 `card.md/spec.md/tasks.md/test-spec.md` 的错误相对路径噪声；它们未削弱当前完整 basis 的覆盖。

## 测试证据分层与限制

- Unit/local：E000017 记录 targeted Go、Vitest、`make check`、race、vet、漏洞扫描和构建通过；本 Review 未重跑。
- Integration：真实 PostgreSQL Repository/race、迁移 round-trip、CLI + PostgreSQL、schema/index/FK 检查通过。
- Runtime：本地 Hertz 两页 cursor 和 Worker SIGINT 验证通过。
- External：三个受控公网 Feed 只记录于 Apply E000015，不等同独立正式 Test。
- Not-run：Playwright 浏览器 E2E、生产网络 SSRF、时变公网 Feed 长尾兼容矩阵。
- basis 复核：排除 verdict 会修改的 `card.md/events.jsonl/log.md` 后，实际 diff 的 64 个输入路径全部被 basis 覆盖，`verify` 返回 `valid=true`。
