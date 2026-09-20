# 账户与权限基础规格

## 1. 目标与范围

本 change 建立后续审核、订阅和 Agent 能力可复用的身份边界，首版交付本地账户、登录会话、普通用户与管理员两种角色、最小账户页面、本地运维 CLI、限流和审计。

首版不包含邮箱、短信、第三方登录、找回或修改密码、设备会话列表、细粒度权限、审核员角色以及 Agent 业务能力。前端只提供注册、登录、当前账户、昵称修改、退出和刷新恢复，不扩展复杂管理界面。

## 2. 产品规则

### 2.1 账户

- 登录方式为用户名加密码。
- 用户名为 3～32 位半角字符，首位必须是英文字母，后续只能是英文字母、数字或下划线；输入不得包含首尾空白。
- 用户名按 ASCII 小写规范化后存储、比较和返回，`Alice` 与 `alice` 属于同一账户；注册后不可修改。不设置保留用户名，身份和权限不能依赖名称。
- 展示昵称为 1～32 个 Unicode 码点，允许中文、重名和修改；去除 Unicode 首尾空白后，拒绝空值、换行和控制字符。注册时省略昵称则使用规范化用户名，显式空昵称校验失败。
- 账户状态只有 `active` 和 `disabled`；角色只有 `user` 和 `admin`。
- I1 中 `admin` 不授予任何 HTTP 能力：本 change 不提供需要管理员角色的 HTTP 端点，管理动作只由本地 CLI 承担。角色字段作为 I2 审核权限的数据基础，其 HTTP 授权边界在 I2 定稿；因此 I1 不实现 `403 AUTH_FORBIDDEN`。
- 注册入口默认关闭。注册成功后前端跳转登录页并预填用户名，不自动建立会话。

### 2.2 密码

- 长度 8～20，只允许 ASCII `0x21`～`0x7E`，不允许空白、中文、控制字符和不可见字符。
- 必须在大写字母、小写字母、数字、ASCII 标点四类中至少包含三类。
- 按原始字节校验和散列，不做 trim、大小写转换或 Unicode 规范化。
- 使用随应用发布的本地常见密码表做完整匹配，比较时仅对 ASCII 字母忽略大小写，不做子串匹配；密码与规范化用户名完全相同也拒绝。
- 密码表采用 SecLists `Passwords/Common-Credentials/10k-most-common.txt`，固定提交 `d9458f277ed978a608ad165e5eb2fdb389e4c7ee`。Apply 阶段保存来源、文件 SHA-256 和 MIT 许可声明，运行时不访问外部服务。
- 使用 Argon2id；每个密码使用独立的 16 字节加密安全随机盐，PHC 字符串保存算法、版本和参数。初始参数为内存 `19456 KiB`、迭代 `2`、并行度 `1`、输出 `32` 字节。
- 每个 API 进程最多同时执行一次密码散列且不排队；容量已占用时立即返回 `503 AUTH_HASH_BUSY`，不计作登录失败。
- I1 不提供修改密码、找回密码或管理员代改密码的能力（既无 HTTP 端点，CLI 也不提供）。用户忘记密码时按 §7 的例外处置执行。
- 注册页面必须明示长度 8–20 且只允许半角可打印字符，避免用户被静默拒绝。

### 2.3 会话

- 允许多设备同时登录，每次成功登录建立独立会话；退出当前会话不影响其他设备。
- 会话采用登录时确定的 7 天绝对期限，刷新不延期。访问令牌有效期 15 分钟，且不得超过会话剩余期限。
- 退出立即撤销当前会话。所有受保护请求在 JWT 校验后查询 PostgreSQL 中的账户和会话状态；数据库不可用时拒绝访问并返回 503。
- 管理员禁用账户或实际变更角色时，在同一事务中撤销该账户全部会话；重新启用或变更角色后需重新登录。
- 刷新令牌单次轮换。成功刷新时，旧令牌在同一事务中标记为已使用并签发后继令牌；旧令牌再次出现时撤销当前会话。未知随机令牌只返回无效会话，不撤销其他会话。
- 前端协调并发刷新；浏览器不得在结果不明的网络失败后自动重放刷新请求。

## 3. 令牌与密钥

### 3.1 访问令牌

