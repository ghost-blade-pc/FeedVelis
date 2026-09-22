# source-administration Specification

## Purpose

定义管理员通过 HTTP 和 Web 安全维护系统级 Feed 来源、观察抓取历史和触发手动抓取的行为，同时保持 RSS 自动发布、租约隔离和明确的 SSRF 信任边界。

## Requirements

### Requirement: Source HTTP 管理仅向管理员开放

系统 SHALL 在认证开启时提供管理员 Source 新增、列表、详情、周期修改、暂停、恢复、手动抓取和历史读取接口；普通用户不得访问，认证关闭时不得注册 `/admin/*` 路由且现有本地管理 CLI SHALL 保留。

#### Scenario: 管理员新增 Source
- **WHEN** 管理员提交合法且尚不存在的无凭据 HTTP/HTTPS Feed URL 和可选抓取周期
- **THEN** 系统 SHALL 创建系统级 Source，默认周期为 30 分钟并安排首次抓取

#### Scenario: 普通用户访问 Source 管理
- **WHEN** 普通用户调用任一 Source 管理接口
- **THEN** 系统 SHALL 拒绝访问且不得返回管理数据

#### Scenario: 认证关闭
- **WHEN** API 以 `auth.enabled=false` 启动
- **THEN** Source HTTP 管理 SHALL 不注册，匿名文章读取、Worker 和本地管理 CLI SHALL 继续可用

### Requirement: Source 可变范围保持受控

管理员 SHALL 能把抓取周期设置在 5 分钟至 24 小时之间并暂停或恢复 Source；已创建 Source 的 Feed URL SHALL 不可修改，且 I2 不提供 Source 删除、自定义请求头、Cookie、认证 Feed 或页面级代理设置。

#### Scenario: 修改合法抓取周期
- **WHEN** 管理员以当前 `If-Match` 设置合法周期
- **THEN** 系统 SHALL 增加 Source `lock_version` 并让后续调度使用新周期

#### Scenario: 尝试修改 Feed URL
- **WHEN** 管理员需要更换已有 Source 地址
- **THEN** API SHALL 不提供原地修改能力，管理员必须新增 Source 并暂停旧 Source

#### Scenario: 暂停和恢复
- **WHEN** 管理员暂停 Source 后再恢复
- **THEN** 暂停 SHALL 取消当前租约并阻止新调度，恢复 SHALL 重新安排抓取且不得删除既有文章或历史

### Requirement: 手动抓取复用租约和幂等运行身份

手动抓取 SHALL 作为有界同步 HTTP 操作复用 Source 租约、fencing、条件请求和事务入库；同一 Source 不得由 Worker 或管理员并发抓取，且手动抓取 SHALL 具有可查询的运行 ID。

#### Scenario: 手动抓取成功
- **WHEN** 管理员触发未被占用 Source 的手动抓取
- **THEN** 系统 SHALL 返回对应运行 ID 和 not-modified、插入、更新、未变化及跳过统计，并按 Source 周期安排下次抓取

#### Scenario: Source 已被认领
- **WHEN** Worker 或另一个手动请求持有仍有效的 Source 租约
- **THEN** 新的不同操作 SHALL 返回明确冲突且不得并发访问上游

#### Scenario: 同一手动操作被重试
- **WHEN** 相同调用者以相同 `Idempotency-Key` 重试同一手动抓取
- **THEN** 运行中 SHALL 返回同一运行 ID，完成后 SHALL 重放同一结果而不得发起第二次抓取

#### Scenario: 进程在手动抓取中退出
- **WHEN** 手动抓取留下 `pending` 操作和过期租约
- **THEN** 系统 SHALL 将旧运行标记为失败或中止，并允许同一操作键安全恢复而不接受旧 fencing 完成写入

### Requirement: 抓取历史可观察且有界

系统 SHALL 为定时与手动抓取记录运行来源、开始/结束时间、结果、HTTP 304、条目统计和受控错误分类，并允许管理员按 Source 查看最近记录；运行历史不得包含凭据或完整响应正文。

#### Scenario: 成功或 304 被记录
- **WHEN** 一次抓取成功入库或上游返回 304
- **THEN** 抓取历史 SHALL 原子反映最终结果和统计，并能关联 Source 与触发方式

#### Scenario: 抓取失败被记录
- **WHEN** DNS、SSRF、连接、HTTP 状态、响应限制、解析、租约或入库失败
- **THEN** 历史 SHALL 保存稳定错误分类和完成时间，且不得记录完整 Feed 正文、代理凭据或敏感请求头

#### Scenario: 运行中进程崩溃
- **WHEN** 历史记录保持运行中但对应租约已经失效
- **THEN** 后续认领或清理 SHALL 将其收敛为中止状态而不得永久显示为运行中

### Requirement: RSS 抓取写入统一公开文章模型

