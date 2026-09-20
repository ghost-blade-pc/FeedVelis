# 任务拆分 — I1 账户与权限基础

所有实现任务 pending；propose 已定稿，只有用户显式授权 `/apply` 后才能编码。

## 前置门禁

- [x] D1–D4 已定稿，精确接口/schema/配置/权限与回滚已确认。
- [x] propose/complete 将 card 转为 ready。
- [x] 评审后按 AC-01–AC-12 重映射任务与用例，并补齐端点矩阵、代理拓扑、指标载体与例外处置（2026-09-19 修订）。
- [ ] 用户显式授权 `/apply`。
- [ ] 重查工作树、迁移编号（现为 `000002`，本次应为 `000003`）与真实数据库测试隔离范围。

| Task | 目标与文件范围 | AC 映射 | 依赖 | 验证与主要风险 |
| --- | --- | --- | --- | --- |
| T1 | account Domain：用户名与昵称规范、角色与状态、会话与令牌规则、自有 UUID 值对象 | AC-01、AC-02、AC-05 | 契约定稿 | 纯单测；ASCII 规范化、Unicode 码点计数、状态与权限越界 |
| T2 | `000003` 迁移与 postgres 账户/会话/刷新/限流/审计仓储，并抽出集成测试基座（迁移应用、清表、固定时钟） | AC-05、AC-09、AC-12 | T1 | 真实 PG 唯一约束、轮换竞争、初始化并发、新装/升级、down→up；事务锁顺序；`velis.` schema 前缀；专用 `_test` 库需应用新迁移 |
| T3 | Argon2id、常见密码表、JWT active/previous 密钥适配及 Auth 配置校验 | AC-02、AC-04、AC-09 | T1 | 算法固定、claims、轮换、参数上限、依赖许可与脱敏；密钥不得有示例默认值；不引入第三方 UUID 依赖 |
| T4 | account Application 用例与当前身份解析（含调用者上下文：用户/会话/角色取数据库当前值） | AC-03、AC-05、AC-06、AC-09 | T1–T3 | 假端口单测 + PG 集成；退出/刷新/禁用竞争与回滚 |
| T5 | Hertz Handler、认证中间件（按端点矩阵的 Origin/CSRF/正文类型）、错误码常量表、OpenAPI、API bootstrap | AC-04、AC-05、AC-06、AC-10、AC-12 | T4 | 严格 DTO、错误语义、Cookie 与可信代理配置示例、数据库故障不放行；`INVALID_ARGUMENT` → `VALIDATION_FAILED` 迁移并同步 OpenAPI 与既有 handler 测试；只写低基数日志字段，不新增 `/metrics` |
| T6 | 现有 CLI 扩展账户初始化及已确认维护命令 | AC-07、AC-08 | T4 | 为 Runner 新增 stdin 通道与 TTY 检测，隐藏输入与 `--password-stdin` 可用假 stdin 测试；重复执行、最后管理员保护、原 `source` 命令回归 |
| T7 | 最小 Vue 账户闭环：注册、登录、账户、退出及会话恢复 | AC-10、AC-11 | T5 | 访问令牌仅内存；会话协调抽为纯 TS 模块并以 vitest（node 环境）断言，测试文件必须位于 `src/` 且以 `.test.ts` 结尾才能被 `npm test` 收集；Web Locks/BroadcastChannel 真实行为留人工冒烟；只实现必要交互。无 Web Locks 时的协调不在本 change（原租约降级已移除，见 spec §4.3） |
| T8 | 后端主链路验证、最小 Web 回归、资源测试、配置示例、第三方声明和 README 更新 | AC-12（含全部回归） | T5–T7 | `make check`、`go vet ./...`、专用 PG、多实例/并发、1 核/512 MiB、人工冒烟按固定步骤记录浏览器与版本；区分跳过与通过，人工冒烟不得写成自动化通过 |

每项沿用四层边界；新增路径在 Apply 中按真实符号记录。需要 security/domain-rules 上下文；验证保留命令、环境与结果。T2 的集成基座供 T4/T6 复用；T5 只写结构化日志字段、不引入指标依赖；T8 记录运行方式、密钥轮换、忘记密码例外流程与“无 `/metrics`”的限制。当前没有实现证据或实际偏差。

## Deferred

投稿审核、Source 管理、邮件/短信/第三方登录、头像上传、密码修改或找回（含 CLI 改密）、设备会话列表、完整账户管理后台、Playwright E2E 与 Prometheus 指标接入不在本 change。忘记密码的例外处置见 spec §7，自助改密在后续阶段提案。

**无 Web Locks 时的跨标签刷新协调**：原订的 localStorage 租约降级已在 fix 轮次 4 移除（R2-F1，见 spec §4.3 与 AC-10）。`localStorage` 没有 CAS，两版租约实现都只能收窄而不能关闭「同一时刻两个标签并发刷新」的窗口；该能力留待后续提案单独设计（可考虑服务端刷新宽限窗口，或明确只支持 Web Locks）。检测不到 Web Locks 时当前行为是各标签各自刷新，由服务端单次轮换与重放撤销兜底。