- 使用 `github.com/golang-jwt/jwt/v5`，Apply 阶段锁定 `v5.3.1` 并复核模块兼容性、许可证和漏洞信息。
- JWT 只接受 `HS256`，不得根据令牌输入选择算法。Header 必须包含 `alg=HS256`、`typ=JWT` 和已知 `kid`。
- 必需 claims 为 `iss`、`aud`、`sub`、`sid`、`jti`、`iat`、`nbf`、`exp`、`token_type=access`。`sub` 为用户 UUID，`sid` 为会话 UUID，`jti` 为随机 UUID。
- 令牌不携带角色、用户名或账户状态；鉴权使用数据库当前值。
- 默认 issuer 为 `velis-api`，audience 为 `velis-web`，启用认证时两者必须非空并严格匹配。时钟容差 30 秒，`exp` 必须存在，JWT 总长度上限 4096 字节，并启用严格 Base64URL 解码。

### 3.2 密钥与轮换

- HMAC 密钥由 Base64 配置注入，解码后不得少于 32 字节；禁止提交真实密钥或写入日志。
- 配置包含 active `kid/key`，可选 previous `kid/key`；最多接受两把密钥，`kid` 必须唯一且为 1～32 位 ASCII 字母、数字、点、下划线或短横线。
- 新令牌只用 active 密钥签名，验证可用 active 或 previous 密钥。认证启用时，active 缺失或无效会使 API 启动失败；previous 的 ID 与密钥必须同时出现。
- 轮换时先让所有实例获得新验证密钥，再切换新密钥为 active、旧密钥为 previous；从最后一次旧密钥签名起至少保留旧密钥 15 分 30 秒，确认所有实例完成切换后移除。回滚恢复同一 active/previous 组合。

### 3.3 刷新令牌

- 原始刷新令牌使用 32 字节加密安全随机数及无填充 Base64URL 编码，只通过 Cookie 传给浏览器；数据库只保存 SHA-256 摘要。
- 令牌与 CSRF 值使用常量时间比较。刷新令牌有效期等于会话剩余绝对期限；撤销、过期或重放后不得恢复。

## 4. 浏览器安全与最小前端

### 4.1 Cookie

- 生产刷新 Cookie 为 `__Host-velis_refresh`，属性为 `HttpOnly; Secure; SameSite=Strict; Path=/`，不得设置 `Domain`，`Max-Age` 不超过会话剩余期限。
- CSRF Cookie 为 `__Host-velis_csrf`，值为 32 字节随机数的 Base64URL 编码，可供页面读取，其余作用域属性与刷新 Cookie 相同。
- 开发环境可使用 `velis_refresh` 和 `velis_csrf` 且 `Secure=false`，但只允许 `environment=development` 且来源为精确 `localhost` 或 `127.0.0.1`。其他环境关闭 Secure 属于启动错误。
- 部署对照：本地 Compose 以 `VELIS_APP_ENVIRONMENT=development` 运行，浏览器经 nginx 同源反代访问 `http://localhost:5173`，因此走开发分支（非前缀 Cookie 名、`Secure=false`），`auth.allowed_origin` 必须精确等于实际访问来源（`http://localhost:5173` 或 `http://127.0.0.1:5173` 二选一，切换主机名需同步改配置）。生产使用 `__Host-` 前缀与 `Secure=true` 的精确 https 来源。配置示例与 README 必须同时给出两套取值，不得把 development 取值当作生产模板。
- 登录时建立新 CSRF 值；退出、会话失效或刷新重放时清除两个 Cookie。

### 4.2 CSRF、来源与代理

端点校验矩阵（未列出的接口不做来源或 CSRF 校验）：

| 端点 | 正文 | CSRF | 来源校验 |
| --- | --- | --- | --- |
| `POST /auth/register`、`POST /auth/login` | `application/json` | 不要求（尚未建立会话） | 要求精确 `Origin`，缺失时用精确 Referer 回退，两者都缺返回 `403 CSRF_REJECTED` |
| `POST /auth/refresh`、`POST /auth/logout` | `application/json` | 要求 `X-CSRF-Token` 等于 CSRF Cookie | 同上 |
| `PATCH /account/me` | `application/json` | 不要求 | 不要求（Bearer 鉴权，不依赖环境凭证） |
| `GET /account/me` | - | 不要求 | 不要求 |

