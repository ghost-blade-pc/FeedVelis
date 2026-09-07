# 变更规格 — 文章展示基础能力与库表设计

> change id、profile 和生命周期状态只读取同目录 `card.md`，本文件不复制这些字段。

## 1. 背景、目标与非目标

- 已确认方向：第一版是系统级 Feed 阅读器，不绑定用户。流程为“通过本地 CLI 添加 Feed 地址 → 定时抓取 RSS 2.0、Atom 或 JSON Feed → 解析去重 → PostgreSQL 保存 → Web 列表 → 点击跳转原站”。
- 可观察目标：至少一个真实 Feed 能被安全抓取并稳定展示；连续抓取不重复，内容未变不更新，失败可退避重试。
- Article 模块基础职责：接收已标准化的外部条目、校验业务字段、按 `(source_id, dedupe_key)` 幂等写入、识别内容变化、提供只读列表。
- Source 模块相邻职责：Source 身份与抓取状态、URL 安全、条件请求、调度/退避；它不拥有文章正文生命周期。
- 本次不包含：`users`、`subscriptions`、内部文章、站内详情、互动、推荐、搜索、向量、缓存、消息系统、图片上传。

## 2. Research 证据

| 事实 | 证据 | 对设计的影响 |
|---|---|---|
| Article、Source、Fetcher、Scheduler 当前均为占位 | `backend/internal/{domain,application}/article/doc.go`、`domain/source/doc.go`、`infrastructure/fetcher/httpfeed/doc.go`、`interfaces/scheduler/doc.go` | 可按明确边界建立首个纵向闭环，无旧业务兼容负担。 |
| 数据库只有 `velis` schema | `backend/migrations/000001_initialize_platform.up.sql` | Source/Article 必须走新版本化迁移，SQL 显式使用 `velis.`。 |
| API 仅有健康接口 | `backend/api/openapi/velis.yaml` | Source 写入口和文章列表都是新增外部契约。 |
| Web `/latest` 仍为健康占位 | `web/src/router/index.ts`、`web/src/views/HomeView.vue` | 可复用 `/latest` URL，替换为真实文章列表。 |
| 连接池未设置 `search_path` | `backend/internal/infrastructure/persistence/postgres/pool.go#Open` | Repository SQL 不依赖隐式 schema。 |
| 用户此前明确首版不做用户、推荐和站内详情 | 2026-09-04 RSS Article MVP 讨论记录；`AGENTS.md` 当前要求开发文档只作参考并与开发者沟通 | 本提案按 RSS 列表闭环校正，不恢复内部 Markdown/Article Detail 范围。 |

## 3. 功能与验收标准

### F1 — 管理系统级 Source

- [ ] 通过 `velis-admin source add --url <URL>` 添加一个 HTTP(S) Feed URL，经规范化后以 `normalized_feed_url` 唯一保存；重复添加幂等返回已有 Source ID 并以退出码 0 结束。
- [ ] Source 可处于 `active / paused / degraded`；无用户和 `subscriptions` 表。
- [ ] 管理面只提供本地 CLI：`source add/list/pause/resume/fetch`；本 change 不提供 Source HTTP 写 API。

### F2 — 安全抓取与解析

- [ ] Worker 认领到期 Source，带 ETag/Last-Modified 请求；304 只更新时间，不解析条目。
- [ ] 每次 DNS 解析和重定向跳转都拒绝环回、链路本地、私网和云元数据地址；限制协议、端口、重定向次数、连接/总超时与响应体大小。
- [ ] 解析 RSS 2.0、Atom 与 JSON Feed；畸形文档失败但不破坏旧文章。响应和完整正文不写日志。

### F3 — 幂等保存与内容更新

- [ ] `dedupe_key` 优先取 GUID/Atom ID 的 SHA-256，否则取规范化 canonical URL 的 SHA-256；两者都缺失时跳过并记录稳定原因码。
- [ ] 一个事务内 upsert Article 与 ArticleContent；`content_hash` 未变时只更新 Source 检查状态，不触碰 Article `updated_at`。
- [ ] 同一 identity 内容变化时更新标题、作者、摘要/正文、上游更新时间和 content_hash，不创建新 article id。
- [ ] 原始摘要最多保存 256 KiB、原始正文最多保存 1 MiB；超限时在合法 UTF-8 边界截断并记录 truncation 标志，同时保留 Article 元数据。

