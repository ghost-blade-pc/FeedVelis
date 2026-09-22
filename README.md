# Velis Feed

Velis 是一个可自托管的图文 Feed 项目，当前已实现 RSS 自动发布与用户 Markdown 图文投稿的统一内容池、稳定 latest、站内阅读、私有图片资产、管理员 Source 管理，以及账户与会话闭环。后端采用 Go + CloudWeGo Hertz + PostgreSQL，前端采用 Vue 3 + TypeScript。

本文只描述当前功能、开发现状和运行方式。项目目标、技术决策、开发顺序与验收标准统一见 [Velis Roadmap](<Velis Roadmap.md>)。目录占位、依赖声明和 Compose 服务不代表业务能力已实现。

项目以共享 RSS/用户投稿内容池、AI 内容增强、推荐 Feed 和两层 Agent（对话内即时推荐、后续定时任务写入站内收件箱）为业务主线，同时用于实践可靠异步、搜索缓存、微服务演进、Docker/Kubernetes、可观测性、压测与 Go GC 优化。规划中的技术能力不代表当前已经实现。

## 当前功能与边界

| 能力 | 已实现 | 当前边界 |
| --- | --- | --- |
| 工程 | API、Worker、迁移、管理 CLI 四个入口；四层目录、依赖边界测试、配置、日志、健康检查与 CI | 仍处于早期业务建设阶段 |
| 来源管理 | CLI 及管理员 HTTP/Web 新增、列出、改周期、暂停、恢复、同步抓取与抓取历史；5 分钟至 24 小时来源级周期 | 系统级来源；Feed URL 创建后不可修改，无删除、认证 Feed、自定义请求头或用户订阅 |
| 抓取 | RSS 2.0、Atom、JSON Feed；ETag/Last-Modified、304、租约/fencing、失败退避、运行历史、超时与 5 MiB 限制 | Worker 直接编排，尚未接入 MQ/Outbox；默认忽略通用环境代理 |
| 文章存储 | RSS/用户互斥来源身份、不可变修订、固定站内发布时间、乐观锁、作者与管理员下架优先级 | 用户发布后直接公开，无审核队列、协作编辑或版本历史 UI |
| 阅读 | 匿名联合 latest、`(effective_published_at,id)` 稳定游标、站内详情；RSS 展示 Source/原文，投稿只展示作者稳定 ID/昵称 | RSS 有原站时间时优先排序，缺失时回退固定站内发布时间；新文章插入不提供跨请求数据库快照 |
| 图片资产 | 私有 MinIO 预签名直传、服务端确认、版本引用、额度/格式限制、匿名授权流式读取和孤儿清理 | JPEG/PNG/WebP；不转码、不生成缩略图、不剥离 EXIF；API 承担公开图片下行流量 |
| 账户与鉴权 | 用户名密码注册登录、会话刷新轮换、RBAC、本人资料、登录限流、管理审计与维护 CLI | 默认关闭（`auth.enabled=false`）；无改密/找回/注销或设备会话列表 |
| Web | latest/详情、账户闭环、本人文章列表与 Markdown 编辑/预览/图片上传、管理员 Source 页面 | 手动保存，不自动保存/合并；无互动、搜索、recommend 或 Agent 界面 |

推荐信号、recommend Feed、Redis 业务缓存、RabbitMQ/Outbox Relay、OpenSearch、Eino AI、对话 Agent 与定时 Agent 均未实现。用户级 RSS 订阅、投稿审核、following/hot Feed 和社交功能不在当前范围；导航中的占位页不代表对应能力已实现。

抓取器默认忽略 `HTTP_PROXY`、`HTTPS_PROXY` 与 `ALL_PROXY`，直连时会校验每次 DNS 结果、实际连接、重定向、协议和端口。只有 `VELIS_FEED_PROXY_URL` 会启用专用可信出口代理；此时最终 DNS/IP 安全边界委托给代理，应用无法声称仍能验证最终目标 IP。代理地址可以含凭据，但日志只记录脱敏模式与主机。

