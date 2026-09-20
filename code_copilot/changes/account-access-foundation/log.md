# 变更日志 — I1 账户与权限基础

本文件由 v3 生命周期工具按事件追加，面向人解释“为什么改、改了哪里、依据是什么”。当前状态读取 `card.md`，机器历史读取 `events.jsonl`，精确代码事实读取 Git diff。

每个事件块必须包含事件 ID；代码位置优先记录符号，同时可记录绑定文件或 diff hash 的当时行号。不得复制敏感值、完整源码或把未运行、Mock、本地验证写成生产通过。

## E000001 — propose / created
- 时间：2026-09-19T03:48:05Z
- 状态：`None` → `draft`
- 原因：创建独立 v3 change
- 输入/输出依据：- / -

## E000002 — propose / start
- 时间：2026-09-19T03:51:51Z
- 状态：`draft` → `draft`
- 原因：用户授权 propose 账户与权限需求分析；已静态核对现有路由、配置、依赖和迁移，形成 AC-01–AC-10、T1–T8 与测试草案。用户明确确认用户名＋密码；注册默认开关、账户维护范围及精确安全/数据契约待定，保持 draft，不执行 propose/complete。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/card.md | modify | - | - | 填充目标、范围与阻塞叙述，未手改生命周期字段 | - |
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | 记录需求来源、代码事实、验收与待定契约 | - |
| code_copilot/changes/account-access-foundation/tasks.md | modify | - | - | 形成验收映射和实现依赖 | - |
| code_copilot/changes/account-access-foundation/test-spec.md | modify | - | - | 形成高风险测试计划，结果仍为 not-run | - |

## E000003 — propose / start
- 时间：2026-09-19T04:08:24Z
- 状态：`draft` → `draft`
- 原因：用户明确确认 D2：注册默认关闭，由管理员通过配置开启。同步需求与 card 叙述；其他选项尚未确认，保持 draft。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | 记录 D2 用户确认，移除对应待决策描述 | - |
| code_copilot/changes/account-access-foundation/card.md | modify | - | - | 记录 D2 用户确认，移除对应待决策描述 | - |

## E000004 — propose / start
- 时间：2026-09-19T04:09:06Z
- 状态：`draft` → `draft`
- 原因：记录用户两项明确选择：提供初始化/角色修改/启禁用 CLI 和最后有效管理员保护；I1 仅普通用户、管理员，不引入 reviewer。保留会话与精确契约待定，继续 draft。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | 同步 D3 和两角色范围，清除三角色建议及已确认范围的 TODO | - |
| code_copilot/changes/account-access-foundation/card.md | modify | - | - | 同步 D3 和两角色范围，清除三角色建议及已确认范围的 TODO | - |

## E000005 — propose / start
- 时间：2026-09-19T04:15:40Z
- 状态：`draft` → `draft`
- 原因：用户确认允许多设备同时登录、会话独立；同时记录用户要求其余方案逐项解释后选择。期限与重放策略仍待确认，保持 draft。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | 记录多设备会话决策及逐项确认偏好 | - |

## E000006 — propose / start
- 时间：2026-09-19T04:20:16Z
- 状态：`draft` → `draft`
- 原因：用户确认登录会话绝对期限 7 天，后台刷新不延长，到期需重新输入密码。访问令牌期限和重放策略仍待逐项确认，保持 draft。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | 将 7 天会话绝对期限从建议更新为用户确认 | - |

## E000007 — propose / start
- 时间：2026-09-19T04:21:21Z
- 状态：`draft` → `draft`
- 原因：用户确认访问令牌有效期 15 分钟，可在已确认的 7 天绝对会话期限内自动刷新。退出失效方式、重放策略及其他契约仍待逐项确认，保持 draft。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | 将访问令牌 15 分钟有效期标为用户确认 | - |

## E000008 — propose / start
- 时间：2026-09-19T04:22:35Z
- 状态：`draft` → `draft`
- 原因：用户确认退出立即撤销当前会话，服务端逐次检查会话状态，其他设备独立会话不受影响。明确撤销提交后新鉴权拒绝，已鉴权在途请求不追溯取消；继续 draft。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | 记录退出即时撤销决策及请求并发边界 | - |

## E000009 — propose / start
- 时间：2026-09-19T04:25:27Z
- 状态：`draft` → `draft`
- 原因：用户确认刷新令牌重放严格撤销当前会话并要求重新登录，不影响其他独立会话，不采用容错窗口；记录并发/响应丢失影响，继续 propose draft。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | 将刷新重放处理从建议更新为用户确认，保留前端协调细节待定 | - |

## E000010 — propose / start
- 时间：2026-09-19T04:26:40Z
- 状态：`draft` → `draft`
- 原因：用户确认用户名 3–32 位、英文字母开头、后续仅字母/数字/下划线、不区分大小写、注册后不可修改；昵称独立支持中文。同步规则及确认记录，继续 propose draft。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | 记录已确认用户名约束，将未确认规范化细节单独保留 | - |

## E000011 — propose / start
- 时间：2026-09-19T04:28:34Z
- 状态：`draft` → `draft`
- 原因：用户确认展示昵称规则：1–32 个 Unicode 字符，支持中文，可重名和修改，去首尾空白后非空，禁止换行/控制字符，注册未填默认用户名，昵称仅作展示。继续 propose draft。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | 记录昵称规则与用户确认，单独列出计数及空值的待定细节 | - |

## E000012 — propose / start
- 时间：2026-09-19T04:29:26Z
- 状态：`draft` → `draft`
- 原因：用户确认注册成功跳转登录页、预填用户名，不自动登录；注册仅创建账户，会话由登录建立，密码不传递到登录页。继续 propose draft。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | 同步注册响应、Web 流程与用户确认 | - |

## E000013 — propose / start
- 时间：2026-09-19T04:30:41Z
- 状态：`draft` → `draft`
- 原因：用户确认禁用账户撤销所有设备会话，重新启用必须重新登录，旧会话不可恢复；同步状态与撤销的原子性要求，继续 propose draft。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | 记录禁用及重新启用的会话语义 | - |

## E000014 — propose / start
- 时间：2026-09-19T04:33:31Z
- 状态：`draft` → `draft`
- 原因：用户确认角色变更撤销账户全部会话，所有设备重新登录；同步角色与撤销的事务一致性及当前角色鉴权要求，继续 propose draft。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | 记录角色变更后的会话撤销语义 | - |

## E000015 — propose / start
- 时间：2026-09-19T04:36:34Z
- 状态：`draft` → `draft`
- 原因：用户确认密码长度和组成规则：15–128 个 Unicode 字符，允许中文及空格，区分大小写，不强制字符组合，支持粘贴和密码管理器；弱密码拦截及字符处理细节仍待确认，继续 draft。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | 记录密码规则、官方参考及剩余边界 | - |

## E000016 — propose / start
- 时间：2026-09-19T05:12:12Z
- 状态：`draft` → `draft`
- 原因：用户确认设置密码时以可更新本地列表按完整密码匹配拦截常见弱密码，不调用外部检测服务；明确覆盖限制，列表来源/更新及字符处理契约待定，保持 draft。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | 记录弱密码拦截决策及待定列表契约 | - |

## E000017 — propose / start
- 时间：2026-09-19T05:15:07Z
- 状态：`draft` → `draft`
- 原因：用户明确修正密码规则：首尾和中间空格都不允许。替换此前允许空格的当前契约，含空格直接拒绝，不自动删减；其他 Unicode 空白边界待确认，保持 draft。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | 按最新用户选择修正密码空格规则，保留历史决定的被替代关系 | - |

## E000018 — propose / start
- 时间：2026-09-19T05:21:50Z
- 状态：`draft` → `draft`
- 原因：用户确认密码同时禁止空白和不可见控制字符，含全角空格、制表符、换行、零宽空格，直接拒绝而非自动删除；同步规则与确认记录，继续 draft。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | 记录禁止空白及不可见控制字符的密码约束 | - |

## E000019 — propose / start
- 时间：2026-09-19T05:25:59Z
- 状态：`draft` → `draft`
- 原因：用户要求简化为传统密码方案：默认 8–20 位，四类字符至少三类；撤销原 15–128 Unicode/无组合要求，NFC 不采用。“少数允许 6 位”例外范围待确认，不擅自放宽。保持 draft。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | 以最新密码规则替代旧方案，并明确 6 位例外尚未定稿 | - |

## E000020 — propose / start
- 时间：2026-09-19T05:27:27Z
- 状态：`draft` → `draft`
- 原因：用户确认所有账户密码统一 8–20 位、不设置任何 6 位例外，保留四类字符至少三类及禁止空白/不可见控制字符规则；移除例外待决项，继续 draft。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | 确定统一密码长度并关闭 6 位例外待决项 | - |

## E000021 — propose / start
- 时间：2026-09-19T05:30:18Z
- 状态：`draft` → `draft`
- 原因：用户确认半角 ASCII 密码范围；将规则统一为 0x21–0x7E、8–20 位、四类至少三类，删除已被替代的 Unicode 方案，补充边界和标点验收计划，保持 draft。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | 收敛已确认密码规则与特殊符号集合 | - |
| code_copilot/changes/account-access-foundation/test-spec.md | modify | - | - | 补充密码长度/分类/字符边界测试计划；结果保持 not-run | - |

## E000022 — propose / start
- 时间：2026-09-19T05:31:57Z
- 状态：`draft` → `draft`
- 原因：用户确认访问令牌仅页面内存、刷新令牌 HttpOnly Cookie，重载通过 Cookie 恢复、不写 localStorage；同步测试计划，来源校验与多标签页协调待精确设计，继续 draft。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | 记录浏览器凭证保存方案 | - |
| code_copilot/changes/account-access-foundation/test-spec.md | modify | - | - | 明确凭证位置及重载恢复的验收；保持 not-run | - |

## E000023 — propose / start
- 时间：2026-09-19T05:36:57Z
- 状态：`draft` → `draft`
- 原因：用户确认首版 Web/API 同源访问，独立后端经反向代理暴露同源 /api，本地继续 Vite 代理；同步范围与验收，不引入跨域部署，保持 draft。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | 记录同源访问与跨域范围边界 | - |
| code_copilot/changes/account-access-foundation/test-spec.md | modify | - | - | 加入代理访问的验收场景；保持 not-run | - |

## E000024 — propose / start
- 时间：2026-09-19T05:38:17Z
- 状态：`draft` → `draft`
- 原因：用户确认 CLI 隐藏输入初始化规则：无管理员可创建新管理员，同名冲突不覆盖/提权，已有管理员拒绝；同步 AC-07 及测试，区分尚未确认的 stdin 扩展，保持 draft。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | 记录初始化行为，修正空账户库前置条件 | - |
| code_copilot/changes/account-access-foundation/test-spec.md | modify | - | - | 补充初始化前置条件与并发验收；保持 not-run | - |

## E000025 — propose / start
- 时间：2026-09-19T05:40:04Z
- 状态：`draft` → `draft`
- 原因：用户确认成功管理操作同事务入库审计，失败记脱敏日志，CLI 使用本地运维来源；补充审计原子性验收，保留精确字段和保留期限待定，继续 draft。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | 确定管理审计持久化和事务边界 | - |
| code_copilot/changes/account-access-foundation/test-spec.md | modify | - | - | 补充审计失败回滚及脱敏测试计划；保持 not-run | - |

## E000026 — propose / start
- 时间：2026-09-19T05:41:19Z
- 状态：`draft` → `draft`
- 原因：用户确认首版管理审计不自动清理，后续制定归档策略；明确失败日志独立保留，补充审计不得随会话清理删除的验收，继续 draft。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | 确定管理审计保留边界 | - |
| code_copilot/changes/account-access-foundation/test-spec.md | modify | - | - | 补充管理审计与会话清理隔离的验收；保持 not-run | - |

## E000027 — propose / start
- 时间：2026-09-19T05:42:49Z
- 状态：`draft` → `draft`
- 原因：用户确认账号/IP 临时限流、到期自动恢复，不改变账户启用状态；阈值/窗口及计数实现待定，同步验收计划，继续 draft。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | 记录登录失败处理策略 | - |
| code_copilot/changes/account-access-foundation/test-spec.md | modify | - | - | 补充自动恢复及账户状态不变的验收；保持 not-run | - |

## E000028 — propose / start
- 时间：2026-09-19T05:44:18Z
- 状态：`draft` → `draft`
- 原因：用户确认登录限流初始参数：账号 15 分钟内失败 5 次，IP 15 分钟内失败 30 次，任一触发后暂停 15 分钟；限流请求不延长等待，参数可配置。同步需求与边界验收，继续 draft。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | 记录已确认限流参数及共享 IP 影响 | - |
| code_copilot/changes/account-access-foundation/test-spec.md | modify | - | - | 补充阈值、到期与配置覆盖验收；保持 not-run | - |

## E000029 — propose / start
- 时间：2026-09-19T05:46:39Z
- 状态：`draft` → `draft`
- 原因：用户确认登录失败计数及限流截止时间持久化 PostgreSQL，跨 API 实例共享、重启不清空、不引入 Redis；同步数据设计、任务及真实依赖验收，继续 draft。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | 确定限流持久化及多实例语义 | - |
| code_copilot/changes/account-access-foundation/test-spec.md | modify | - | - | 补充多实例、并发及重启持久化验收；保持 not-run | - |
| code_copilot/changes/account-access-foundation/tasks.md | modify | - | - | 将限流和审计存储纳入迁移仓储任务 | - |

## E000030 — propose / start
- 时间：2026-09-19T05:49:01Z
- 状态：`draft` → `draft`
- 原因：用户确认失败计数采用最近 15 分钟滚动窗口；同步 PostgreSQL 失败时间记录需求和跨时间边界验收，成功登录后的计数处理仍待确认，继续 draft。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | 确定滚动窗口并移除窗口算法待决项 | - |
| code_copilot/changes/account-access-foundation/test-spec.md | modify | - | - | 补充滚动移出与跨固定边界验收；保持 not-run | - |

## E000031 — propose / start
- 时间：2026-09-19T05:50:59Z
- 状态：`draft` → `draft`
- 原因：用户确认成功登录后保留账号及 IP 失败记录，随滚动窗口自然过期；不在限流期提前放行，更新规则和测试计划，继续 draft。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | 确定成功登录后的失败记录语义 | - |
| code_copilot/changes/account-access-foundation/test-spec.md | modify | - | - | 补充成功不清计数及限流期间正确密码不放行验收；保持 not-run | - |

## E000032 — propose / start
- 时间：2026-09-19T05:52:22Z
- 状态：`draft` → `draft`
- 原因：用户确认账号不存在/密码错误/禁用统一登录失败提示，限流单独提示；同步同状态码与错误码契约和验收，继续 draft。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | 明确统一失败响应 | - |
| code_copilot/changes/account-access-foundation/test-spec.md | modify | - | - | 补充三类失败响应一致与限流区分验收；保持 not-run | - |

## E000033 — propose / start
- 时间：2026-09-19T05:58:39Z
- 状态：`draft` → `draft`
- 原因：用户确认 Argon2id/golang.org/x/crypto，独立随机盐并存散列及参数；更新 Build-vs-Buy，补充散列验证与参数边界测试计划。参数预算及版本审计未完成，继续 draft。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | 记录 Argon2id 选择、已核对官方来源及未决参数 | - |
| code_copilot/changes/account-access-foundation/test-spec.md | modify | - | - | 加入散列与参数安全边界验收；保持 not-run | - |

## E000034 — propose / start
- 时间：2026-09-19T05:59:49Z
- 状态：`draft` → `draft`
- 原因：用户确认 API 1 核/512 MiB 为暂定设计预算，后续实测调整，不含 PostgreSQL/Worker；同步参数设计和验证要求，尚未确认具体 Argon2id 数值，保持 draft。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | 记录暂定 API 资源预算及非性能承诺边界 | - |
| code_copilot/changes/account-access-foundation/test-spec.md | modify | - | - | 补充对应资源预算的实测要求；保持 not-run | - |

## E000035 — propose / start
- 时间：2026-09-19T06:03:59Z
- 状态：`draft` → `draft`
- 原因：用户确认 Argon2id m=19 MiB/t=2/p=1、进程级计算并发 1、不排队，繁忙 503 且不计密码错误；同步实现约束及资源/取消验收，保持 draft，未进行性能实测。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | 确定散列初始参数、共享并发额度和过载语义 | - |
| code_copilot/changes/account-access-foundation/test-spec.md | modify | - | - | 补充并发额度、过载不计错及取消边界；保持 not-run | - |

