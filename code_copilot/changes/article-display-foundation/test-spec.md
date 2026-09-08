---
schema: spec-copilot/test-v3
change_id: article-display-foundation
test_result: passed
updated: 2026-09-08
---

# 测试规格 — 文章展示基础能力与库表设计

## 风险与验收映射

| 风险/验收标准 | 测试层级 | 场景 | 预期 | 证据 |
|---|---|---|---|---|
| Source 重复 | CLI + PostgreSQL integration | 等价 Feed URL 多次添加 | normalized URL 唯一，幂等返回已有 Source ID 且退出码 0 | TODO |
| SSRF | Fetcher integration | loopback、私网、link-local、metadata、DNS/重定向变址 | 每一跳 fail-closed，无请求到受限目标 | TODO |
| 资源耗尽 | Fetcher | 5s 连接/20s 总超时、超过 5 次重定向、解压后超过 5 MiB、超过 500 条 | 及时取消或停止并返回稳定 error_code | TODO |
| Feed 格式兼容 | Parser | RSS 2.0、Atom、JSON Feed、命名空间、CDATA、缺字段、畸形输入 | 正确标准化或逐条跳过，不 panic | TODO |
| 文章去重 | Domain + PostgreSQL | GUID 重复、无 GUID 同 URL、并发 ingest | 同 source 仅一个 article id | TODO |
| 内容更新 | Application integration | hash 相同/变化 | 相同不写；变化原 id 更新且正文同事务 | TODO |
| 原文大小边界 | Parser/Application | description 超过 256 KiB、content 超过 1 MiB、多字节 UTF-8 截断 | 元数据保留，合法 UTF-8 截断，truncation 标志正确 | TODO |
| 条件请求 | Fetcher/Application | ETag、Last-Modified、304 | 发送条件头；304 不 parse、不触碰 Article | TODO |
| 调度租约 | PostgreSQL + race | 两 Worker 抢同 Source、租约过期、shutdown | 单次有效认领，可恢复，无泄漏 | TODO |
| 失败退避 | Domain/Application | 1m 指数退避、6h 上限、±20% 抖动、连续 5 次失败、人工 fetch 成功 | 参数边界正确，进入 degraded 后停止自动认领，成功恢复 active | TODO |
| 稳定分页 | Repository + API | 同 sort_at、多页、期间插入新文章 | 不重不漏，cursor/has_more 正确 | TODO |
| 字段与大字段隔离 | API + SQL | 列表请求 | 不返回/读取 raw_content、sanitized_html、hash、抓取状态 | TODO |
| Web 外链安全 | Vitest | 恶意标题/摘要、非 HTTP URL、点击 | 文本转义、危险 URL 不可用、noopener/noreferrer | TODO |
| HTML 清理 | Sanitizer unit/security corpus | script、事件属性、style、SVG、iframe、data/javascript URL | 仅保留 v1 allowlist，链接限定 HTTP(S) 并补安全 rel | TODO |
| 迁移回滚 | PostgreSQL | up/down/up 空库与约束检查 | 可重复；有数据 down 必须人工确认而非测试自动破坏 | TODO |
| 分层边界 | Architecture test | 新 imports | 无 pgx/Hertz/Parser 类型进入 Domain | TODO |

## 执行结果

| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| `go test -count=1 ./internal/... ./cmd/...`（targeted 单测） | 本地 Go 1.26.6；无 DB/网络依赖 | passed | domain/shared、domain/source、domain/article、application/source、application/article、fetcher/httpfeed、interfaces/cli、interfaces/http/hertz、architecture、cmd/velis-migrate 全部 ok。覆盖 URL 规范化与 dot-segment、content_hash 含截断标志、dedupe key、退避参数与抖动边界、条件请求/304、SSRF 拒绝、三种 Feed 格式与 500 条上限、畸形输入、sanitizer allowlist、分页契约与不透明游标、CLI 参数校验、Hertz 错误信封。 |
| `npx vitest run` | 本地 Node.js 24；Web | passed | 2 文件 5 项通过：只允许 HTTP(S) 原文链接、缺发布时间回退、ping、ApiError 映射、listArticles 游标。恶意标题/摘要的组件级渲染转义无专项组件测试，依赖 Vue 模板默认转义（见未覆盖）。 |
| `go vet ./...` | 本地 Go 1.26.6 | passed | 后端全包静态检查无告警。 |
| `gofmt -l .` | 本地 Go 1.26.6 | passed | 无输出，无未格式化 Go 文件。 |
| `git diff --check` | 当前工作区 | passed | 退出码 0，无空白错误；仅有既有 Git 行尾 LF→CRLF 提示。 |
| 迁移 round-trip `up/down/up` + `version` | PostgreSQL 17 + pgvector（Docker），临时库 `velis_migrate_test` | passed | up→`version=2 dirty=false`、down→`version=1`、再 up→`version=2 dirty=false`，空库可重复回滚。 |
| 约束与索引检查（psql `\d` + `EXPLAIN`） | 同上，临时库 `velis_integ_test`（迁移 up 后） | passed | `sources_normalized_feed_url_key` UNIQUE、`articles (source_id, dedupe_key)` UNIQUE、租约 fencing CHECK、URL/长度/状态枚举 CHECK、FK RESTRICT 全部存在；列表查询 `EXPLAIN` 命中 `articles_list_idx`（Index Scan）。 |
| `go test -race -count=1 -v ./test/integration` | PostgreSQL 17 + pgvector，临时库 `velis_integ_test`（`VELIS_TEST_DATABASE_URL` 注入，迁移 up 后） | passed | 真实库下租约 fencing、pause/过期/重新认领、hidden 状态保留、Source 完成失败时文章事务回滚、重复抓取幂等全部通过，race 无告警。 |
| `make check` | 本地 Go 1.26.6、Node.js 24 | passed | 退出码 0：backend-fmt（无改动）、backend-test 26 包 ok、backend-race（集成包无 DB URL 自动 skip）、四个 cmd 构建、Vitest 5 项、vue-tsc、Vite production build 全部通过；真实 PG 集成层由上一条独立命令补足。 |
| Spec Copilot formal Test | Fix 完成后的新 basis | passed | 本轮即正式 Test；全部通过，无新 finding。 |

## 未覆盖与 Deferred

- 未覆盖：公网真实 Feed 兼容矩阵、生产网络策略、长期容量、站内 HTML 详情、用户鉴权、Playwright 真实浏览器 E2E、恶意标题/摘要的 Vue 组件级渲染转义专项测试（依赖 Vue 模板默认转义 + 模型层 HTTP(S) 校验，无独立组件测试证据）。
- 原因：公网/浏览器与生产网络验证不在本轮测试范围；详情与用户鉴权不在 MVP。
- 替代证据：SSRF 用本地 loopback/私网/受限地址矩阵在拨号前拒绝（单测层）；HTML 清理用恶意 corpus 单测；Web 安全用 model 层 URL 校验单测。
- Deferred：Review I1 至 I3（OpenAPI Request ID 契约、Test 文档同步、CLI 网络错误 URL query 脱敏）按 Fix 范围协议延期，未在本轮验证。
- 剩余风险：Mock 网络不能证明所有云环境 SSRF 防线；第三方 parser/sanitizer 依赖漏洞审查需随依赖升级持续进行。

## 未覆盖与 Deferred

- 未覆盖：公网真实 Feed 兼容矩阵、生产网络策略、长期容量、站内 HTML 详情、用户鉴权。
- 原因：当前为 proposal，且详情/用户明确不在 MVP。
- 替代证据：Apply 阶段使用本地恶意服务、受控真实 Feed、真实 PostgreSQL 与浏览器组件测试。
- 剩余风险：Mock 网络不能证明所有云环境 SSRF 防线；第三方 parser/sanitizer 仍需依赖与漏洞审查。