### F4 — 文章列表 API

- [ ] `GET /api/v1/articles?cursor=&limit=`，默认 limit 20、范围 1–50。
- [ ] 返回 `{items, next_cursor, has_more}`；按 `(sort_at DESC, id DESC)` keyset 分页，读取 `limit + 1` 判断下一页。
- [ ] Item 包含 id、title、canonical_url、source、author_name、excerpt、source_published_at、discovered_at；不返回 raw/sanitized 正文、content_hash 或抓取字段。
- [ ] 非法/版本不支持的游标返回 `INVALID_CURSOR`，数据库失败返回稳定错误且不泄露 SQL。

### F5 — Web 最新文章

- [ ] `/latest` 提供加载、成功、空、失败与重试状态；卡片按 API 顺序展示。
- [ ] 标题使用新窗口跳转 `canonical_url`，并设置 `noopener noreferrer`；摘要以文本节点渲染，不使用 `v-html`。
- [ ] 分页不重不漏；页面不展示伪造的点赞、推荐或用户态。

## 4. 规则与边界

- Source 是系统级订阅事实；首版没有用户所有权，只能通过本地管理 CLI 修改。
- Article 首版只有 `origin=external`，不预留无 FK 的 `author_user_id`；内部文章在 Account 存在后通过增量迁移加入。
- `source_id + dedupe_key` 是外部文章稳定身份；canonical URL 可变化但不能单独作为数据库主键。
- 上游未返回发布时间时保留 `source_published_at = NULL`；`discovered_at` 保存首次收录时间，`sort_at` 固定为 `COALESCE(source_published_at, discovered_at)`；UI 分别显示“发布于”或“收录于”。
- Feed 中某条旧文章消失不等于文章被删除，只更新本次仍出现条目的 `last_seen_at`。Source 的删除意图映射为 pause，不级联删除文章；物理清理由未来独立管理操作负责。
- 列表只读 `articles` 元数据，不 join `article_contents`。
- RabbitMQ、Outbox、Redis 不参与首版正确性；Worker 直接调用 Application Service。

## 5. 编码前契约门禁

### 5.1 API、CLI 与 UI

- 文章读取：`GET /api/v1/articles?cursor=&limit=`，匿名只读；成功字段为 snake_case，所有响应带 `X-Request-ID`。
- Source 写入：新增本地 `velis-admin` CLI，至少支持 `source add/list/pause/resume/fetch`；通过现有 YAML + `VELIS_*` 数据库配置访问 PostgreSQL，不提供 Source HTTP 写 API。
- 游标：Base64URL 编码版本化 JSON，至少包含 `v`、`sort_at`、`article_id`；客户端不解析或构造 SQL 字段。
- 本次没有 `/articles/{id}`，点击标题直接去原站。

### 5.2 数据、schema 与配置

#### `velis.sources`

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | `bigint GENERATED ALWAYS AS IDENTITY` | 主键。 |
| `feed_url` | `text` | 用户登记的可请求 URL，应用层最多 4096 bytes；首版不因重定向自动改写。 |
| `normalized_feed_url` | `text` | 唯一业务键。 |
| `site_url` | `text NULL` | Feed 声明的站点 URL，仅 HTTP(S)。 |
| `title` | `varchar(500)` | 来源展示名，最多 500 Unicode 字符，可由 Feed 元数据更新。 |
| `status` | `varchar(16)` | `active/paused/degraded` CHECK。 |
| `etag` / `last_modified` | `text NULL` | 条件请求值，各最多 1024 bytes。 |
| `next_fetch_at` | `timestamptz` | 到期调度索引。 |
| `last_checked_at` / `last_success_at` | `timestamptz NULL` | 抓取状态。 |
| `consecutive_failures` | `integer` | 非负，成功归零。 |
| `last_error_code` | `varchar(64) NULL` | 稳定分类，不存任意错误正文。 |
| `lease_owner` / `lease_expires_at` | `varchar(128)/timestamptz NULL` | 多 Worker 租约；两者同时空或同时非空。 |
| `created_at` / `updated_at` | `timestamptz` | UTC。 |

