# Design

## Context

见 [proposal.md](proposal.md)。当前 `articles` 强制关联 `sources`，正文一对一保存在 `article_contents`，列表使用可变的 `COALESCE(source_published_at, discovered_at)` 排序，RSS 更新会覆盖现有正文；Source 只由 CLI 管理，正常周期硬编码为 30 分钟。认证已经提供数据库当前身份、角色和会话校验，但默认关闭。MinIO 只有 Compose 服务和空适配器目录，现有 Feed 客户端还会隐式尊重通用环境代理。

实现必须保持 Domain/Application/Infrastructure/Interfaces 四层及 bootstrap 装配边界，使用 pgx 显式 SQL和版本化迁移；协议 DTO、SQL、对象存储 SDK 类型和 Markdown/HTML 映射不能进入领域模型。PostgreSQL 是内容、权限和幂等事实来源，I2 不新增 MQ、Redis、OpenSearch 或模型依赖。

## Goals / Non-Goals

**Goals:**

- 用一个文章聚合承载 RSS 与用户稿件，同时保留互斥来源身份和稳定发布时间。
- 让内容修订可被未来 AI/索引任务稳定引用，并让编辑、状态变更和 RSS 更新具备清晰事务边界。
- 在 HTTP 重试、并发标签页、Worker/管理员竞争、MinIO 故障和历史升级下保持权限和数据不变量。
- 让 API、Worker、CLI 和 Web 复用应用用例，而不是绕过领域规则直接操作表。

**Non-Goals:**

- 不提供审核队列、投稿审批、管理员编辑/删除投稿、版本历史 UI 或删除恢复。
- 不提供富文本编辑、自动保存、自动合并、协同编辑、外部投稿图片、缩略图或 EXIF 清理。
- 不提供 Source 删除、Feed URL 修改、认证 Feed、自定义请求头或用户级 RSS 订阅。
- 不实现 Outbox Relay、消息消费者、AI 元数据、搜索、recommend Feed 或缓存。

## Decisions

### 1. 以文章聚合和不可变修订分离身份、状态与内容

新增迁移将数据组织为：

- `articles`：`origin_type`、RSS 身份列或 `author_user_id`、生命周期、固定 `published_at`、`current_revision_id`、`lock_version`、管理员/作者下架信息、删除时间和时间戳。
- `article_versions`：`article_id`、单调 `revision_no`、标题、投稿 Markdown、RSS 原始内容、清洗 HTML、纯文本、摘要、语言、内容哈希、清洗器版本、创建者和时间。
- `article_asset_references`：内容修订与资产的唯一关联。

数据库 CHECK 保证 `rss` 必须有 Source、去重键和原文 URL且没有站内作者，`user` 必须有作者且没有 RSS 身份。`current_revision_id` 外键指向修订；创建聚合、首个修订和回写指针必须处于同一事务。修订号仅在内容哈希变化时递增，`lock_version` 在内容或状态成功变化时递增。

选择不可变版本表而不是继续原地覆盖，是因为后续 AI/索引必须绑定确定内容，且事务切换当前指针能保证已发布编辑失败时旧内容仍可读。选择单一当前指针而不是“工作版本/公开版本”双指针，是因为产品已经确定已发布编辑立即公开；双指针会引入未要求的二次发布流程。

### 2. 状态机和权限由应用用例集中执行

用户文章状态转换如下：

```text
draft ----publish----> published ----author offline----> offline(author)
  |                        |                                  |
  |                        +----admin offline----> offline(admin)
  |                                                           |
  +------------------------- delete <--------------------------+

offline(author) ----publish----> published
offline(admin)  ----admin restore----> published
```

`deleted` 是终态。作者可以编辑任一未删除的本人文章；编辑 `published` 会原子切换公开内容，编辑 `offline(admin)` 只更新当前修订。管理员只能对当前公开文章执行全局下架，并且只能恢复 `offline(admin)`；管理员没有草稿读取、投稿编辑或删除接口。RSS 只允许系统抓取更新内容，管理员管理可见性。

权限检查先基于数据库当前身份和资源归属，再执行状态转换。非所有者读取私有文章统一映射 404；本人因管理员锁定而发布失败映射 403。禁用账户只撤销访问，不隐式修改既有文章。