## E000036 — propose / start
- 时间：2026-09-19T06:08:41Z
- 状态：`draft` → `draft`
- 原因：用户明确项目重点为后端及后续 Agent，前端无需复杂。将 I1 Web 收敛为注册、登录、本人账户、会话恢复和退出的最小演示闭环，后端权限、并发、数据一致性及运行保障优先；Agent 仍留在 Roadmap 后续阶段。保持 propose draft。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | 收敛 Web 范围并明确后端和后续 Agent 的优先关系 | - |
| code_copilot/changes/account-access-foundation/tasks.md | modify | - | - | 将前端任务缩减为最小账户闭环，验证重点转向后端 | - |
| code_copilot/changes/account-access-foundation/test-spec.md | modify | - | - | 将 Web 验证限定为 Vitest 与最小浏览器冒烟 | - |
| code_copilot/changes/account-access-foundation/card.md | modify | - | - | 同步 change 范围和验证重点；未修改生命周期字段 | - |

## E000037 — propose / start
- 时间：2026-09-19T06:21:08Z
- 状态：`draft` → `draft`
- 原因：用户确认 JWT 采用 HS256 对称签名。补充只接受 HS256、拒绝算法替换，以及随机密钥不少于 32 字节和配置失败即拒绝启动的安全边界；key ID 与轮换仍待下一项确认，保持 draft。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | 记录 HS256 决策、密钥强度和算法固定要求 | - |
| code_copilot/changes/account-access-foundation/test-spec.md | modify | - | - | 补充算法替换与密钥配置失败验收；结果保持 not-run | - |

## E000038 — propose / complete
- 时间：2026-09-19T06:33:38Z
- 状态：`draft` → `ready`
- 原因：用户授权所有剩余未决事项采用推荐方案；已将 JWT 轮换、claims、Cookie/CSRF、限流存储、数据模型、CLI 并发保护、清理、配置、发布回滚和资源验证收敛为可实施契约。提案无未决阻塞，完成 propose。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | 整合全部用户确认和推荐默认，形成完整 high-risk 产品与安全契约 | - |
| code_copilot/changes/account-access-foundation/tasks.md | modify | - | - | 按最终契约更新实现拆分、依赖和验证重点 | - |
| code_copilot/changes/account-access-foundation/test-spec.md | modify | - | - | 补齐 JWT 轮换、CSRF、多标签、清理、配置和资源预算测试矩阵，保持业务测试未运行 | - |
| code_copilot/changes/account-access-foundation/card.md | modify | - | - | 同步最终范围、风险和无 propose 阻塞状态；生命周期字段仍交由 Runner 更新 | - |

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| rg -n 'TODO\|TBD\|待定\|待确认\|尚未确认\|精确测试输入待' spec.md tasks.md test-spec.md card.md | 本地 change 文档，2026-09-19 | passed | 当前权威提案文档不存在未决占位；历史 events/log 中的旧状态不属于当前契约。 |
| 人工交叉核对 spec.md、tasks.md、test-spec.md、card.md | Spec Copilot propose/high-risk | passed | 范围、接口、数据、权限、并发、依赖、测试、发布与回滚互相一致；未修改应用代码。 |
| 业务代码与测试 | propose 阶段 | not-run | 本阶段只定稿 change 文档；实现验证留待显式 /apply 后执行。 |

## E000039 — system / block
- 时间：2026-09-19T07:29:49Z
- 状态：`ready` → `blocked`
- 原因：用户评审后要求修订已定稿契约：AC 编号追溯断裂、默认部署下 IP 维度限流退化、admin 角色无 HTTP 挂载点、指标交付无载体、Origin/JSON 校验端点集合不明确、down migration 语义冲突，以及用户名/昵称验收缺口、浏览器验证载体、CLI stdin 改动面、Compose Cookie 分支与集成测试基座等补强项。契约修订需要回到 propose 语义，先把 change 置为 blocked 并指定恢复到 draft。
- 输入/输出依据：- / -

## E000040 — system / resume
- 时间：2026-09-19T07:30:02Z
- 状态：`blocked` → `draft`
- 原因：按 card.resume_to 恢复到 draft，准备以 propose 语义修订契约文档。
- 输入/输出依据：- / -

## E000041 — propose / start
- 时间：2026-09-19T07:30:15Z
- 状态：`draft` → `draft`
- 原因：按用户要求修订 propose 契约：把评审发现的 14 项问题逐条落进 spec/tasks/test-spec，并为用户指定的三项口径（admin 不挂 HTTP、不新增改密命令、浏览器验证不新增依赖）写入明确结论。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | 补齐 AC 编号、admin 口径、Origin/JSON 端点集合、down 语义、改密例外、UUID 生成、指标载体、trusted proxy 部署绑定、Compose Cookie 分支与错误码规范 | - |
| code_copilot/changes/account-access-foundation/tasks.md | modify | - | - | 按新 AC 编号重映射任务、补 CLI stdin 改动面、集成测试基座与指标证据口径 | - |
| code_copilot/changes/account-access-foundation/test-spec.md | modify | - | - | 按新 AC 编号重映射用例、补用户名/昵称边界、反代 IP 隔离与浏览器人工冒烟载体 | - |
| code_copilot/changes/account-access-foundation/card.md | modify | - | - | 同步范围口径与剩余风险；生命周期字段仍交由 Runner 更新 | - |

## E000042 — propose / complete
- 时间：2026-09-19T07:32:24Z
- 状态：`draft` → `ready`
- 原因：按用户要求完成契约修订：AC 编号体系重建并按新编号重映射任务与用例；写入 admin 口径（I1 无管理员 HTTP 端点、不实现 AUTH_FORBIDDEN）、端点校验矩阵、可信代理与反代拓扑绑定、指标载体（不引入 Prometheus 与 /metrics）、down migration 语义、忘记密码例外处置、UUID 生成与依赖口径、用户名/昵称边界用例、浏览器人工冒烟载体、CLI stdin 改动面、Compose 与生产 Cookie 对照、集成测试基座，以及错误码统一约定。提案无未决占位，AC-01–AC-12 定义与引用双向闭合。
- 输入/输出依据：E000041 propose/start；用户评审结论与三项口径选择 / AC-01–AC-12 定稿契约、T1–T8 映射、P0-01–P0-13 与 P1-01–P1-07 用例矩阵

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/spec.md | modify | - | - | AC-01–AC-12 编号化验收、admin 口径、端点校验矩阵、可信代理部署绑定、Cookie 部署对照、指标载体、down 语义、忘记密码例外、UUID 与 schema 口径、错误码统一 | - |
| code_copilot/changes/account-access-foundation/tasks.md | modify | - | - | 按新 AC 重映射 T1–T8、补 CLI stdin 改动面、集成测试基座、日志字段证据与 Deferred 口径 | - |
| code_copilot/changes/account-access-foundation/test-spec.md | modify | - | - | 重映射 P0/P1 用例并新增 P0-02（用户名/昵称边界）、P0-13（反代 IP 隔离）、P1-07（日志字段）等，明确人工冒烟边界 | - |
| code_copilot/changes/account-access-foundation/card.md | modify | - | - | 同步修订后范围、已接受限制与剩余风险；生命周期字段交由 Runner 更新 | - |

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| rg -n 'TODO\|TBD\|待定\|待确认\|尚未确认\|精确测试输入待' spec.md tasks.md test-spec.md card.md | 本地 change 文档，2026-09-19 | passed | 当前权威文档无未决占位；历史 events 与 log 保留旧叙述，不属于当前契约 |
| python 正则提取 spec 定义的 AC-01–AC-12 与 tasks/test-spec 的引用集合并求双向差集 | 本地 change 文档，2026-09-19 | passed | 定义 12 条，引用无悬空、无未覆盖；test-spec 的 AC 列已由裸数字统一为 AC-xx 前缀 |
| 人工交叉核对 spec/tasks/test-spec/card 与代码事实（router.go、config.go、migrations、presenter/response.go、cli/source.go、scheduler.go、nginx.conf、compose.yaml、package.json、vite.config.ts） | 只读代码核查 + 文档，2026-09-19 | passed | 修订项与仓库现状一致：现存 5 条路由无 auth 代码、错误信封无 details、CLI 为 flag 手写分发且无 stdin 先例、最大迁移编号 000002、nginx 反代设置 X-Forwarded-For、Compose 以 development 运行、vitest 为 node 环境且未装 Playwright |
| 业务代码与测试 | propose 阶段 | not-run | 本阶段只定稿 change 文档，未修改任何应用代码或测试 |

## E000043 — apply / start
- 时间：2026-09-19T07:38:07Z
- 状态：`ready` → `applying`
- 原因：用户显式授权 apply，但限定本批次只完成 T1（account Domain）、T2（000003 迁移与 postgres 仓储及集成测试基座）、T3（Argon2id、常见密码表、JWT 与 Auth 配置校验），完成后暂停等待后续指令；不进入整体 apply/complete。
- 输入/输出依据：E000042 propose/complete（ready）；spec.md AC-01–AC-12 与 tasks.md T1–T3 / -

## E000044 — system / block
- 时间：2026-09-19T07:53:32Z
- 状态：`applying` → `blocked`
- 原因：用户授权的 apply 批次限定为 T1、T2、T3，完成后暂停等待后续指令。三项任务已实现并验证：T1 account Domain（用户名/昵称/角色/状态、密码规则、会话与令牌规则、限流策略、自有 UUID 值对象、仓储抽象），T2 000003 迁移与 postgres 账户/会话/刷新/限流/审计/清理仓储及集成测试基座，T3 Argon2id 散列、本地常见密码表、JWT HS256 active/previous 适配与 auth 配置校验。T4–T8 未开始，因此不进入 testing，按协议置为 blocked 并保留 resume_to=applying。
- 输入/输出依据：E000043 apply/start；spec.md AC-01–AC-12 与 tasks.md T1–T3 / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| backend/internal/domain/account/uuid.go | add | T1 | - | 自有 UUID 值对象，避免引入第三方 UUID 依赖 | - |
| backend/internal/domain/account/account.go | add | T1 | - | 用户名规范化、昵称校验、角色/状态与最后管理员保护规则 | - |
| backend/internal/domain/account/password.go | add | T1 | - | 密码字符集/长度/类别规则与 Blocklist 端口 | - |
| backend/internal/domain/account/session.go | add | T1 | - | 会话绝对期限、访问令牌上限与刷新令牌规则 | - |
| backend/internal/domain/account/throttle.go | add | T1 | - | 登录失败限流策略与阈值/窗口取值 | - |
| backend/internal/domain/account/repository.go | add | T1 | - | 账户、会话、刷新令牌、限流、审计与清理仓储抽象 | - |
| backend/internal/domain/account/uuid_test.go | add | T1 | - | UUID 版本位、解析与非法输入测试 | - |
| backend/internal/domain/account/account_test.go | add | T1 | - | 用户名/昵称边界与最后管理员测试 | - |
| backend/internal/domain/account/password_test.go | add | T1 | - | 密码长度/字符集/类别/弱密码表/用户名相同测试 | - |
| backend/internal/domain/account/session_test.go | add | T1 | - | 会话期限、访问令牌上限与刷新令牌可用性测试 | - |
| backend/internal/domain/account/throttle_test.go | add | T1 | - | 限流参数校验与截止时间测试 | - |
| backend/migrations/000003_create_accounts_and_access.up.sql | add | T2 | - | 建立 users/auth_sessions/refresh_tokens/login_failure_events/login_blocks/account_audit_logs 与约束索引 | - |
| backend/migrations/000003_create_accounts_and_access.down.sql | add | T2 | - | 按依赖顺序回退六张表 | - |
| backend/internal/infrastructure/persistence/postgres/account_repository.go | add | T2 | - | 账户仓储、唯一冲突映射、乐观锁更新与 CLI 事务锁 | - |
| backend/internal/infrastructure/persistence/postgres/session_repository.go | add | T2 | - | 会话仓储与刷新轮换事务（锁会话、消费旧令牌、插入后继、重放撤销） | - |
| backend/internal/infrastructure/persistence/postgres/throttle_repository.go | add | T2 | - | 滚动窗口失败计数与限制写入，advisory lock 序列化并发 | - |
| backend/internal/infrastructure/persistence/postgres/audit_repository.go | add | T2 | - | 管理审计写入，参与调用方事务 | - |
| backend/internal/infrastructure/persistence/postgres/cleanup_repository.go | add | T2 | - | 会话、令牌、失败事件与限制的分批保留期清理 | - |
| backend/internal/infrastructure/persistence/postgres/transaction.go | modify | T2 | - | 新增事务感知的 querier，保证读写看到同一事务 | - |
| backend/test/integration/harness_test.go | add | T2 | - | 集成测试共享基座：连接校验、迁移应用、分区清表与固定时钟 | - |
| backend/test/integration/account_repository_test.go | add | T2 | - | 账户并发唯一、更新与管理员计数、刷新轮换/重放/并发单赢家、会话撤销、限流原子计数、审计与清理、管理锁测试 | - |
| backend/test/integration/article_repository_test.go | modify | T2 | - | 改用共享基座，去掉重复的连接与清表代码 | - |
| backend/test/integration/README.md | modify | T2 | - | 记录基座、清表范围、临时实例与迁移回退命令 | - |
| backend/internal/application/ports/security.go | add | T3 | - | PasswordHasher 与 TokenSigner 端口及声明类型 | - |
| backend/internal/infrastructure/security/password_hasher.go | add | T3 | - | Argon2id PHC 散列与校验、参数上限与进程级并发额度 | - |
| backend/internal/infrastructure/security/password_blocklist.go | add | T3 | - | 内嵌本地常见密码表，仅完整匹配 | - |
| backend/internal/infrastructure/security/jwt_signer.go | add | T3 | - | HS256 主动/上一把密钥签发与校验、claims 与 kid 校验 | - |
| backend/internal/infrastructure/security/data/10k-most-common.txt | add | T3 | - | 固定提交的 SecLists 常见密码表 | - |
| backend/internal/infrastructure/security/data/NOTICE.md | add | T3 | - | 记录来源、提交、文件 SHA-256 与 MIT 许可声明 | - |
| backend/internal/infrastructure/security/password_hasher_test.go | add | T3 | - | 散列往返、独立盐、畸形/超预算参数与额度占用测试 | - |
| backend/internal/infrastructure/security/password_blocklist_test.go | add | T3 | - | 密码表完整匹配与子串不命中测试 | - |
| backend/internal/infrastructure/security/jwt_signer_test.go | add | T3 | - | 令牌轮换、kid/typ/claims/算法替换/过期/超长与非严格编码测试 | - |
| backend/internal/infrastructure/config/auth.go | add | T3 | - | auth 配置分组、期限与来源校验、密钥解码与策略映射 | - |
| backend/internal/infrastructure/config/auth_test.go | add | T3 | - | 默认值、密钥强度、期限边界、来源精确性、Cookie 规则、限流交叉关系与环境变量覆盖测试 | - |
| backend/internal/infrastructure/config/config.go | modify | T3 | - | 接入 auth 分组、默认值、VELIS_AUTH_* 环境变量与布尔/整数解析辅助 | - |
| backend/go.mod | modify | T3 | - | 新增 golang-jwt/jwt/v5 v5.3.1 与 golang.org/x/crypto v0.56.0 | - |
| backend/go.sum | modify | T3 | - | 同步依赖校验和 | - |

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| cd backend && go build ./... && go vet ./... | 本地，Go 1.26.6 | passed | 四个入口与新增包全部编译通过，go vet 无输出 |
| cd backend && go test -count=1 ./... | 本地单元与架构测试 | passed | 15 个包通过，含 architecture 层级依赖检查；test/integration 未设置 URL 时跳过 |
| cd backend && go test -race -count=1 ./internal/... | 本地竞态检测 | passed | domain/account、infrastructure/security、config 等全部通过 |
| VELIS_TEST_DATABASE_URL=<临时 pgvector/pg17 容器> go test -count=1 ./test/integration | 真实 PostgreSQL 17.11（临时容器，用后删除） | passed | 10 个用例通过：账户并发唯一（1 成功/3 冲突）、乐观锁版本冲突、刷新轮换单赢家与重放撤销对应会话、未知摘要不误撤销、退出幂等且不影响其他设备、限流滚动窗口与不延长、并发原子计数（恰好等于阈值）、审计与清理保留期隔离、advisory lock 阻止重复初始化、Source/Article 既有用例 |
| go run ./cmd/velis-migrate -path migrations -steps 1 down 后 up | 真实 PostgreSQL 17.11 专用 _test 库 | passed | down 后 version=2 且 6 张账户表消失、RSS 数据（5 源/4 文章）保留；up 后 version=3 且六张表重建，未改写历史迁移 |
| gofmt -l <新增与修改文件> | 本地，Go 1.26.6 | passed | 新增文件全部格式化干净；既有 CRLF 文件去掉 CR 后同样干净 |
| go run golang.org/x/vuln/cmd/govulncheck@latest ./... | 本地，漏洞库在线 | passed | 影响代码的漏洞 0 个；升级 x/crypto 至 v0.56.0 后仅剩 1 个无修复版本的模块级告警（GO-2026-5932，未调用） |
| make check、Web/Vitest、资源预算实测 | T8 范围 | not-run | 本批次只交付 T1–T3，未运行前端与 1 vCPU/512 MiB 资源验证 |

