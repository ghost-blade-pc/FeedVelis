# Velis Feed

Velis 是一个可自托管的图文 Feed 项目，当前已实现 RSS 自动发布与用户 Markdown 图文投稿的统一内容池、稳定 latest、BM25 文章搜索、站内阅读、私有图片资产、AI 内容增强、管理员 Source 管理，以及账户与会话闭环。后端采用 Go + CloudWeGo Hertz/Eino + PostgreSQL，前端采用 Vue 3 + TypeScript。

本文只描述当前功能、开发现状和运行方式。项目目标、技术决策、开发顺序与验收标准统一见 [Velis Roadmap](<Velis Roadmap.md>)。目录占位、依赖声明和 Compose 服务不代表业务能力已实现。

项目以共享 RSS/用户投稿内容池、AI 内容增强、推荐 Feed 和两层 Agent（对话内即时推荐、后续定时任务写入站内收件箱）为业务主线，同时用于实践可靠异步、搜索缓存、微服务演进、Docker/Kubernetes、可观测性、压测与 Go GC 优化。规划中的技术能力不代表当前已经实现。

## 当前功能与边界

| 能力 | 已实现 | 当前边界 |
| --- | --- | --- |
| 工程 | API、Worker、迁移、管理 CLI 四个入口；四层目录、依赖边界测试、配置、日志、健康检查与 CI | 仍处于早期业务建设阶段 |
| 来源管理 | CLI 及管理员 HTTP/Web 新增、列出、改周期、暂停、恢复、同步抓取与抓取历史；5 分钟至 24 小时来源级周期 | 系统级来源；Feed URL 创建后不可修改，无删除、认证 Feed、自定义请求头或用户订阅 |
| 抓取 | RSS 2.0、Atom、JSON Feed；ETag/Last-Modified、304、租约/fencing、失败退避、运行历史、超时与 5 MiB 限制 | 抓取调度仍由 Worker 直接编排；公开文章事实会原子写入 Outbox；默认忽略通用环境代理 |
| 文章存储 | RSS/用户互斥来源身份、不可变修订、固定站内发布时间、乐观锁、作者与管理员下架优先级 | 用户发布后直接公开，无审核队列、协作编辑或版本历史 UI |
| 阅读 | 匿名联合 latest、`(effective_published_at,id)` 稳定游标、站内详情；RSS 展示 Source/原文，投稿只展示作者稳定 ID/昵称 | RSS 有原站时间时优先排序，缺失时回退固定站内发布时间；新文章插入不提供跨请求数据库快照 |
| 图片资产 | 私有 MinIO 预签名直传、服务端确认、版本引用、额度/格式限制、匿名授权流式读取和孤儿清理 | JPEG/PNG/WebP；不转码、不生成缩略图、不剥离 EXIF；API 承担公开图片下行流量 |
| 账户与鉴权 | 用户名密码注册登录、会话刷新轮换、RBAC、本人资料、登录限流、管理审计与维护 CLI | 默认关闭（`auth.enabled=false`）；无改密/找回/注销或设备会话列表 |
| AI 内容增强 | Eino 有界分层 Workflow、独立 generation/Embedding profile、租约与 fencing、版本化摘要/关键词/主题/向量、补录 CLI、API/Web 降级与指标 | 默认关闭；模型失败不阻塞发布与阅读；向量仅持久化，尚无检索 API |
| 搜索投影与查询 | 每文章唯一收敛槽位、租约与 fencing 的投影 Worker、版本化严格索引模板与读写别名、逐项分类的 Bulk、可恢复的在线重建、切换/回滚/清理 CLI、匿名 BM25 搜索、精确筛选、PIT 游标与 PostgreSQL 可见性复核 | 默认未配置；没有 KNN、查询 Embedding、RRF 或 recommend；索引是可丢弃派生状态，不是事实源 |
| Web | latest/详情、搜索与加载更多、账户闭环、本人文章列表与 Markdown 编辑/预览/图片上传、管理员 Source 页面 | 手动保存，不自动保存/合并；无互动、recommend 或 Agent 界面 |

