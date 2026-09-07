# 项目工程上下文

> 所有阶段的稳定导航。只记录仓库证据或用户已确认约定；计划、假设和通用建议放在 change 或 `product-context.md`。

## 项目概况

| 项目 | 应用 | 模式 | 技术栈 | 构建/测试 | 根包或命名空间 |
|---|---|---|---|---|---|
| `FeedVelis` | `Velis Feed（API、Worker、迁移工具与 Web）` | `scaffolded` | `Go 1.26、CloudWeGo Hertz、PostgreSQL + pgvector、Vue 3 + TypeScript` | `Make、Go toolchain、npm/Vite` / `Go testing、竞态检测、Vitest、架构依赖测试` | `github.com/ghost-blade-pc/Velis_Feed/backend` |

模式判定证据：仓库已有三个可构建 Go 进程入口、四层目录、数据库迁移、Compose、CI、健康检查与最小 Web 客户端；但多数业务领域包仍是 `doc.go` 占位，Redis、RabbitMQ、MinIO、推荐与 Embedding 尚未形成已实现业务能力。证据见 `README.md`、`backend/cmd/`、`backend/internal/`、`compose.yaml` 与 `.github/workflows/ci.yml`。

## 模块、职责与依赖

- `backend/internal/domain/`：领域类型与规则的目标位置；当前多数子域仅有包说明。允许依赖 Domain，不得依赖 Application、Infrastructure 或 Interfaces。
- `backend/internal/application/` 与 `backend/internal/application/ports/`：用例、编排和技术端口；当前已实现健康检查服务。允许依赖 Application 与 Domain。
- `backend/internal/infrastructure/`：配置、PostgreSQL、日志及后续基础设施适配器。允许依赖 Infrastructure、Application 与 Domain。
- `backend/internal/interfaces/`：Hertz HTTP 适配器及后续 consumer、scheduler、CLI。允许依赖 Interfaces、Application 与 Domain。
- `backend/internal/bootstrap/`：API 与 Worker 的 Composition Root，可装配所有层。
- `backend/internal/architecture/dependencies_test.go#TestLayerDependencies`：使用 `go/parser` 强制上述后端层级依赖方向。
- `web/src/`：Vue 3 Web 客户端；`api/` 承担 HTTP 客户端，`views/` 与 `router/` 提供当前最小页面和路由，`features/` 等仍为占位。
- `backend/migrations/`：golang-migrate 版本化 SQL；`000001_initialize_platform.up.sql` 初始化 `velis` schema、pgcrypto 与 vector 扩展。

## 入口、集成与依赖

- 入口与契约：`backend/cmd/velis-api/main.go`、`backend/cmd/velis-worker/main.go`、`backend/cmd/velis-migrate/main.go`、`web/src/main.ts`；HTTP 契约入口为 `backend/api/openapi/velis.yaml`，当前路由装配见 `backend/internal/interfaces/http/hertz/router.go#NewServer`。
- 中间件与外部依赖：Hertz 中间件顺序为 Request ID → Recovery → Access Log；Go 依赖及版本见 `backend/go.mod`，Web 依赖见 `web/package.json`，PostgreSQL/pgvector、Redis、RabbitMQ、MinIO 与可观测组件边界见 `compose.yaml`。
- 核心业务域：`account、source、article、feed、interaction、relation、exposure、recommendation、embedding`；除 health 外的大部分领域/Application 能力尚未实现，不把目录占位视为可用能力。

外部依赖只记录用途、版本/所有权边界、配置路径和配置键，不记录 secret。

## 构建、验证与运行诊断

| 用途 | 命令或入口 | 证据/限制 |
|---|---|---|
| 构建 | `make backend-build web-build` | 构建三个 Go 进程与 Web；Web 构建同时执行 `vue-tsc -b`。首次执行需已有 Go/npm 依赖。 |
| targeted tests | `cd backend && GOCACHE=/tmp/feedvelis-go-cache go test ./internal/application/health ./internal/interfaces/http/hertz`；`cd web && npx vitest run src/api/client.test.ts` | 仅覆盖健康服务、HTTP 适配器和当前 Web API 客户端；不代表外部依赖或完整业务通过。 |
| broader regression | `make check`；另执行 `cd backend && GOCACHE=/tmp/feedvelis-go-cache go vet ./...` | `make check` 包含 `gofmt -w`，会修改未格式化 Go 文件；其余范围为 Go 测试/竞态/构建与 Web 测试/构建。 |
| 本地运行/依赖 | `docker compose up --build -d`；或只启动 PostgreSQL 后运行 `make migrate-up`、`go run ./cmd/velis-api -config configs/config.example.yaml` 与 `npm run dev` | 可能下载镜像/依赖并占用端口；配置及完整步骤见 `README.md`。 |
| 日志、指标、Trace、健康检查、Runbook | `/livez`、`/readyz`、`/api/v1/ping`；`docker compose logs <service>`；`docker compose --profile observability up` | Prometheus/Grafana 配置已存在；业务指标、Trace、告警与正式 Runbook 尚未完成，不能从 Compose 配置推断为已验证能力。 |

## 风险导航

- 关键词：`认证授权、事务与 Outbox、幂等、并发、迁移、SSRF、隐私、缓存降级、消息重试、Embedding 降级`
- 敏感配置位置：真实凭据只允许经环境变量注入；示例键见 `.env.example`、`backend/configs/config.example.yaml` 与 `backend/internal/infrastructure/config/config.go#applyEnvironment`。不得把真实数据库 URL、Token、Cookie 或 Provider Key 写入工作区证据。
- 不可逆或生产副作用：`docker compose down -v` 删除本地数据卷；迁移 `down`/`force`、数据修复、生产配置与外部服务操作必须先确认目标、备份/回滚和兼容性。
- 需要人工授权的操作：Git commit/push/merge/release、部署、生产或真实外部账户操作、采购/新增服务、不可逆迁移及删除持久化数据。

## 免协议改动清单

> 与用户确认后填写。清单内改动不改变系统行为，可直接执行、不建 change；超出清单或不确定是否影响行为时，一律走 change 流程。候选类别（确认后才可列入）：注释与文案、格式化与风格调整、无行为变化的重命名。

| 范围 | 说明 | 确认依据 |
|---|---|---|
| TODO: 尚未确认 | 当前不预授权任何免协议改动 | 等待开发者确认 |