## E000045 — system / resume
- 时间：2026-09-19T07:56:45Z
- 状态：`blocked` → `applying`
- 原因：用户指定继续 T4（account Application 用例与当前身份解析），解除阻塞并回到 applying 语义。
- 输入/输出依据：- / -

## E000046 — system / block
- 时间：2026-09-19T08:01:04Z
- 状态：`applying` → `blocked`
- 原因：用户指定的第二批 apply 范围 T4 已完成：account Application 用例（注册、登录含双维度限流、刷新轮换、退出、当前身份解析、昵称乐观锁修改）与管理用例（init-admin、set-role、set-status 的全局管理员锁、最后管理员保护、会话撤销与同事务审计），配套假端口单测与真实 PostgreSQL 端到端用例。T5–T8 未开始，故仍不进入 testing，按协议置为 blocked 并保留 resume_to=applying。
- 输入/输出依据：E000045 system/resume；tasks.md T4（AC-03、AC-05、AC-06、AC-09） / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| backend/internal/application/account/service.go | add | T4 | - | 用例依赖装配、限流键派生与双维度失败计数辅助 | - |
| backend/internal/application/account/auth.go | add | T4 | - | 注册、登录、刷新轮换、退出与会话建立用例 | - |
| backend/internal/application/account/identity.go | add | T4 | - | 当前身份解析（会话与账户取数据库当前值）与昵称修改 | - |
| backend/internal/application/account/admin.go | add | T4 | - | init-admin/set-role/set-status 管理用例，锁顺序、最后管理员保护、会话撤销与审计同事务 | - |
| backend/internal/application/account/errors.go | add | T4 | - | 统一凭证错误、注册关闭、限制等待与管理员冲突错误 | - |
| backend/internal/application/account/service_test.go | add | T4 | - | 假端口单测：注册规则、登录三类失败一致、限流不提前放行、轮换与重放、身份取当前值、乐观锁、管理幂等与最后管理员保护、锁顺序 | - |
| backend/internal/application/account/fakes_test.go | add | T4 | - | 内存假端口：账户、会话、令牌轮换、限流、审计、锁、散列、签名、密钥、事务与时钟 | - |
| backend/internal/application/ports/account.go | add | T4 | - | AccountLocks 端口：全局管理员锁与用户行锁，锁顺序写进接口契约 | - |
| backend/internal/application/ports/security.go | modify | T4 | - | 新增 ThrottleKeys 与 TokenSecrets 端口 | - |
| backend/internal/infrastructure/security/throttle_keys.go | add | T4 | - | HMAC-SHA-256 限流查找键，账号与 IP 维度域分离 | - |
| backend/internal/infrastructure/security/token_secrets.go | add | T4 | - | 32 字节随机刷新令牌与 SHA-256 摘要 | - |
| backend/test/integration/account_service_test.go | add | T4 | - | 真实 PostgreSQL 端到端：注册→登录→身份→改昵称→刷新→重放撤销→退出幂等；限流跨 IP 生效；管理维护与最后管理员保护 | - |
| backend/internal/domain/account/session.go | modify | T4 | - | 会话期限与访问令牌期限改为参数化，取配置值 | - |
| backend/internal/domain/account/session_test.go | modify | T4 | - | 适配参数化期限并补配置期限用例 | - |
| backend/internal/infrastructure/persistence/postgres/session_repository.go | modify | T4 | - | 后继令牌的会话与有效期由被锁会话决定，避免调用方预知 | - |
| backend/test/integration/account_repository_test.go | modify | T4 | - | 适配参数化会话构造 | - |

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| cd backend && go test -count=1 ./... | 本地单元与架构测试，Go 1.26.6 | passed | 16 个包全部通过，含新增 application/account；architecture 层级检查未退化 |
| cd backend && go test -race -count=1 ./internal/... ./test/integration | 本地竞态检测 | passed | 单元与真实库集成在 -race 下均通过 |
| VELIS_TEST_DATABASE_URL=<临时 pgvector/pg17 容器> go test -count=1 -v ./test/integration | 真实 PostgreSQL 17.11（临时容器，用后删除） | passed | 13 个用例通过，含 3 个新端到端用例：账户生命周期（注册/登录/身份/昵称乐观锁/刷新/重放撤销/退出幂等）、登录限流（账号维度跨 IP 生效）、管理维护（初始化管理员、重复初始化拒绝、角色变更撤销会话、同值幂等、最后管理员保护、禁用后登录失败） |
| cd backend && go vet ./... | 本地，Go 1.26.6 | passed | 无输出 |
| gofmt -l <本批次文件> | 本地，Go 1.26.6 | passed | 新增与修改文件全部格式化干净 |
| make check、Web/Vitest、资源预算实测 | T8 范围 | not-run | 本批次只交付 T4，未运行前端与 1 vCPU/512 MiB 资源验证 |

## E000047 — system / resume
- 时间：2026-09-19T08:06:55Z
- 状态：`blocked` → `applying`
- 原因：用户指定继续 T5（HTTP 层与认证中间件）与 T6（CLI 适配），解除阻塞回到 applying。
- 输入/输出依据：- / -

## E000048 — system / block
- 时间：2026-09-19T08:21:09Z
- 状态：`applying` → `blocked`
- 原因：用户指定的本批 apply 范围 T5、T6 已完成。T5：按端点矩阵实现 Hertz 认证路由与中间件（JSON 内容类型、精确来源、CSRF、可信代理来源 IP）、Bearer 身份解析、严格 JSON 解码（未知/重复字段与尾随内容）、Cookie 策略（__Host- 与非前缀两套）、错误码常量表与统一映射、application/account 与 article 两处错误码迁移、OpenAPI 契约与配置示例更新、API bootstrap 装配。T6：CLI 运行器新增 stdin 通道与 TTY 隐藏输入、account init-admin/set-role/set-status 命令、AdminService 装配与管理端回归。T7（最小 Vue 闭环）与 T8（主链路回归、资源实测、README 收口）未开始，因此不进入 testing，按协议置为 blocked 并保留 resume_to=applying。
- 输入/输出依据：E000047 system/resume；tasks.md T5（AC-04、AC-05、AC-06、AC-10、AC-12）与 T6（AC-07、AC-08） / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| backend/internal/interfaces/http/hertz/presenter/codes.go | add | T5 | - | 统一错误码常量表，调用点不再写字面量 | - |
| backend/internal/interfaces/http/hertz/presenter/errors.go | add | T5 | - | 认证错误到 HTTP 状态/码/Retry-After 的映射，未知错误按依赖不可用拒绝 | - |
| backend/internal/interfaces/http/hertz/presenter/decode.go | add | T5 | - | 严格 JSON 解码：正文上限、未知字段、重复字段与尾随内容 | - |
| backend/internal/interfaces/http/hertz/presenter/response.go | modify | T5 | - | 改用错误码常量 | - |
| backend/internal/interfaces/http/hertz/middleware/guard.go | add | T5 | - | 端点矩阵校验：JSON 内容类型、精确来源（Origin/Referer 回退、cross-site 拒绝）与 CSRF 常量时间比较 | - |
| backend/internal/interfaces/http/hertz/middleware/auth.go | add | T5 | - | Bearer 身份解析中间件与身份上下文读取 | - |
| backend/internal/interfaces/http/hertz/middleware/client_ip.go | add | T5 | - | 可信代理来源 IP 解析：仅信任链内读取转发头，畸形头回退对端 | - |
| backend/internal/interfaces/http/hertz/middleware/client_ip_test.go | add | T5 | - | 信任链边界测试：直连/多层/全可信/畸形/空配置 | - |
| backend/internal/interfaces/http/hertz/middleware/recovery.go | modify | T5 | - | 改用错误码常量 | - |
| backend/internal/interfaces/http/hertz/handler/account.go | add | T5 | - | 注册、登录、刷新、退出、本人账户读取与昵称修改处理器；低基数日志只记操作/结果/request ID/用户与会话 ID | - |
| backend/internal/interfaces/http/hertz/handler/cookies.go | add | T5 | - | Cookie 策略：__Host- 前缀与本地开发两套名称、Secure/HttpOnly/SameSite/Path 与 CSRF 值生成 | - |
| backend/internal/interfaces/http/hertz/handler/article.go | modify | T5 | - | INVALID_ARGUMENT → VALIDATION_FAILED 迁移并使用错误码常量 | - |
| backend/internal/interfaces/http/hertz/handler/health.go | modify | T5 | - | 改用错误码常量 | - |
| backend/internal/interfaces/http/hertz/dto/account.go | add | T5 | - | 认证请求与账户响应 DTO，刷新令牌不进入正文 | - |
| backend/internal/interfaces/http/hertz/router.go | modify | T5 | - | Options 装配结构与认证路由注册（auth 为 nil 时不注册，保持匿名行为） | - |
| backend/internal/interfaces/http/hertz/router_test.go | modify | T5 | - | 适配 Options 与新的 VALIDATION_FAILED 错误码 | - |
| backend/internal/interfaces/http/hertz/account_test.go | add | T5 | - | 端点契约测试：注册/登录/刷新/退出/本人资源、来源与 CSRF 矩阵、严格解码、Cookie 属性、Bearer 与错误映射 | - |
| backend/internal/bootstrap/account.go | add | T5 | - | 认证用例与管理用例装配，以及可信代理网段解析 | - |
| backend/internal/bootstrap/api.go | modify | T5 | - | auth.enabled 时装配认证路由并记录开关与代理数量 | - |
| backend/api/openapi/velis.yaml | modify | T5 | - | 新增认证与账户端点、安全方案、请求响应 Schema 与错误码说明 | - |
| backend/configs/config.example.yaml | modify | T5 | - | auth 分组示例：开发/生产 Cookie 对照、可信代理说明与限流、散列参数 | - |
| backend/internal/application/account/admin.go | modify | T6 | - | 管理用例拆分为独立 AdminService，使 CLI 在注入认证配置前可用 | - |
| backend/internal/application/account/service.go | modify | T6 | - | 认证用例 Deps 去掉管理专用依赖 | - |
| backend/internal/interfaces/cli/cli.go | modify | T6 | - | 运行器改为 Options 装配，新增 stdin 通道与 source/account 命令组分发 | - |
| backend/internal/interfaces/cli/account.go | add | T6 | - | init-admin（-password-stdin 与隐藏输入二次确认）、set-role、set-status 命令 | - |
| backend/internal/interfaces/cli/tty.go | add | T6 | - | TTY 隐藏输入，非终端环境提示改用 -password-stdin | - |
| backend/internal/interfaces/cli/source.go | modify | T6 | - | Source 命令迁到新运行器，行为保持不变 | - |
| backend/internal/interfaces/cli/source_test.go | modify | T6 | - | 适配新构造并保留 Source 命令回归 | - |
| backend/internal/interfaces/cli/account_test.go | add | T6 | - | stdin 解析（含只移除一个结尾 LF/CRLF）、隐藏输入确认、错误与幂等输出测试 | - |
| backend/internal/bootstrap/admin.go | modify | T6 | - | 装配管理用例、stdin 与 TTY 隐藏输入 | - |
| backend/go.mod | modify | T6 | - | 新增 golang.org/x/term（隐藏输入的规格要求） | - |
| backend/go.sum | modify | T6 | - | 同步依赖校验和 | - |

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| cd backend && go build ./... && go vet ./... | 本地，Go 1.26.6 | passed | 四个入口与全部新增包编译通过，vet 无输出 |
| cd backend && go test -count=1 ./... && go test -race -count=1 ./... | 本地单元、架构与竞态检测 | passed | 16 个包全部通过，含 architecture 层级检查；middleware 与 http/hertz 新增用例通过 |
| VELIS_TEST_DATABASE_URL=<临时 pgvector/pg17 容器> go test -count=1 -race ./test/integration | 真实 PostgreSQL 17.11（临时容器，用后删除） | passed | 13 个集成用例通过，覆盖账户仓储、轮换重放、限流原子计数、管理与清理 |
| 以 VELIS_AUTH_* 配置启动 velis-api 并对真实数据库执行 curl 闭环 | 真实 PostgreSQL 17.11 + 真实 API 进程（127.0.0.1:18080） | passed | 注册 201（用户名规范化为 alice、昵称保留中文）；弱密码 400 VALIDATION_FAILED；登录 200 并下发 velis_refresh（HttpOnly）与 velis_csrf；GET /account/me 200；PATCH 昵称 200；匿名访问 401 AUTH_SESSION_INVALID；缺少 Origin 的登录 403 CSRF_REJECTED；刷新 200 并轮换 Cookie；重放旧刷新令牌 401 且该会话被撤销（新令牌同时失效）；退出 204 且重复退出仍 204 |
| 对真实数据库执行 velis-admin account 命令 | 真实 PostgreSQL 17.11 + CLI | passed | init-admin（-password-stdin）创建管理员并写入 local_cli 审计；重复初始化返回「已存在有效管理员」；禁用与降级最后一位有效管理员都被拒绝；非法角色与未知账户给出明确错误；无 TTY 且未给 -password-stdin 时提示改用该参数；source list 回归正常 |
| OpenAPI 解析与引用校验 | 本地 YAML 解析，2026-09-19 | passed | 10 条路径（新增 6 条认证与账户端点），全部 $ref 可解析，bearerAuth 与 refreshCookie 安全方案就位 |
| gofmt -l <本批次文件> | 本地，Go 1.26.6 | passed | 新增与修改文件全部格式化干净；既有 CRLF 文件去 CR 后同样干净 |
| make check、Web/Vitest、1 vCPU/512 MiB 资源实测、README 收口 | T8 范围 | not-run | 本批次只交付 T5、T6 |

## E000049 — system / resume
- 时间：2026-09-19T08:29:18Z
- 状态：`blocked` → `applying`
- 原因：用户指定继续 T7（最小 Vue 账户闭环），解除阻塞回到 applying。
- 输入/输出依据：- / -