推荐信号、recommend Feed、Redis 业务缓存、向量检索、对话 Agent 与定时 Agent 均未实现。当前搜索只提供 BM25 与关键词/主题/来源精确筛选。用户级 RSS 订阅、投稿审核、following/hot Feed 和社交功能不在当前范围；导航中的占位页不代表对应能力已实现。

抓取器默认忽略 `HTTP_PROXY`、`HTTPS_PROXY` 与 `ALL_PROXY`，直连时会校验每次 DNS 结果、实际连接、重定向、协议和端口。只有 `VELIS_FEED_PROXY_URL` 会启用专用可信出口代理；此时最终 DNS/IP 安全边界委托给代理，应用无法声称仍能验证最终目标 IP。代理地址可以含凭据，但日志只记录脱敏模式与主机。

核心发布与阅读链路不依赖 MQ、Redis、搜索或模型：PostgreSQL 可用时，RSS、纯文本投稿、latest 和详情即可工作。OpenSearch 未配置或故障时仅搜索返回明确的 `503 SEARCH_UNAVAILABLE`，不会影响这些核心接口或 readiness。MQ 故障时事件留在 Outbox，恢复后 Relay 追赶；模型未配置或故障时 `enhancement` 为 null，Web 回退到原始 excerpt。

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

当前 Compose 仍使用 `pgvector/pgvector:pg17`，初始迁移创建 `vector` 扩展；AI Embedding 已以 PostgreSQL `real[]` 版本化持久化，但没有向量查询实现。OpenSearch 已用于可重建投影和 BM25 查询，后续 KNN/RRF 仍未实现；遗留 pgvector 的迁移清理列入 Roadmap。

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
| RabbitMQ 指标 | <http://localhost:15692/metrics> |
| Worker 指标（容器网络） | `velis-worker:9091/metrics` |
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

AI profile 升级不会自动触发全量费用。管理员必须在精确文章、有限批次、全量候选三种范围中恰好选择一种。可先精确重试单篇，或按有效发布时间从新到旧处理有限批次：

```bash
cd backend
go run ./cmd/velis-admin -config configs/config.example.yaml ai backfill -stage all -mode missing-only -article-id 324 -dry-run
go run ./cmd/velis-admin -config configs/config.example.yaml ai backfill -stage all -mode missing-only -article-id 324
go run ./cmd/velis-admin -config configs/config.example.yaml ai backfill -stage all -mode missing-only -limit 20 -order newest
```

AI 停用一段时间后，可先预览没有 AI 内容增强内容的全部文章，再显式确认全量推进：

```bash
go run ./cmd/velis-admin -config configs/config.example.yaml ai backfill -stage all -mode missing-only -all -dry-run
go run ./cmd/velis-admin -config configs/config.example.yaml ai backfill -stage all -mode missing-only -all -confirm-all
```

`-mode missing-only` 只为没有 AI 内容增强内容的文章补齐：当前没有生成结果的文章（`stage=all` 时连同 Embedding 一起补齐），以及已有摘要、关键词和主题但缺 Embedding 的文章；已有当前 generation 与 embedding 结果的文章不会被全量命令重算；`-article-id` 与 `-limit` 只改变范围，不绕过这一判断。要推进 profile 落后的文章请改用 `-mode outdated-only`。

`-order oldest|newest` 只适用于 `-limit 1..1000`，默认 `oldest`。`-all` 固定命令开始时的文章 ID 快照并在内部短事务分页，只推进异步任务，不在 CLI 进程内调用模型；非 dry-run 必须带 `-confirm-all`。相同目标的 pending/running/retry_wait 任务会跳过，failed 任务可显式重推。全量执行可能产生大量模型费用，应先 dry-run，并检查 Token 预算、错误指标及 generation/Embedding 配置；`stage=all` 重新生成时会同时更新两个目标 profile。

推进 profile 升级后，用下面的查询核对是否仍有文章停留在旧 profile，把两个版本值换成当前部署的取值：

