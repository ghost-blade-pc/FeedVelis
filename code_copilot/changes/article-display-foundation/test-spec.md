---
schema: spec-copilot/test-v3
change_id: article-display-foundation
test_result: not-run
updated: 2026-09-07
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
| 未执行 | propose 阶段 | `not-run` | 本轮只形成规格，未修改应用代码或运行测试。 |

## 未覆盖与 Deferred

- 未覆盖：公网真实 Feed 兼容矩阵、生产网络策略、长期容量、站内 HTML 详情、用户鉴权。
- 原因：当前为 proposal，且详情/用户明确不在 MVP。
- 替代证据：Apply 阶段使用本地恶意服务、受控真实 Feed、真实 PostgreSQL 与浏览器组件测试。
- 剩余风险：Mock 网络不能证明所有云环境 SSRF 防线；第三方 parser/sanitizer 仍需依赖与漏洞审查。