## E000050 — system / block
- 时间：2026-09-19T08:33:06Z
- 状态：`applying` → `blocked`
- 原因：用户指定的 T7（最小 Vue 账户闭环）已完成：统一 API 请求层与认证接口、纯 TS 会话模块（内存令牌、单飞刷新、Web Locks 串行化、无 Web Locks 时的 10 秒租约降级、BroadcastChannel 同步、失败不清重放策略）、注册/登录/本人账户三个最小页面与路由守卫、页面重载恢复。T8（后端主链路回归、最小 Web 回归、资源实测、配置示例与 README 收口）未开始，因此不进入 testing，按协议置为 blocked 并保留 resume_to=applying。
- 输入/输出依据：E000049 system/resume；tasks.md T7（AC-10、AC-11）与 spec §4.3 令牌驻留与多标签规则 / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| web/src/types/account.ts | add | T7 | - | 账户、会话响应与注册输入的共享类型 | - |
| web/src/api/client.ts | modify | T7 | - | 抽出统一请求层：同源凭证、Bearer 注入、错误信封解析为带错误码的 ApiError | - |
| web/src/api/auth.ts | add | T7 | - | 注册、登录、刷新、退出、本人账户读取与昵称修改接口 | - |
| web/src/features/auth/session.ts | add | T7 | - | 纯 TS 会话模块：访问令牌只驻留内存、单标签单飞刷新、锁/租约/广播协调、失败不重放 | - |
| web/src/features/auth/browser.ts | add | T7 | - | 浏览器实现：Web Locks 锁、BroadcastChannel、只存元数据的 localStorage 租约、CSRF Cookie 读取与应用级单例 | - |
| web/src/features/auth/useSession.ts | add | T7 | - | 把会话快照接到 Vue 响应式，供页面与导航使用 | - |
| web/src/views/RegisterView.vue | add | T7 | - | 最小注册页：明示用户名与密码规则，成功后跳转登录页并预填用户名 | - |
| web/src/views/LoginView.vue | add | T7 | - | 最小登录页：写入内存会话并按 redirect 跳转，不做自动重试 | - |
| web/src/views/AccountView.vue | add | T7 | - | 本人账户页：读取账户、修改昵称、退出登录 | - |
| web/src/router/index.ts | modify | T7 | - | 新增注册、登录与受保护的账户路由，守卫先尝试用刷新 Cookie 恢复会话 | - |
| web/src/App.vue | modify | T7 | - | 导航按登录状态显示账户入口或登录/注册 | - |
| web/src/main.ts | modify | T7 | - | 应用启动时尝试恢复会话 | - |
| web/src/styles.css | modify | T7 | - | 账户页面所需的最小样式，沿用现有设计令牌 | - |
| web/src/features/auth/session.test.ts | add | T7 | - | 会话协调单测：内存驻留、过期边界、单飞、锁与租约降级、失败不重放、跨标签退出 | - |
| web/src/features/auth/browser.test.ts | add | T7 | - | CSRF Cookie 读取与租约存储只保存元数据的测试 | - |
| web/src/api/auth.test.ts | add | T7 | - | 认证请求契约测试：方法、CSRF 头、Bearer 头与错误码解析 | - |

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| cd web && npm test | 本地 Vitest（node 环境），2026-09-19 | passed | 5 个测试文件 29 个用例通过，含会话协调 13 例、认证请求契约 5 例、Cookie 与租约 5 例，以及既有文章与客户端用例 |
| cd web && npm run build | 本地 vue-tsc 类型检查 + Vite 生产构建 | passed | 类型检查与构建通过，注册/登录/账户页按路由分包产出 |
| 启动 Vite 开发服务器 + 真实 API（auth 启用）并用 curl 走代理 | 真实 PostgreSQL 17.11（临时容器）+ 真实后端进程 + Vite 5173 | passed | 首页 200 且标题为 Velis；经代理的 /api/v1/ping 200；经代理注册返回 201；经代理登录返回 200 且 Cookie 落在 localhost 域、velis_refresh 为 HttpOnly |
| 浏览器闭环（重载恢复、多标签 Web Locks/BroadcastChannel、跨标签退出） | 需要真实浏览器 | not-run | 按测试规格留作人工冒烟，由 T8 记录步骤与结果；当前仓库未引入浏览器自动化依赖 |
| make check、1 vCPU/512 MiB 资源实测、README 与第三方声明收口 | T8 范围 | not-run | 本批次只交付 T7 |

## E000051 — system / resume
- 时间：2026-09-19T08:49:32Z
- 状态：`blocked` → `applying`
- 原因：T8 已开始执行，解除 T7 之后的暂停状态回到 applying，以便按协议收尾。
- 输入/输出依据：- / -

## E000052 — apply / complete
- 时间：2026-09-19T08:49:32Z
- 状态：`applying` → `testing`
- 原因：T8 已完成，T1–T8 全部交付：后端主链路回归（make check、go vet、真实 PostgreSQL 集成）、最小 Web 回归（Vitest 与构建）、真实浏览器闭环冒烟、多实例与重启语义、1 vCPU/512 MiB 资源实测、配置示例与第三方声明收口、README 更新。apply 阶段按协议收尾进入 testing。
- 输入/输出依据：E000049 system/resume（T7 完成后回到 applying）；tasks.md T8（AC-12 含全部回归） / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| README.md | modify | T8 | - | 新增账户与认证章节、认证端点、环境变量、可信代理要求与当前限制；更新功能表与验证现状 | - |
| .env.example | modify | T8 | - | 列出认证相关变量名与本地开发取值，真实密钥仅通过环境注入 | - |
| compose.yaml | modify | T8 | - | velis-api 增加可信代理网段变量与说明，避免反代下降级为全局共享 IP | - |
| backend/THIRD_PARTY_NOTICES.md | add | T8 | - | 第三方组件、固定版本、许可证与认证相关组件用途 | - |

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| make check（gofmt + Go 单测 + 竞态 + 构建 + Vitest + Web 构建） | 本地，Go 1.26.6 / Node 24 | passed | 退出码 0；后端全部包通过，前端 5 个测试文件 29 个用例通过，Web 类型检查与生产构建通过 |
| cd backend && go vet ./... | 本地，Go 1.26.6 | passed | 无输出 |
| VELIS_TEST_DATABASE_URL=<临时 pgvector/pg17> go test -count=1 -race -v ./test/integration | 真实 PostgreSQL 17.11（临时容器，用后删除） | passed | 13 个集成用例全部通过，覆盖账户仓储、刷新轮换与重放、限流原子计数、审计与清理、管理锁与地端到端生命周期 |
| 两个 API 实例共用同一数据库执行跨实例检查 | 真实 PostgreSQL + 两个 API 进程（18081/18082） | passed | 实例 A 签发的访问令牌在实例 B 上可用（200）；实例 A 上 5 次失败后实例 B 用正确密码也被 429 拦截；重启实例 A 后限流状态仍在（429）；在实例 B 退出后同一令牌在实例 A 上失效（401） |
| docker run --cpus=1 --memory=512m 运行 API 并测量登录延迟、内存与并发额度 | 受限容器 1 vCPU / 512 MiB + 真实 PostgreSQL | passed | 30 次顺序登录 P95=46.4ms（目标 ≤750ms）；散列期间内存增量 54.9 MiB（目标 ≤64 MiB，基线 8.1 MiB → 峰值 63.0 MiB，容器上限 512 MiB 未触发 OOM）；并发两次登录时其中一次在 16ms 内返回 503 AUTH_HASH_BUSY（目标 ≤100ms） |
| CDP 驱动真实 Chrome for Testing 148 执行账户闭环冒烟 | 真实浏览器 + Vite 5173 + 真实 API + 真实 PostgreSQL | passed | 9/9 通过：注册后跳转登录页并预填用户名；登录进入账户页；localStorage 与 sessionStorage 中无令牌；重载后用刷新 Cookie 恢复登录；修改昵称成功；第二个标签共享同一登录态；标签 1 退出后标签 2 同步清空（BroadcastChannel）；未登录访问受保护页重定向到登录页；缺少 CSRF 头的刷新返回 403 |
| docker compose config -q 与 config.example.yaml 解析 | 本地 Compose v2 与 YAML 解析 | passed | compose 配置校验通过（含可信代理变量）；示例配置含 auth 分组且 18 个键可加载 |
| go run golang.org/x/vuln/cmd/govulncheck@latest ./... | 本地，漏洞库在线 | passed | 影响代码的漏洞 0 个；模块级仅剩 1 个无修复版本且未被调用的告警 |
| 浏览器兼容性矩阵与生产反代端到端（Safari/Firefox、nginx 真实部署） | 需要多浏览器与生产形态部署 | not-run | 本轮只在 Chrome for Testing 148 与 Vite 开发代理下验证；生产同源反代形态未在浏览器中核对，列入剩余风险 |

## E000053 — test / start
- 时间：2026-09-19T08:56:54Z
- 状态：`testing` → `testing`
- 原因：用户授权：test 所有任务都已完成，开始整体测试。按 test-spec.md 的 P0-01–P0-13 与 P1-01–P1-07 执行独立验证，不修改实现代码。
- 输入/输出依据：E000052 apply/complete（状态 testing）；change 文档 spec.md/tasks.md/test-spec.md / -

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| python3 scripts/inspect_change.py changes/account-access-foundation | 本地仓库，2026-09-19 | passed | 状态 testing，最后事件 E000052，建议下一阶段 test；事件尾 hash sha256-v1:419af19c… |

## E000054 — test / fail
- 时间：2026-09-19T09:49:40Z
- 状态：`testing` → `changes-requested`
- 原因：整体测试完成：20 个用例中 18 个通过，2 个未达标并记录 Finding。P1-04 清理任务在运行期没有任何调用方（清理仓储仅被集成测试引用，Worker 只调度抓取，auth.cleanup_interval/cleanup_batch 无消费点），保留期清理不会发生；P1-07 规格要求的指标载体缺失密码散列耗时、清理数量与刷新重放三项，限流日志亦不区分账号/IP 维度。
- 输入/输出依据：E000053 test/start；change 文档 spec.md、tasks.md、test-spec.md；工作树实现 / test-spec.md 执行结果表与未覆盖章节更新；状态转入 changes-requested（failure_source=test），建议下一阶段 fix
- Findings：T1-F1, T1-F2

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/test-spec.md | modify | P1-04, T1-F1, T1-F2 | - | 按用例逐条记录执行结果、层级与摘要，并补记未覆盖项与规格内部张力 | - |

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| make check（gofmt + Go 单测 + 竞态 + 构建 + Vitest + Web 构建） | 本地 Go 1.26.6 / Node 24.15 | passed | 退出码 0；后端全部包通过（含架构层级依赖测试），前端 5 个文件 29 例通过，类型检查与生产构建通过 |
| cd backend && go vet ./... | 本地 Go 1.26.6 | passed | 无输出 |
| VELIS_TEST_DATABASE_URL=<临时 pgvector/pg17> go test -count=1 -race -v ./test/integration | 真实 PostgreSQL 17.11 临时容器 | passed | 13 个集成用例全部通过（仓储唯一性、刷新轮换与重放、限流原子计数、审计与清理 SQL 语义、管理锁、账户生命周期） |
| node t_validation.mjs（P0-02/P0-03） | 真实 API + 真实 PG | passed | 77/77：用户名与昵称边界、大小写等价、弱密码表命中与子串不误拒、密码类别与字符集、登录错误一致性 |
| node t_register.mjs（P0-01/P0-06） | 真实 API + 真实 PG + CLI | passed | 18/18：并发同名注册（默认散列额度下 503 AUTH_HASH_BUSY 拒绝、放开额度后 9 次 409）、注册开关、禁用后令牌立即失效 |
| node t_jwt.mjs（P0-04） | 真实 API + 真实 PG | passed | 30/30：签名、算法、声明、时间、长度与严格 Base64URL 攻击面全部 401 |
| node t_rotation_setup/phase2/phase3.mjs（P0-04 轮换） | 真实 API 三次重启 + 真实 PG | passed | 9/9：k1 → active k2/previous k1 → 仅 k2 三阶段；未升级实例拒绝 k2 令牌；移除 previous 后 k1 令牌被拒 |
| node t_session.mjs（P0-05/P0-06） | 真实 API + 真实 PG | passed | 30/30：轮换、重放撤销、未知令牌不误撤销、退出幂等、跨设备隔离、并发刷新单赢家、本人资源边界、管理接口不存在 |
| node t_guard.mjs（P0-09） | 真实 API | passed | 32/32：来源矩阵、CSRF、内容类型与严格 JSON |
| node t_throttle.mjs + t_throttle_restart.sh + t_throttle_window.mjs（P0-12） | 真实 API 多实例 + 真实 PG + 短窗口实例 | passed | 30/30：账号与 IP 维度阈值、Retry-After、跨实例共享、重启保留、滚动窗口与到期恢复、成功不清除记录、数据库故障 503 不放行 |
| node t_proxy.mjs + t_collapse.mjs（P0-13） | 真实 nginx 反代容器 + 真实 API + 真实 PG | passed | 14/14：转发链 8 例、真实反代按 XFF 解析、伪造头被覆盖、按客户端隔离、未配置可信代理时退化为单一来源 |
| bash t_config.sh + t_shapes.sh（P1-05） | 真实 API 多形态 | passed | 32/32：11 项非法配置启动失败（含短密钥不泄露）、本地/生产 Cookie 形态、认证关闭时匿名能力保持 |
| bash t_cli.sh（P0-07/P0-08） | 真实 CLI + pty + 真实 PG | passed | 39/39：初始化并发、同名不覆盖、最后管理员保护、幂等、会话撤销、审计来源与脱敏、隐藏输入与 -password-stdin 一致 |
| bash t_password.sh（P0-11） | 真实 PG + 真实 API | passed | 12/12：独立随机盐、PHC 参数、不落明文、参数上限与畸形值拒绝启动 |
| bash t_migration.sh（P0-10） | 真实 PG + 迁移 CLI | passed | 22/22：空库新装、既有 RSS 库升级保留数据、down→up 可重复、约束完整 |
| bash t_regression.sh（P1-03） | 真实 Worker + 真实 API + 真实 PG | passed | 17/17：Source CLI、Worker 运行与失败回退、匿名阅读链路、架构层级 |
| node t_browser.mjs（P1-01/P1-02） | 真实 Chrome 148 + Vite + 真实 API + 真实 PG | passed | 16/16：错误与重试、注册登录闭环、重载恢复、改昵称、两标签并发重载保持登录、跨标签退出广播、CSRF 拒绝、匿名阅读 |
| node t_resource.mjs + bash t_resource_mem.sh（P1-06） | 1 vCPU/512 MiB 容器 + 真实 PG | passed | P95 32–44 ms（目标 ≤750）；内存增量 40 MiB（冷基线）/20 MiB（预热），目标 ≤64 MiB；并发散列仅一个执行，其余 7–21 ms 内 503 且不计失败 |
| bash t_logs.sh（P1-07） | 真实 API 日志 | failed | 20 项中 16 项通过（字段规则与脱敏全部通过），4 项失败：缺密码散列耗时、缺清理数量、缺刷新重放信号、限流日志不区分维度 |
| 代码核对：NewCleanupRepository / DeleteExpired* / CleanupInterval 的调用方 | 工作树静态核对 | failed | NewCleanupRepository 仅出现在 test/integration；CleanupInterval/CleanupBatch 只在校验处被读取，无运行时消费点；Worker 仅装配抓取调度 |

## E000055 — fix / start
- 时间：2026-09-19T13:13:05Z
- 状态：`changes-requested` → `fixing`
- 原因：按用户授权处理 T1-F1（Worker 接入保留期清理）与 T1-F2（补齐散列耗时、刷新重放、清理数量三项指标载体及限流维度）。均为实现偏差，不改动 spec 行为契约。
- 输入/输出依据：E000054 test/fail；未关闭 findings T1-F1、T1-F2 / -
- Findings：T1-F1, T1-F2

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| python3 scripts/inspect_change.py changes/account-access-foundation | 本地仓库，2026-09-19 | passed | 状态 changes-requested，未关闭 findings T1-F1、T1-F2，建议下一阶段 fix |