成功解析的新 RSS 条目 SHALL 自动创建为 `published` 文章和不可变 revision 1；已存在条目内容变化 SHALL 创建新修订并切换当前修订，但不得改变固定站内 `published_at` 或覆盖管理员下架状态。

#### Scenario: 新 RSS 条目自动发布
- **WHEN** 成功抓取一个新的合法 RSS、Atom 或 JSON Feed 条目
- **THEN** 系统 SHALL 在 Source 完成事务内按源内身份去重、创建公开文章和内容修订，并使匿名 latest/详情可读

#### Scenario: RSS 条目内容更新
- **WHEN** 同一 `(source_id, dedupe_key)` 的内容哈希变化
- **THEN** 系统 SHALL 创建下一修订并切换当前内容，保留文章 ID、`published_at` 和可见性状态

#### Scenario: 已下架 RSS 条目再次出现
- **WHEN** 管理员下架的 RSS 文章在后续抓取中新增或更新内容
- **THEN** 系统 MAY 更新其当前内容修订，但 SHALL 保持管理员下架且不得重新进入 latest

#### Scenario: 租约失效导致事务回滚
- **WHEN** RSS 条目写入完成前 Source fencing 已失效
- **THEN** 文章修订、Source 成功状态和抓取完成记录 SHALL 一并拒绝或回滚

### Requirement: Feed 默认使用受限直连网络策略

Feed 抓取 SHALL 默认忽略 `HTTP_PROXY`、`HTTPS_PROXY` 和 `ALL_PROXY`，只允许无凭据 HTTP/HTTPS 的 80/443 端口；每次 DNS 解析、实际连接和重定向均 SHALL 阻止非公网及受限地址，并维持最多 5 次重定向、20 秒总超时和 5 MiB 响应上限。

#### Scenario: 环境变量设置代理
- **WHEN** 进程环境存在通用代理变量但未配置专用 Feed 代理
- **THEN** Feed 抓取 SHALL 继续使用受限直连且不得把通用代理视为安全授权

#### Scenario: DNS 或重定向指向受限地址
- **WHEN** 目标是 IPv4/IPv6 loopback、私网、链路本地、云元数据、其他受限地址，DNS 结果混有受限地址，或重定向到上述目标
- **THEN** 抓取 SHALL 在连接前以稳定 SSRF 错误拒绝

#### Scenario: 响应超过安全限制
- **WHEN** 上游超过重定向、时间、响应头或 5 MiB 正文限制
- **THEN** 抓取 SHALL 中止、记录受控失败分类并执行失败退避

### Requirement: 可信出口代理必须显式配置

部署者 SHALL 只能通过专用 `VELIS_FEED_PROXY_URL` 启用可信 Feed 出口代理；系统 SHALL 把代理视为最终目标地址安全边界，启动时输出不含凭据的安全警告，且不得声称代理模式具备应用侧最终 IP 校验保证。

#### Scenario: 显式启用可信代理
- **WHEN** 专用代理 URL 配置合法
- **THEN** Feed 抓取 SHALL 使用该代理并记录代理模式及安全边界，不得记录用户名、密码或完整敏感 URL

#### Scenario: 通用代理与专用代理同时存在
- **WHEN** 通用环境代理和专用 Feed 代理同时配置
- **THEN** 系统 SHALL 只采用专用 Feed 代理配置

### Requirement: Source 写接口具备并发与幂等保护

全部 I2 Source 写接口 SHALL 要求 `Idempotency-Key`，修改现有 Source 还 SHALL 要求 `If-Match`；成功结果默认保留 24 小时，规范化 URL 唯一性和目标状态语义 SHALL 在保留期外继续防止数据损坏。

#### Scenario: 重复新增规范化相同 Source
- **WHEN** 管理员重复提交等价 Feed URL
- **THEN** 系统 SHALL 返回既有 Source 或明确冲突，不得创建两个调度身份

#### Scenario: Source 乐观锁冲突
- **WHEN** 管理员使用过期 `If-Match` 修改周期、暂停或恢复
- **THEN** 系统 SHALL 返回版本冲突且不得覆盖较新的 Source 状态

### Requirement: 管理员 Web 提供最小 Source 闭环

Web SHALL 为管理员提供新增、列表、详情、周期修改、暂停、恢复、手动抓取和最近历史界面，并在运行、空、失败和并发冲突状态下给出明确反馈。

#### Scenario: 管理员完成 Source 管理
- **WHEN** 管理员从 Web 新增 Source、触发抓取并查看结果
- **THEN** 页面 SHALL 展示当前 Source 状态、下次抓取、最近成功、连续失败、最近错误和对应抓取历史

#### Scenario: 非管理员导航
- **WHEN** 当前账户不是管理员
- **THEN** Web SHALL 不展示管理员入口，但后端权限校验仍 SHALL 独立生效
