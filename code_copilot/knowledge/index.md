# 可复用知识导航

本页只索引经过代码、配置、测试或已审查 change 验证，且未来会重复使用的事实。一次性实现细节留在 change；没有可靠内容时不创建额外知识文档。

## 稳定入口

- 工程架构与构建：`rules/project-context.md`
- 产品与待决策边界：`rules/product-context.md`
- API 契约：`backend/api/openapi/velis.yaml`
- 数据契约：`backend/migrations/`
- 日志与健康检查：`backend/internal/infrastructure/observability/logger.go`、`backend/internal/interfaces/http/hertz/router.go`
- ADR 与长期开发定义：`Velis Roadmap.md`

## 已验证知识

格式：

```text
- **关键词**：可复用事实或约束 → `真实路径#符号`、测试或 `changes/<id>/log.md`；最近验证：日期/版本
```

- **技术栈/构建**：Go 1.26、Hertz、pgx/PostgreSQL、Vue 3；统一命令入口为根目录 `Makefile` → `backend/go.mod`、`web/package.json`、`Makefile`；最近验证：2026-09-11 文档收敛静态核对；未重跑业务测试。
- **根包/依赖**：根包为 `github.com/ghost-blade-pc/Velis_Feed/backend`，四层 import 方向由测试强制 → `backend/go.mod`、`backend/internal/architecture/dependencies_test.go#TestLayerDependencies`；最近验证：2026-09-08 `changes/article-display-foundation` 归档时测试通过。
- **风险导航**：认证授权、事务/Outbox、幂等并发、迁移、SSRF、隐私及缓存/MQ/Embedding 降级 → `rules/project-context.md`、`Velis Roadmap.md`；最近验证：2026-09-11 文档收敛静态核对；未重跑业务测试。
- **URL 规范化**：只移除 fragment、默认端口与 dot-segment（RFC 3986 §5.2.4 语义），保留重复斜杠、尾斜杠、percent-encoding 与 query 顺序；只接受 http/https、拒绝 userinfo 与非 80/443 端口 → `backend/internal/domain/shared/url.go#NormalizeHTTPURL`；最近验证：2026-09-08 `changes/article-display-foundation`（R0-F2 修复后单测 + R1 passed）。
- **租约 fencing**：认领返回 `Lease{Owner, ExpiresAt}`，完成写入必须以 owner+expiry+status+未过期为 SQL 条件，0 行受影响返回 `ErrLeaseLost`；pause/过期/新认领均拒绝旧任务提交，过期租约可由新 Worker 重新认领自愈 → `backend/internal/domain/source/source.go#Lease`、`backend/internal/infrastructure/persistence/postgres/source_repository.go#MarkSuccess`；最近验证：2026-09-08 `changes/article-display-foundation`（R0-F1 修复后真实 PostgreSQL 集成测试 + race）。
- **文章去重、幂等入库与 hidden 保留**：唯一键为 (source_id, dedupe_key)，dedupe_key 来自 GUID 或规范化 canonical URL 哈希；content_hash 覆盖标题/URL/作者/语言/发布时间/原始描述与正文及其截断标志与清洗器版本；hash 相同不写正文，冲突更新保留受控 status（如 hidden），插入默认 published；文章与 Source 完成写入同事务原子提交 → `backend/internal/domain/article/article.go#ContentHash`、`backend/internal/infrastructure/persistence/postgres/article_repository.go#upsertArticle`；最近核对：2026-09-11 当前源码静态核对（包括清洗器版本）；基础行为的历史证据见 2026-09-08 `changes/article-display-foundation`，不代表本次重跑。
- **PostgreSQL 集成测试约定**：`VELIS_TEST_DATABASE_URL` 的接受条件是 **DSN 实际生效的库名**以 `_test` 结尾，而不是 path 的字面内容——查询串里的 `dbname`/`database`/`host` 会覆盖 path，只看 path 会让迁移与 `TRUNCATE` 打到非测试库；连接后再用 `current_database()` 二次核对。基座 `harness_test.go` 提供 `resetAccounts`（清账户 6 表）与 `resetArticles`（清文章 3 表）；未设置变量时数据库用例逐条 skip（守卫解析用例 `harness_guard_test.go` 仍会执行），`ok` 输出不代表跑过 → `backend/test/integration/harness_test.go#testDatabaseName`、`#requireTestDatabase`；最近验证：2026-09-19 `changes/account-access-foundation` R2 复核（真实 PostgreSQL 正例 14/14、负例 `?dbname=` 被拦且目标库 0 表）。
- **客户端来源判定（可信代理）**：仅当 TCP 对端落在 `auth.trusted_proxy_cidrs` 内才读 `X-Forwarded-For`，并从右向左取第一个不可信跳点；畸形头整体不信任、回退对端地址。**可信范围必须只写反代自身的地址，不能写整个子网**：子网含网桥网关，而客户端流量从发布端口进来时的源地址正是网关，把网关列为可信会让客户端自带的 `X-Forwarded-For` 成为「最右不可信跳点」被采纳，IP 维度限流可被轮换绕过 → `backend/internal/interfaces/http/hertz/middleware/client_ip.go#resolveClientIP`；最近验证：2026-09-19 `changes/account-access-foundation`（T3-F1、R2-F1 的定向验证与承重性实验）。
- **部署拓扑限制（容器化反代）**：nginx 在容器里且端口以 `-p` 发布时，宿主来源地址在发布端口处已被 DNAT 抹成网桥网关，**真实客户端地址物理上不可得**；因此 Compose 本机拓扑下所有本机浏览器共用同一个 IP 维度键，这是拓扑决定的，不能靠配置修好。生产拓扑（远端客户端经 DNAT 保留源地址）不受影响 → `compose.yaml`、`README.md#账户与认证`；最近验证：2026-09-19 `changes/account-access-foundation`（容器内 `netstat` 实测与 P0-13 复核）。
- **前端会话与令牌驻留**：访问令牌只驻留内存，不写 `localStorage`/`sessionStorage`/IndexedDB/Cookie；多标签用 Web Locks 的 `velis-auth-refresh` 锁串行化刷新，并用 BroadcastChannel 同步令牌与退出事件；**无 Web Locks 时不做跨标签协调**——`localStorage` 没有 CAS，用租约无法达成「同一时刻只有一个刷新者」，两版实现都只能收窄而不能关闭并发窗口，该能力已移出 I1 留给后续提案 → `web/src/features/auth/session.ts#createSession`、`browser.ts#createBrowserLock`；最近验证：2026-09-19 `changes/account-access-foundation` 第 5 轮 Test（Vite 与 nginx 两种形态各 16/16，持久存储全程为空）。
- **口令散列**：Argon2id，参数 `m=19456 KiB,t=2,p=1`、每口令独立 16 字节 CSPRNG 盐、输出 32 字节、以 PHC 字符串存储（算法/版本/参数可解析）；进程级单槽位，容量已占用时立即返回 `503 AUTH_HASH_BUSY` 且不计登录失败；弱口令表为固定 commit 的 SecLists 10k 文件，比较时仅忽略 ASCII 大小写、不做子串匹配。PHC 解析必须同时校验上界与**下界**（`t=0`/`p=0` 会让 `argon2.IDKey` panic） → `backend/internal/infrastructure/security/password_hasher.go`、`backend/internal/domain/account/password.go`；最近验证：2026-09-19 `changes/account-access-foundation` R2 复核（真实 API + 真实 PG）。
- **访问令牌与密钥轮换**：只接受 HS256，Header 必须含 `typ=JWT` 与已知 `kid`；claims 为 `iss`/`aud`/`sub`/`sid`/`jti`/`iat`/`nbf`/`exp`/`token_type=access`，时钟容差 30 秒、总长上限 4096 字节、启用严格 Base64URL；配置 active/previous 两把密钥，新令牌只用 active 签名、验证接受两者，轮换时旧密钥至少保留 15 分 30 秒 → `backend/internal/infrastructure/security/jwt_signer.go`、`backend/internal/infrastructure/config/auth.go`；最近验证：2026-09-19 `changes/account-access-foundation`（16 项 JWT 对抗探针全部 401）。
- **会话与刷新轮换**：会话 7 天绝对期限不延期，访问令牌 15 分钟且不超过会话剩余期限；刷新单次轮换，旧令牌重放只撤销**对应**会话（未知令牌不误撤销），事务内先锁会话再锁令牌；受保护请求在验签后查库取账户与会话当前值，数据库不可用返回 503 而非放行 → `backend/internal/infrastructure/persistence/postgres/session_repository.go#Rotate`、`backend/internal/interfaces/http/hertz/middleware/auth.go`；最近验证：2026-09-19 `changes/account-access-foundation`（真实 PG 并发单选、重放撤销、未知摘要不误伤）。
- **登录失败限流**：账号 15 分钟 5 次、IP 15 分钟 30 次任一触发后暂停 15 分钟；账号与 IP 用**独立配置密钥**做 HMAC-SHA-256 查找键（域分离前缀），不保存未知用户名或原始 IP；计数与 upsert 在数据库内完成并用 advisory 事务锁串行化，限制期间不新增失败、不延长截止，成功登录不清除记录 → `backend/internal/domain/account/throttle.go`、`backend/internal/infrastructure/persistence/postgres/throttle_repository.go`；最近验证：2026-09-19 `changes/account-access-foundation`（真实 PG 并发原子计数、跨实例共享、重启保留）。

发现冲突时重新验证当前事实，更新最近验证依据，并保留导致变化的 change/ADR 链接。