## E000056 — fix / complete
- 时间：2026-09-19T13:21:38Z
- 状态：`fixing` → `testing`
- 原因：T1-F1 与 T1-F2 已按最小完整修正处理：清理用例接入 Worker 并消费 auth.cleanup_interval/cleanup_batch；补齐密码散列耗时、刷新重放、清理数量三项指标载体，并让限流日志区分账号/IP 维度。HTTP 响应语义未变。
- 输入/输出依据：E000055 fix/start（finding_ids T1-F1、T1-F2）；E000054 test/fail 的失败证据 / 代码与测试修改、README 与配置示例更新；状态转入 testing，建议下一阶段 test
- 已处理 Findings：T1-F1, T1-F2

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| backend/internal/application/account/cleanup.go | add | T1-F1 | FailureRetention/BlockRetention/SessionRetention (14-22)、CleanupService.Run (56)、drain (80) | 新增保留期清理用例：按 spec 保留期分批删除，单轮批次数有上限，失败返回已完成的部分结果 | - |
| backend/internal/application/account/cleanup_test.go | add | T1-F1 | TestCleanupAppliesRetentionCutoffs、TestCleanupDrainsUntilBatchNotFull、TestCleanupStopsAtBatchCap、TestCleanupReportsPartialResultOnFailure、TestCleanupRejectsInvalidDeps | 覆盖保留期计算、分批排空、批次上限与失败语义 | - |
| backend/internal/interfaces/scheduler/cleanup.go | add | T1-F1, T1-F2 | CleanupScheduler.Run (28)、runOnce (42) | 按 cleanup_interval 周期触发清理，并以结构化字段记录删除数量与耗时 | - |
| backend/internal/interfaces/scheduler/cleanup_test.go | add | T1-F1 | TestCleanupSchedulerLogsCounts、TestCleanupSchedulerRepeatsAndToleratesFailure | 验证周期执行、日志字段与失败不影响调度 | - |
| backend/internal/bootstrap/worker.go | modify | T1-F1 | RunWorker (19)、buildCleanupScheduler (58) | Worker 并发运行抓取调度与清理调度；认证关闭时不启动清理（清理间隔此时为零值） | - |
| backend/internal/infrastructure/security/timed_password_hasher.go | add | T1-F2 | TimedPasswordHasher.Hash (21)、Verify (28)、observe (35) | 记录密码散列与校验耗时，错误原样透传，不写口令与散列值 | - |
| backend/internal/infrastructure/security/timed_password_hasher_test.go | add | T1-F2 | TestTimedPasswordHasherRecordsDurationWithoutSecrets、TestTimedPasswordHasherKeepsErrorsAndBusyIsolation | 验证耗时字段落地、错误透传且不泄露口令与散列 | - |
| backend/internal/bootstrap/account.go | modify | T1-F2 | buildAuthService (22)、timedHasher (27) | API 路径的散列实现外包观测层 | - |
| backend/internal/bootstrap/api.go | modify | T1-F2 | buildAuthService 调用处 | 向认证装配传入进程日志器 | - |
| backend/internal/application/account/errors.go | modify | T1-F2 | RateLimitedError.Dimension (26) | 限流错误携带触发维度，仅用于日志 | - |
| backend/internal/application/account/service.go | modify | T1-F2 | activeThrottle (77) | 返回真正触发截止时间的维度 | - |
| backend/internal/application/account/auth.go | modify | T1-F2 | Login (74-80) | 把维度写入限流错误 | - |
| backend/internal/application/account/service_test.go | modify | T1-F2 | TestLoginThrottleReportsIPDimension | 断言账号与 IP 两种维度都被正确标注 | - |
| backend/internal/interfaces/http/hertz/handler/logging.go | add | T1-F2 | logAttributes (13) | 为限流补 dimension 字段、为刷新重放补 event=refresh_replay，只影响日志 | - |
| backend/internal/interfaces/http/hertz/handler/logging_test.go | add | T1-F2 | TestLogAttributesDistinguishesThrottleDimension、TestLogAttributesDoNotChangeResponseMapping | 断言字段规则与响应映射不变 | - |
| backend/internal/interfaces/http/hertz/handler/account.go | modify | T1-F2 | reject (167) | 认证失败日志附加观测字段 | - |
| README.md | modify | T1-F1, T1-F2 | - | 写明保留期清理由 Worker 执行、认证关闭时不清理，并更新日志字段清单 | - |
| backend/configs/config.example.yaml | modify | T1-F1 | - | 说明 cleanup_interval/cleanup_batch 的生效条件与固定保留期 | - |

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| make check（gofmt + Go 单测 + 竞态 + 构建 + Vitest + Web 构建） | 本地 Go 1.26.6 / Node 24.15 | passed | 退出码 0；新增清理与观测用例一并通过，架构层级依赖测试通过 |
| cd backend && go test -count=1 -race ./... | 本地 Go 1.26.6 | passed | 全量包强制重跑通过（含 -race） |
| cd backend && go vet ./... | 本地 Go 1.26.6 | passed | 无输出 |
| VELIS_TEST_DATABASE_URL=<临时 pgvector/pg17> go test -count=1 -race ./test/integration | 真实 PostgreSQL 17.11 临时容器 | passed | 13 个集成用例通过，清理仓储语义未受影响 |
| bash t_fix_cleanup.sh | 真实 Worker + 真实 PostgreSQL（构造跨越保留期的数据） | passed | 23/23：认证开启时按 1s 周期清理，删除 8 天前撤销的会话、7 天前过期的令牌、45 分钟前的失败事件、25 小时前过期的限制，保留未超期数据；用户与管理审计未被删除；认证关闭时不清理且无清理日志；日志含 operation=cleanup/result/deleted_*/duration_ms |
| node t_fix_logs.mjs + bash t_fix_logs.sh | 真实 API + 真实 PostgreSQL + 日志核对 | passed | 22/22：出现 operation=password_hash\|password_verify + duration_ms、code=AUTH_RATE_LIMITED + dimension=account\|ip、event=refresh_replay；重放仍返回 401 AUTH_SESSION_INVALID、限流仍返回 429；日志不含口令、散列、原始 IP 与令牌 |

## E000057 — test / start
- 时间：2026-09-19T13:25:45Z
- 状态：`testing` → `testing`
- 原因：用户授权重跑测试：按 test-spec.md 全量重跑 P0-01–P0-13 与 P1-01–P1-07，重点复核修复后的 P1-04（Worker 清理）与 P1-07（可观测性）。不修改实现代码。
- 输入/输出依据：E000056 fix/complete（状态 testing，fix 轮次 1）；test-spec.md 用例矩阵 / -

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| python3 scripts/inspect_change.py changes/account-access-foundation | 本地仓库，2026-09-19 | passed | 状态 testing，未关闭 findings 为空，建议下一阶段 test |

## E000058 — test / complete
- 时间：2026-09-19T13:33:52Z
- 状态：`testing` → `reviewing`
- 原因：第 2 轮全量重跑：20 个用例全部通过，上一轮的 T1-F1、T1-F2 已复核关闭。所有用例在真实进程、真实 PostgreSQL、真实 nginx 反代、1 vCPU/512 MiB 容器与真实 Chrome 148 上执行。
- 输入/输出依据：E000057 test/start；E000056 fix/complete 的实现与定点证据 / test-spec.md 执行结果表更新；状态转入 reviewing，建议下一阶段由独立 Agent 执行 review

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/test-spec.md | modify | - | - | 按第 2 轮重跑结果更新执行结果表：P1-04/P1-07 由 failed 改为 passed，P1-06 更新实测数值，并记录编排脚本未启动资源容器导致的假通过与修正 | - |

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| make check（gofmt + Go 单测 + 竞态 + 构建 + Vitest + Web 构建） | 本地 Go 1.26.6 / Node 24.15 | passed | 退出码 0；后端全部包与架构层级依赖测试通过，前端 29 例与生产构建通过 |
| cd backend && go test -count=1 -race ./... 与 go vet ./... | 本地 Go 1.26.6 | passed | 全量包强制重跑通过，vet 无输出 |
| VELIS_TEST_DATABASE_URL=<临时 pgvector/pg17> go test -count=1 -race ./test/integration | 真实 PostgreSQL 17.11 临时容器 | passed | 13 个集成用例通过（仓储唯一性、刷新轮换与重放、限流原子计数、审计与清理保留期、管理锁、账户生命周期） |
| node t_validation.mjs、t_register.mjs、t_jwt.mjs、t_rotation_*.mjs、t_session.mjs、t_guard.mjs | 真实 API 多实例 + 真实 PG | passed | P0-02/P0-03 77/77、P0-01/P0-06 18/18、P0-04 30/30 与轮换 9/9、P0-05/P0-06 30/30、P0-09 32/32 |
| bash t_password.sh、t_config.sh、t_shapes.sh | 真实 API 多形态 + 真实 PG | passed | P0-11 12/12（独立盐、PHC 参数、不落明文、参数上限拒绝启动）、P1-05 启动校验 11/11、本地/生产/关闭三种形态 21 项 |
| node t_throttle.mjs + bash t_throttle_restart.sh + node t_throttle_window.mjs | 真实 API 多实例 + 真实 PG + 数据库停机 | passed | P0-12 30/30：账号与 IP 维度阈值、Retry-After、跨实例共享、重启保留、滚动窗口与到期恢复、成功不清除记录、数据库故障 503 不放行 |
| node t_proxy.mjs + t_collapse.mjs（真实 nginx 反代容器） | 真实 nginx + 真实 API + 真实 PG | passed | P0-13 14/14：转发链 8 例、真实反代按 XFF 解析、伪造头被覆盖、按客户端隔离、未配置可信代理时退化为单一来源 |
| bash t_migration.sh | 真实 PG + 迁移 CLI | passed | P0-10 22/22：空库新装、既有 RSS 库升级保留数据、down→up 可重复、约束完整 |
| bash t_cli.sh | 真实 CLI + pty + 真实 PG | passed | P0-07/P0-08 39/39：初始化并发、同名不覆盖、最后管理员保护、幂等、会话撤销、审计来源与脱敏、隐藏输入与 -password-stdin 一致 |
| bash t_regression.sh | 真实 Worker + 真实 API + 真实 PG | passed | P1-03 17/17：Source CLI、Worker 运行与失败回退、匿名阅读链路、架构层级 |
| bash t_fix_cleanup.sh（P1-04） | 真实 Worker + 真实 PG（构造跨越保留期的数据） | passed | 23/23：按 cleanup_interval 周期清理，删除 8 天前撤销的会话、7 天前过期的令牌、45 分钟前的失败事件、25 小时前过期的限制并保留未超期数据；用户与管理审计不受影响；认证关闭时不清理；日志含 operation=cleanup 与各 deleted_* 计数 |
| package: node t_resource.mjs + bash t_resource_mem.sh（P1-06） | 1 vCPU/512 MiB 容器 + 真实 PG | passed | 9/9：P95 32.9 ms（目标 ≤750）；cgroup 内存增量 39 MiB 冷基线 / 20 MiB 预热（目标 ≤64）；并发散列仅一个执行，其余 18.4 ms 内 503 且不计失败 |
| bash t_logs.sh（P1-07） | 真实 API + Worker + 日志核对 | passed | 24/24：八类载体字段全部落地（含 dimension=account\|ip、event=refresh_replay、operation=password_hash\|password_verify+duration_ms、operation=cleanup+deleted_*）；脱敏检查全部通过；/metrics 404 且 README 与 card 写明该限制 |
| node t_browser.mjs（P1-01/P1-02） | 真实 Chrome 148 + Vite + 真实 API + 真实 PG | passed | 16/16：错误与重试、注册登录闭环、重载恢复、改昵称、两标签并发重载保持登录、跨标签退出广播、CSRF 拒绝、匿名阅读 |
| 第 2 轮全量编排 run_all.sh 汇总 | 本地，2026-09-19 | passed | 24 个用例分组中 24 个通过；编排脚本首轮的资源容器缺失已修正，内存测量脚本改为缺容器或缺样本即失败 |

## E000059 — review / start
- 时间：2026-09-19T13:38:56Z
- 状态：`reviewing` → `reviewing`
- 原因：用户授权对 account-access-foundation 执行 review 阶段：由未参与实现的独立上下文对应用代码、测试与 Spec 做只读审查，并生成 review basis。
- 输入/输出依据：E000058 test/complete（状态 reviewing，fix 轮次 1，未关闭 findings 为空）；card.md；spec.md；tasks.md；test-spec.md 与其第 2 轮执行结果 / -

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| python3 scripts/inspect_change.py changes/account-access-foundation | 本地仓库，2026-09-19 | passed | 状态 reviewing，未关闭 findings 为空，建议下一阶段 review |
| python3 scripts/compute_review_basis.py compute . changes/account-access-foundation | 本地仓库，head c513dec2 | passed | 生成 review-basis-v1 内容寻址清单，覆盖工作树改动文件与 card 正文 hash |

## E000060 — review / fail
- 时间：2026-09-19T13:54:21Z
- 状态：`reviewing` → `changes-requested`
- 原因：R1 Review 完成：判定 changes-requested，记录 3 条 must-fix。R1-F1：compose.yaml:83 的 VELIS_AUTH_TRUSTED_PROXY_CIDRS 默认值 172.18.0.0/16 与本机真实 velis_default 网段 172.23.0.0/16 不符（该值属于无关的 dev-ops_my-network），且 compose 未声明 networks/ipam，硬编码猜测值不可能稳定正确；nginx 反代对端不被信任导致 client_ip.go:21-23 退回 TCP 对端，全体用户共用同一 IP 限流键，任一人 30 次失败即全局锁死登录（spec §4.2 点名的部署配置错误），而 bootstrap/api.go:61 只记录 trusted_proxy_count 使配错不可见。R1-F2：test/integration/harness_test.go:31-34 的 _test 库保护只看 url.Parse(...).Path，pgx 会用查询串覆盖同名设置（pgconn/config.go:601-616），带 ?dbname= 的 DSN 即可绕过，随后 m.Up() 与 TRUNCATE ... RESTART IDENTITY CASCADE 会打到非测试库，造成账户、会话、审计、文章、抓取源不可逆丢失。R1-F3：web/src/features/auth/session.ts:178-193 在无 Web Locks 的降级路径上用等待前捕获的 startedAt 计算租约到期时间，且 waitForPeer 返回后不复检租约，导致租约写入即过期、并发等待者同时超时并一起刷新，两会话标签用同一枚单次刷新 Cookie 触发服务端重放撤销。另有 13 条 important 与 17 条 suggestion 进入 deferred，不触发 Fix；候选 SUG-18（velis-migrate DSN 泄露）经 git hash-object 比对确认属既有代码，已撤销。
- 输入/输出依据：E000059 review/start；review basis sha256-v1:b9e3e444f58b9dcd6559eccdab09e75641fe5ff951c6ea623b2ea3266cd26b1b（108 个文件，head c513dec2，verify 通过）；card.md、spec.md、tasks.md、test-spec.md；code_copilot/rules/project-context.md 与 agents/review-protocol.md；工作树完整 diff / review basis sha256-v1:b9e3e444f58b9dcd6559eccdab09e75641fe5ff951c6ea623b2ea3266cd26b1b 上判定 changes-requested；Review 产物写入 evidence/review/report-b9e3e444….md；状态转入 changes-requested（failure_source=review），建议下一阶段 fix，仅处理 R1-F1、R1-F2、R1-F3
- Findings：R1-F1, R1-F2, R1-F3

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/evidence/review/report-b9e3e444f58b9dcd6559eccdab09e75641fe5ff951c6ea623b2ea3266cd26b1b.md | add | R1-F1, R1-F2, R1-F3 | - | Review 产物：承载 must-fix 的绑定行号、触发条件、影响与证据，important/suggestion 的 deferred 清单，以及四项规格口径裁决。位于 basis 未覆盖的 evidence/ 下。 | - |

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| python3 compute_review_basis.py compute . changes/account-access-foundation --write | 本地仓库，head c513dec2 | passed | 生成 review-basis-v1 内容寻址清单，108 个文件，basis=sha256-v1:b9e3e444f58b9dcd6559eccdab09e75641fe5ff951c6ea623b2ea3266cd26b1b |
| python3 compute_review_basis.py verify . changes/account-access-foundation sha256-v1:b9e3e444… | 本地仓库，结论形成后重算 | passed | valid=true；三个维度的审查与裁决均严格只读，输入字节未变化，结论有效 |
| 三维度独立只读审查 + 独立裁决复核（4 个全新上下文） | 本地仓库与 /tmp 一次性验证程序 | passed | 候选 1–19 逐条打开文件核实行号并复现证据；SUG-18 撤销；修正若干转述偏差；仓库内未创建、修改或删除任何文件 |
| docker network inspect velis_default / dev-ops_my-network | 本机 Docker | passed | velis_default Subnet=172.23.0.0/16；172.18.0.0/16 属无关的 dev-ops_my-network，确认 R1-F1 |
| go test ./test/integration/ 与 go test -v（未设置 VELIS_TEST_DATABASE_URL） | 本地 Go 1.26.6 | failed | 输出 ok 而 -v 下 13 SKIP / 0 PASS，确认 IMP-8：跳过与通过在自动门禁中不可区分 |
| /tmp 下用仓库同一份 go.mod/go.sum 复现 pgconn.ParseConfig 的 DSN 覆盖 | pgx v5.9.2 | failed | ?dbname=velis 与 ?database=velis 均把 database 覆盖为 velis，?host= 亦生效，确认 R1-F2 |
| /tmp 下用 golang.org/x/crypto@v0.56.0 复现 argon2 panic | x/crypto v0.56.0 | failed | t=0 → PANIC: number of rounds too small；p=0 → PANIC: parallelism degree too low，确认 IMP-2 |
| git hash-object 与 git rev-parse HEAD:backend/cmd/velis-migrate/main.go 比对 | 本地仓库 | passed | 逐字节一致，确认 SUG-18 属既有代码并撤销该条 |

