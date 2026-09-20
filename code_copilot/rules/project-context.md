# 项目工程上下文

> 执行导航，不维护独立产品路线。当前功能以 [README](../../README.md) 与代码为准；用户确认目标和实施顺序以 [Velis Roadmap][roadmap] 为准。发生文档/代码漂移时先报告并校准。

## 项目概况

- 项目：FeedVelis；模式保持 `scaffolded`，表示已有业务基础但大部分目标能力尚待建设，不表示只有空骨架。
- 当前实际技术：Go/Hertz、pgx/PostgreSQL、golang-migrate、Vue/TypeScript；版本见 `backend/go.mod` 与 `web/package.json`。
- 已确认目标技术与边界只在 Roadmap §2 维护。当前 Compose 的 pgvector 镜像/vector 初始扩展是尚未清理的遗留，不能作为继续实现 pgvector 的决策依据。
- 根包：`github.com/ghost-blade-pc/Velis_Feed/backend`。
- 最近现状核对：2026-09-19 `changes/account-access-foundation` 第 5 轮 Test（真实进程/真实 PostgreSQL/真实 nginx/真实 Chrome，24 个用例分组通过）；文档收敛部分仍以 2026-09-11 的静态核对为准。

## 模块、职责与证据

| 入口 | 当前用途与边界 |
| --- | --- |
| `backend/cmd/velis-api`、`velis-worker`、`velis-migrate`、`velis-admin` | 四个进程入口；Worker 已调度 RSS，Admin 已管理 Source |
| `backend/internal/domain/source`、`domain/article`、`domain/account` | 来源身份/租约、外部文章身份/内容与读取模型、账户/会话/令牌/限流的纯规则（无 SQL 与框架类型） |
| `backend/internal/application/source/service.go`、`article/service.go`、`account/` | 抓取编排、事务入库、列表和详情、账户用例（注册/登录/刷新/退出/本人资料/管理/清理）；health 为健康用例 |
| `backend/internal/infrastructure/persistence/postgres/` | pgx 仓储与事务；Source/Article/Content 与账户/会话/刷新令牌/限流/审计/清理均已实现 |
| `backend/internal/infrastructure/fetcher/httpfeed/` | RSS/Atom/JSON Feed 抓取解析清洗；环境代理与直连安全边界不同 |
| `backend/internal/interfaces/`、`bootstrap/` | Hertz、CLI、Scheduler 适配与依赖装配 |
| `backend/internal/interfaces/http/hertz/router.go` | /livez、/readyz、/api/v1/ping、文章列表与详情；认证开启时另注册 4 条 `/auth/*` 与 2 条 `/account/me`；仍无管理员 HTTP 端点与 HTTP Source 写入口 |
| `backend/migrations/` | 000001 初始化 schema/扩展，000002 创建 Source/Article/Content，000003 创建账户与访问相关的六张表（不改写历史迁移） |
| `web/src/router/index.ts`、`features/article/`、`views/ArticleView.vue`、`features/auth/`、`views/{Register,Login,Account}View.vue` | 最新列表、站内阅读与原文外链；以及注册/登录/账户/退出与 Web Locks 多标签会话协调 |
| `backend/api/openapi/velis.yaml` | 当前接口事实契约，规划端点不等于已落地 |
| `compose.yaml`、`.github/workflows/ci.yml` | 环境与自动化入口；部署配置不等于业务接入或运行验证 |

## 依赖规则

Domain → Domain；Application → Application/Domain；Infrastructure → Infrastructure/Application/Domain；Interfaces → Interfaces/Application/Domain；bootstrap 装配全部层。`backend/internal/architecture/dependencies_test.go#TestLayerDependencies` 强制层级边界；跨模块所有权另由 Review 检查。

## 构建、验证与运行诊断

命令在项目根目录执行，除非显式 cd。完整运行说明只维护于 README。

| 用途 | 命令或入口 | 证据/限制 |
| --- | --- | --- |
| 构建 | `make backend-build web-build` | 四个 Go 入口与 Vue 类型检查/构建；需已有依赖 |
| targeted tests | `cd backend && GOCACHE=/tmp/feedvelis-go-cache go test ./internal/domain/... ./internal/application/... ./internal/interfaces/http/hertz ./internal/infrastructure/fetcher/httpfeed`；`cd web && npm test` | 根据改动进一步缩小；不能代表真实依赖或浏览器 E2E |
| integration | `cd backend && GOCACHE=/tmp/feedvelis-go-cache go test -count=1 ./test/integration` | 需 VELIS_TEST_DATABASE_URL 指向已迁移专用 _test 库，会清表；未配置跳过 |
| broader regression | `make check`；`cd backend && GOCACHE=/tmp/feedvelis-go-cache go vet ./...` | make check 含 gofmt -w，会修改文件 |
| 本地运行 | README 的 Compose 或宿主机开发流程 | 下载依赖/占用端口；RSS 基础链路仅需 PostgreSQL |
| 日志与健康 | `/livez`、`/readyz`；`docker compose logs velis-api velis-worker` | Prometheus/Grafana 仅有配置；业务指标/Trace/告警未完成 |

## 风险导航

- 认证权限、事务/Outbox、幂等并发、租约 fencing、迁移、SSRF/代理、HTML/图片、隐私、缓存/MQ/模型降级与索引旧写；目标约束见 Roadmap 各节。
- 敏感配置入口：`.env.example`、`backend/configs/config.example.yaml`、`backend/internal/infrastructure/config/config.go`；不记录真实 DSN/Token/密码。
- commit/push/merge/release、部署、生产/真实外部账户、采购和不可逆删除遵守用户授权边界。下迁移/force 与数据修复需要明确目标、备份和恢复方式。

## 免协议改动清单

| 范围 | 说明 | 确认依据 |
| --- | --- | --- |
| 本次文档收敛，2026-09-11 | 用户已授权重写 README、建立唯一 Roadmap、移除被吸收的重复规划文档、同步协作导航；仅本次纯文档整理，不修改业务、运行配置、迁移或历史 change 证据，不产生业务阶段授权 | 本会话用户确认方案后要求“可以，开始执行吧” |

其余改动仍按既有阶段协议执行；本次例外不扩展为永久免协议授权。

[roadmap]: <../../Velis Roadmap.md>