```sql
SELECT s.article_id, s.generation_profile_version, s.embedding_profile_version
FROM velis.ai_current_selections s
JOIN velis.articles a ON a.id = s.article_id AND a.current_revision_id = s.revision_id
WHERE a.status = 'published'
  AND (s.generation_profile_version <> 'generation-v2' OR s.embedding_profile_version <> 'embedding-v2');
```

Compose 部署中执行：`docker compose exec postgres psql -U velis -d velis -c "<上面的 SQL>"`。有结果说明该文章仍是旧 profile，用 `-mode outdated-only` 精确推进；没有结果表示所有公开文章的增强结果都与当前 profile 一致。

## 当前 API

完整字段与错误信封见 [OpenAPI](backend/api/openapi/velis.yaml)。认证端点在 `auth.enabled=true` 时才会注册。

| 方法与路径 | 用途 |
| --- | --- |
| `GET /livez` | 进程存活 |
| `GET /readyz` | 当前依赖就绪状态 |
| `GET /api/v1/ping` | 基础连通 |
| `GET /api/v1/articles?limit=20&cursor=...` | 匿名已发布文章列表；limit 为 1–50 |
| `GET /api/v1/search/articles?q=...&limit=20&cursor=...` | 匿名 BM25 搜索；支持关键词、主题、来源精确筛选与 PIT 分页 |
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

列表返回 `items`、`next_cursor`、`has_more`；latest 只读取 `published`，按 `(effective_published_at,id)` 倒序，其中 RSS 优先使用 `source_published_at`、缺失时回退固定站内 `published_at`，站内投稿使用固定站内 `published_at`。文章来源由 `origin.type=rss|user` 判别。latest 游标为版本 2 且有格式校验，旧排序语义生成的版本 1 游标返回 `INVALID_CURSOR`，调用方应从第一页重新获取；latest 游标尚无 HMAC 签名。搜索游标绑定规范化查询、筛选条件、PIT、排序位置和过期时间，并使用 HMAC-SHA256 防篡改；查询或筛选变化时必须从第一页重新搜索。响应携带 `X-Request-ID`，错误使用统一 `error` 信封（`code`/`message`/`request_id`，无 `details`）。

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

搜索采用独立的局部降级边界：OpenSearch 正常且游标键有效时 `/api/v1/search/articles` 可用；端点未配置、production 缺少游标键或 OpenSearch 查询失败时，仅该接口返回带 `Retry-After` 的 `503 SEARCH_UNAVAILABLE`。latest、详情、投稿、抓取及 `/readyz` 不把搜索作为核心依赖；PostgreSQL 复核失败时搜索同样不返回部分结果。

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

常用环境变量除资产、幂等与 Feed 配置外，还包括 `VELIS_RABBITMQ_URL`、`VELIS_RELAY_*`、`VELIS_CONSUMER_*`、`VELIS_OUTBOX_*` 和 `VELIS_WORKER_METRICS_ADDRESS`。RabbitMQ URL 留空时只禁用 Relay/Consumer，文章事务仍写 Outbox。完整上限及默认值见示例 YAML；对象存储凭据、MQ 凭据和含凭据的代理 URL 不得提交或写入日志。

### AI 内容增强配置

generation 与 Embedding 是两个独立的 OpenAI-compatible profile。某组的 provider/base URL/API Key/model 任一被填写时，其余项必须完整；两组均为空时不装配 AI Worker，也不影响 readiness。API Key 只通过 `VELIS_AI_GENERATION_API_KEY`、`VELIS_AI_EMBEDDING_API_KEY` 注入。

Compose 用户可直接在本地 `.env` 中按 [.env.example](.env.example) 的 AI 区块取消注释并填写；这些参数只传给 `velis-worker`，无需也不应把真实密钥写入 YAML。Embedding 未配置时仍会生成并公开摘要、关键词和主题。