## E000061 — fix / start
- 时间：2026-09-19T13:59:34Z
- 状态：`changes-requested` → `fixing`
- 原因：用户授权执行 fix，只处理 R1 Review 的三条 must-fix：R1-F1（compose 可信代理默认网段与真实网络不符，导致 IP 维度限流静默退化）、R1-F2（集成测试 _test 库保护可被 DSN 查询参数绕过）、R1-F3（无 Web Locks 时租约写入即过期且等待方不复检）。13 条 important 与 17 条 suggestion 保持 deferred，不在本轮处理。
- 输入/输出依据：E000060 review/fail（changes-requested，failure_source=review，未关闭 findings R1-F1/R1-F2/R1-F3）；review basis sha256-v1:b9e3e444…；evidence/review/report-b9e3e444….md 中的三条 must-fix 详情 / -
- Findings：R1-F1, R1-F2, R1-F3

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| python3 scripts/inspect_change.py changes/account-access-foundation | 本地仓库，2026-09-19 | passed | 状态 changes-requested，未关闭 findings 为 R1-F1/R1-F2/R1-F3，建议下一阶段 fix |

## E000062 — fix / complete
- 时间：2026-09-19T14:04:29Z
- 状态：`fixing` → `testing`
- 原因：R1 的三条 must-fix 已全部修复并验证。R1-F1：compose.yaml 显式声明 velis_default 的子网为 172.30.0.0/24，并把 velis-api 的 VELIS_AUTH_TRUSTED_PROXY_CIDRS 默认值改为同一取值，消除「默认值指向本机动态分配到的另一个网段」导致的 IP 维度限流静默退化；启动日志改记实际生效的网段而非数量，使配错可见；配置示例、.env.example 与 README 同步。R1-F2：集成测试基座不再用 url.Parse(...).Path 判断 _test 后缀，改为 pgconn.ParseConfig 解析出的实际生效库名，并在连接后、迁移与清表之前用 current_database() 做第二道核对。R1-F3：无 Web Locks 的租约降级路径用写入时刻计算到期时间（不再沿用等待前的时刻），等待返回后重新读取租约再决定，最多让出两轮，避免同时超时的标签用同一枚刷新令牌并发刷新。13 条 important 与 17 条 suggestion 仍为 deferred，本轮未处理。
- 输入/输出依据：E000061 fix/start；E000060 review/fail 的 R1-F1/R1-F2/R1-F3；evidence/review/report-b9e3e444….md / 三条 must-fix 已修复；应用与测试改动见上表；状态待 fix/complete 收尾，建议下一阶段 test（重新验证，不顺手改代码）
- 已处理 Findings：R1-F1, R1-F2, R1-F3

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| compose.yaml | modify | R1-F1 | velis-api environment（83）、networks.default.ipam（133-142） | 把可信代理默认值改为与显式声明的子网一致；子网被占用时 compose 直接报错而不是静默换一个网段 | - |
| backend/internal/bootstrap/api.go | modify | R1-F1 | RunAPI 启动日志（63） | 记录实际生效的网段列表而不是数量，使「配了但配错」可从启动日志发现 | - |
| backend/configs/config.example.yaml | modify | R1-F1 | trusted_proxy_cidrs 注释（41-43） | 示例给出与 compose 固定子网一致的取值，不再写另一个网段 | - |
| .env.example | modify | R1-F1 | 账户与认证段（18-21） | 同步可信代理网段取值与生效条件 | - |
| README.md | modify | R1-F1 | 账户与认证 / VELIS_AUTH_TRUSTED_PROXY_CIDRS 段（162） | 说明 Compose 已固定子网且无需额外设置，并指出启动日志字段可用于核对 | - |
| backend/test/integration/harness_test.go | modify | R1-F2 | testDatabaseName（23）、newTestEnv（37-62）、requireTestDatabase（64-73） | 改为按实际生效库名判定 _test 后缀；连接后、迁移与清表之前再加一道 current_database() 核对。该文件由本 change 的 apply 阶段新增，本轮修改 | - |
| backend/test/integration/harness_guard_test.go | add | R1-F2 | TestTestDatabaseNameUsesEffectiveTarget（9） | 钉住守卫所依据的取值必须来自实际生效目标；覆盖 dbname/database/host 查询串覆盖与 keyword/value 形式，且不需要数据库即可运行 | - |
| web/src/features/auth/session.ts | modify | R1-F3 | refreshWithLease（178-206） | 到期时间按写入时刻计算；等待返回后重新读取租约、最多让出两轮。该文件由 apply 阶段新增，本轮修改 | - |
| web/src/features/auth/session.test.ts | modify | R1-F3 | memoryLease 记录写入（62）、fakeTimerHarness（105）、两个新用例（207、231） | 新增回归用例：租约到期时间必须从写入时刻起算；等待期间有人接管时必须重新等待而不是并发刷新 | - |

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| make check（gofmt + Go 单测 + 竞态 + 构建 + Vitest + Web 构建） | 本地 Go 1.26.6 / Node 24.15 | passed | 退出码 0；后端全部包与架构层级依赖测试通过，前端 5 个文件 31 例通过，类型检查与生产构建通过 |
| cd backend && go build ./... && go vet ./... && go test -count=1 ./... | 本地 Go 1.26.6 | passed | 构建与 vet 无输出，全部包测试通过 |
| VELIS_TEST_DATABASE_URL=postgres://…:55433/velis_test go test -count=1 ./test/integration/ | 真实 PostgreSQL 17（临时 pgvector/pg17 容器） | passed | 14 个用例通过（含新增守卫用例），基座两道闸在真实库上均正常 |
| 负例：VELIS_TEST_DATABASE_URL='postgres://…/velis_test?dbname=velis' go test -run 'TestAccountCreateUniquenessUnderConcurrency\|TestSourceAndArticleRepositories' ./test/integration/ | 真实 PostgreSQL 17，同一实例上并存 velis 与 velis_test | passed | 两个用例被拦下并报「集成测试只允许使用名称以 _test 结尾的数据库，实际连接目标是 "velis"」；事后核对生产语义的 velis 库表数为 0，证明未迁移也未清表；对照 velis_test 库已建 9 张表 |
| cd web && npm test | 本地 Node 24.15 / Vitest 4.1.11 | passed | 31/31 通过（原 29 例 + 新增 2 例） |
| 回归效力验证：把 session.ts 临时还原为修复前的实现（循环与 lease.write 均为旧版）后重跑 | 本地 Vitest | passed | 两个新用例在旧实现下均断言失败（2 failed / 29 passed），恢复修复后 31/31 通过——证明用例确实覆盖该缺陷而非恒真。首次还原只换了循环未换 lease.write，导致只失败 1 例，已完整还原后重测 |
| 旧检查行为取证：对三个绕过 DSN 执行 url.Parse(dsn).Path + HasSuffix | 本地 Go 1.26.6 一次性程序（/tmp） | passed | 三个 DSN（含 ?dbname=velis 与 ?host=…&dbname=velis）的 Path 都是 /velis_test，旧检查全部放行；与 pgconn.ParseConfig 的实际库名 velis 形成对照 |
| 以 VELIS_AUTH_TRUSTED_PROXY_CIDRS=172.30.0.0/24 启动 velis-api 并读取启动日志 | 本地构建产物 + 真实 PostgreSQL | passed | 日志为 trusted_proxy_cidrs=[172.30.0.0/24]，替代原先恒为 1 的 trusted_proxy_count |
| docker compose config 与 docker network create --subnet 172.30.0.0/24 | 本机 Docker | passed | compose 解析出 networks.default.ipam.config[0].subnet=172.30.0.0/24；该子网在本机可创建（验证后已删除），说明固定值与真实网络一致 |
| docker compose config 校验通过后清理临时资源 | 本机 Docker | passed | 临时 pgvector 容器与校验网络均已删除，无残留容器与进程 |

## E000063 — test / start
- 时间：2026-09-19T14:10:13Z
- 状态：`testing` → `testing`
- 原因：用户授权重新执行测试：按 test-spec.md 全量重跑 P0-01–P0-13 与 P1-01–P1-07，重点复核 fix 轮次 2 修复的三条 must-fix —— R1-F1（compose 可信代理网段与真实网络一致、IP 维度限流不退化）、R1-F2（集成测试 _test 库守卫按实际生效目标判定）、R1-F3（无 Web Locks 时租约不再写入即过期）。测试阶段只验证既定实现，不修改实现代码。
- 输入/输出依据：E000062 fix/complete（状态 testing，fix 轮次 2，未关闭 findings 为空）；test-spec.md 用例矩阵；fix 轮次 2 的实现改动 / -

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| python3 scripts/inspect_change.py changes/account-access-foundation | 本地仓库，2026-09-19 | passed | 状态 testing，未关闭 findings 为空，建议下一阶段 test |

## E000064 — test / fail
- 时间：2026-09-19T14:18:31Z
- 状态：`testing` → `changes-requested`
- 原因：第 3 轮全量重跑：24 个用例分组全部通过，fix 轮次 2 的三条 must-fix 经定向验证确认修复生效；但为 R1-F1 增补的 Compose 拓扑定向验证暴露一条新的 must-fix 缺陷 T3-F1。T3-F1：web/nginx.conf:10 用 proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for（追加语义，保留客户端自带前缀），而 compose.yaml 把整个 172.30.0.0/24 设为可信代理范围，该范围的网关 172.30.0.1 正是客户端流量从发布端口进入 nginx 时的 $remote_addr。于是 nginx 转发链为「客户端前缀, 172.30.0.1」，网关落在可信范围内被跳过，客户端前缀成为最右的不可信跳点并被采纳为来源 IP。容器拓扑实测：无转发头时解析为网关 172.30.0.1；带 X-Forwarded-For: 203.0.113.50 时解析为该伪造值；203.0.113.50 与 203.0.113.60 产生两个不同的 IP 维度键——同一客户端可轮换 IP 维度额度，绕过 30 次/15 分钟的 IP 维度限流做撞库或喷洒（账号维度 5 次/15 分钟仍生效）。这违反 test-spec P0-13「伪造与畸形转发头不能扩大信任」与 AC-09，故 P0-13 判 failed。既有 P0-13 用例之所以此前判为通过，是因为 harness 用的是覆盖式 proxy_set_header X-Forwarded-For $http_x_test_client_ip，掩盖了仓库自带 nginx 配置的追加语义。
- 输入/输出依据：E000063 test/start；E000062 fix/complete（fix 轮次 2）；test-spec.md 用例矩阵与 P0-13 验收描述 / test-spec.md 执行结果更新为第 3 轮；P0-13 判 failed；新增 Finding T3-F1；状态待 test/fail 收尾，建议下一阶段 fix（只处理 T3-F1）
- Findings：T3-F1

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/test-spec.md | modify | P0-13, T3-F1 | - | 更新第 3 轮执行结果：编排 24 个分组全通过；补充 R1-F1/R1-F2/R1-F3 定向验证小节；P0-13 判 failed 并新增 Findings 表记录 T3-F1；刷新 P1-06 实测数值；补充 Compose 本地拓扑无法按客户端隔离的限制说明 | - |

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| bash run_all.sh（24 个用例分组全量编排） | 真实进程 + 真实 PostgreSQL 17.11 + 真实 nginx 容器 + 1 vCPU/512 MiB 容器 + 真实 Chrome 148 | passed | 24/24 分组通过：P0-01 18/18、P0-02/P0-03 77/77、P0-04 30/30 与轮换 3+4+2、P0-05/P0-06 30/30、P0-07/P0-08 39、P0-09 32/32、P0-11 12、P1-05 启动 11 与形态 5、P0-12 13+5+12、P0-13 12/12 与 2/2、P0-10 22、P1-03 17、P1-06 6/6 与 3/3、P1-07 24、P1-01/P1-02 16/16、P1-04 23 |
| bash t_compose_net.sh（R1-F1 定向：compose 子网与默认可信代理网段比对 + 容器拓扑观察） | docker compose config + 真实容器（api 与 nginx 同处 172.30.0.0/24，挂载仓库 web/nginx.conf） | failed | 通过 3 项：compose 声明子网与 velis-api 默认值一致（均为 172.30.0.0/24）、反代容器地址落在该网段内、启动日志记录实际生效网段；失败 2 项：伪造 X-Forwarded-For 被采纳为来源 IP、两个伪造值产生两个不同的 IP 维度键。另观察：无转发头时解析为网关 172.30.0.1，畸形值按规格回退到 TCP 对端（nginx 容器） |
| VELIS_TEST_DATABASE_URL=postgres://…:55432/velis_test go test -count=1 -v ./test/integration/ | 真实 PostgreSQL 17 | passed | R1-F2 正例：14 个集成用例全部通过，含新增的守卫用例 TestTestDatabaseNameUsesEffectiveTarget |
| 负例：VELIS_TEST_DATABASE_URL='postgres://…/velis_test?dbname=velis' go test -run 'TestAccountCreateUniquenessUnderConcurrency\|TestSourceAndArticleRepositories\|TestAccountLifecycleAgainstPostgres' ./test/integration/ | 真实 PostgreSQL 17（同一实例并存 velis 与 velis_test） | passed | R1-F2 负例：三个用例均被拦下并报「集成测试只允许使用名称以 _test 结尾的数据库，实际连接目标是 "velis"」；事后核对 velis 库表数仍为 0，证明未迁移未清表 |
| cd web && npm test | Node 24.15 / Vitest 4.1.11 | passed | R1-F3：31/31 通过，含两例钉住租约到期时间按写入时刻起算、等待期间有人接管时重新等待 |
| docker compose config --format json 提取 subnet 与 VELIS_AUTH_TRUSTED_PROXY_CIDRS | 本地 Docker | passed | networks.default.ipam.config[0].subnet = 172.30.0.0/24，services.velis-api.environment.VELIS_AUTH_TRUSTED_PROXY_CIDRS = 172.30.0.0/24，两者一致 |
| docker logs velis-api \| grep trusted_proxy_cidrs | 容器内运行的 velis-api | passed | trusted_proxy_cidrs=[172.30.0.0/24]，启动日志记录的是实际生效网段而非数量 |

## E000065 — fix / start
- 时间：2026-09-19T14:28:34Z
- 状态：`changes-requested` → `fixing`
- 原因：用户授权执行 fix，只处理 T3-F1：Compose 拓扑下客户端自带的 X-Forwarded-For 被采纳为来源 IP，同一客户端可轮换 IP 维度额度绕过限流。13 条 important 与 17 条 suggestion 保持 deferred，不在本轮处理。
- 输入/输出依据：E000064 test/fail（changes-requested，failure_source=test，未关闭 findings 为 T3-F1）；test-spec.md 的 Findings 表与 P0-13 行；t_compose_net.sh / t_compose_net.mjs 的实测输出 / -
- Findings：T3-F1

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| python3 scripts/inspect_change.py changes/account-access-foundation | 本地仓库，2026-09-19 | passed | 状态 changes-requested，未关闭 findings 为 T3-F1，建议下一阶段 fix |

