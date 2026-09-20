# Velis Feed

Velis 是一个可自托管的图文 Feed 项目，当前已实现系统级 RSS 内容采集、文章保存、最新列表、站内阅读，以及用户注册、登录、会话恢复、本人资料和退出闭环。后端采用 Go + CloudWeGo Hertz + PostgreSQL，前端采用 Vue 3 + TypeScript。

本文只描述当前功能、开发现状和运行方式。项目目标、技术决策、开发顺序与验收标准统一见 [Velis Roadmap](<Velis Roadmap.md>)。目录占位、依赖声明和 Compose 服务不代表业务能力已实现。

## 当前功能与边界

| 能力 | 已实现 | 当前边界 |
| --- | --- | --- |
| 工程 | API、Worker、迁移、管理 CLI 四个入口；四层目录、依赖边界测试、配置、日志、健康检查与 CI | 仍处于早期业务建设阶段 |
| 来源管理 | CLI 添加、列出、暂停、恢复与手动抓取 Source | 系统级来源，无用户订阅关系和 HTTP 管理接口 |
| 抓取 | RSS 2.0、Atom、JSON Feed；ETag/Last-Modified、304、租约调度、失败退避、超时与响应限制 | Worker 直接编排抓取，尚未接入 MQ/Outbox；正常抓取间隔目前固定为 30 分钟 |
| 文章存储 | 按 Source 与条目身份去重、内容哈希、原始正文与清洗正文分离、事务入库 | RSS 直接入库为 published；仅有 published/hidden，无投稿、审核与历史版本 |
| 阅读 | 匿名文章列表、复合游标分页、站内详情、清洗后的 HTML/图片及原文链接 | 尚无来源级展示授权策略与图片代理；排序字段更新时不承诺跨请求快照一致性 |
| 账户与鉴权 | 用户名密码注册登录、独立会话与刷新轮换、退出与禁用撤销、本人昵称、登录失败限流、管理审计、本地维护 CLI | 默认关闭（`auth.enabled=false`）；无管理员 HTTP 接口、无改密/找回/注销、无设备会话列表 |
| Web | `/`、`/latest`、`/articles/:id`、`/register`、`/login`、`/account`；加载、空、失败、重试与加载更多 | 前端只做最小账户闭环，无投稿、管理、互动和搜索界面 |

用户订阅、关注、点赞/收藏、曝光、四场景 Feed Strategy、搜索、推荐、Eino AI 与 Agent 均未实现。Redis、RabbitMQ、MinIO 仅有部署配置或代码占位。

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
openspec/              当前 OpenSpec 规范、change 与归档
code_copilot/          迁移前 Spec Copilot 历史证据（只读）
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

完整字段与错误信封见 [OpenAPI](backend/api/openapi/velis.yaml)。认证端点在 `auth.enabled=true` 时才会注册。

| 方法与路径 | 用途 |
| --- | --- |
| `GET /livez` | 进程存活 |
| `GET /readyz` | 当前依赖就绪状态 |
| `GET /api/v1/ping` | 基础连通 |
| `GET /api/v1/articles?limit=20&cursor=...` | 匿名已发布文章列表；limit 为 1–50 |
| `GET /api/v1/articles/{article_id}` | 匿名已发布文章详情，含 content_html |
| `POST /api/v1/auth/register` | 注册普通用户；成功 201，不建立会话 |
| `POST /api/v1/auth/login` | 登录并建立独立会话，返回访问令牌并下发 Cookie |
| `POST /api/v1/auth/refresh` | 用刷新 Cookie 单次轮换令牌 |
| `POST /api/v1/auth/logout` | 撤销当前会话并清除 Cookie；已退出仍返回 204 |
| `GET /api/v1/account/me` | 读取本人账户 |
| `PATCH /api/v1/account/me` | 修改本人昵称 |

列表返回 `items`、`next_cursor`、`has_more`；排序为 `COALESCE(source_published_at, discovered_at)` 与 ID 倒序。当前游标有版本与格式校验，尚无 HMAC 签名。响应携带 `X-Request-ID`，错误使用统一 `error` 信封（`code`/`message`/`request_id`，无 `details`）。