| 配置 | 默认值 | 硬边界/说明 |
| --- | --- | --- |
| generation timeout / stage budget | 30s / 2m | 单次 1s–5m；阶段预算不小于单次且不超过 15m |
| structured output mode | prompt | `prompt|json_object|json_schema`；只在 Provider 明确兼容时启用后两者 |
| output token 参数名 | max_tokens | `max_tokens|max_completion_tokens`；Provider 忽略参数名时输出上限静默失效，需按实测选择 |
| single input / chunk chars | 12000 / 6000 | 1000–100000 / 500–single input，按 Unicode 字符计 |
| Map summary / repair input chars | 800 / 16000 | 64–4000 / 1000–100000，防止中间与纠正输入无界 |
| max chunks / concurrency / calls | 8 / 2 / 9 | 1–64 / 1–8 / 1–65，调用数至少覆盖 map + reduce |
| generation attempts / backoff | 3 / 5s–5m | 尝试 1–5；退避 1s–1h 且有界抖动 |
| output / audit Token | 3072 / 20000 | 输出 64–8192；审计预算不小于输出且最多 1000000 |
| summary / keyword / topic / label | 1000 / 12 / 5 / 64 字符 | summary 最多 4000；关键词 1–12、主题 1–5、标签最多 128 |
| Embedding timeout / stage budget | 20s / 30s | dimensions 启用时必填且为 1–65536；`request_dimensions=false` 时只校验、不发送参数 |
| Embedding input / attempts / audit Token | 12000 / 3 / 10000 | 输入 1000–100000；尝试 1–5；审计预算最多 1000000 |
| Worker lease / poll / batch | 2m / 1s / 8 | lease 10s–10m；poll 100ms–1m；batch 1–100 |

### 搜索投影配置

OpenSearch 是可选依赖。`search.endpoints` 为空时投影 Worker 不装配、API 搜索返回 `503 SEARCH_UNAVAILABLE`：文章发布、RSS 抓取、latest、详情与 AI 增强全部照常工作，只是 PostgreSQL 里保留一份待处理槽位。非开发环境必须使用 HTTPS 并显式提供 `search.username` 与 `search.password`；凭据注入 API 与 Worker，日志只记录脱敏后的协议与主机。

Compose 已内置固定 `opensearchproject/opensearch:3.8.0` 单节点服务，默认把 API 与 Worker 指向它。把 `VELIS_SEARCH_ENDPOINTS` 显式写成空值即可关闭投影与查询而不影响其它组件。

| 配置 | 默认值 | 硬边界/说明 |
| --- | --- | --- |
| endpoints | 空（未配置） | 最多 8 个；不得携带凭据、查询或路径 |
| index_prefix | velis-articles | 2–64 位小写标识；读写别名由它派生为 `-read` / `-write` |
| schema_version | 1 | 1–100；与映射、分析器、维度和投影编码共同构成 schema 身份 |
| embedding_dimensions | 1024 | 1–65536；启用 Embedding profile 时必须与 `ai.embedding.dimensions` 一致 |
| connect / request timeout | 5s / 30s | 1s–1m / 1s–5m |
| query timeout / PIT keep-alive | 3s / 2m | 100ms–30s / 30s–10m；查询超时独立于投影写入超时 |
| candidate batch / request maximum | 100 / 500 | 单批 1–500；单请求最多检查 1–5000 个候选且不得小于单批 |
| bulk_max_items / bytes / document chars | 500 / 5 MiB / 65536 | 1–10000 / 1KiB–64MiB / 1000–4MiB；超限文档单独永久失败 |
| worker lease / poll / batch | 2m / 1s / 20 | lease 10s–10m 且必须大于 request timeout；poll 100ms–1m；batch 1–500 |
| worker attempts / backoff | 5 / 2s–5m | 尝试 1–20；退避 1s–1h |
| rebuild snapshot batch / rollback window / sample | 500 / 24h / 200 | 批 1–10000；窗口 1m–30d；抽样 1–10000 |