关键约束/索引：`UNIQUE(normalized_feed_url)`；调度部分索引 `(next_fetch_at, id) WHERE status='active'`；CHECK 保证失败计数和租约字段一致。首版不建 `subscriptions`。

#### `velis.articles`

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | `bigint GENERATED ALWAYS AS IDENTITY` | 已确认主键。 |
| `source_id` | `bigint` | FK → sources，`ON DELETE RESTRICT`。 |
| `dedupe_key` | `char(64)` | GUID/ID 优先、URL 兜底的 SHA-256。 |
| `source_item_id` | `text NULL` | 保存上游 GUID/Atom/JSON Feed ID 供诊断，最多 2048 bytes。 |
| `canonical_url` | `text` | 用户点击的 HTTP(S) 原文 URL，最多 4096 bytes。 |
| `title` | `varchar(500)` | 非空；缺失使用“未命名文章”，超长按 Unicode 字符安全截断。 |
| `author_name` | `varchar(300) NULL` | 上游展示作者，最多 300 Unicode 字符，不等同站内用户。 |
| `excerpt` | `text` | 从 plain_text 生成，最多 1000 Unicode 字符。 |
| `language` | `varchar(16)` | 缺失用 `und`。 |
| `source_published_at` | `timestamptz NULL` | 上游事实，不伪造。 |
| `discovered_at` | `timestamptz` | 首次收录时间，不随更新改变。 |
| `sort_at` | `timestamptz GENERATED ALWAYS AS (COALESCE(source_published_at, discovered_at)) STORED` | 稳定游标排序时间。 |
| `source_updated_at` | `timestamptz NULL` | 上游更新时间。 |
| `content_hash` | `char(64)` | 规范化业务内容哈希。 |
| `status` | `varchar(16)` | 首版 `published/hidden` CHECK；不实现物理删除状态机。 |
| `last_seen_at` | `timestamptz` | 最近一次在响应中看到，不用于自动删除。 |
| `created_at` / `updated_at` | `timestamptz` | 本地事实时间。 |

关键约束/索引：`UNIQUE(source_id, dedupe_key)`；`(sort_at DESC, id DESC) WHERE status='published'`；`(source_id, last_seen_at DESC)`。首版不提供 Source/Article 物理删除命令，Source 删除意图只执行 pause，外键明确使用 `ON DELETE RESTRICT`。

#### `velis.article_contents`

| 字段 | 类型 | 说明 |
|---|---|---|
| `article_id` | `bigint` | PK + FK → articles；物理清理时显式 `ON DELETE CASCADE`。 |
| `raw_description` | `text NULL` | Feed 原始摘要，最多保留 256 KiB。 |
| `raw_content` | `text NULL` | Feed 原始正文，最多保留 1 MiB。 |
| `raw_description_truncated` / `raw_content_truncated` | `boolean` | 标识原始字段是否因上限截断。 |
| `sanitized_html` | `text NULL` | 对 content 优先、description 兜底执行 allowlist 清理后的派生物；本次列表不返回。 |
| `plain_text` | `text` | 摘要、后续搜索/Embedding 的可重建输入。 |
| `sanitizer_version` | `integer` | 首版为 1，策略升级时支持重建。 |
| `created_at` / `updated_at` | `timestamptz` | UTC。 |

正文分表保证列表不搬运大字段。HTML allowlist v1 只保留 `p/br/h1-h6/strong/em/blockquote/code/pre/ul/ol/li/a`；链接只允许 HTTP(S)，统一增加 `nofollow noopener noreferrer`；拒绝 `style/class/id`、事件属性、图片、SVG、iframe、表单和媒体。`plain_text` 从清理后的结构生成，`excerpt` 取前 1000 个 Unicode 字符。首版不建 tags、stats、search_documents、embedding 或 revision 表。

#### 迁移与回滚