I2 核心链路不依赖 MQ、Redis、搜索或模型：PostgreSQL 可用时，RSS、纯文本投稿、latest 和详情即可工作。下一阶段为 I3“可靠异步与 AI 增强”，尚未实现。

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

认证开启后管理员可在 Web 的“Source 管理”完成新增、周期、暂停/恢复、手动抓取和历史查看；本地 CLI 仍保留。未登记来源且没有用户投稿时，文章列表为空。

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

Vite 在 `:5173` 提供页面，将 `/api`、`/livez`、`/readyz` 代理到 `localhost:8080`。RSS、纯文本投稿与阅读主链路只需要 PostgreSQL；图片上传/读取需要 MinIO，未配置时按下表局部降级。无需模型密钥。

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

`source fetch 1 --force` 可忽略条件请求头，重新拉取并清洗内容；它仍遵循来源认领规则。正常运行时 Worker 按每个 Source 的周期自动认领。CLI 与管理员 HTTP 复用应用规则。

## 当前 API

完整字段与错误信封见 [OpenAPI](backend/api/openapi/velis.yaml)。认证端点在 `auth.enabled=true` 时才会注册。

| 方法与路径 | 用途 |
| --- | --- |
| `GET /livez` | 进程存活 |
| `GET /readyz` | 当前依赖就绪状态 |
| `GET /api/v1/ping` | 基础连通 |
| `GET /api/v1/articles?limit=20&cursor=...` | 匿名已发布文章列表；limit 为 1–50 |
| `GET /api/v1/articles/{article_id}` | 匿名已发布文章详情，含 content_html |
| `GET/POST /api/v1/me/articles` | 本人文章列表；创建草稿或直接发布 |
| `GET/PATCH/DELETE /api/v1/me/articles/{article_id}` | 本人私有详情、编辑与软删除 |
| `POST /api/v1/me/articles/{article_id}/publish\|offline` | 作者发布/重新发布与下架 |
| `POST /api/v1/me/articles/preview` | 无状态服务端 Markdown 预览 |
| `POST /api/v1/me/assets`、`POST .../{asset_id}/confirm` | 创建预签名图片上传并确认实际对象 |
| `GET/HEAD /api/v1/assets/{asset_id}/content` | 当前公开引用的匿名流式读取或作者预览 |
| `POST /api/v1/admin/articles/{article_id}/offline\|restore` | 管理员下架与恢复公开文章 |
| `/api/v1/admin/sources...` | Source 新增、详情、改周期、暂停/恢复、手动抓取与历史 |
| `POST /api/v1/auth/register` | 注册普通用户；成功 201，不建立会话 |
| `POST /api/v1/auth/login` | 登录并建立独立会话，返回访问令牌并下发 Cookie |
| `POST /api/v1/auth/refresh` | 用刷新 Cookie 单次轮换令牌 |
| `POST /api/v1/auth/logout` | 撤销当前会话并清除 Cookie；已退出仍返回 204 |
| `GET /api/v1/account/me` | 读取本人账户 |
| `PATCH /api/v1/account/me` | 修改本人昵称 |

列表返回 `items`、`next_cursor`、`has_more`；latest 只读取 `published`，按 `(effective_published_at,id)` 倒序，其中 RSS 优先使用 `source_published_at`、缺失时回退固定站内 `published_at`，站内投稿使用固定站内 `published_at`。文章来源由 `origin.type=rss|user` 判别。latest 游标为版本 2 且有格式校验，旧排序语义生成的版本 1 游标返回 `INVALID_CURSOR`，调用方应从第一页重新获取；游标尚无 HMAC 签名。响应携带 `X-Request-ID`，错误使用统一 `error` 信封（`code`/`message`/`request_id`，无 `details`）。

I2 内容、资产和 Source 写接口要求 `Idempotency-Key`，修改既有资源还要求强 `If-Match: "<lock_version>"`；成功结果默认保留 24 小时。浏览器在同一用户动作的认证刷新或结果不明重试中复用原键，用户明确再次提交才生成新键。保留期结束后不承诺响应重放，但数据库唯一约束和状态机仍保护数据一致性。版本冲突返回 409，Web 保留本地 Markdown，不自动合并或换键覆盖。