搜索游标签名键只能通过 `VELIS_SEARCH_CURSOR_KEY` 注入，值为 Base64，解码后至少 32 字节；YAML 中的同名字段会被忽略。development/test 未设置时会为当前进程生成临时键，重启后旧游标失效；production 未设置时搜索局部禁用。多副本部署必须为所有 API 实例配置同一个持久密钥，否则游标跨实例不可用。

BM25 查询固定使用读别名和可见文档，标题、关键词、主题、摘要、正文的权重依次降低；`keyword`、`topic`、`source_id` 是精确筛选。分页通过 PIT 与 `search_after` 保持快照，PIT 过期或游标无效时返回受控错误，客户端应从第一页重试。OpenSearch 候选始终由 PostgreSQL 批量复核当前公开状态、当前修订和当前 AI 选择后才返回，因此索引延迟或迟到文档不会泄露已下架内容。查询身份只需读别名上的搜索与 PIT 权限，不应授予索引写入或管理权限；当前接口不会执行 KNN、查询 Embedding 或 RRF。

## 搜索投影运维

**OpenSearch 不是事实源。** 文章、修订、公开状态与 AI 当前选择都以 PostgreSQL 为准；索引只是可丢弃的派生投影。删除整个索引不会丢任何业务事实，重建即可恢复。投影写入永远不会反向修改 PostgreSQL。

一次完整的首次初始化与存量重建：

```bash
export VELIS_SEARCH_ENDPOINTS=http://localhost:9200   # Compose 只把 OpenSearch 发布到本机端口
ADMIN="go run ./cmd/velis-admin -config configs/config.example.yaml"

# 1. 建立首个物理索引、读写别名与 schema 身份（幂等，可重复执行）
$ADMIN search index init

# 2. 全量重建：创建候选索引、快照公开文章、追赶增量、校验后停在 validated
$ADMIN search rebuild start

# 3. 查看服务索引、活动重建与最近记录
$ADMIN search rebuild status

# 4. 校验通过后再原子切换读写别名，并打开 24h 回滚窗口
$ADMIN search rebuild cutover

# 5. 回滚窗口结束后清理旧索引（必须显式确认，且只删除精确目标）
$ADMIN search rebuild cleanup -index velis-articles-v1-20260925t120000z-aaaaaa -confirm
```

命令默认读取 `configs/config.example.yaml`；该配置的数据库指向 `localhost:5432`，与 Compose 发布的端口一致。OpenSearch 端点必须显式给出，因为示例配置默认不启用投影。

运维边界：

- `rebuild start` 一次只允许一个活动重建；已有活动重建或回滚窗口未关闭时会明确拒绝。
- `rebuild resume` 从持久化的文章 ID 与 change sequence 水位继续，进程中途退出不会丢进度，也不会重写已完成的批次。
- 校验不通过（公开文档数与候选索引不一致、存在落后投递、抽样身份或内容指纹不符）时**拒绝切换**，当前索引继续服务；失败报告持久化在重建记录上。
- `rollback` 只能在回滚窗口内执行；窗口内 Worker 会同时写新旧两个索引，因此切回不会有落后文档。
- `cleanup` 拒绝删除当前读索引、回滚窗口内的索引、仍被投递引用或前缀未知的索引。回滚窗口到期后它会先停止该索引的投递再删除。
- 观察积压：`velis_search_projection_jobs`、`velis_search_oldest_pending_age_seconds`、`velis_search_lagging_deliveries`、`velis_search_bulk_items_total` 与 `velis_search_rebuild_phase`。
- 失败投递超过自动尝试上限后进入 `failed` 并停止自动重试；目标再次变化会自动重新激活，也可以用 `search retry -limit <1..1000> [-article-id <id>] [-index <物理索引>]` 有界重试，命令输出可审计报告。
- 单篇文章的永久失败（例如映射错误）只影响该篇，不会阻塞同批其它文档。
- 应用回滚时先停投影 Worker；旧应用忽略新增表，文章主链路继续可用。不要因应用回滚执行 down migration 或删除 OpenSearch 索引。

## 数据迁移与回滚

