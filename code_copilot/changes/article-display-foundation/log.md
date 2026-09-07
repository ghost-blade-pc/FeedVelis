# 变更日志 — 文章展示基础能力与库表设计

本文件由 v3 生命周期工具按事件追加，面向人解释“为什么改、改了哪里、依据是什么”。当前状态读取 `card.md`，机器历史读取 `events.jsonl`，精确代码事实读取 Git diff。

每个事件块必须包含事件 ID；代码位置优先记录符号，同时可记录绑定文件或 diff hash 的当时行号。不得复制敏感值、完整源码或把未运行、Mock、本地验证写成生产通过。

## E000001 — propose / created
- 时间：2026-09-07T05:46:41Z
- 状态：`None` → `draft`
- 原因：创建独立 v3 change
- 输入/输出依据：- / -

## E000002 — propose / start
- 时间：2026-09-07T05:54:07Z
- 状态：`draft` → `draft`
- 原因：基于当前仓库事实形成文章展示基础能力的 high-risk 初稿；D1-D5 尚待开发者确认，因此保持 draft
- 输入/输出依据：用户 2026-09-07 propose 请求；Velis新项目开发文档.md；当前 M0 源码与迁移/OpenAPI/Web 路由 / article-display-foundation 的 card/spec/tasks/test-spec 初稿

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| card.md | modify | - | - | 记录目标、推荐范围、验收和编码前阻塞 | - |
| spec.md | modify | - | - | 拆解文章展示需求、API、数据模型、风险、依赖候选和 D1-D5 决策 | - |
| tasks.md | modify | - | - | 将后续实现拆成可独立验证且受前置门禁约束的任务 | - |
| test-spec.md | modify | - | - | 定义可见性、迁移、分页、字段泄漏、Web 与 XSS 边界的风险验收映射 | - |

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| git diff --check | 本地工作树，propose 阶段 | passed | change 文档无空白错误；未运行应用测试 |
| inspect_change.py code_copilot/changes/article-display-foundation | Spec Copilot workspace v3 | passed | change 可读取，当前状态 draft，事件链 E000001 有效 |

## E000003 — propose / start
- 时间：2026-09-07T05:59:40Z
- 状态：`draft` → `draft`
- 原因：依据 2026-09-04 已确认的 RSS MVP 边界校正提案：系统级 Source、抓取保存、文章列表和原站跳转；D1-D5 尚待确认
- 输入/输出依据：用户 2026-09-07 propose 请求；2026-09-04 RSS Article MVP 已确认范围；当前 M0 源码与迁移/OpenAPI/Web 路由 / article-display-foundation 的 RSS 列表闭环校正版 card/spec/tasks/test-spec

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| card.md | modify | - | - | 将目标校正为系统级 RSS 抓取保存和文章列表闭环 | - |
| spec.md | modify | - | - | 按既有决定拆解 Source/Article 需求、三表设计、抓取安全和 D1-D5 决策 | - |
| tasks.md | modify | - | - | 按 Source 到 Web 列表纵向链路重排可独立验证任务 | - |
| test-spec.md | modify | - | - | 定义 SSRF、解析、去重、调度、迁移、分页与 Web 外链的风险验收映射 | - |

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| git diff --check | 本地工作树，propose 阶段 | passed | RSS MVP 校正版 change 文档无空白错误；未运行应用测试 |
| inspect_change.py code_copilot/changes/article-display-foundation | Spec Copilot workspace v3 | passed | change 可读取，当前状态 draft，事件链 E000001-E000002 有效 |

