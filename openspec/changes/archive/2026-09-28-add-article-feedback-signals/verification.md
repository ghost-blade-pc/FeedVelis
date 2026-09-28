# 验收记录（2026-09-28）

测试库为本机 PostgreSQL 中单独创建的 `velis_feedback_test`，名称以 `_test` 结尾。没有对业务库执行降级或清表。

## 可复现命令

```bash
docker compose up -d postgres
docker exec velis-postgres-1 createdb -U velis velis_feedback_test
cd backend
VELIS_TEST_DATABASE_URL='postgres://velis:velis@localhost:5432/velis_feedback_test?sslmode=disable' \
  go test ./test/integration -run '^TestArticleFeedback' -count=1 -v
cd ..
make check
```

已有测试库时跳过 `createdb`。测试会迁移并清理专用测试库中的账户与文章数据；不得把该命令指向非测试库。

## 结果

- 反馈迁移测试通过：空表降级后可重新升级；非空表降级被拒绝，事实仍保留；约束和索引存在。
- 仓储与画像真实 PostgreSQL 测试通过：并发重复阅读、收藏和负反馈各自只保留唯一事实；跨窗口按 UTC 日计权且单篇阅读权重封顶 3；A/B 用户状态和画像隔离；收藏取消后贡献消失；负反馈有效期内不续期、到期可重设、撤销后排除消失；负反馈与收藏同时存在时文章排除优先，原因固定为 `not_interested_article`；下架后状态与画像过滤；旧修订主题不再贡献；失败写入回滚；下架竞争拒绝新反馈。
- `make check GOCACHE_DIR=/tmp/velis-go-cache` 通过：Go 格式、单测、race、build，包含 `backend/internal/architecture/`；Web ESLint、Vitest 58 项及生产构建通过。Web 组件测试覆盖详情、latest、搜索及登录引导。
- `go test ./api/openapi` 通过，新增反馈端点与 HTTP 测试的状态码和响应结构一致。

运行上述集成命令需要可连接本机专用 PostgreSQL。未配置该环境变量时，`make check` 中的集成测试会跳过，不能据此声称真实数据库测试通过。