`000004_unify_content_supply` 会保留历史 RSS 文章 ID，把旧正文回填为 revision 1，并以原 `discovered_at` 固定 `published_at`。迁移前应备份并在专用环境核对文章数、ID、状态、修订和 latest 抽样。

`000005_sort_latest_by_source_time` 将公开文章的 latest 索引替换为 `(COALESCE(source_published_at, published_at), id)` 倒序表达式索引；down migration 只恢复旧索引，不修改文章数据。应用回滚时应先回滚 API，再回滚该迁移，避免查询排序与索引语义不一致。

`000006_create_reliable_article_async` 创建 Outbox、消费 Inbox 与异步任务槽位。空表时可下迁移；只要存在事件、消费记录或任务，down 会拒绝执行。应用回滚前先停 Relay/Consumer、确认并备份这些表；不得用 force 或删数据绕过保护。已发布 Outbox 默认保留 7 天并由 Worker 分批清理，未发布事件不会被清理。

`000007_add_ai_content_enrichment` 扩展任务阶段、租约、重试与完成状态，并创建不可变 generation/Embedding 结果、独立 current 指针和模型调用审计表。回滚前先停 AI Worker；只要存在增强结果、调用记录或任务已进入新状态，down 会明确拒绝。向量保存为 `real[]`，此阶段没有向量查询或索引。

`000008_harden_ai_provider_acceptance` 增加同一任务 generation 跨重试共享的单次格式纠正额度，并为模型调用审计补充结构化输出模式与低基数安全原因。只要已经使用纠正额度或存在新调用类型/审计字段数据，down 会拒绝；应用回滚时应保留该迁移和审计事实。

`000009_add_search_projection` 创建搜索投影槽位、按物理索引的投递、索引服务状态与重建记录，并建立一个全局 change sequence。down 只在投影槽位、投递与重建记录全为空、且回滚窗口已关闭时允许执行，避免静默丢失诊断状态。应用回滚时先停投影 Worker；旧应用忽略这些表，不要用 force 绕过保护。

down migration 受保护：只有数据库仍是“单修订 RSS、无投稿、无资产”的旧模型可表达状态时才允许回退。只要存在用户投稿、资产或第二修订，down 会在事务内明确失败。不要使用 force 或删除数据绕过保护；此时应保持数据库前滚并修复/回滚应用。

## 验证与开发现状

```bash
make check
cd backend && GOCACHE=/tmp/feedvelis-go-cache go vet ./...

# 专用 _test PostgreSQL 与 RabbitMQ 均已启动时
VELIS_TEST_DATABASE_URL='postgres://velis:velis@localhost:5432/velis_test?sslmode=disable' \
VELIS_TEST_RABBITMQ_URL='amqp://velis:开发密码@localhost:5672/velis' \
make integration-async

# OpenSearch 模板/Bulk/别名、BM25/PIT 契约测试，以及 PostgreSQL + OpenSearch 搜索链路测试
VELIS_TEST_OPENSEARCH_URL='http://127.0.0.1:9200' make integration-opensearch
VELIS_TEST_DATABASE_URL='postgres://velis:velis@localhost:5432/velis_test?sslmode=disable' \
VELIS_TEST_OPENSEARCH_URL='http://127.0.0.1:9200' make integration-search
```

`make check` 包含 gofmt、Go 单测/竞态/构建、Vitest 与 Web 类型检查/构建；其中 gofmt 会修改未格式化的 Go 文件。独立命令见 [Makefile](Makefile)。CI 另外执行 `go vet`，详见 [ci.yml](.github/workflows/ci.yml)。