- 新 migration 创建 Source → Article → ArticleContent，down 反序删除。
- 空表可 round-trip；存在文章后生产回滚优先回退应用并保留表，删除表前必须备份和人工确认。
- 不提交演示文章；集成测试在事务/独立测试库写 fixture。

### 5.3 抓取、重试与并发

- URL 规范化：要求绝对 URL、无 userinfo；scheme/host 小写、IDNA 转 ASCII、删除 fragment 和默认端口、清理 dot-segment、空 path 归一为 `/`，保留 query 的键值与顺序，不激进重写 percent-encoding。重定向不自动覆盖已登记 `feed_url`。
- 网络限制：只允许 HTTP(S) 且端口限 80/443；连接超时 5s、总超时 20s、最多 5 次重定向、解压后响应体最多 5 MiB、单次最多处理 500 条；DNS 结果与每一跳地址都重新校验。
- Worker 每 30 分钟抓取正常 Source，每批使用 `FOR UPDATE SKIP LOCKED` 认领最多 20 个并写入 2 分钟数据库租约；租约到期可被其他 Worker 恢复。
- 失败退避为 1m × 2^n、上限 6h、±20% 抖动；连续 5 次失败进入 degraded 并停止自动认领，`source fetch` 可人工探测，成功后恢复 active 并清零失败计数。
- 新 Source 初始为 active 且立即到期；`source pause` 转为 paused 并清空租约，`source resume` 转为 active 且立即到期；paused 只能人工 resume。Source 不提供 delete，Article 新增时为 published；hidden 仅作为受控数据修复状态，不在本 change 提供切换命令。
- 单次抓取对同一 Source 串行；数据库唯一约束仍是最终并发防线。
- HTTP 304 不解析、不更新 Article `last_seen_at`；200 才处理条目。
- 本 change 不发消息，不需要 Inbox/Outbox。

### 5.4 去重、更新与异常条目默认规则

- `dedupe_key = SHA-256("id\0" + trim(source_item_id))`；ID 缺失时使用 `SHA-256("url\0" + normalized_canonical_url)`。ID 区分大小写；ID 和 URL 都缺失则跳过该条目。
- `content_hash` 对固定字段结构编码后做 SHA-256，字段为 title、normalized canonical URL、author、language、source_published_at，以及按上限保留后的 raw description/raw content；统一无效 UTF-8、CRLF 和首尾空白，不包含 source_updated_at、抓取时间、last_seen_at、ETag 或 Source 状态，因此数据库内容可重算同一哈希。
- canonical URL 缺失或非 HTTP(S) 时跳过条目；Article 标题缺失使用“未命名文章”，Source 标题缺失使用规范化 host；单条无效不回滚同批其他合法条目，返回稳定原因码和计数。
- content_hash 未变时允许更新 `last_seen_at` 与上游非内容元数据，但不更新 Article `updated_at` 或正文。

### 5.5 安全与不可逆操作

- Fetcher 负责 URL/DNS/重定向每跳校验，Parser 不自行联网。
- 只允许 HTTP(S) canonical URL；Web 不渲染上游 HTML，摘要走文本节点。
- 日志不记录完整 URL query、响应体、正文或上游任意错误文本；使用 source_id、稳定 error_code、status、latency。
- Source 管理入口、删除 Source/Article 和含数据的 down migration 是人工确认点。

### 5.6 列表响应默认契约

- 成功响应为 `{items, next_cursor, has_more}`；最后一页 `next_cursor=null`。
- `items[].source = {id, title, site_url}`；时间为 RFC 3339 UTC，`source_published_at` 可为 null，`discovered_at` 必有值；不返回内部 `sort_at`。
- `limit` 默认 20、最小 1、最大 50；游标 Base64URL JSON 为 `{v:1, sort_at, article_id}`。
- 稳定错误码至少包括 `INVALID_ARGUMENT`、`INVALID_CURSOR`、`INTERNAL_ERROR`；CLI 重复 add 是幂等成功，不映射为冲突。

## 6. 实现方案与任务映射