- 由于注册、登录、刷新与退出都要求来源校验，浏览器之外的调用方必须显式提供受信任来源头；I1 不提供面向非浏览器客户端的登录通道，独立客户端或 Agent 工具的接入另立提案。
- 所有请求体严格解码 JSON，拒绝未知字段、重复字段和尾随内容。
- 拒绝 `Sec-Fetch-Site: cross-site`；`same-site` 不能替代精确同源判断。首版不启用跨域凭证 CORS。
- 生产必须配置一个包含 scheme、host 和非默认 port 的精确同源来源，不接受通配符、子域或前缀匹配。
- 可信代理 CIDR 默认空。仅当直连地址属于可信代理时读取转发链，并以第一个不可信跳点确定来源 IP；否则使用 TCP 对端地址。畸形转发头不得扩大信任。
- 本项目部署绑定：Compose 中 `velis-web` 的 nginx 反代 `/api/` 并设置 `X-Forwarded-For`，`velis-api` 的 TCP 对端是反代地址。因此 `auth.trusted_proxy_cidrs` 必须配置为该反代网段；留空会使 IP 维度限流与来源判定退化为单一地址（所有用户共享一份 IP 额度，30 次失败即可全局锁死登录），属于部署配置错误。配置示例与 README 必须给出本地 Compose 与生产反代两套取值，并在验收中验证反代拓扑下来源 IP 仍按真实客户端隔离。

### 4.3 令牌驻留与多标签页

- 访问令牌只驻留内存，不写入 `localStorage`、`sessionStorage`、IndexedDB 或 Cookie；刷新页面后用刷新 Cookie 恢复登录。
- 单标签页用共享 Promise 合并并发刷新。多标签页优先用 Web Locks 的 `velis-auth-refresh` 锁串行化刷新，并用 BroadcastChannel 广播新访问令牌、到期时间和退出事件。
- 不支持 Web Locks 时**不做跨标签刷新协调**：各标签各自刷新，服务端按单次轮换与重放撤销处理。无 Web Locks 下的协调能力移出 I1（见 §12 AC-10），留给后续提案单独设计——`localStorage` 没有 CAS，用它无法达成「同一时刻只有一个刷新者」这一不变量，已实现的两版租约都只能把并发窗口收窄而不能关闭。
- 任何情况下都不得把访问令牌或刷新令牌写入 `localStorage`、`sessionStorage`、IndexedDB 或 Cookie（访问令牌）；跨标签只通过 BroadcastChannel 传递。
- 刷新返回 401、发现重放或发生结果不明的网络错误时，清空内存令牌并引导重新登录，不自动重试刷新。

## 5. HTTP API

认证请求正文上限 4 KiB，昵称修改上限 1 KiB；严格解码 JSON，拒绝未知字段、重复字段和尾随内容。响应沿用统一 envelope 和 request ID。

| 方法与路径 | 鉴权 | 行为 |
| --- | --- | --- |
| `POST /api/v1/auth/register` | 匿名 | 开关开启时创建普通用户；成功 `201`，不创建会话 |
| `POST /api/v1/auth/login` | 匿名 | 校验限流和密码，建立独立会话；成功 `200`，设置 Cookie 并返回访问令牌 |
| `POST /api/v1/auth/refresh` | Cookie + CSRF | 单次轮换；成功 `200`，更新 Cookie 并返回访问令牌 |
| `POST /api/v1/auth/logout` | Cookie + CSRF | 撤销当前会话并清 Cookie；已撤销也返回 `204` |
| `GET /api/v1/account/me` | Bearer | 返回 ID、用户名、昵称、角色、状态和创建时间 |
| `PATCH /api/v1/account/me` | Bearer | 只允许修改昵称，返回更新后的账户 |

- I1 不定义 `403 AUTH_FORBIDDEN`：本 change 没有需要管理员角色的 HTTP 端点（§2.1），该码随 I2 的审核权限一并引入。
- 错误码约定：400 一律使用 `VALIDATION_FAILED`；现存 `INVALID_ARGUMENT` 是同一语义的旧码，在 T5 中一并迁移并同步 OpenAPI 与现有 handler 测试，迁移后仓库内不得并存两个同语义错误码。错误码集中定义在 Interfaces 层常量表，调用点不写字面量。
- 错误信封沿用现有三字段 `error: {code, message, request_id}`，I1 不新增 `details`；Roadmap 中的 `details?` 保持可选且本阶段不实现。