## E000004 — propose / complete
- 时间：2026-09-07T11:16:51Z
- 状态：`draft` → `ready`
- 原因：开发者确认 D1-D5、A1-A4，并授权 E1-E11 采用默认值；补齐 CLI、三种 Feed 格式、schema、安全、并发和验证契约后完成 Proposal
- 输入/输出依据：用户确认消息；article-display-foundation E000003 draft；当前 workspace v3 项目事实 / article-display-foundation 完整 high-risk Proposal，D/A/E 契约已确认

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| card.md | modify | - | - | 记录全部决策已确认、无 Proposal 阻塞并可进入 ready | - |
| spec.md | modify | - | - | 固化本地 velis-admin、RSS 2.0/Atom/JSON Feed、三表 schema、D/A 决策和 E1-E11 默认契约 | - |
| tasks.md | modify | - | - | 将契约锁定任务标记完成，并同步 CLI、JSON Feed 和后续 Apply 任务范围 | - |
| test-spec.md | modify | - | - | 同步 JSON Feed、CLI 幂等、资源上限、内容截断、退避和 HTML 清理验收 | - |

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| rg -n '^- \[x\] (D[1-5]\|A[1-4]\|E1)' spec.md | 本地 Spec Copilot change 文档 | passed | D1-D5、A1-A4 与 E1-E11 授权共 10 条确认记录齐全 |
| cross-file contract search for JSON Feed, velis-admin, bigint, sort_at and source_fetch_logs | card/spec/tasks/test-spec | passed | 关键决策已同步到范围、任务、schema 与测试规格 |
| git diff --check | 本地工作树，propose 阶段 | passed | change 文档无空白错误；AGENTS.md/CLAUDE.md 仅有用户既有行尾警告 |
| inspect_change.py code_copilot/changes/article-display-foundation | Spec Copilot workspace v3 | passed | 完成转换前 change 可读取，状态 draft，事件链截至 E000003 有效 |

## E000005 — system / block
- 时间：2026-09-07T14:10:28Z
- 状态：`ready` → `blocked`
- 原因：开发者要求在 Apply 前补充并逐张确认用例图、业务流程图、ER 图和流程时序图，因此重新打开已完成的 Proposal
- 输入/输出依据：- / -

## E000006 — system / resume
- 时间：2026-09-07T14:10:58Z
- 状态：`blocked` → `draft`
- 原因：已按开发者授权明确将 change 恢复到 Proposal 草稿阶段，继续补充并逐张确认设计图
- 输入/输出依据：- / -

## E000007 — propose / start
- 时间：2026-09-07T14:14:17Z
- 状态：`draft` → `draft`
- 原因：按开发者要求开始逐张补充 Apply 前设计图；第 1 张用例图已落入 change，等待开发者确认
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/article-display-foundation/diagrams.md | add | Task 1 | - | 新增四类设计图载体，并完成第 1 张用例图及边界说明 | - |
| code_copilot/changes/article-display-foundation/spec.md | modify | Task 1 | - | 关联设计图并记录逐张确认门禁 | - |
| code_copilot/changes/article-display-foundation/tasks.md | modify | Task 1 | - | 补充四张设计图的任务进度 | - |
| code_copilot/changes/article-display-foundation/card.md | modify | Task 1 | - | 记录当前 Proposal 图示确认条件 | - |

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| rg 检查 diagrams.md Mermaid 围栏与章节结构 | 本地文档静态检查 | passed | 检测到一组闭合 Mermaid 代码围栏，四个图示章节均存在 |
| git diff --check -- code_copilot/changes/article-display-foundation | 本地 Git 工作区 | passed | change 文档未发现空白错误 |
| Mermaid CLI 渲染 | 本地环境 | not-run | 本地未安装 mmdc；当前仅完成 Mermaid 源码静态检查，需由支持 Mermaid 的渲染器预览 |

## E000008 — propose / start
- 时间：2026-09-07T14:27:22Z
- 状态：`draft` → `draft`
- 原因：开发者确认第 1 张用例图通过；继续完成第 2 张业务流程图并等待逐张确认
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/article-display-foundation/diagrams.md | modify | Task 1 | - | 记录用例图确认结果，并新增覆盖 Source 登记、抓取、入库和列表展示的业务流程图 | - |
| code_copilot/changes/article-display-foundation/spec.md | modify | Task 1 | - | 同步两张图的当前确认状态 | - |
| code_copilot/changes/article-display-foundation/tasks.md | modify | Task 1 | - | 勾选用例图并标记业务流程图待确认 | - |
| code_copilot/changes/article-display-foundation/card.md | modify | Task 1 | - | 更新 Proposal 图示确认进度 | - |

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| awk 检查 diagrams.md Mermaid 围栏数量 | 本地文档静态检查 | passed | 两张 Mermaid 图均具有一组闭合代码围栏 |
| rg 检查业务流程关键分支与约束说明 | 本地文档静态检查 | passed | HTTP 304、content_hash、历史文章保留等关键分支和说明均存在 |
| git diff --check -- code_copilot/changes/article-display-foundation | 本地 Git 工作区 | passed | change 文档未发现空白错误 |
| Mermaid CLI 渲染 | 本地环境 | not-run | 本地未安装 mmdc；当前仅完成 Mermaid 源码静态检查 |

