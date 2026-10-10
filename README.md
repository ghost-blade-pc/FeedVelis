# Velis Feed

Velis 是一个可自托管的图文 Feed 项目。RSS 自动发布与用户 Markdown 投稿进入统一内容池，对外提供稳定 latest、匿名/登录推荐 Feed、BM25 与可选混合搜索、站内阅读，并带有私有图片资产、AI 内容增强、管理员 Source 管理和账户会话闭环。后端 Go + CloudWeGo Hertz/Eino + PostgreSQL，前端 Vue 3 + TypeScript。

当前功能与限制以本文、[OpenAPI](backend/api/openapi/velis.yaml)、[迁移](backend/migrations/)和 [openspec/specs/](openspec/specs/) 为准；项目目标、技术决策与实施顺序见 [Velis Roadmap](<Velis Roadmap.md>)。规划中的能力不代表已经实现。

## 功能

- 内容供给：RSS 2.0、Atom、JSON Feed 抓取与用户 Markdown 投稿共享公开文章模型；来源身份互斥、不可变修订、作者与管理员下架。
- 阅读与发现：匿名 latest 稳定游标分页、站内详情、BM25 搜索（可选 KNN/RRF 混合与精确筛选）、登录后推荐 Feed（返回推荐原因与降级标记）。
- 内容增强：Eino 有界 Workflow 生成摘要、关键词、主题与向量，模型失败不阻塞发布与阅读。
- 图片资产：MinIO 私有预签名直传，匿名读取经 API 核对当前公开引用后流式返回；图片不剥离 EXIF，上传前自行移除隐私信息。
- 账户与反馈：用户名密码注册登录、会话刷新轮换、RBAC、本人资料；详情阅读、收藏与文章级“不感兴趣”。
- 可靠异步：Outbox、RabbitMQ Relay、消费幂等与版本化任务槽位，AI 增强和搜索投影由 Worker 异步推进。
- 管理：管理员在 Web 或 CLI 管理 Source（新增、周期、暂停/恢复、手动抓取、历史），并下架或恢复公开文章。

认证、AI、搜索投影与 Redis 读取缓存默认关闭或未配置；核心发布与阅读链路只依赖 PostgreSQL，RSS、纯文本投稿、latest 和详情即可工作。用户级 RSS 订阅、投稿审核、following/hot Feed、社交功能与对话/定时 Agent 尚未实现。

## 技术栈

- 后端：Go 1.26.6+，CloudWeGo Hertz、Eino，pgx 显式 SQL，golang-migrate 版本化迁移。
- 存储与中间件：PostgreSQL（事实源）、OpenSearch（可丢弃的搜索投影）、Redis（可选读取缓存）、RabbitMQ（可靠异步）、MinIO（私有图片资产）。
- 前端：Vue 3、TypeScript、Vite、Pinia、Vue Router。
- 部署与观测：Docker Compose（`compose.yaml`、`deploy/`），Prometheus 抓取 API、Worker 与 RabbitMQ 指标。

## 目录结构

```text
backend/cmd/           velis-api、velis-worker、velis-migrate、velis-admin
backend/internal/      domain、application、infrastructure、interfaces、bootstrap
backend/migrations/    版本化 PostgreSQL SQL
backend/api/openapi/   当前 HTTP 契约
backend/test/          集成测试、fixtures 与其他测试入口
web/                   Vue 3、TypeScript、Vite、Pinia、Vue Router
deploy/、compose.yaml   本地与部署入口
openspec/              长期规格、change 与归档
code_copilot/          迁移前历史证据（只读）
```

后端 module 为 `github.com/ghost-blade-pc/Velis_Feed/backend`。当前 Compose 仍使用 `pgvector/pgvector:pg17` 并在初始迁移创建 `vector` 扩展，属于遗留配置：Embedding 已以 `real[]` 版本化持久化，迁移清理列入 Roadmap。

## 快速开始

### Docker Compose

需要 Docker Compose v2，首次运行会下载镜像并构建依赖。在仓库根目录执行：

```bash
cp .env.example .env
docker compose up --build -d
docker compose ps
```

已有 `.env` 时直接使用，不重复覆盖。Compose 自动执行迁移后启动 API、Worker 和 Web，全量环境还包含 Redis、RabbitMQ、MinIO。

| 入口 | 地址 |
| --- | --- |
| Web | <http://localhost:5173> |
| API 存活检查 | <http://localhost:8080/livez> |
| API 就绪检查 | <http://localhost:8080/readyz> |
| RabbitMQ 管理页 | <http://localhost:15672> |
| RabbitMQ 指标 | <http://localhost:15692/metrics> |
| Worker 指标（容器网络） | `velis-worker:9091/metrics` |
| MinIO Console | <http://localhost:9001> |