## 统一内容与资产边界

- 用户稿支持 `draft`、`published`、`offline`、`deleted`；首次发布固定 `published_at`。作者可编辑、下架、重新发布和软删除本人文章，管理员只能下架/恢复公开文章，不能读取他人草稿或编辑/删除投稿。
- Markdown 最大 256 KiB；原始 HTML 不生效，外部图片不允许。发布要求标题为 1–200 个 Unicode 字符，并具有可见文本或有效 `asset:<uuid>` 图片。
- 图片只接受 JPEG、PNG、WebP；单文件不超过 10 MiB、宽高各不超过 8192、像素不超过 4000 万；每篇最多 20 张/50 MiB，每用户默认 1 GiB且最多 20 个待确认资产。
- I2 保存原始图片字节，**不会剥离 EXIF 或其他元数据**。上传前应自行移除位置、设备等隐私信息。超过 24 小时未确认、超过 7 天未绑定的图片会由 Worker 清理；文章下架不删除图片，软删除会立即撤销读取并进入可重试对象清理。

### 认证与 MinIO 降级矩阵

| 条件 | 匿名 RSS/latest | 用户纯文本投稿 | `/me/*`、`/admin/*` | 图片上传/确认/字节读取 | `/readyz` |
| --- | --- | --- | --- | --- | --- |
| `auth.enabled=false` | 可用 | 不提供 | 不注册（404） | 匿名图片读取路由存在；无本人上传路由 | 只以 PostgreSQL 等核心依赖判断 |
| 认证开启、MinIO 正常 | 可用 | 可用 | 按身份/RBAC 可用 | 可用 | 可用 |
| 认证开启、MinIO 未配置或故障 | 可用 | 无图片引用时可用 | Source 与文章文本操作可用 | `503 ASSET_UNAVAILABLE` | 不仅因 MinIO 故障失败 |

MinIO Bucket 必须私有；匿名图片只能经 API 实时核对“当前公开修订引用”后流式读取。预签名 URL 仅用于 15 分钟直传，不能作为公开读取契约。对象存储的三个地址概念不可混用：`assets.endpoint`/`VELIS_ASSET_ENDPOINT` 是 API 与 Worker 使用的内部 `host:port`，`assets.upload_endpoint`/`VELIS_ASSET_UPLOAD_ENDPOINT` 是浏览器可达的完整 HTTP(S) origin，`assets.web_origin`/`VELIS_ASSET_WEB_ORIGIN` 是 Bucket CORS 允许发起上传的精确前端来源。本地 Compose 分别使用 `minio:9000`、`http://localhost:9000` 和 `http://localhost:5173`。

启用对象存储后公共上传端点为必填项，这是一次有意的配置兼容性变更：已有部署升级前必须补充该字段，系统不会回退到内部 DNS 名称，也不会从请求 Host/Origin 推导。生产应使用客户端可解析、可路由且证书有效的 HTTPS 资产入口；反向代理必须原样保留签名时使用的 Host，否则 AWS Signature V4 校验会失败。公共端点不得包含用户信息、查询、片段或路径前缀。

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

当前限制：**没有修改密码、找回密码或注销账户的能力**，也没有通过 HTTP 管理账户角色/状态的接口；I2 的管理员 HTTP 仅覆盖文章下架/恢复和 Source 管理。忘记密码只能由运维人员在确认身份、取得授权并具备备份的前提下按例外流程处置。

## 配置

配置顺序：内置默认值 → YAML → `VELIS_*` 环境变量 → 启动校验。配置入口见 [config.example.yaml](backend/configs/config.example.yaml) 和 [config.go](backend/internal/infrastructure/config/config.go)。

示例仅供本地开发；真实数据库地址、密码、Token 和模型密钥通过环境变量注入，不提交到仓库。宿主机连接配置应与 Compose 中 PostgreSQL 的实际账户和数据库匹配。