注册字段为 `username/password/nickname?`，登录为 `username/password`，昵称修改只允许 `nickname`。登录非幂等，客户端不得自动重试，每次成功都建立新会话。注册由用户名唯一约束防重，冲突返回 409。

| HTTP | code | 场景 |
| --- | --- | --- |
| 400 | `VALIDATION_FAILED` | 字段、正文、密码或昵称不合规 |
| 401 | `AUTH_INVALID_CREDENTIALS` | 用户不存在、密码错误或账户禁用，统一消息 |
| 401 | `AUTH_SESSION_INVALID` | 会话无效、过期、撤销或重放 |
| 403 | `AUTH_REGISTRATION_DISABLED` | 注册关闭 |
| 403 | `CSRF_REJECTED` | CSRF 或来源校验失败 |
| 409 | `AUTH_USERNAME_CONFLICT` | 规范化用户名已存在 |
| 429 | `AUTH_RATE_LIMITED` | 账号或 IP 被限制，返回 `Retry-After` |
| 503 | `AUTH_HASH_BUSY` | 密码散列槽位占用，返回短 `Retry-After` |
| 503 | `DEPENDENCY_UNAVAILABLE` | 必需依赖不可用 |

## 6. 登录限流

- 账号维度最近 15 分钟失败 5 次后暂停 15 分钟；IP 维度最近 15 分钟失败 30 次后暂停 15 分钟，任一命中即拒绝。
- 限制期间不新增失败、不延长截止。成功登录不清除记录，记录随滚动窗口移出。
- 用户不存在、密码错误和禁用账户按相同路径计数和响应。散列繁忙、数据库错误、格式失败和已限流请求不计作密码失败。
- 状态保存在 PostgreSQL。账号与 IP 用独立配置密钥做 HMAC-SHA-256 查找键，不保存未知用户名或原始 IP；认证启用时密钥解码后至少 32 字节。
- 并发更新使用数据库事务与唯一键冲突处理保证原子性，不能依赖进程内先查后写。

## 7. 管理 CLI 与审计

```text
velis-admin account init-admin --username <name>
velis-admin account init-admin --username <name> --password-stdin
velis-admin account set-role --username <name> --role user|admin
velis-admin account set-status --username <name> --status active|disabled
```

- 命令沿用现有全局 `-config` 标志（上方示例省略），实现上需要为 CLI 运行器新增 stdin 通道并支持 TTY 检测——现有 Runner 只接收 stdout/stderr，且仓库中没有任何 stdin、隐藏输入或交互确认先例；装配沿用 `bootstrap/admin.go`，交互输入与 `--password-stdin` 都必须可用注入的假 stdin 测试。
- `init-admin` 默认从 TTY 隐藏输入并二次确认；自动化只允许 `--password-stdin`，不接受命令行密码。stdin 只移除一个结尾 LF 或 CRLF，其余按密码规则校验。
- 仅在不存在有效管理员时创建管理员；用户名已存在时报冲突，不覆盖密码或自动提权。已有有效管理员时拒绝。
- 目标角色或状态与现值相同时幂等成功，不撤销会话、不新增审计行，只写无敏感信息的低级别日志。
- 禁止禁用或降级最后一位有效管理员。相关事务使用 PostgreSQL advisory transaction lock；锁顺序为全局管理员锁、目标用户行 `FOR UPDATE`、会话行、审计插入。
- 管理员创建、实际角色变更和实际状态变更与审计同事务提交。审计记录动作、目标账户、前后角色或状态、时间、来源 `local_cli` 和操作 ID，不保存密码、令牌、Cookie、IP 或散列。
- 失败操作只写脱敏日志。审计首版不自动清理。
- 忘记密码的例外处置：I1 不提供改密命令（§2.2），只能由运维人员在确认身份并取得用户显式授权后，在有备份、可回滚的前提下用与当前参数一致的离线工具生成 PHC 散列并直接更新数据库，同时撤销该账户全部会话并记录操作证据。该路径不属于应用能力，必须列入 I1 剩余风险与 README 限制说明；自助改密、找回密码与管理员代改密码在后续阶段提案。