### 3. 固定 `published_at` 是 latest 的唯一业务时间

用户首次发布和新 RSS 条目首次入库时设置 `published_at`，后续内容更新、下架、恢复均不修改。匿名查询只扫描 `status='published'` 的部分索引，按 `(published_at DESC, id DESC)` 排序，游标升级版本并只编码这两个值。

保留 `/api/v1/articles` 作为 latest，避免为了名称引入重复集合端点。响应使用 OpenAPI `oneOf` 和 `origin.type` 判别 RSS Source/原文信息与用户作者摘要。详情只暴露当前清洗 HTML；作者详情另外暴露 Markdown、状态、修订号、`lock_version` 和资产。

该设计接受新文章插入时跨请求分页不是数据库快照，但固定排序键可避免编辑导致的重复和跳跃。管理员恢复或作者重新发布保留原位置，这是稳定发布时间的预期结果。

### 4. Markdown 渲染与资产解析是服务端受控管线

用户输入先做 UTF-8、大小和标题规范化，再以禁用原始 HTML的 Markdown 解析器生成节点。图片节点只接受 `asset:<uuid>`；应用层批量加载资产并校验所有者、`ready` 状态、文章绑定、数量及总大小，然后由接口适配器生成 `/api/v1/assets/{id}/content` 地址。渲染结果最后进入用户稿件专用白名单清洗器并生成纯文本和摘要。

RSS 保留现有 HTML 解析、绝对 URL 解析与远程图片清洗策略，不强制转成 Markdown。两条管线输出同一修订读取模型，但使用不同原始字段和内容哈希输入。服务端预览复用投稿渲染管线，但不创建修订或资产绑定。

选择服务端渲染而非信任浏览器预览，是为了让最终 HTML、资产引用和校验结果唯一；禁用用户原始 HTML则降低富文本白名单复杂度。

### 5. 幂等记录与聚合事务组合

新增 `idempotency_operations`，键为 `(actor_user_id, operation, key)`，保存规范化请求 SHA-256、状态、版本化业务结果、资源标识、创建/完成/过期时间。应用命令从已解析的语义字段生成确定性摘要，不能对原始 JSON 字节散列。

普通数据库命令按以下顺序在一个事务中执行：

1. 查找或尝试插入幂等身份；并发相同键由唯一约束和行锁串行化。
2. 已成功且摘要相同则重放业务结果；摘要不同返回 `IDEMPOTENCY_KEY_REUSED`。
3. 对既有资源校验 `If-Match` 与当前 `lock_version`。
4. 执行业务变化并写成功结果后一起提交。

校验错误、权限错误、版本冲突和事务错误不固化为成功结果。默认 24 小时后由本地清理任务删除；资源唯一约束和目标状态规则在保留期外继续保护数据，但不保证响应重放。

手动抓取包含网络 I/O，不能保持数据库事务跨越整个请求。它先短事务提交 `pending` 幂等操作、抓取运行和 Source 租约，再执行网络调用，最后用 fencing 在完成事务中提交文章、Source、运行结果和成功幂等结果。相同键查询同一运行；过期租约对应的遗留运行由后续认领/清理标为中止并允许恢复。

接口层统一解析 `Idempotency-Key` 和强 ETag 形式的 `If-Match: "<lock_version>"`，但应用层接收显式值并维护语义，不依赖 Hertz 请求对象。

### 6. 资产使用私有 Bucket、预签名直传和实时授权代理

新增 `article_assets` 保存 UUID、所有者、可为空的绑定文章、固定对象键、`pending|ready|delete_pending|deleted` 状态、可信媒体信息、校验值、额度计数时间和生命周期时间。对象键由服务端生成，不使用原文件名。

创建资产短事务检查用户总额度与 pending 数量并返回 15 分钟预签名直传策略。Bucket CORS 只允许配置的 Web Origin 和必要上传方法/头。确认流程先从 PostgreSQL验证所有权，再用 HEAD 检查实际大小，并通过受限对象流识别 JPEG/PNG/WebP 签名和尺寸；确认事务以行锁保证额度只增加一次。I2 保存原始字节，不转码、不生成缩略图且不剥离 EXIF。