## E000066 — fix / complete
- 时间：2026-09-19T14:28:48Z
- 状态：`fixing` → `testing`
- 原因：T3-F1 已修复并验证。修复分两处：① 信任边界收窄——compose.yaml 把 velis-web 固定为 172.30.0.10，velis-api 的 VELIS_AUTH_TRUSTED_PROXY_CIDRS 默认值改为 172.30.0.10/32，不再把整个 172.30.0.0/24（含网桥网关）列为可信；② 入口侧加固——web/nginx.conf 由 $proxy_add_x_forwarded_for 追加改为 $remote_addr 覆盖，使客户端自带的 X-Forwarded-For 不再进入转发链。实测确认：反代链路与直连 API 端口两条路径下，客户端伪造的转发头都不再被采纳，两个伪造值产生同一个 IP 维度键。承重性验证：把可信范围放回整个子网、其余不变时，直连 API 伪造 XFF 会被采纳（键等于伪造值），说明收窄是承重的；在 /32 下把 nginx 换回追加语义，伪造仍不被采纳，说明 nginx 那处是纵深防御而非承重。配置示例、.env.example 与 README 同步为「只填反代自身地址，不要填整个子网」。13 条 important 与 17 条 suggestion 仍为 deferred。
- 输入/输出依据：E000065 fix/start；E000064 test/fail 的 T3-F1；test-spec.md 的 Findings 表与 P0-13 行 / T3-F1 已修复；改动 5 个文件；状态待 fix/complete 收尾，建议下一阶段 test（重跑 P0-13 与浏览器链路，不顺手改代码）
- 已处理 Findings：T3-F1

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| compose.yaml | modify | T3-F1 | velis-api environment（84）、velis-web networks.ipv4_address（112-116）、networks 注释（140-148） | 把可信代理默认值从整个子网收窄为反代容器自身的 /32，并给 velis-web 固定地址使该 /32 稳定；子网本身仍为 172.30.0.0/24 | - |
| web/nginx.conf | modify | T3-F1 | location /api/ 的 X-Forwarded-For（4-14） | 入口代理必须覆盖而不是追加客户端自带的转发头；注释写明将来在 nginx 前再加一层代理时的调整方式 | - |
| backend/configs/config.example.yaml | modify | T3-F1 | trusted_proxy_cidrs 注释（41-45） | 改为只填反代自身地址并说明为何不能填整个子网 | - |
| .env.example | modify | T3-F1 | 可信代理段（18-21） | 同步取值与理由 | - |
| README.md | modify | T3-F1 | 账户与认证 / VELIS_AUTH_TRUSTED_PROXY_CIDRS 段（162-164） | 写明 Compose 已固定 velis-web 地址、生产填反代自身地址，以及为何不能填整个子网、nginx 为何用覆盖语义 | - |

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| make check（gofmt + Go 单测 + 竞态 + 构建 + Vitest + Web 构建） | 本地 Go 1.26.6 / Node 24.15 | passed | 退出码 0 |
| bash t_compose_net.sh（T3-F1 定向：compose 配置断言 + 容器拓扑运行时判定） | 真实容器（api 与 nginx 同处 172.30.0.0/24，nginx 固定 172.30.0.10，挂载仓库 web/nginx.conf，API 另开 18093 直连端口） | passed | 9/9：可信范围是反代自身 /32、反代地址与之一致、网关不在可信范围、启动日志记录网段；经反代时无转发头解析为网关、伪造 XFF 仍解析为网关、两个伪造值同一维度键；直连 API 时无转发头与伪造 XFF 都解析为 TCP 对端 |
| 承重性验证：把 trusted_proxy_cidrs 放回 172.30.0.0/24，其余不变，直连 API 端口发送伪造 XFF | 真实容器 + 真实 PostgreSQL | passed | 键等于伪造值 203.0.113.50（true）——证明收窄信任边界是承重的，不是装饰性改动 |
| 纵深防御验证：保持 /32，把 nginx 换回 $proxy_add_x_forwarded_for 并置于可信地址，经反代发送伪造 XFF | 真实容器 | passed | 解析为网关而非伪造值——说明在 /32 下 nginx 那处改动不改变可见行为，属纵深防御；其价值在于即使将来把可信范围放宽回子网，客户端输入也不会进入转发链 |
| docker compose config --format json 提取 subnet / 可信代理 / velis-web 地址 | 本地 Docker | passed | 子网=172.30.0.0/24、可信代理=172.30.0.10/32、velis-web networks.default.ipv4_address=172.30.0.10 |
| 旧取值残留检查（grep proxy_add_x_forwarded_for 与 172.18.0.0/16） | 本地仓库 | passed | 仅剩解释性引用（注释与 README 说明为何不用追加语义），无遗留配置 |

## E000067 — test / start
- 时间：2026-09-19T14:32:41Z
- 状态：`testing` → `testing`
- 原因：用户授权重跑指定范围：P0-13（可信代理与转发链）与浏览器链路（P1-01/P1-02）。触发原因是 fix 轮次 3 改动了 web/nginx.conf 的 X-Forwarded-For 语义与 compose.yaml 的可信代理范围，二者都落在反代拓扑与浏览器可见链路上。测试阶段只验证既定实现，不修改实现代码。
- 输入/输出依据：E000066 fix/complete（状态 testing，fix 轮次 3，未关闭 findings 为空）；test-spec.md 的 P0-13 与 P1-01/P1-02 用例描述 / -

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| python3 scripts/inspect_change.py changes/account-access-foundation | 本地仓库，2026-09-19 | passed | 状态 testing，未关闭 findings 为空，建议下一阶段 test |

## E000068 — test / complete
- 时间：2026-09-19T14:38:18Z
- 状态：`testing` → `reviewing`
- 原因：第 4 轮测试通过：用户指定范围 P0-13 与浏览器链路全部通过，编排同时把其余用例一并重跑，24 个分组全绿。P0-13 计 12/12（转发链解析、真实 nginx 反代解析与伪造头覆盖、反代拓扑下 A 被限流 B 不受牵连）与 2/2（未配置可信代理时退化为单一来源）；浏览器链路 16/16。因既有浏览器用例走 Vite 开发代理、不经过 fix 轮次 3 改动的 web/nginx.conf，本轮另跑了一条 Compose 形态的浏览器链路（nginx 按仓库 web/nginx.conf 提供刚构建的前端并反代 /api，api 与 nginx 同在 compose 声明的子网内、nginx 固定 172.30.0.10），同一套浏览器用例同样 16/16 通过，确认覆盖语义的改动没有让浏览器可见链路回归。T3-F1 定向复核 9/9 通过：可信范围是反代自身 /32、网关不在可信范围内、经反代与直连 API 两条路径下客户端伪造的 X-Forwarded-For 都不被采纳、两个伪造值落到同一 IP 维度键。
- 输入/输出依据：E000067 test/start；E000066 fix/complete（fix 轮次 3）；test-spec.md 的 P0-13 与 P1-01/P1-02 用例描述 / test-spec.md 更新为第 4 轮；T3-F1 复核通过并在 Findings 表标注已修复；状态待 test/complete 收尾，建议下一阶段由独立 Agent 执行 review（本轮修复改动了应用代码，上一轮 review 结论已失效）

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/test-spec.md | modify | P0-13 | - | 更新为第 4 轮执行结果：P0-13 由 failed 改回 passed 并写明复核依据；新增「经 nginx 的浏览器链路」与「T3-F1 定向复核」小节；P1-01/P1-02 补记 Compose 形态；P0-13 验收描述补上「可信范围只覆盖反代自身地址」；Findings 表标注 T3-F1 已修复 | - |

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| bash run_all.sh（全量编排，本轮由用户指定范围触发） | 真实进程 + 真实 PostgreSQL 17.11 + 真实 nginx 容器 + 1 vCPU/512 MiB 容器 + 真实 Chrome 148 | passed | 24/24 分组通过；P0-13 12/12 与 2/2；P1-01/P1-02 16/16 |
| bash t_browser_nginx.sh（Compose 形态浏览器链路：nginx 按 web/nginx.conf 提供前端并反代 /api） | 真实 Chrome 148 + nginx 容器（172.30.0.10）+ api 容器 + 真实 PostgreSQL | passed | 16/16：注册校验与跳转、登录失败与重试、账户页、令牌不落持久存储、租约只含元数据、重载由刷新 Cookie 恢复、改昵称、双标签共享登录态、并发重载后都保持登录、跨标签退出广播、退出后重定向、缺 CSRF 头刷新 403、匿名列表与文章详情 |
| bash t_compose_net.sh（T3-F1 定向复核） | 真实 api 容器（可信代理 172.30.0.10/32）+ nginx 容器（挂载仓库 web/nginx.conf，固定 172.30.0.10）+ 真实 PostgreSQL | passed | 9/9：可信范围是反代自身 /32、反代地址与之一致、网关不在可信范围内、启动日志记录实际生效网段；经反代时无转发头与伪造 XFF 都解析为网关、两个伪造值同一维度键；直连 API 时无转发头与伪造 XFF 都解析为 TCP 对端 |
| grep '^FAIL' logs/summary.txt | 本地，2026-09-19 | passed | 无失败项 |
| 测试规格表格完整性校验（每个用例在验收映射表与结果表各出现一次） | 本地 | passed | 20 个用例各出现 2 次，两张表行数一致 |

## E000069 — review / start
- 时间：2026-09-19T14:42:26Z
- 状态：`reviewing` → `reviewing`
- 原因：用户授权执行第 2 轮 review：由未参与实现的独立上下文对应用代码、测试与 Spec 做只读审查。本轮审查对象是 fix 轮次 2 与 3 之后的当前工作树，重点复核 R1-F1/R1-F2/R1-F3 与 T3-F1 四条修复是否完整、是否引入回归，并对全量变更再做一次独立扫描以发现 R1 遗漏的问题。
- 输入/输出依据：E000068 test/complete（状态 reviewing，fix 轮次 3，未关闭 findings 为空）；E000060 review/fail 的 R1 结论与 evidence/review/report-b9e3e444….md；test-spec.md 第 4 轮执行结果 / -

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| python3 scripts/inspect_change.py changes/account-access-foundation | 本地仓库，2026-09-19 | passed | 状态 reviewing，未关闭 findings 为空，建议下一阶段 review |

## E000070 — review / fail
- 时间：2026-09-19T15:21:23Z
- 状态：`reviewing` → `changes-requested`
- 原因：R2 Review 完成：判定 changes-requested，记录 1 条 must-fix R2-F1。R2-F1：无 Web Locks 的租约降级仍会在同一枚单次刷新 Cookie 上产生并发刷新，三条机制同属一个缺陷——(b) 两个标签页同时启动刷新、都读到空租约，不需要任何异常条件（裁决方在真实 Chromium 148 关掉 navigator.locks 并发加载双标签页，干净批次 20 次命中 4 次，两个刷新请求携带同一枚 Cookie 且同一毫秒到达；Web Locks 对照组每次只 1 个请求）；(a) 持有者刷新挂起超过租约 TTL 时等待者到期接管（刷新请求没有任何客户端超时，session.ts:234 不传 signal，client.ts 无 timeout）；(c) clear() 无归属语义，会删掉别的标签写的租约，是 (a)(b) 的放大器。后果是服务端按 spec §2.3 撤销整个会话，用户被强制登出。裁决方判定这是实现缺陷而非规格窗口：spec §4.3 的「最长 10 秒」限定的是租约元数据、「可被接管」是持有者已死的存活兜底，规格从未授权在持有者仍在飞时并发第二枚请求；§2.3、§3.3、§4.3、AC-10 四条可以同时满足（把持有者请求寿命限定在租约内即可）。另 7 条 important 与 14 条 suggestion 进入 deferred。本轮两个独立方向对核心问题给出相反结论（方向一报 1 条 must-fix、方向二报 0 条），裁决方用自己跑的证据裁断，两个定级都被修正（方向二把 (b) 报低了、方向一把 (a) 报高了而未报 (b)）。
- 输入/输出依据：E000069 review/start；review basis sha256-v1:26439331f14840e42caaec414e64363b4b29d1d9ae220a3c232e475e997ea694（111 个路径，head c513dec2，verify 通过）；两方向的候选清单 /tmp/velis-review/candidates-r2.md；evidence/review/report-26439331….md / review basis sha256-v1:26439331… 上判定 changes-requested；Review 产物写入 evidence/review/report-26439331….md（未登记进事件 files，避免像 R1 那样使自身 basis 失效）；状态转入 changes-requested（failure_source=review），建议下一阶段 fix，仅处理 R2-F1
- Findings：R2-F1

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| python3 compute_review_basis.py compute . changes/account-access-foundation --write | 本地仓库，head c513dec2 | passed | 生成 review-basis-v1 内容寻址清单，111 个路径，basis=sha256-v1:26439331… |
| python3 compute_review_basis.py verify . changes/account-access-foundation sha256-v1:26439331… | 本地仓库，结论形成后重算 | passed | valid=true；两方向审查与裁决均严格只读，输入字节未变化，结论有效 |
| 两方向独立只读审查 + 独立裁决复核（3 个全新上下文） | 本地仓库、/tmp 一次性程序、一次性容器与真实 Chromium | passed | 候选 1–7 逐条打开文件核实行号；仓库内未创建、修改或删除任何文件，工作树入口数审查前后一致（128） |
| 真实 Chromium 148 双标签页并发加载（navigator.locks 置空走降级分支） | 真实构建产物 + mock 后端 | failed | 干净批次 20 次里 4 次出现两个刷新请求携带同一枚 Cookie（同一毫秒到达）；Web Locks 对照组每次只 1 个请求——确认 R2-F1 机制 (b) |
| 真实 10 秒 TTL 的租约接管时间线复现 | 仓库原样 session.ts（tsc 逐字节转译）+ 真实定时器与 BroadcastChannel | failed | 10139ms 时 tab-b 接管并发出第二次刷新，两个请求携带同一枚 token——确认 R2-F1 机制 (a) |
| 迁移 down→up 实测 + 集成测试 -race 全量 | 一次性 pgvector/pg17 + 仓库 cmd/velis-migrate | passed | down 后 6 张账户表归零、既有 sources/articles 完整保留、up 后 version=3，随后 14 例集成测试全绿 |
| 16 项 schema 对抗探针、16 项 JWT 对抗探针、19 项端点矩阵实测 | 一次性 PostgreSQL + 真实 API | passed | 全部符合 spec §8/§3.1/§4.2；唯一放行项是 SUG-10（昵称换行） |
| R1-F2 守卫的正/负例复核（13 种 DSN 形式穷举） | 真实 PostgreSQL | passed | 正例 14/14（0 跳过）；负例 ?dbname=velis 在 0.00s 被拦且目标库 0 表；无「闸门放行但 migrate 连到别处」的形式 |
| T3-F1 信任边界的逐项复核（RemoteAddr 来源、转发头种类、nginx real_ip、直连端口、IPv4-mapped IPv6、IPv6 路径） | 本地代码 + /tmp 一次性程序 | passed | 未找到任何让客户端控制来源 IP 判定的剩余路径 |

## E000071 — fix / start
- 时间：2026-09-19T15:39:43Z
- 状态：`changes-requested` → `fixing`
- 原因：用户授权执行 fix，只处理 R2-F1，方式为「移除缺陷机制」而不是继续打补丁。背景：本 change 的循环门禁在 fix_cycle=3 时触发（协议 max_completed_fix_cycles=3），dry-run 返回 to_status=blocked，未写入；我按要求输出了根因分析并等待决策。用户选择 B3——把无 Web Locks 时的多标签协调移出本 change、留待后续单独提案处理；移除范围限定为「只移除租约降级」（保留已验证可用的 Web Locks 多标签协调）；越过门禁的方式为「提高门禁阈值」。据此把工作区协议 code_copilot/protocol/change-v3.json 的 max_completed_fix_cycles 由 3 上调为 4，本轮 fix 计为第 4 轮。R2-F1 的处置是移除其描述的机制（session.ts 的 refreshWithLease 与 browser.ts 的租约存储），而不是按建议方向修补；7 条 important 与 14 条 suggestion 保持 deferred。
- 输入/输出依据：E000070 review/fail（changes-requested，failure_source=review，未关闭 findings 为 R2-F1）；evidence/review/report-26439331….md 的 R2-F1 详情；循环门禁 dry-run（E000071 候选，to_status=blocked）与根因分析 / -
- Findings：R2-F1

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/protocol/change-v3.json | modify | R2-F1 | - | 按用户决策把 loop_guard.max_completed_fix_cycles 由 3 上调为 4，使第 4 轮 fix 可在协议内执行；这是工作区级配置变更，已获用户明确授权，如实记录 | - |

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| python3 scripts/inspect_change.py changes/account-access-foundation | 本地仓库，2026-09-19 | passed | 状态 changes-requested，未关闭 findings 为 R2-F1 |
| transition_change.py --stage fix --action start（提高阈值前） | 本地仓库 | failed | dry-run 返回 to_status=blocked、事件动作改写为 loop-guard，未写入；循环门禁按协议生效 |
| transition_change.py --stage fix --action start（提高阈值后） | 本地仓库 | passed | dry-run 返回 to_status=fixing，门禁不再触发 |