## 8. 数据与事务

Apply 新增下一序号迁移（现最大编号为 `000002`，本次为 `000003`，Apply 前重新核对），不改写历史迁移，至少建立：

- `users`：UUID、规范化用户名唯一约束、密码 PHC、昵称、角色、状态、版本和时间戳，并用 CHECK 约束关键枚举和用户名。
- `auth_sessions`：UUID、用户外键、创建/绝对过期/最后刷新/撤销时间、撤销原因和版本；建立活动会话与清理索引。
- `refresh_tokens`：UUID、会话外键、唯一 32 字节摘要、签发/过期/消费时间和后继 ID；每会话最多一个未消费令牌。
- `login_failure_events`：自增 ID、`account|ip` 维度、32 字节 HMAC 查找键和时间；索引支持滚动统计。
- `login_blocks`：维度和查找键联合主键、限制截止和更新时间。
- `account_audit_logs`：自增 ID、动作、目标用户、前后值、来源、操作 ID 和时间；建立目标/时间与动作/时间索引。

新表建在现有 `velis` schema（迁移强制 `search_path=public`，SQL 必须显式带 `velis.` 前缀），命名与 `velis.sources`、`velis.articles` 保持一致。ID 由应用层生成 RFC 4122 v4 UUID（16 字节，`crypto/rand`），便于测试注入固定值；Domain 定义自有 UUID 值对象，Infrastructure 用 pgx 的 `pgtype.UUID` 适配，不引入第三方 UUID 依赖。

刷新先锁会话再锁令牌，消费旧令牌和插入后继在一个事务完成；并发只能一个成功，已消费令牌触发会话撤销。退出锁会话后撤销并保持幂等。禁用和实际角色变化锁用户、撤销全部会话并写审计后共同提交。受保护请求不得在数据库失败时回退到仅信任 JWT。

Worker 每小时分批清理，每批最多 500 条：登录失败事件保留 30 分钟；过期限制再保留 24 小时；刷新令牌和会话在过期或撤销后保留 7 天。用户与管理审计不自动删除。清理失败按 §10 记录结构化日志字段，不影响 API readiness。

## 9. 配置

沿用默认值 < YAML < `VELIS_*` 环境变量优先级。

| 配置 | 默认与校验 |
| --- | --- |
| `auth.enabled` | `false`；关闭时不注册认证路由，保留现有匿名行为 |
| `auth.registration_enabled` | `false` |
| `auth.access_ttl` | `15m`，允许 5～30 分钟 |
| `auth.session_ttl` | `168h`，允许 1～720 小时 |
| `auth.jwt_issuer` / `jwt_audience` | `velis-api` / `velis-web`，启用时非空 |
| `auth.jwt_active_kid` / `jwt_active_key` | 启用时必填；Base64 密钥解码后至少 32 字节 |
| `auth.jwt_previous_kid` / `jwt_previous_key` | 可选且必须成对 |
| `auth.allowed_origin` | 启用时必填的精确来源 |
| `auth.cookie_secure` | `true`；仅限定的本地开发可关闭 |
| `auth.throttle_key` | 启用时必填；Base64 解码后至少 32 字节 |
| `auth.trusted_proxy_cidrs` | 默认空列表；部署在反代之后时必须配置为反代网段，否则 IP 维度限流退化为单一来源（见 §4.2） |
| `auth.cleanup_interval` / `cleanup_batch` | `1h` / `500` |

账号/IP 阈值、窗口、等待时间和 Argon2 参数允许配置，但必须校验正数、上限、交叉关系和资源预算；默认使用本规格。认证密钥、密码、令牌和 Cookie 不得出现在示例真实值、日志字段与后续的指标标签或错误响应中。

## 10. 可观测性、安全与资源验证