I2 常用环境变量包括 `VELIS_ASSET_ENDPOINT`、`VELIS_ASSET_UPLOAD_ENDPOINT`、`VELIS_ASSET_BUCKET`、`VELIS_ASSET_ACCESS_KEY`、`VELIS_ASSET_SECRET_KEY`、`VELIS_ASSET_USE_TLS`、`VELIS_ASSET_WEB_ORIGIN`、`VELIS_IDEMPOTENCY_RETENTION` 和 `VELIS_FEED_PROXY_URL`。完整上限及默认值见示例 YAML；对象存储凭据和含凭据的代理 URL 不得提交或写入日志。

## 数据迁移与回滚

`000004_unify_content_supply` 会保留历史 RSS 文章 ID，把旧正文回填为 revision 1，并以原 `discovered_at` 固定 `published_at`。迁移前应备份并在专用环境核对文章数、ID、状态、修订和 latest 抽样。

`000005_sort_latest_by_source_time` 将公开文章的 latest 索引替换为 `(COALESCE(source_published_at, published_at), id)` 倒序表达式索引；down migration 只恢复旧索引，不修改文章数据。应用回滚时应先回滚 API，再回滚该迁移，避免查询排序与索引语义不一致。

down migration 受保护：只有数据库仍是“单修订 RSS、无投稿、无资产”的旧模型可表达状态时才允许回退。只要存在用户投稿、资产或第二修订，down 会在事务内明确失败。不要使用 force 或删除数据绕过保护；此时应保持数据库前滚并修复/回滚应用。

## 验证与开发现状

```bash
make check
cd backend && GOCACHE=/tmp/feedvelis-go-cache go vet ./...
```

`make check` 包含 gofmt、Go 单测/竞态/构建、Vitest 与 Web 类型检查/构建；其中 gofmt 会修改未格式化的 Go 文件。独立命令见 [Makefile](Makefile)。CI 另外执行 `go vet`，详见 [ci.yml](.github/workflows/ci.yml)。

- 已有 Domain/Application、抓取解析清洗、Hertz、CLI、配置、架构依赖与前端测试；账户领域、认证用例、HTTP 中间件、安全适配器与前端会话模块都有单测。
- PostgreSQL 集成测试通过 `VELIS_TEST_DATABASE_URL` 启用，要求已迁移的专用 `_test` 数据库（测试基座会自动应用迁移）；测试会清空 Source/Article 与账户相关表。未设置该变量时跳过，普通 CI 通过不能代替数据库集成验证。
- 真实 MinIO 测试通过 `VELIS_TEST_MINIO_ENDPOINT`、`VELIS_TEST_MINIO_UPLOAD_ENDPOINT`、`VELIS_TEST_MINIO_ACCESS_KEY`、`VELIS_TEST_MINIO_SECRET_KEY`、`VELIS_TEST_MINIO_BUCKET` 启用；Compose CORS 测试还要求 `VELIS_TEST_MINIO_WEB_ORIGIN`。内部与公共测试端点应使用不同 authority（例如 `127.0.0.1:9000` 与 `http://localhost:9000`）；未设置内部端点时测试会明确报告跳过，不能计作通过。
- [I2 可复现闭环脚本](web/e2e/README.md) 会在本地/`.test` API 创建临时用户、文章和 Source，验证用户直发、RSS 抓取、联合 latest、编辑冲突和作者/管理员下架；它不是生产脚本。
- 认证相关计数与耗时以结构化日志字段落地：认证请求为 `operation`/`result`/`request_id`（确认身份后附 `user_id`/`session_id`），密码散列为 `operation=password_hash|password_verify` + `duration_ms`，限流为 `code=AUTH_RATE_LIMITED` + `dimension=account|ip`，刷新重放为 `event=refresh_replay`，清理为 `operation=cleanup` + `deleted_*`/`duration_ms`。**当前没有 `/metrics` 端点**，Prometheus 接入在后续阶段。
- 浏览器 Playwright E2E、压测、完整监控告警和故障演练尚未完成；当前 I2 闭环以确定性前端单测、HTTP/集成测试和可复现脚本覆盖，不把它描述为完整浏览器兼容性验证。
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