## E000009 — propose / start
- 时间：2026-09-07T14:39:52Z
- 状态：`draft` → `draft`
- 原因：开发者要求开始第 3 张 ER 图；将第 2 张业务流程图记为已确认，并完成三表关系、字段和数据库约束图示
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/article-display-foundation/diagrams.md | modify | Task 1 | - | 记录业务流程图确认结果，并新增 Source、Article、ArticleContent ER 图及约束说明 | - |
| code_copilot/changes/article-display-foundation/spec.md | modify | Task 1 | - | 同步前三张图的当前确认状态 | - |
| code_copilot/changes/article-display-foundation/tasks.md | modify | Task 1 | - | 勾选业务流程图并标记 ER 图待确认 | - |
| code_copilot/changes/article-display-foundation/card.md | modify | Task 1 | - | 更新 Proposal 图示确认进度 | - |

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| awk 检查 diagrams.md Mermaid 围栏数量 | 本地文档静态检查 | passed | 三张 Mermaid 图均具有一组闭合代码围栏 |
| rg 核对 ER 关系、组合唯一键、删除策略和 sort_at 生成表达式 | 本地文档与 spec.md 契约比对 | passed | 1:N、1:1、ON DELETE RESTRICT/CASCADE、UNIQUE(source_id,dedupe_key) 和 COALESCE 规则均已记录 |
| git diff --check -- code_copilot/changes/article-display-foundation | 本地 Git 工作区 | passed | change 文档未发现空白错误 |
| Mermaid CLI 渲染 | 本地环境 | not-run | 项目和本地环境均未安装 Mermaid CLI；当前仅完成 Mermaid 源码静态检查 |

## E000010 — propose / start
- 时间：2026-09-07T14:49:45Z
- 状态：`draft` → `draft`
- 原因：开发者要求开始最后一张流程时序图；将第 3 张 ER 图记为已确认，并完成抓取入库与列表展示端到端调用时序
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/article-display-foundation/diagrams.md | modify | Task 1 | - | 记录 ER 图确认结果，并新增定时/人工抓取、条件请求、解析入库及列表展示的端到端时序图 | - |
| code_copilot/changes/article-display-foundation/spec.md | modify | Task 1 | - | 同步四张图的当前确认状态 | - |
| code_copilot/changes/article-display-foundation/tasks.md | modify | Task 1 | - | 勾选 ER 图并标记流程时序图待确认 | - |
| code_copilot/changes/article-display-foundation/card.md | modify | Task 1 | - | 更新 Proposal 图示确认进度 | - |

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| awk 检查 diagrams.md Mermaid 围栏数量 | 本地文档静态检查 | passed | 四张 Mermaid 图均具有一组闭合代码围栏 |
| rg 核对时序图关键分支和职责边界 | 本地文档与 spec.md 契约比对 | passed | 自动/人工触发、重定向校验、304、三类 upsert 结果、失败退避、列表查询和原站跳转均已记录 |
| git diff --check -- code_copilot/changes/article-display-foundation | 本地 Git 工作区 | passed | change 文档未发现空白错误 |
| Mermaid CLI 渲染 | 本地环境 | not-run | 项目和本地环境均未安装 Mermaid CLI；当前仅完成 Mermaid 源码静态检查 |

## E000011 — propose / start
- 时间：2026-09-07T15:21:08Z
- 状态：`draft` → `draft`
- 原因：开发者确认后端四层实现落点采用包、主要组件或接口、职责及新增或修改的颗粒度；据此补充四层实现架构图和分层改动清单，并修正时序图中 Interfaces 绕过 Application 直接访问 PostgreSQL 的表达
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/article-display-foundation/diagrams.md | modify | Task 1 | - | 新增后端四层实现架构图、分层改动清单和明确不修改模块，并修正时序图的层间调用边界 | - |
| code_copilot/changes/article-display-foundation/spec.md | modify | Task 1 | - | 固化架构落点、端口归属、Bootstrap 职责和开发者确认的颗粒度 | - |
| code_copilot/changes/article-display-foundation/tasks.md | modify | Task 1 | - | 加入第 5 张架构图确认门禁，并把后续任务落点细化到包级 | - |
| code_copilot/changes/article-display-foundation/card.md | modify | Task 1 | - | 记录流程时序图和后端四层实现架构图仍待确认，Proposal 保持 draft | - |

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| git diff --check -- code_copilot/changes/article-display-foundation | 本地 Git 工作区，Proposal 文档 | passed | change 文档未发现空白错误 |
| rg 核对五张 Mermaid 围栏、四层架构章节、层间边界与文件级延后说明 | 本地文档静态检查 | passed | 五张 Mermaid 图均有闭合围栏，架构图、清单、Interfaces 不直连数据库和实现颗粒度说明均已记录 |
| inspect_change.py code_copilot/changes/article-display-foundation | Spec Copilot workspace v3 | passed | change 可读取，状态 draft，事件链截至 E000010 有效 |
| Mermaid CLI 渲染 | 本地环境 | not-run | 本地未安装 mmdc；本轮只完成 Mermaid 源码静态检查 |
| 应用测试 | Proposal 文档阶段 | not-run | 本轮未修改应用代码或测试，应用验证留到 Apply/Test |