## 账户与认证

认证默认关闭。开启前请先初始化管理员并注入密钥，顺序为：应用迁移 → 部署 `auth.enabled=false` 版本 → CLI 初始化管理员 → 注入 JWT、限流、来源与 Cookie 配置 → 开启认证 → 需要时再开启注册。

```bash
# 初始化管理员：默认从终端隐藏输入并二次确认；自动化场景用 -password-stdin
cd backend
go run ./cmd/velis-admin -config configs/config.example.yaml account init-admin -username root
printf '%s\n' "$ADMIN_PASSWORD" | go run ./cmd/velis-admin account init-admin -username root -password-stdin

# 角色与状态维护：值相同时幂等成功，不撤销会话也不新增审计
go run ./cmd/velis-admin account set-role -username alice -role admin
go run ./cmd/velis-admin account set-status -username alice -status disabled
```

开启认证至少需要以下环境变量（密钥只通过环境注入，不写入示例与日志）：

| 变量 | 说明 |
| --- | --- |
| `VELIS_AUTH_ENABLED` | `true` 时注册认证路由；关闭时保持匿名行为 |
| `VELIS_AUTH_REGISTRATION_ENABLED` | 是否开放注册入口 |
| `VELIS_AUTH_JWT_ACTIVE_KID` / `VELIS_AUTH_JWT_ACTIVE_KEY` | 主动签名密钥：Base64，解码后至少 32 字节 |
| `VELIS_AUTH_THROTTLE_KEY` | 登录失败限流查找键的密钥，同样至少 32 字节 |
| `VELIS_AUTH_ALLOWED_ORIGIN` | 精确同源来源，例如本地 `http://localhost:5173`、生产 `https://velis.example.com` |
| `VELIS_AUTH_COOKIE_SECURE` | 生产保持 `true`；只有 `development` 且来源为本机时才能关闭 |
| `VELIS_AUTH_TRUSTED_PROXY_CIDRS` | 部署在反向代理之后时必须填写反代网段 |

`VELIS_AUTH_TRUSTED_PROXY_CIDRS` 尤其重要：Compose 中 `velis-web` 的 nginx 反代 `/api` 并设置 `X-Forwarded-For`，若不把反代列为可信，IP 维度限流会把所有用户合并成同一个来源，30 次失败即可全局锁死登录。`compose.yaml` 已把 `velis_default` 的子网固定为 `172.30.0.0/24`、把 `velis-web` 固定为 `172.30.0.10`，并把该地址的 `/32` 作为 `velis-api` 的默认值，因此 Compose 部署无需额外设置；生产部署填**反代自身的地址**。

只填反代自己的地址，不要填整个子网：子网还包含网桥网关，而客户端流量从发布端口进入时的源地址正是网关；把网关也列为可信，客户端自带的 `X-Forwarded-For` 就会成为「最右的不可信跳点」被采纳，来源 IP 可被伪造、IP 维度限流可被轮换绕过。同理，`web/nginx.conf` 使用 `$remote_addr` 覆盖而不是 `$proxy_add_x_forwarded_for` 追加——nginx 是本项目部署的入口代理，前面没有其它反向代理。启动日志中的 `trusted_proxy_cidrs` 字段给出实际生效的取值，可用它核对是否配错。

登录会话与浏览器行为：

- 访问令牌有效期 15 分钟且不超过会话剩余期限，只驻留页面内存，不写 `localStorage`、`sessionStorage`、IndexedDB 或 Cookie；重载页面后用刷新 Cookie 恢复登录。
- 刷新令牌为 `HttpOnly` Cookie，单次轮换；旧令牌再次出现会撤销该会话，因此客户端在结果不明的网络失败后不会自动重放刷新请求。
- 多标签页用 Web Locks 的 `velis-auth-refresh` 锁串行化刷新，并用 BroadcastChannel 同步令牌与退出事件；访问令牌与刷新令牌都不落持久存储。浏览器不支持 Web Locks 时**不做跨标签协调**，各标签各自刷新，由服务端的单次轮换与重放撤销兜底——该跨标签协调能力不在 I1 范围，留给后续提案单独设计。
- 退出、账户禁用与角色变更都会立即撤销会话；重新启用需要重新登录。