## E000072 — fix / complete
- 时间：2026-09-19T15:43:03Z
- 状态：`fixing` → `testing`
- 原因：R2-F1 已按用户决策（B3：把无 Web Locks 时的多标签协调移出本 change）以「移除缺陷机制」的方式处置，而不是按裁决的建议方向修补。移除范围经用户确认为「只移除租约降级」，保留已验证可用的 Web Locks 多标签协调。具体改动：session.ts 删除 LeaseStore/TabLease/LEASE_TTL_MS、SessionOptions 的 lease/leaseTtlMs/randomId、refreshWithLease、waitForPeer/releasePeers/peers 与 defaultRandomId，performRefresh 只剩 lock 与直连两条分支；browser.ts 删除 createLeaseStore、LEASE_KEY 与 defaultStorage，createBrowserLock 的注释改为说明「不可用时不做跨标签协调」；两个测试文件删除租约相关用例，并新增一例钉住修订后的契约（无 lock 时直接刷新，不再读写任何跨标签协调元数据）。规格与文档同步：spec §4.3 改写该条并说明为何移除（localStorage 无 CAS，两版租约都只能收窄而不能关闭并发窗口），AC-10 明确该场景不在本 change 范围，tasks.md 的 T7 与 Deferred、README 的多标签说明、test-spec 的 P1-02 一并更新。7 条 important 与 14 条 suggestion 仍为 deferred。
- 输入/输出依据：E000071 fix/start；E000070 review/fail 的 R2-F1；evidence/review/report-26439331….md 的 R2-F1 详情 / R2-F1 以移除机制的方式处置完成；改动 9 个文件（含 2 个测试文件与 5 处 change 文档/README）；状态待 fix/complete 收尾，建议下一阶段 test（重点复核 Web Locks 多标签路径与浏览器链路，并重新记录 P1-02）
- 已处理 Findings：R2-F1

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| web/src/features/auth/session.ts | modify | R2-F1 | 删除 LeaseStore/TabLease/LEASE_TTL_MS、SessionOptions.lease/leaseTtlMs/randomId、refreshWithLease、waitForPeer/releasePeers/peers、defaultRandomId；performRefresh 移除 lease 分支 | 移除 R2-F1 描述的机制：无 Web Locks 时的 localStorage 租约协调 | - |
| web/src/features/auth/browser.ts | modify | R2-F1 | 删除 createLeaseStore/LEASE_KEY/defaultStorage；createBrowserLock 注释（32-35）；createBrowserSession（去掉 lease 注入） | 同步移除浏览器侧的租约存储与装配 | - |
| web/src/features/auth/session.test.ts | modify | R2-F1 | 删除 memoryLease/fakeTimerHarness 与三例租约用例；新增「无 Web Locks 时不做跨标签刷新协调，各标签各自刷新」 | 用例随机制移除；新增一例钉住修订后的契约，防止在没有设计的情况下重新引入做不到互斥的协调机制 | - |
| web/src/features/auth/browser.test.ts | modify | R2-F1 | 删除 createLeaseStore 的两例用例 | 被测函数已移除 | - |
| code_copilot/changes/account-access-foundation/spec.md | modify | R2-F1 | §4.3 租约条目（96-97）、§12 AC-10（218） | 行为契约修订：无 Web Locks 时不做跨标签协调，该能力移出 I1 并说明理由 | - |
| code_copilot/changes/account-access-foundation/tasks.md | modify | R2-F1 | T7 验证列（21）、Deferred（28-30） | 同步任务验收与 Deferred 说明 | - |
| code_copilot/changes/account-access-foundation/test-spec.md | modify | R2-F1 | P1-02 验收映射（28）与结果行（73）、未覆盖段 | P1-02 口径随规格修订；结果行标注早前的租约结论作废，需在下一轮 Test 重新记录 | - |
| README.md | modify | R2-F1 | 账户与认证 / 多标签说明（170） | 告知使用者无 Web Locks 时的实际行为 | - |
| code_copilot/changes/account-access-foundation/card.md | modify | R2-F1 | - | 在已接受的限制中记录本次范围决策与门禁阈值上调，使后续读者知道为何 fix 轮次是 4 | - |

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| make check（gofmt + Go 单测 + 竞态 + 构建 + Vitest + 类型检查 + Web 构建） | 本地 Go 1.26.6 / Node 24.15 | passed | 退出码 0；前端 5 个文件 27 例通过，类型检查与生产构建通过 |
| cd web && npm test | 本地 Vitest 4.1.11 | passed | 27/27 通过（移除租约用例前为 31 例） |
| grep -rn 'lease\\|Lease\\|LEASE' web/src/ | 本地仓库 | passed | 无残留引用 |
| grep -rn 'refresh-lease\|LEASE_TTL_MS\|createLeaseStore' web/src/ backend/ | 本地仓库 | passed | 全仓无残留 |
| transition_change.py --stage fix --action start（提高阈值前后各一次 dry-run） | 本地仓库 | passed | 阈值 3 时返回 to_status=blocked（未写入）；上调为 4 后返回 to_status=fixing |

## E000073 — test / start
- 时间：2026-09-19T15:45:53Z
- 状态：`testing` → `testing`
- 原因：用户授权重跑指定范围：Web Locks 多标签协调与浏览器链路。触发原因是 fix 轮次 4 移除了无 Web Locks 时的租约降级，改动了 web/src/features/auth/{session,browser}.ts 与两个测试文件，需要复核 Web Locks 路径未回归、浏览器闭环仍可用，并重新记录 P1-02（上一轮的租约相关结论已作废）。测试阶段只验证既定实现，不修改实现代码。
- 输入/输出依据：E000072 fix/complete（状态 testing，fix 轮次 4，未关闭 findings 为空）；test-spec.md 的 P1-01/P1-02 用例描述与修订后的 AC-10 / -

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| python3 scripts/inspect_change.py changes/account-access-foundation | 本地仓库，2026-09-19 | passed | 状态 testing，未关闭 findings 为空，建议下一阶段 test |

## E000074 — test / complete
- 时间：2026-09-19T15:54:28Z
- 状态：`testing` → `reviewing`
- 原因：第 5 轮测试通过：用户指定范围 Web Locks 多标签与浏览器链路全部通过，编排同时把其余用例一并重跑，24 个分组全绿。P1-01/P1-02 在两种形态下各 16/16：Vite 开发代理与 Compose 形态（nginx 按仓库 web/nginx.conf 提供前端并反代 /api）。fix 轮次 4 移除租约降级后 Web Locks 路径未回归——两标签并发重载后都保持登录（刷新串行化，未触发重放撤销）、跨标签退出广播清空其他标签会话、第二个标签共享同一登录态；持久存储全程为空（localStorage 与 sessionStorage 均为 {}），既无令牌也无任何跨标签协调元数据；缺 CSRF 头刷新 403；纯 TS 会话模块 27 例覆盖单飞刷新、Web Locks 串行化、锁内复用他人结果与不自动重放。P1-02 已按修订后的规格重新记录，此前的租约相关结论作废。
- 输入/输出依据：E000073 test/start；E000072 fix/complete（fix 轮次 4）；test-spec.md 的 P1-01/P1-02 与修订后的 AC-10 / test-spec.md 更新为第 5 轮；P1-02 重新记录；状态待 test/complete 收尾，建议下一阶段由独立 Agent 执行 review（fix 轮次 4 改动了应用代码与规格，上一轮 review 结论与 basis 均已失效）

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/account-access-foundation/test-spec.md | modify | P1-02 | - | 更新为第 5 轮执行结果：P1-02 按修订后的规格重新记录（Web Locks 路径未回归、持久存储为空），P1-01 补记两种形态各 16/16，并记录本轮修掉的两处 harness 缺陷 | - |

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| bash run_all.sh（全量编排） | 真实进程 + 真实 PostgreSQL 17.11 + 真实 nginx 容器 + 1 vCPU/512 MiB 容器 + 真实 Chrome 148 | passed | 24/24 分组通过；P1-01/P1-02 浏览器 16/16 |
| bash t_browser_nginx.sh（Compose 形态浏览器链路，nginx 按 web/nginx.conf 提供前端并反代 /api） | 真实 Chrome 148 + nginx 容器（172.30.0.10）+ api 容器 + 真实 PostgreSQL | passed | 16/16，含 Web Locks 多标签四项：双标签共享登录态、并发重载后都保持登录、跨标签退出广播清空其他标签会话、持久存储全程为空 |
| cd web && npm test | 本地 Vitest 4.1.11 | passed | 27/27；含「无 Web Locks 时不做跨标签刷新协调，各标签各自刷新」一例，钉住 fix 轮次 4 修订后的契约 |
| grep '^FAIL' logs/summary.txt | 本地，2026-09-19 | passed | 无失败项 |

## E000075 — review / start
- 时间：2026-09-19T16:08:55Z
- 状态：`reviewing` → `reviewing`
- 原因：用户授权执行第 3 轮 review，范围经明确收窄：只复核 fix 轮次 4 的增量与第 5 轮 Test 之后的当前工作树——即「移除无 Web Locks 时的租约降级」这一处改动及其连带的规格修订与文档同步，不再重跑前两轮那种全量审查。目的：为归档取得一个真实挣到的 passed verdict；归档要求 verified 状态与有效 basis，而上一轮 basis 已因 fix 轮次 4 改动覆盖范围内的文件而失效。
- 输入/输出依据：E000074 test/complete（状态 reviewing，fix 轮次 4，未关闭 findings 为空）；E000072 fix/complete 的改动清单；test-spec.md 第 5 轮执行结果 / -

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| python3 scripts/inspect_change.py changes/account-access-foundation | 本地仓库，2026-09-19 | passed | 状态 reviewing，未关闭 findings 为空，建议下一阶段 review |

## E000076 — review / complete
- 时间：2026-09-19T16:16:08Z
- 状态：`reviewing` → `verified`
- 原因：R3 窄范围复核通过：判定 passed，无 must-fix。范围由用户明确收窄为复核 fix 轮次 4 的增量（移除无 Web Locks 时的 localStorage 租约降级）及其连带的规格修订与文档同步。复核方独立核实：租约机制在代码、测试、注释、导出、规格五个层面均无残留（web/src 全目录无 lease 与 localStorage/sessionStorage/indexedDB 命中）；剩余的「有 lock 取锁」与「无 lock 直连」两条分支自洽，广播的 token/logout 均有消费者，npm test 27 例通过、vue-tsc exit 0；规格 §4.3/AC-10 与 tasks.md、card.md、README、test-spec 口径一致；project-context.md 的端点数量、迁移编号、新模块与 knowledge/index.md 的 _test 守卫语义均与代码属实。唯一 important（IMP-A：test-spec 的 P0-09 仍写「localStorage 只允许短租约元数据」，租约移除后成为恒真假证据）与两条 suggestion（README 指代歧义、knowledge 的 skip 语义不准）已按复核建议一并修正，复核方复读改后输入确认 IMP-A 关闭且 passed 维持。复核方另确认新 basis 112/112 与工作树逐字节一致、web/ 代码零变化，故此前跑的单测与类型检查结论对该 basis 仍有效。
- 输入/输出依据：E000075 review/start；review basis sha256-v1:10d645390f315aa2238257230a37e43b76adc501d30a26de2a91dc582eed77dd（112 个路径，head c513dec2，verify 通过）；evidence/review/report-10d64539….md / review basis sha256-v1:10d64539… 上判定 passed；Review 产物写入 evidence/review/report-10d64539….md；状态转入 verified，建议下一阶段 archive（归档时需声明 knowledge_updates，知识库已在 review/start 之前整理完毕）

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| python3 compute_review_basis.py compute . changes/account-access-foundation --write | 本地仓库，head c513dec2 | passed | 生成 review-basis-v1 内容寻址清单，112 个路径，basis=sha256-v1:10d64539…（前一版 b6ce64a7 因文档修正被替换） |
| python3 compute_review_basis.py verify . changes/account-access-foundation sha256-v1:10d64539… | 本地仓库，结论形成后重算 | passed | valid=true；复核方另独立核对 112/112 与工作树逐字节一致 |
| cd web && npm test 与 npx vue-tsc -b --force（复核方执行） | 本地 Node 24.15 / Vitest 4.1.11 | passed | 5 个文件 27 例全部通过；类型检查 exit 0，无断裂 import |
| grep -rn 'lease\|Lease\|LEASE\|localStorage\|sessionStorage\|indexedDB' web/src（复核方执行） | 本地仓库 | passed | 全部命中 0，租约机制无残留、持久存储无任何写入 |
| go test -v ./test/integration（不设 VELIS_TEST_DATABASE_URL，复核方执行） | 本地 Go 1.26.6 | passed | 13 个数据库用例逐条 SKIP、守卫解析用例 PASS，包仍打印 ok——据此修正了 knowledge/index.md 的表述 |
| IMP-A / SUG-A / SUG-B 修正后复读确认（复核方执行） | 本地仓库，只读 | passed | IMP-A 关闭且断言变为可证伪；SUG-A、SUG-B 表述与代码事实一致；未引入新矛盾；passed 维持 |

## E000077 — archive / start
- 时间：2026-09-19T16:16:41Z
- 状态：`verified` → `verified`
- 原因：用户授权执行 archive。只读前检：状态已为 verified、review verdict 为 passed 且 review basis 有效；Task/验收/Test/Review/deferred/风险互相一致；上一轮无未关闭 Findings。第 5 轮 Test 的浏览器结论与历轮环境验证均已按层级如实记录，未表述成生产验证。
- 输入/输出依据：E000076 review/complete（verified，review_basis sha256-v1:10d64539…）；E000074 test/complete（第 5 轮测试通过）；evidence/review/report-10d64539….md / -

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| python3 scripts/inspect_change.py changes/account-access-foundation | 本地仓库，2026-09-19 | passed | 状态 verified，review passed，未关闭 findings 为空，建议下一阶段 archive |
| python3 compute_review_basis.py verify . changes/account-access-foundation sha256-v1:10d645390f315aa2238257230a37e43b76adc501d30a26de2a91dc582eed77dd | 本地仓库 | passed | valid=true，被审输入未变化 |
| 逐项核对 Task/验收/Test/Review/deferred/风险的一致性 | 本地仓库，只读 | passed | spec 的 AC-01–AC-12 与 tasks.md、test-spec 用例矩阵、两轮 review 报告与 deferred 清单相互对齐；R2 的 IMP-7 与 SUG-1 因 fix 轮次 4 移除机制而不可达，归档时一并标注关闭 |

## E000078 — archive / complete
- 时间：2026-09-19T16:17:40Z
- 状态：`verified` → `archived`
- 原因：归档 account-access-foundation。change 建立了注册、登录、会话轮换与撤销、本人资料、退出、管理员 CLI、登录限流、管理审计与最小 Vue 闭环，并已收敛到本仓库当前可验证的状态：两轮全量审查 + 一轮窄范围复核、四轮修复、五轮测试。归档只提升经代码、测试与已审查 change 验证、未来会复用的知识，其余细节留在 change 内。 注意：knowledge_updates 的 path 相对工作区根 code_copilot/（校验器以工作区为基准），即 knowledge/index.md 与 rules/project-context.md。
- 输入/输出依据：E000077 archive/start；E000076 review/complete（verified，passed，review_basis sha256-v1:10d64539…）；E000074 test/complete（第 5 轮）；R1/R2/R3 三份 review 报告 / 状态 verified → archived；change 稳定知识已提升至 code_copilot/knowledge/index.md 与 code_copilot/rules/project-context.md；事件、日志、deferred 与剩余风险全部保留在 change 内

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| python3 compute_review_basis.py verify . changes/account-access-foundation sha256-v1:10d64539… | 本地仓库，归档前重算 | passed | valid=true；被审输入自 Review 以来未变化，且本轮未修改任何 basis 覆盖范围内的文件 |
| python3 scripts/inspect_change.py changes/account-access-foundation | 本地仓库，2026-09-19 | passed | verified → archived，未关闭 findings 为空 |
| 归档前一致性核对（Task/验收/Test/Review/deferred/风险） | 本地仓库，只读 | passed | spec 的 AC-01–AC-12、tasks.md、test-spec 用例矩阵、三份 review 报告与 deferred 清单相互对齐 |
| make check（第 5 轮 Test 期间执行，退出码 0） | 本地 Go 1.26.6 / Node 24.15 | passed | gofmt、Go 单测/竞态/构建、Vitest 27 例、Web 类型检查与生产构建全部通过 |

### 知识沉淀
| 文件 | 关键词 |
|---|---|
| knowledge/index.md | 可信代理来源判定, 集成测试 _test 守卫, 前端会话与令牌驻留, 口令散列, 访问令牌与密钥轮换, 会话与刷新轮换, 登录失败限流, 部署拓扑限制 |
| rules/project-context.md | 账户四层模块, Hertz 认证端点, 迁移 000003, Web 账户页面 |
