# Velis Feed

Velis 是一个可自托管的图文 Feed 项目，当前已实现系统级 RSS 内容采集、文章保存、最新列表和站内阅读。后端采用 Go + CloudWeGo Hertz + PostgreSQL，前端采用 Vue 3 + TypeScript。

本文只描述当前功能、开发现状和运行方式。项目目标、技术决策、开发顺序与验收标准统一见 [Velis Roadmap](<Velis Roadmap.md>)。目录占位、依赖声明和 Compose 服务不代表业务能力已实现。

## 当前功能与边界

| 能力 | 已实现 | 当前边界 |
| --- | --- | --- |
| 工程 | API、Worker、迁移、管理 CLI 四个入口；四层目录、依赖边界测试、配置、日志、健康检查与 CI | 仍处于早期业务建设阶段 |
| 来源管理 | CLI 添加、列出、暂停、恢复与手动抓取 Source | 系统级来源，无用户订阅关系和 HTTP 管理接口 |
| 抓取 | RSS 2.0、Atom、JSON Feed；ETag/Last-Modified、304、租约调度、失败退避、超时与响应限制 | Worker 直接编排抓取，尚未接入 MQ/Outbox；正常抓取间隔目前固定为 30 分钟 |
| 文章存储 | 按 Source 与条目身份去重、内容哈希、原始正文与清洗正文分离、事务入库 | RSS 直接入库为 published；仅有 published/hidden，无投稿、审核与历史版本 |
| 阅读 | 匿名文章列表、复合游标分页、站内详情、清洗后的 HTML/图片及原文链接 | 尚无来源级展示授权策略与图片代理；排序字段更新时不承诺跨请求快照一致性 |
| Web | `/`、`/latest`、`/articles/:id`；加载、空、失败、重试与加载更多 | 暂无账户、投稿、管理、互动和搜索界面 |

账户鉴权、用户订阅、关注、点赞/收藏、曝光、四场景 Feed Strategy、搜索、推荐、Eino AI 与 Agent 均未实现。Redis、RabbitMQ、MinIO 仅有部署配置或代码占位。

抓取器校验 URL、限制重定向并在直连时检查目标地址；环境代理由代理侧解析目标，不能把直连防护等同于代理链路的完整 SSRF 防护。HTML 采用允许列表清洗，远程图片仍由浏览器直接请求。

## 目录与技术现状

```text
backend/cmd/           velis-api、velis-worker、velis-migrate、velis-admin
backend/internal/      domain、application、infrastructure、interfaces、bootstrap
backend/migrations/    版本化 PostgreSQL SQL
backend/api/openapi/   当前 HTTP 契约
backend/test/          集成测试、fixtures 与其他测试入口
web/                   Vue 3、TypeScript、Vite、Pinia、Vue Router
部署入口               compose.yaml、deploy/、各应用 Dockerfile
code_copilot/          Spec Coding 执行协议、change 与证据
```

后端 module：`github.com/ghost-blade-pc/Velis_Feed/backend`。实际数据访问为 pgx + 显式 SQL，迁移使用 golang-migrate。

当前 Compose 仍使用 `pgvector/pgvector:pg17`，初始迁移创建 `vector` 扩展；没有向量业务表或查询实现。目标搜索与向量方案已确定为 OpenSearch，但尚未接入，遗留 pgvector 的迁移清理列入 Roadmap。本次文档定稿不改变运行配置。

## 本地启动

### Docker Compose

需要 Docker Compose v2，首次运行会下载镜像并构建依赖。在仓库根目录执行：

```bash
cp .env.example .env
docker compose up --build -d
docker compose ps
```

已有 `.env` 时直接使用，不重复覆盖。Compose 自动执行迁移后启动 API、Worker 和 Web；全量环境还包含 Redis、RabbitMQ、MinIO。

| 入口 | 地址 |
| --- | --- |
| Web | <http://localhost:5173> |
| API 存活检查 | <http://localhost:8080/livez> |
| API 就绪检查 | <http://localhost:8080/readyz> |
| RabbitMQ 管理页 | <http://localhost:15672> |
| MinIO Console | <http://localhost:9001> |

Source 管理使用下文宿主机 CLI，需要 Go 工具链；当前没有 Web 管理页。未登记来源或完成抓取前，文章列表为空。

```bash
docker compose logs velis-api velis-worker
docker compose down
```

`docker compose down -v` 会删除数据卷；保留数据时不要执行。

### 宿主机开发