保留期清理由 `velis-worker` 承担：认证开启时，Worker 每个 `auth.cleanup_interval`（默认 1 小时）分批删除超过保留期的数据，每批最多 `auth.cleanup_batch`（默认 500）条。保留期为登录失败事件 30 分钟、过期限制 24 小时、会话与刷新令牌在过期或撤销后 7 天；用户与管理审计不参与清理。认证关闭（`auth.enabled=false`）时 Worker 不启动清理。

当前限制：**没有修改密码、找回密码或注销账户的能力**，也没有管理员 HTTP 接口。忘记密码只能由运维人员在确认身份、取得授权并具备备份的前提下按例外流程处置；自助改密在后续阶段提案。

## 配置

配置顺序：内置默认值 → YAML → `VELIS_*` 环境变量 → 启动校验。配置入口见 [config.example.yaml](backend/configs/config.example.yaml) 和 [config.go](backend/internal/infrastructure/config/config.go)。

示例仅供本地开发；真实数据库地址、密码、Token 和模型密钥通过环境变量注入，不提交到仓库。宿主机连接配置应与 Compose 中 PostgreSQL 的实际账户和数据库匹配。

## 验证与开发现状

```bash
make check
cd backend && GOCACHE=/tmp/feedvelis-go-cache go vet ./...
```

`make check` 包含 gofmt、Go 单测/竞态/构建、Vitest 与 Web 类型检查/构建；其中 gofmt 会修改未格式化的 Go 文件。独立命令见 [Makefile](Makefile)。CI 另外执行 `go vet`，详见 [ci.yml](.github/workflows/ci.yml)。

- 已有 Domain/Application、抓取解析清洗、Hertz、CLI、配置、架构依赖与前端测试；账户领域、认证用例、HTTP 中间件、安全适配器与前端会话模块都有单测。
- PostgreSQL 集成测试通过 `VELIS_TEST_DATABASE_URL` 启用，要求已迁移的专用 `_test` 数据库（测试基座会自动应用迁移）；测试会清空 Source/Article 与账户相关表。未设置该变量时跳过，普通 CI 通过不能代替数据库集成验证。
- 认证相关计数与耗时以结构化日志字段落地：认证请求为 `operation`/`result`/`request_id`（确认身份后附 `user_id`/`session_id`），密码散列为 `operation=password_hash|password_verify` + `duration_ms`，限流为 `code=AUTH_RATE_LIMITED` + `dimension=account|ip`，刷新重放为 `event=refresh_replay`，清理为 `operation=cleanup` + `deleted_*`/`duration_ms`。**当前没有 `/metrics` 端点**，Prometheus 接入在后续阶段。
- 浏览器 Playwright E2E、契约测试体系、压测、完整监控告警和故障演练尚未完成；账户闭环的浏览器行为（重载恢复、跨标签退出、CSRF 拒绝）在 change 验证中用 CDP 驱动真实 Chrome 冒烟，未固化为仓库内的自动化测试。
- Prometheus/Grafana 仅有 Compose/配置入口；可通过 `docker compose --profile observability up -d` 启动，不代表业务指标与仪表盘已经交付。

文档中的“已实现”依据当前代码，不代表每次文档更新都重新执行了运行验证。I1 及更早的详细 change 验证保存在只读的 `code_copilot/changes/`；后续规范与 change 统一使用 `openspec/`。

## 文档分工

- 本 README：当前功能、限制、启动与测试。
- [Velis Roadmap](<Velis Roadmap.md>)：唯一项目目标、架构决策和后续实施路线。
- `AGENTS.md`、`CLAUDE.md` 与 `openspec/`：当前协作入口、规范、change 与归档，不另立产品规划。
- `code_copilot/`：迁移前的历史 change 与证据，只读保留，不再作为执行入口。
- OpenAPI、迁移和测试目录说明：对应实现的技术契约与局部使用说明。
- [第三方声明](backend/THIRD_PARTY_NOTICES.md)：随应用发布的第三方组件、版本与许可证。

仓库暂未添加开源许可证，默认保留全部权利。