文章修订事务批量锁定所引用资产。未绑定资产原子绑定当前文章；已绑定同一文章可复用；绑定其他文章时拒绝。下架不改变资产。删除文章只在 PostgreSQL 中立即撤销公开授权并标记资产待删除，对象删除由 scheduler 幂等重试，避免伪造 PostgreSQL 与 MinIO 的分布式原子事务。

匿名及作者图片读取均经过 `/api/v1/assets/{id}/content`。仓储一次查询判断“当前公开修订引用”或“当前身份是所有者”，对象适配器随后流式转发 GET/HEAD 并设置可信响应头。I2 不签发匿名下载 URL，避免文章下架后短期 URL仍有效；代价是 API 承担下行带宽，后续必须基于真实负载再优化。

MinIO 客户端初始化和请求失败只映射资产端点 503。`readyz` 继续以 PostgreSQL 为核心依赖，资产存储状态通过结构化日志和降级字段报告，不使整个 API 排流。

### 7. Source 聚合增加周期、乐观锁和抓取运行

`sources` 新增 `fetch_interval_seconds`（默认 1800，约束 300 至 86400）与 `lock_version`。新增 `source_fetch_runs` 保存 UUID、Source、`scheduled|manual` 触发方式、可为空的管理员、状态、租约 generation、开始/完成时间、not-modified、条目统计和稳定错误码，不保存完整响应或凭据。

历史接口使用不透明游标倒序分页，默认 50 条、单页最多 100 条；I2 保留全部运行记录，不在缺少容量证据时引入自动删除策略。

定时调度完成后使用 Source 自身周期计算 `next_fetch_at`。暂停、恢复、修改周期使用 `If-Match`；暂停清除当前租约，因此旧抓取的完成写入会被 fencing 拒绝。Feed URL保持不可变且无删除端点。

HTTP 管理端点放在 `/api/v1/admin/sources` 下，包含集合创建/列表、详情/周期 PATCH、pause、resume、`POST /{id}/fetches` 和 `GET /{id}/fetches`。手动抓取保持同步响应，但运行 ID 在执行前持久化，允许重试观察同一操作。CLI 改为调用同一应用服务，不通过 HTTP。

### 8. RSS 更新和抓取完成保持原子

Source 成功完成事务批量 upsert 条目：新条目创建公开聚合和 revision 1，已有条目按 `(source_id, dedupe_key)` 锁定；内容哈希变化创建下一修订并切换指针，不变只更新 `last_seen_at`。更新永远不修改 `published_at` 或 `offline(admin)` 状态。

文章写入、Source 元数据/条件请求状态、抓取运行完成和租约 fencing 在同一事务提交。若租约丢失，整批文章变化回滚。失败路径在独立短事务中完成 Source 退避和运行失败记录；若该写入也因旧 fencing 被拒绝，只保留可识别的租约丢失结果。

批量 500 条的事务是现有行为的延续，I2 不引入部分成功语义或 MQ；后续如有真实事务时长问题再拆分。

### 9. Feed 网络默认禁用环境代理

`httpfeed.Fetcher` 不再使用 `http.ProxyFromEnvironment`，默认 Transport 的 Proxy 为空。直连路径继续在每次请求和重定向验证协议、凭据、端口和字面 IP；解析域名时只要任一结果受限就拒绝，并只拨号已验证 IP，避免连接阶段重新解析。

专用 `VELIS_FEED_PROXY_URL` 映射到 Feed 配置。仅该配置存在时启用代理 Transport；启动日志只记录代理模式和脱敏主机，不记录 userinfo。代理模式明确把最终解析与受限地址防护委托给可信出口代理，应用仍验证 URL语法、协议、端口、重定向次数、总超时和响应体上限。通用环境代理无论是否存在都不参与选择。

选择显式可信代理而不是试图在通用 HTTP 代理前本地预解析，是因为代理可能独立解析或发生 DNS rebinding，本地预检无法证明最终目标安全。

### 10. API 和 Web 按身份条件装配