需要 Go 1.26.6 或更高兼容补丁版本、Node.js 24，以及 PostgreSQL。以下方式与全量 Compose 应用启动方式二选一，避免端口冲突：

```bash
# 仓库根目录
docker compose up -d postgres
make migrate-up

# API 终端
cd backend
go run ./cmd/velis-api -config configs/config.example.yaml
```

```bash
# Worker 终端，从仓库根目录开始
cd backend
go run ./cmd/velis-worker -config configs/config.example.yaml
```

```bash
# Web 终端，从仓库根目录开始
cd web
npm ci
npm run dev
```

Vite 在 `:5173` 提供页面，将 `/api`、`/livez`、`/readyz` 代理到 `localhost:8080`。当前 RSS/阅读主链路只需要 PostgreSQL，无需模型密钥或其他业务中间件。

### 登记和抓取来源

从仓库根目录进入 `backend/`，将示例 URL 和 ID 替换为实际来源：

```bash
cd backend
go run ./cmd/velis-admin -config configs/config.example.yaml source add -url https://example.com/feed.xml
go run ./cmd/velis-admin -config configs/config.example.yaml source list
go run ./cmd/velis-admin -config configs/config.example.yaml source fetch 1
go run ./cmd/velis-admin -config configs/config.example.yaml source pause 1
go run ./cmd/velis-admin -config configs/config.example.yaml source resume 1
```

`source fetch 1 --force` 可忽略条件请求头，重新拉取并清洗内容；它仍遵循来源认领规则。正常运行时 Worker 自动认领到期 Source。Source 写操作只提供本地 CLI。

## 当前 API

完整字段与错误信封见 [OpenAPI](backend/api/openapi/velis.yaml)。

| 方法与路径 | 用途 |
| --- | --- |
| `GET /livez` | 进程存活 |
| `GET /readyz` | 当前依赖就绪状态 |
| `GET /api/v1/ping` | 基础连通 |
| `GET /api/v1/articles?limit=20&cursor=...` | 匿名已发布文章列表；limit 为 1–50 |
| `GET /api/v1/articles/{article_id}` | 匿名已发布文章详情，含 content_html |

列表返回 `items`、`next_cursor`、`has_more`；排序为 `COALESCE(source_published_at, discovered_at)` 与 ID 倒序。当前游标有版本与格式校验，尚无 HMAC 签名。响应携带 `X-Request-ID`，错误使用统一 `error` 信封。

## 配置

配置顺序：内置默认值 → YAML → `VELIS_*` 环境变量 → 启动校验。配置入口见 [config.example.yaml](backend/configs/config.example.yaml) 和 [config.go](backend/internal/infrastructure/config/config.go)。

示例仅供本地开发；真实数据库地址、密码、Token 和模型密钥通过环境变量注入，不提交到仓库。宿主机连接配置应与 Compose 中 PostgreSQL 的实际账户和数据库匹配。

## 验证与开发现状

```bash
make check
cd backend && GOCACHE=/tmp/feedvelis-go-cache go vet ./...
```

`make check` 包含 gofmt、Go 单测/竞态/构建、Vitest 与 Web 类型检查/构建；其中 gofmt 会修改未格式化的 Go 文件。独立命令见 [Makefile](Makefile)。CI 另外执行 `go vet`，详见 [ci.yml](.github/workflows/ci.yml)。

- 已有 Domain/Application、抓取解析清洗、Hertz、CLI、配置、架构依赖与前端测试。
- PostgreSQL 集成测试通过 `VELIS_TEST_DATABASE_URL` 启用，要求已迁移的专用 `_test` 数据库；测试会清空 Source/Article 相关表。未设置该变量时跳过，普通 CI 通过不能代替数据库集成验证。
- 浏览器 Playwright E2E、契约测试体系、压测、完整监控告警和故障演练尚未完成。
- Prometheus/Grafana 仅有 Compose/配置入口；可通过 `docker compose --profile observability up -d` 启动，不代表业务指标与仪表盘已经交付。

文档中的“已实现”依据当前代码，不代表每次文档更新都重新执行了运行验证。详细 change 验证保存在 `code_copilot/changes/`。

## 文档分工

- 本 README：当前功能、限制、启动与测试。
- [Velis Roadmap](<Velis Roadmap.md>)：唯一项目目标、架构决策和后续实施路线。
- `AGENTS.md`、`CLAUDE.md` 与 `code_copilot/`：协作入口、执行记录与证据，不另立产品规划。
- OpenAPI、迁移和测试目录说明：对应实现的技术契约与局部使用说明。

仓库暂未添加开源许可证，默认保留全部权利。