## E000012 — propose / start
- 时间：2026-09-07T15:30:11Z
- 状态：`draft` → `draft`
- 原因：开发者明确确认后端四层实现架构图通过；同步图示、规格、任务和卡片中的确认状态，Proposal 继续等待流程时序图确认
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/article-display-foundation/diagrams.md | modify | Task 1 | - | 将后端四层实现架构图及分层改动清单标记为开发者已确认 | - |
| code_copilot/changes/article-display-foundation/spec.md | modify | Task 1 | - | 同步架构图确认结果并保留流程时序图确认门禁 | - |
| code_copilot/changes/article-display-foundation/tasks.md | modify | Task 1 | - | 勾选后端四层实现架构图确认项 | - |
| code_copilot/changes/article-display-foundation/card.md | modify | Task 1 | - | 将当前剩余确认项收敛为流程时序图 | - |

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| git diff --check -- code_copilot/changes/article-display-foundation | 本地 Git 工作区，Proposal 文档 | passed | change 文档未发现空白错误 |
| rg 核对架构图已确认与流程时序图待确认状态 | 本地 change 文档静态检查 | passed | diagrams/spec/tasks/card 一致记录架构图已确认，流程时序图仍待确认 |
| inspect_change.py code_copilot/changes/article-display-foundation | Spec Copilot workspace v3 | passed | 转换前 change 可读取，状态 draft，事件链截至 E000011 有效 |
| 应用测试与 Mermaid 渲染 | Proposal 文档阶段 | not-run | 本轮仅同步确认状态；未修改应用代码，本地也未安装 Mermaid CLI |

## E000013 — propose / complete
- 时间：2026-09-07T15:33:18Z
- 状态：`draft` → `ready`
- 原因：开发者明确确认流程时序图通过；至此用例图、业务流程图、ER 图、流程时序图、后端四层实现架构图及分层改动清单均已确认，Proposal 契约完整且无未决需求阻塞
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/article-display-foundation/diagrams.md | modify | Task 1 | - | 将流程时序图标记为开发者已确认，五张图全部通过 | - |
| code_copilot/changes/article-display-foundation/spec.md | modify | Task 1 | - | 记录全部图示与分层清单已确认，Proposal 可完成 | - |
| code_copilot/changes/article-display-foundation/tasks.md | modify | Task 1 | - | 完成图示确认、验证基线和契约锁定任务 | - |
| code_copilot/changes/article-display-foundation/card.md | modify | Task 1 | - | 清除 Proposal 阻塞并记录 ready 条件已满足 | - |

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| awk 统计 diagrams.md Mermaid 开始与结束围栏 | 本地文档静态检查 | passed | 检测到 5 个 Mermaid 开始围栏和 5 个闭合围栏 |
| rg 核对五张图确认、Task 1 完成、ready 条件和残留待确认标记 | 本地 change 文档静态检查 | passed | diagrams/spec/tasks/card 一致记录五张图已确认，未发现残留图示待确认或保持 draft 标记 |
| git diff --check -- code_copilot/changes/article-display-foundation | 本地 Git 工作区，Proposal 文档 | passed | change 文档未发现空白错误 |
| inspect_change.py code_copilot/changes/article-display-foundation | Spec Copilot workspace v3 | passed | 完成转换前 change 可读取，状态 draft，事件链截至 E000012 有效 |
| Mermaid CLI 渲染 | 本地环境 | not-run | 本地未安装 mmdc；已完成围栏、章节和关键契约静态检查，未验证实际渲染 |
| 应用测试 | Proposal 文档阶段 | not-run | Proposal 阶段没有修改应用代码或测试；运行验证留到 Apply/Test |
