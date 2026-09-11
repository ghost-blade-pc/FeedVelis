# 可复用知识导航

本页只索引经过代码、配置、测试或已审查 change 验证，且未来会重复使用的事实。一次性实现细节留在 change；没有可靠内容时不创建额外知识文档。

## 稳定入口

- 工程架构与构建：`rules/project-context.md`
- 产品与待决策边界：`rules/product-context.md`
- API 契约：`backend/api/openapi/velis.yaml`
- 数据契约：`backend/migrations/`
- 日志与健康检查：`backend/internal/infrastructure/observability/logger.go`、`backend/internal/interfaces/http/hertz/router.go`
- ADR 与长期开发定义：`Velis Roadmap.md`

## 已验证知识

格式：

```text
- **关键词**：可复用事实或约束 → `真实路径#符号`、测试或 `changes/<id>/log.md`；最近验证：日期/版本
```

- **技术栈/构建**：Go 1.26、Hertz、pgx/PostgreSQL、Vue 3；统一命令入口为根目录 `Makefile` → `backend/go.mod`、`web/package.json`、`Makefile`；最近验证：2026-09-11 文档收敛静态核对；未重跑业务测试。
- **根包/依赖**：根包为 `github.com/ghost-blade-pc/Velis_Feed/backend`，四层 import 方向由测试强制 → `backend/go.mod`、`backend/internal/architecture/dependencies_test.go#TestLayerDependencies`；最近验证：2026-09-08 `changes/article-display-foundation` 归档时测试通过。
- **风险导航**：认证授权、事务/Outbox、幂等并发、迁移、SSRF、隐私及缓存/MQ/Embedding 降级 → `rules/project-context.md`、`Velis Roadmap.md`；最近验证：2026-09-11 文档收敛静态核对；未重跑业务测试。
- **URL 规范化**：只移除 fragment、默认端口与 dot-segment（RFC 3986 §5.2.4 语义），保留重复斜杠、尾斜杠、percent-encoding 与 query 顺序；只接受 http/https、拒绝 userinfo 与非 80/443 端口 → `backend/internal/domain/shared/url.go#NormalizeHTTPURL`；最近验证：2026-09-08 `changes/article-display-foundation`（R0-F2 修复后单测 + R1 passed）。
- **租约 fencing**：认领返回 `Lease{Owner, ExpiresAt}`，完成写入必须以 owner+expiry+status+未过期为 SQL 条件，0 行受影响返回 `ErrLeaseLost`；pause/过期/新认领均拒绝旧任务提交，过期租约可由新 Worker 重新认领自愈 → `backend/internal/domain/source/source.go#Lease`、`backend/internal/infrastructure/persistence/postgres/source_repository.go#MarkSuccess`；最近验证：2026-09-08 `changes/article-display-foundation`（R0-F1 修复后真实 PostgreSQL 集成测试 + race）。
- **文章去重、幂等入库与 hidden 保留**：唯一键为 (source_id, dedupe_key)，dedupe_key 来自 GUID 或规范化 canonical URL 哈希；content_hash 覆盖标题/URL/作者/语言/发布时间/原始描述与正文及其截断标志与清洗器版本；hash 相同不写正文，冲突更新保留受控 status（如 hidden），插入默认 published；文章与 Source 完成写入同事务原子提交 → `backend/internal/domain/article/article.go#ContentHash`、`backend/internal/infrastructure/persistence/postgres/article_repository.go#upsertArticle`；最近核对：2026-09-11 当前源码静态核对（包括清洗器版本）；基础行为的历史证据见 2026-09-08 `changes/article-display-foundation`，不代表本次重跑。
- **PostgreSQL 集成测试约定**：`VELIS_TEST_DATABASE_URL` 仅接受名称以 `_test` 结尾的数据库，测试自 TRUNCATE 三表后运行；真实库验证租约/幂等/回滚，临时库用后 DROP、容器 stop → `backend/test/integration/article_repository_test.go`；最近验证：2026-09-08 `changes/article-display-foundation` 正式 Test 阶段。

发现冲突时重新验证当前事实，更新最近验证依据，并保留导致变化的 change/ADR 链接。