- 日志只记录操作、结果、request ID，以及确认身份后的用户 ID/会话 ID；不记录用户名、昵称、原始 IP、Cookie、密码、令牌、摘要、HMAC 查找键或密钥。
- I1 不引入 Prometheus 客户端，也不新增 `/metrics` 端点：认证请求结果、账号/IP 限流、刷新重放、散列繁忙、密码散列耗时、认证耗时和清理数量以结构化日志字段承载，保持低基数并覆盖上述计数，禁止把用户、会话、IP、用户名或 HMAC 查找键写成字段值。Prometheus 指标、`/metrics` 端点、抓取目标与仪表盘在后续阶段接入；当前 `deploy/prometheus/prometheus.yml` 尚无业务抓取目标，该限制必须同步写入 README 现状与 card 剩余风险，验收以日志字段断言为准。
- 在 1 vCPU/512 MiB API 容器下验证 Argon2id：单请求 P95 目标不超过 750 ms，散列期间进程 RSS 增量目标不超过 64 MiB；并发时最多一个执行，其余在 100 ms 内返回 503。未达到目标时记录证据并重新审查，不能静默降低安全参数。
- 固定依赖版本或提交并记录许可证；Apply 审查 `go list -m` 和可用漏洞扫描结果。Argon2id 使用 `golang.org/x/crypto` 的固定兼容版本。

## 11. 发布与回滚

发布顺序：应用向前兼容迁移；部署 `auth.enabled=false` 版本；CLI 初始化管理员；注入 JWT、限流、来源与 Cookie 配置；开启认证；需要时再开启注册。

迁移沿用仓库惯例提供 `down` 脚本（现有 000001、000002 均有），但生产回滚**不执行** down migration：应用回滚时保留新增表，由旧版本忽略。`down` 只在隔离测试库中验证可重复执行（down → up 后既有 RSS 数据与约束仍正确）。紧急时关闭认证路由以恢复原有匿名能力并保留数据。密钥疑似泄露时轮换密钥并撤销受影响会话；生产删表、数据修复和批量会话处置需另行明确备份与回滚。

## 12. 验收标准

- AC-01 账户与注册：用户名规范化与唯一约束、昵称规则、注册开关与 `201` 不建会话、规范化重名 `409`。
- AC-02 密码与散列：8–20 位 `0x21–0x7E` 且四类至少三类；常见密码表完整匹配（仅忽略 ASCII 大小写）与等于用户名拒绝；Argon2id 独立随机盐、PHC 存储、不落明文。
- AC-03 登录与会话建立：多设备独立会话、7 天绝对期限、三类凭证失败统一 `401`、限流 `429`、散列繁忙 `503` 且不计失败。
- AC-04 访问令牌：15 分钟且不超过会话剩余期限、只接受 HS256 与已知 `kid`、必需 claims、issuer/audience/时钟容差、active/previous 轮换与保留期。
- AC-05 刷新、退出与撤销：单次轮换、重放只撤销对应会话、未知令牌不误撤销、退出立即失效且幂等、禁用与实际角色变更撤销全部会话、数据库故障不放行。
- AC-06 鉴权与越权边界：匿名拒绝、本人资源边界、令牌不携带角色与状态、鉴权取数据库当前值、I1 无管理员 HTTP 端点。
- AC-07 本地管理 CLI：`init-admin` 前置条件与并发保护、`set-role`/`set-status`、最后有效管理员保护、幂等语义、隐藏输入与 `--password-stdin`。
- AC-08 管理审计与例外处置：成功变更与审计同事务、失败仅脱敏日志、审计不随清理删除、忘记密码例外流程有据可依。
- AC-09 登录限流：账号/IP 滚动窗口参数、成功不清零、多实例与重启一致、可信代理与反代拓扑下按真实客户端隔离。
- AC-10 浏览器安全：Cookie 属性与清理、CSRF/来源/正文类型按端点矩阵生效、令牌不落持久存储、基于 Web Locks 的多标签刷新协调。**无 Web Locks 时的协调不在本 change 范围**（原订的 localStorage 租约降级已移除，理由见 §4.3），该场景下的并发刷新由服务端单次轮换与重放撤销兜底。
- AC-11 最小 Web 闭环：注册→登录→重载恢复→改昵称→退出可用，错误/过期/重试与匿名阅读回归不退化。
- AC-12 数据、运行与交付：迁移新装与升级、清理保留期、认证开关与回滚策略、错误码与 OpenAPI/配置示例/README 同步、资源预算实测。

## 13. 依据

- JWT：<https://pkg.go.dev/github.com/golang-jwt/jwt/v5>
- Argon2：<https://pkg.go.dev/golang.org/x/crypto/argon2>
- CSRF：<https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html>
- 常见密码表：<https://github.com/danielmiessler/SecLists>
