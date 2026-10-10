# 认证与账户

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

只填反代自己的地址，不要填整个子网：子网还包含网桥网关，而客户端流量从发布端口进入时的源地址正是网关；把网关也列为可信，客户端自带的 `X-Forwarded-For` 就会成为「最右的不可信跳点」被采纳，来源 IP 可被伪造、IP 维度限流可被轮换绕过。同理，`web/nginx.conf` 使用 `$remote_addr` 覆盖而不是 `$proxy_add_x_forwarded_for` 追加，因为 nginx 是本项目部署的入口代理，前面没有其它反向代理。启动日志中的 `trusted_proxy_cidrs` 字段给出实际生效的取值，可用它核对是否配错。

## 会话与浏览器行为

- 访问令牌有效期 15 分钟且不超过会话剩余期限，只驻留页面内存，不写 `localStorage`、`sessionStorage`、IndexedDB 或 Cookie；重载页面后用刷新 Cookie 恢复登录。
- 刷新令牌为 `HttpOnly` Cookie，单次轮换；旧令牌再次出现会撤销该会话，因此客户端在结果不明的网络失败后不会自动重放刷新请求。
- 多标签页用 Web Locks 的 `velis-auth-refresh` 锁串行化刷新，并用 BroadcastChannel 同步令牌与退出事件；访问令牌与刷新令牌都不落持久存储。浏览器不支持 Web Locks 时**不做跨标签协调**，各标签各自刷新，由服务端的单次轮换与重放撤销兜底。该跨标签协调能力不在 I1 范围，留给后续提案单独设计。
- 退出、账户禁用与角色变更都会立即撤销会话；重新启用需要重新登录。

## 保留期清理

保留期清理由 `velis-worker` 承担：认证开启时，Worker 每个 `auth.cleanup_interval`（默认 1 小时）分批删除超过保留期的数据，每批最多 `auth.cleanup_batch`（默认 500）条。保留期为登录失败事件 30 分钟、过期限制 24 小时、会话与刷新令牌在过期或撤销后 7 天；用户与管理审计不参与清理。认证关闭（`auth.enabled=false`）时 Worker 不启动清理。

## 认证与 MinIO 降级矩阵

| 条件 | 匿名 RSS/latest | 用户纯文本投稿 | `/me/*`、`/admin/*` | 图片上传/确认/字节读取 | `/readyz` |
| --- | --- | --- | --- | --- | --- |
| `auth.enabled=false` | 可用 | 不提供 | 不注册（404） | 匿名图片读取路由存在；无本人上传路由 | 只以 PostgreSQL 等核心依赖判断 |
| 认证开启、MinIO 正常 | 可用 | 可用 | 按身份/RBAC 可用 | 可用 | 可用 |
| 认证开启、MinIO 未配置或故障 | 可用 | 无图片引用时可用 | Source 与文章文本操作可用 | `503 ASSET_UNAVAILABLE` | 不仅因 MinIO 故障失败 |

## 当前限制

**没有修改密码、找回密码或注销账户的能力**，也没有通过 HTTP 管理账户角色/状态的接口；管理员 HTTP 仅覆盖文章下架/恢复和 Source 管理。忘记密码只能由运维人员在确认身份、取得授权并具备备份的前提下按例外流程处置。