- 设计图：见 `diagrams.md`。用例图、业务流程图、ER 图、流程时序图和后端四层实现架构图及分层改动清单均已确认。
- 开发者已确认架构清单的颗粒度为“包 + 主要组件/接口 + 职责 + 新增/修改”；Proposal 不锁定具体 `.go` 文件、结构体字段或函数签名，文件级拆分留给 `tasks.md` 细化和 Apply。
- Domain：`domain/source` 承担 Source 状态、租约与退避不变量并声明 Source Repository；`domain/article` 承担外部身份、内容哈希、更新判定和 Article/Content 一致持久化抽象。
- Application：`application/source` 提供 Source 管理、到期认领与单 Source 抓取编排；`application/article` 提供外部条目入库和文章列表；`application/ports` 隔离 Fetcher、Parser、Sanitizer、事务与时钟。
- Infrastructure：`persistence/postgres` 实现 Source/Article Repository、事务、租约和列表查询；`fetcher/httpfeed` 实现安全抓取、三种 Feed 解析与 HTML 清理；`clock`、`observability` 提供时间和受限诊断能力。
- Interfaces：`interfaces/cli`、`interfaces/scheduler` 和 Hertz Handler 只做协议适配并调用 Application，不直接访问 PostgreSQL 或第三方库。
- Composition Root：Bootstrap 负责 API、Worker、Admin 的构造注入和生命周期；新增 `velis-admin` 入口，迁移/OpenAPI/配置作为配套契约同步修改。
- Web：`features/article` 查询并渲染文本卡片，位于后端四层之外。
- 依赖约束：Repository 抽象随聚合放在 Domain，技术端口放在 Application；`application/source` 可调用 `application/article` 入库用例完成跨模块编排；Infrastructure 只实现内层抽象；Bootstrap 不承载业务规则。
- 已确认采用的依赖边界：`mmcdole/gofeed` 解析 RSS 2.0、Atom 与 JSON Feed（MIT；当前候选 v1.4.2），`microcosm-cc/bluemonday` 做 HTML allowlist 消毒（BSD-3-Clause；当前候选 v1.0.26）。Apply 开始时重新核对 Go 1.26/API 兼容、传递依赖和已知漏洞；版本复核是实施检查，不重新扩大功能范围。当前未安装、未做专业安全审计。
- 不自研通用 Feed parser 或 HTML sanitizer；通过 Infrastructure 接口隔离，便于替换。Hertz Client 复用现有依赖。

## 7. 测试与验证策略

- P0：URL/SSRF/重定向/大小/超时、RSS 2.0/Atom/JSON Feed 解析、GUID/URL 去重、content_hash 更新、304、数据库约束、租约并发、稳定分页、Web 外链安全。
- P1：错误退避/degraded 恢复、畸形 XML、超长字段、取消请求、迁移 up/down/up、查询计划、无 N+1 Source 查询。
- 兼容性样本：仓库固定 RSS 2.0/Atom/JSON Feed fixture 各至少一组；本地全链路再选三种格式的受控真实公开 Feed 各至少一个。性能 fixture 使用 100 个 Source、100,000 篇 Article 验证游标查询计划，不在未知硬件上预设生产 SLA。
- 回归：targeted Go tests、真实 PostgreSQL integration、`go test -race -count=1 ./...`、`go vet ./...`、Go build、Vitest、Web build、OpenAPI 校验。
- 不声称：提案阶段未运行应用测试；Mock HTTP 不证明公网兼容；本地 Compose 不证明生产性能或安全审计。

## 8. 风险、发布与回滚

- 风险控制：SSRF 采用网络层 fail-closed；幂等由业务键 + DB 唯一约束双层保证；列表只读小字段；无认证写入口不得公网暴露。
- 发布：先迁移空表，再启用 Source 管理/Worker，最后启用列表 API/Web；先用受控测试 Feed 验证，再接真实 Feed。
- 可观测：记录 fetch result/error_code/latency/item_count、ingest inserted/updated/unchanged、调度 backlog；不使用 URL、标题或正文作为指标标签。
- 回滚：停止 Worker → 回退 Web/API → 保留数据表；确认无数据或有备份后才执行 down。
- 剩余风险：公网真实 Feed 的长尾兼容性、生产网络环境下的 SSRF 防线和容量仍需 Apply/Test 证据；这些是验证风险，不是未决需求。