未登记来源且没有用户投稿时，文章列表为空。认证开启后可以在 Web 的“Source 管理”维护来源，也可以用 CLI：

```bash
cd backend
go run ./cmd/velis-admin -config configs/config.example.yaml source add -url https://example.com/feed.xml
go run ./cmd/velis-admin -config configs/config.example.yaml source fetch 1
```

Source 周期、暂停/恢复、AI 内容补录与搜索投影命令见 [docs/operations.md](docs/operations.md)。

```bash
docker compose logs velis-api velis-worker
docker compose down
```

`docker compose down -v` 会删除数据卷；保留数据时不要执行。

### 宿主机开发

需要 Go 1.26.6 或更高兼容补丁版本、Node.js 24，以及 PostgreSQL。以下方式与全量 Compose 应用启动方式二选一，避免端口冲突：

```bash
# 仓库根目录：启动 PostgreSQL 并执行迁移
docker compose up -d postgres
make migrate-up

# 终端 1：API
cd backend
go run ./cmd/velis-api -config configs/config.example.yaml

# 终端 2：Worker（沿用 backend/ 目录）
go run ./cmd/velis-worker -config configs/config.example.yaml

# 终端 3：Web（从仓库根目录进入）
cd web
npm ci
npm run dev
```

Vite 在 `:5173` 提供页面，将 `/api`、`/livez`、`/readyz` 代理到 `localhost:8080`。RSS、纯文本投稿与阅读主链路只需要 PostgreSQL；图片上传/读取需要 MinIO，未配置时图片相关能力局部降级。无需模型密钥。

## 测试与验证

```bash
make check
cd backend && GOCACHE=/tmp/feedvelis-go-cache go vet ./...

# 专用 _test PostgreSQL 与 RabbitMQ 均已启动时
VELIS_TEST_DATABASE_URL='postgres://velis:velis@localhost:5432/velis_test?sslmode=disable' \
VELIS_TEST_RABBITMQ_URL='amqp://velis:开发密码@localhost:5672/velis' \
make integration-async

# PostgreSQL + OpenSearch 搜索链路
VELIS_TEST_DATABASE_URL='postgres://velis:velis@localhost:5432/velis_test?sslmode=disable' \
VELIS_TEST_OPENSEARCH_URL='http://127.0.0.1:9200' make integration-search
```

`make check` 包含 gofmt、Go 单测/竞态/构建、Vitest 与 Web 类型检查/构建，其中 gofmt 会修改未格式化的 Go 文件；CI 另外执行 `go vet`。真实 PostgreSQL 测试使用专用 `_test` 库并清表，未配置对应 `VELIS_TEST_*` 变量的 Make 目标会直接失败，跳过不等于通过。其余目标为 `make integration-postgres`、`integration-rabbitmq`、`integration-opensearch`、`integration-redis`、`integration-cache-reads`、`integration-all`，各自必需的变量与覆盖范围见 [docs/operations.md](docs/operations.md) 和[集成测试说明](backend/test/integration/README.md)。浏览器 Playwright E2E、压测和正式监控告警尚未完成。

## 配置

配置优先级为内置默认值 < YAML < `VELIS_*` 环境变量，启动时校验。入口见 [config.example.yaml](backend/configs/config.example.yaml)、[.env.example](.env.example) 和 [config.go](backend/internal/infrastructure/config/config.go)。真实凭据只经环境变量注入，不提交、不写日志。

AI 内容增强、搜索投影、Redis 缓存与推荐 Feed 的配置项、默认值和硬边界见 [docs/configuration.md](docs/configuration.md)；认证开启步骤与环境变量见 [docs/authentication.md](docs/authentication.md)。

## 文档

- [Velis Roadmap](<Velis Roadmap.md>)：唯一项目目标、技术决策、实施顺序与完成标准。
- [docs/configuration.md](docs/configuration.md)：完整配置项、默认值与降级开关。
- [docs/authentication.md](docs/authentication.md)：认证开启顺序、环境变量、会话行为与当前限制。
- [docs/operations.md](docs/operations.md)：Source、AI 补录与搜索投影运维，迁移与回滚，验证记录。
- [OpenAPI](backend/api/openapi/velis.yaml)：完整 HTTP 契约、错误信封与分页/幂等约定。
- [openspec/specs/](openspec/specs/)：已同步的长期行为规范，含内容与资产硬限制（Markdown 256 KiB、图片格式与额度）、搜索/推荐/缓存契约；`openspec/changes/` 保存进行中的 change 与归档。
- [集成测试说明](backend/test/integration/README.md)、[I2 闭环脚本](web/e2e/README.md)。
- [第三方声明](backend/THIRD_PARTY_NOTICES.md)：随应用发布的第三方组件、版本与许可证。

仓库暂未添加开源许可证，默认保留全部权利。
