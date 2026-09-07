# 可复用知识导航

本页只索引经过代码、配置、测试或已审查 change 验证，且未来会重复使用的事实。一次性实现细节留在 change；没有可靠内容时不创建额外知识文档。

## 稳定入口

- 工程架构与构建：`rules/project-context.md`
- 产品与待决策边界：`rules/product-context.md`
- API 契约：`backend/api/openapi/velis.yaml`
- 数据契约：`backend/migrations/`
- 日志与健康检查：`backend/internal/infrastructure/observability/logger.go`、`backend/internal/interfaces/http/hertz/router.go`
- ADR 与长期开发定义：`Velis新项目开发文档.md`

## 已验证知识

格式：

```text
- **关键词**：可复用事实或约束 → `真实路径#符号`、测试或 `changes/<id>/log.md`；最近验证：日期/版本
```

- **技术栈/构建**：Go 1.26、Hertz、PostgreSQL/pgvector、Vue 3；统一命令入口为根目录 `Makefile` → `backend/go.mod`、`web/package.json`、`Makefile`；最近验证：2026-09-04 工作区初始化静态核对。
- **根包/依赖**：根包为 `github.com/ghost-blade-pc/Velis_Feed/backend`，四层 import 方向由测试强制 → `backend/go.mod`、`backend/internal/architecture/dependencies_test.go#TestLayerDependencies`；最近验证：2026-09-04 工作区初始化静态核对。
- **风险导航**：认证授权、事务/Outbox、幂等并发、迁移、SSRF、隐私及缓存/MQ/Embedding 降级 → `rules/project-context.md`、`Velis新项目开发文档.md`；最近验证：2026-09-04 工作区初始化静态核对。

发现冲突时重新验证当前事实，更新最近验证依据，并保留导致变化的 change/ADR 链接。