## 9. 待澄清与确认

### 阻塞

- [x] D1：采用本地 `velis-admin` CLI/受控管理命令，不提供 Source HTTP 写 API。
- [x] D2：保存有限大小的原始摘要/正文，同时保存清理后的 HTML 与纯文本派生物。
- [x] D3：`source_published_at` 可空；`discovered_at` 保存首次收录；`sort_at=COALESCE(...)`；UI 区分“发布于/收录于”。
- [x] D4：Feed 条目消失不删除；只更新仍出现条目的 `last_seen_at`；Source 删除意图先 pause；不级联删除；物理清理延期。
- [x] D5：主键使用 `bigint GENERATED ALWAYS AS IDENTITY`。

### 非阻塞假设

- [x] A1：列表 API 使用 `/api/v1/articles`；未来 Feed 模块复用底层查询，不向本端点加入曝光/推荐字段。
- [x] A2：第一版支持 RSS 2.0、Atom 和 JSON Feed。
- [x] A3：Feed/canonical URL 仅允许 HTTP(S)，拒绝 `file/ftp/data/javascript` 等协议。
- [x] A4：不创建 `source_fetch_logs`；Source 保存当前状态，历史问题先用结构化日志排查。
- [x] E1–E11：开发者授权采用本规格第 5、6、7 节记录的默认工程契约；实现验证可以在不改变外部行为和数据语义的前提下收紧安全限制，任何放宽限制或改变 schema/API 仍需回到 Proposal。

### 已确认的 E 系列默认值

| 编号 | 已确认默认契约 | 规格位置 |
|---|---|---|
| E1 | URL 规范化保留 query 语义，不自动用重定向 URL 覆盖登记地址 | §5.3 |
| E2 | CLI 重复添加 Source 幂等返回已有 ID，退出码 0 | §3 F1、§5.6 |
| E3 | 5s 连接、20s 总超时、5 次重定向、5 MiB 解压后响应、500 条上限、80/443 端口 | §5.3 |
| E4 | 30m 抓取周期、20 个批次、2m 租约、1m–6h 指数退避、±20% 抖动、5 次后 degraded | §5.3 |
| E5 | 无 canonical URL 跳过；缺 Article 标题用“未命名文章”；单条失败不回滚整批 | §5.4 |
| E6 | 固定字段规范化编码后 SHA-256，排除本地抓取状态 | §5.4 |
| E7 | raw 大小上限与截断标志、HTML allowlist v1、纯文本摘要 1000 字符 | §3 F3、§5.2 |
| E8 | Source active/paused/degraded；Article published/hidden；首版无物理删除命令 | §5.3 |
| E9 | 嵌套 source、nullable cursor、RFC 3339 UTC、稳定错误码 | §5.6 |
| E10 | gofeed + bluemonday，经 Infrastructure 适配器隔离，Apply 时复核版本和供应链 | §6 |
| E11 | 三种格式固定 fixture + 各一个受控真实 Feed；100 Source/100,000 Article 查询计划基线 | §7 |

### 用户确认

- 已确认：系统级 Feed 来源、本地 CLI、RSS 2.0/Atom/JSON Feed 抓取、有限原文与派生内容、幂等保存、文章列表和跳转原站；排除用户、互动、推荐和站内详情。
- 确认时间：2026-09-07。
- high-risk 人工确认：开发者逐项确认 D1–D5、A1–A4，并授权 E1–E11 采用默认值。
- Apply 前图示确认：开发者要求先完成四类业务设计图，随后补充后端四层实现架构图与分层改动清单；并确认后者采用“包 + 主要组件/接口 + 职责 + 新增/修改”的颗粒度，不在 Proposal 锁定文件和函数签名。用例图、业务流程图、ER 图、流程时序图和后端四层实现架构图及清单均已确认，Proposal 可以完成为 `ready`；后续 `/apply` 仍需单独授权。

## 10. 实施后同步

- 实际偏差：
- 实际改动：
- 验证摘要：
- Review 结论：
- Deferred：