匿名文章和资产公开读取始终注册。`auth.enabled=true` 时才装配 `/me/*` 和 `/admin/*`，并在管理员组追加当前数据库角色检查。Bearer 写接口不依赖 Cookie 身份，因此沿用现有不要求 Origin/CSRF 的账户本人资源策略；MinIO 直传 CORS 使用精确允许来源。

Web 增加本人文章列表、新建/编辑页和管理员 Source 页。编辑器采用手动保存；一个用户动作生成一个 UUID 幂等键并在认证刷新或网络结果不明的重试中复用，用户明确重新提交才生成新键。409 保留本地缓冲区，不自动重放为新键或合并。路由导航只改善体验，权限仍由后端决定。

### 11. 历史迁移保持 ID 并提供受保护降级

新增迁移而不修改 `000001` 至 `000003`：

1. 创建版本、资产、幂等和抓取运行等新表及必要的新列/约束。
2. 为每个现有 RSS 文章复制 `articles` 与 `article_contents` 内容到 revision 1，保留文章 ID、Source 身份和内容哈希。
3. 设置 `origin_type='rss'`、`current_revision_id`、`lock_version=1`，以 `discovered_at` 回填 `published_at`；`hidden` 映射为无 actor 的 `offline(admin)`。
4. 建立新的 latest、来源去重、资产和清理索引，切换读取后再移除不再使用的旧当前内容列/表。
5. 重设 identity sequence 至最大文章 ID 后一位，并用迁移夹具验证数量、ID、哈希、状态和分页。

down migration 先用 `DO` 块检查用户文章、资产、任意 revision 2+ 或其他旧结构无法表达的数据；存在时抛错并保持数据库不变。只有仍可无损折回单一 RSS 内容的数据库才重建 `article_contents` 和旧状态/索引。生产降级仍要求先备份并验证目标。

## Risks / Trade-offs

- [迁移同时改变多张核心表，锁表或失败会影响读取] → 使用专用升级测试库验证 I2 前夹具，保持单个版本化迁移的事务性，记录升级耗时并在部署前备份。
- [统一事务批量写入 500 个 RSS 修订可能增长锁持有时间] → 保持现有条目上限、使用批量查询/索引并增加集成测试；I2 不以破坏原子性换取未经测量的优化。
- [24 小时后相同幂等键不能精确重放] → 在 OpenAPI 明示窗口，Web 只在同一用户动作内复用键，唯一约束和状态机继续防止数据损坏。
- [API 图片代理增加应用带宽和数据库读取] → 流式传输、支持 ETag/HEAD且不缓冲；CDN或内部重定向需以后基于负载和撤销语义设计。
- [不剥离 EXIF 会泄露用户主动上传的元数据] → 上传 UI明确提示，格式/签名严格校验；元数据清理作为后续独立能力，不在 I2 隐式改变原图。
- [对象存储与数据库无法原子提交] → 数据库可见性始终优先，使用 pending/ready/delete_pending 状态和可重试清理；孤儿对象不能变成匿名可读。
- [可信代理模式无法由应用验证最终 IP] → 默认禁用代理、要求专用显式配置并在启动日志和 README 声明信任边界。
- [联合 `origin` 破坏现有前端类型和潜在客户端] → 同一 change 更新 OpenAPI、Web 和契约测试；API 仍处于 0.x且保留端点路径。
- [认证关闭导致写路由 404 而非功能级 503] → 保持 I1 已有安全装配语义，并在 README/示例配置明确启用条件。

## Migration Plan

1. 先在 I2 前结构的专用测试库装入含 published/hidden、重复源时间和正文边界的夹具，执行 up/down 可逆性与受保护拒绝测试。
2. 发布包含新迁移、兼容新表的 API/Worker/CLI 和前端；迁移必须在新进程启动前完成，I2 不支持新旧二进制长期混跑。
3. 初始化私有 Bucket 与精确 CORS；未配置或不可达时允许 API 以资产降级模式启动。
4. 迁移后核对文章数、ID、Source 去重、修订数、状态分布、latest 前后抽样和 identity sequence，再启用投稿与管理员 Web。
5. 如需回滚，先停止新写入并备份；仅在 down guard 证明无新模型数据时执行 down。否则回滚应用必须配合数据库前滚修复，不得强制降级或删除用户数据。