- 已有 Domain/Application、抓取解析清洗、Hertz、CLI、配置、架构依赖与前端测试；账户领域、认证用例、HTTP 中间件、安全适配器与前端会话模块都有单测。
- PostgreSQL 集成测试通过 `VELIS_TEST_DATABASE_URL` 启用，要求已迁移的专用 `_test` 数据库（测试基座会自动应用迁移）；测试会清空 Source/Article 与账户相关表。未设置该变量时跳过，普通 CI 通过不能代替数据库集成验证。
- OpenSearch 集成测试通过 `VELIS_TEST_OPENSEARCH_URL` 启用，覆盖严格模板、Bulk/别名、BM25 字段权重、精确筛选、稳定排序和 PIT 快照；`make integration-search` 同时要求专用 PostgreSQL，覆盖候选批量复核、下架过滤和跨适配器链路。两个 Make 目标缺少对应变量时都会直接失败，不把 skip 视为通过。
- 可靠异步真实依赖测试还要求 `VELIS_TEST_RABBITMQ_URL`；`make integration-async` 在任一变量缺失时直接失败。Broker 重启演练会真实重启容器，默认跳过，需按 [可靠异步验证说明](backend/test/integration/reliable_async.md) 单独执行。
- 推荐的新部署 profile 示例为 generation `generation-v2`（Workflow `hierarchical-v2`、Prompt `summary-v2`）与 Embedding `embedding-v2`（输入 `retrieval-document-v1`、固定维数模型示例为 1024 维）。profile 版本变化不会自动全量重算，应通过精确、有限批次或经确认的全量 backfill 显式推进。
- 真实 MinIO 测试通过 `VELIS_TEST_MINIO_ENDPOINT`、`VELIS_TEST_MINIO_UPLOAD_ENDPOINT`、`VELIS_TEST_MINIO_ACCESS_KEY`、`VELIS_TEST_MINIO_SECRET_KEY`、`VELIS_TEST_MINIO_BUCKET` 启用；Compose CORS 测试还要求 `VELIS_TEST_MINIO_WEB_ORIGIN`。内部与公共测试端点应使用不同 authority（例如 `127.0.0.1:9000` 与 `http://localhost:9000`）；未设置内部端点时测试会明确报告跳过，不能计作通过。
- [I2 可复现闭环脚本](web/e2e/README.md) 会在本地/`.test` API 创建临时用户、文章和 Source，验证用户直发、RSS 抓取、联合 latest、编辑冲突和作者/管理员下架；它不是生产脚本。
- 2026-09-22 使用专用 `_test` 数据库和真实 MinIO 完成 I2 全依赖回归：PostgreSQL 集成套件、MinIO 私有 Bucket/三种图片格式/流式读取与删除测试，以及上述 8 步 HTTP 闭环均通过；临时 API、数据库和测试对象已在验证后清理。
- 认证相关计数与耗时继续使用结构化日志；异步投影日志使用 `trace_id`、`event_id`、`task_id`、`worker_id` 串联，错误限长并脱敏。
- API 与 Worker 均提供独立的内部 `/metrics`；搜索指标仅使用固定 result/stage/PIT 标签，并记录请求、耗时、候选、PostgreSQL 过滤、扫描上限与 PIT 生命周期。Prometheus 同时抓取 API、Worker 与 RabbitMQ。Grafana 只有 Compose 入口，尚无正式仪表盘或告警规则。
- 浏览器 Playwright E2E、压测和完整监控告警尚未完成；可靠异步故障演练范围与证据见单独说明，不把它描述为生产级灾备验证。

文档中的“已实现”依据当前代码，不代表每次文档更新都重新执行了运行验证。I1 及更早的详细 change 验证保存在只读的 `code_copilot/changes/`；后续规范与 change 统一使用 `openspec/`。

## 文档分工

- 本 README：当前功能、限制、启动与测试。
- [Velis Roadmap](<Velis Roadmap.md>)：唯一项目目标、架构决策和后续实施路线。
- `AGENTS.md`、`CLAUDE.md` 与 `openspec/`：当前协作入口、规范、change 与归档，不另立产品规划。
- `code_copilot/`：迁移前的历史 change 与证据，只读保留，不再作为执行入口。
- OpenAPI、迁移和测试目录说明：对应实现的技术契约与局部使用说明。
- [第三方声明](backend/THIRD_PARTY_NOTICES.md)：随应用发布的第三方组件、版本与许可证。

仓库暂未添加开源许可证，默认保留全部权利。
