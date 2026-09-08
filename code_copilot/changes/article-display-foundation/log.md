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

## E000014 — apply / start
- 时间：2026-09-07T15:38:08Z
- 状态：`ready` → `applying`
- 原因：开始实现已确认的文章展示基础能力 change；按 tasks.md 从 Task 2 到 Task 8 推进，保持已确认的四层架构、数据契约和安全边界。
- 输入/输出依据：- / -

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| inspect_change.py code_copilot/changes/article-display-foundation | Spec Copilot workspace v3 | passed | change 状态为 ready，最后事件 E000013，事件链有效且建议下一阶段为 apply |
| git status --short --branch | 本地 Git 工作区 | passed | 已识别并保留 AGENTS.md、CLAUDE.md、agnet/ 等无关用户改动 |

## E000015 — apply / complete
- 时间：2026-09-07T16:59:34Z
- 状态：`applying` → `testing`
- 原因：Task 2 至 Task 8 已完成：系统级 Source 管理、安全 Feed 抓取、三格式解析、Article/Content 幂等事务入库、租约调度、列表 API、Web 最新文章页及配套迁移和验证均已落地。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| backend/internal/domain/shared/url.go | add | Task 2 | NormalizeHTTPURL | 实现保留 query 语义和 percent-encoding 的 HTTP(S) URL 规范化 | - |
| backend/internal/domain/source/source.go | add | Task 2 | Source, Repository, NextFailure | 实现 Source 状态、租约、退避与 Repository 抽象 | - |
| backend/internal/domain/article/article.go | add | Task 2 | Article, Content, DedupeKey, ContentHash | 实现外部文章身份、内容哈希、截断与列表领域模型 | - |
| backend/migrations/000002_create_sources_and_articles.up.sql | add | Task 2 | - | 创建 sources、articles、article_contents、约束与索引 | - |
| backend/migrations/000002_create_sources_and_articles.down.sql | add | Task 2 | - | 按反向依赖顺序回滚三张新表 | - |
| backend/cmd/velis-migrate/main.go | modify | Task 2 | migrationDatabaseURL | 固定迁移 metadata 的 public search_path，避免 velis 用户同名 schema 导致版本漂移 | - |
| backend/internal/application/ports/feed.go | add | Task 3 | FeedFetcher, FeedParser, ContentSanitizer, Clock | 声明抓取解析清理和时钟技术端口 | - |
| backend/internal/infrastructure/fetcher/httpfeed/fetcher.go | add | Task 3 | Fetcher.Fetch, validateRemoteURL, isRestrictedIP | 实现条件请求、逐跳 SSRF 防护、超时、重定向和响应大小限制 | - |
| backend/internal/infrastructure/fetcher/httpfeed/parser.go | add | Task 3 | Parser.Parse | 通过 gofeed 统一解析 RSS 2.0、Atom 和 JSON Feed | - |
| backend/internal/infrastructure/fetcher/httpfeed/sanitizer.go | add | Task 3 | Sanitizer.Sanitize | 通过 bluemonday 实现 HTML allowlist、链接加固与纯文本派生 | - |
| backend/go.mod | modify | Task 3 | - | 加入 gofeed v1.4.2、bluemonday v1.0.27，升级 pgx v5.9.2 并要求 Go 1.26.6 | - |
| backend/go.sum | modify | Task 3 | - | 记录新增及安全升级依赖的模块校验和 | - |
| backend/Dockerfile | modify | Task 3 | - | 容器构建工具链固定到已修复漏洞的 Go 1.26.6 | - |
| backend/internal/application/article/service.go | add | Task 4 | Service.Ingest, Service.List | 编排条目校验、清理、哈希、事务入库和稳定游标列表 | - |
| backend/internal/application/source/service.go | add | Task 4 | Service.Add, Service.FetchByID, Service.FetchClaimed | 编排 Source 管理、条件抓取、304、成功和失败状态 | - |
| backend/internal/application/ports/transaction.go | add | Task 4 | TxManager | 声明 Application 事务技术端口 | - |
| backend/internal/infrastructure/persistence/postgres/article_repository.go | add | Task 4 | ArticleRepository.Upsert, ArticleRepository.ListPublished | 实现 Article/Content 原子 upsert 与 keyset 列表查询 | - |
| backend/internal/infrastructure/persistence/postgres/source_repository.go | add | Task 4 | SourceRepository.ClaimDue, SourceRepository.ClaimByID | 实现 Source 幂等添加、人工和自动租约、状态持久化 | - |
| backend/internal/infrastructure/persistence/postgres/transaction.go | add | Task 4 | TxManager.WithinTransaction | 实现 pgx 事务端口并让 Repository 复用事务上下文 | - |
| backend/internal/bootstrap/feed.go | add | Task 4 | buildFeedServices | 集中装配 Source、Article、Fetcher、Parser、Sanitizer、Repository 和 TxManager | - |
| backend/internal/interfaces/scheduler/scheduler.go | add | Task 5 | Scheduler.Run | 实现批量认领、跨 Source 并发和受限结构化抓取日志 | - |
| backend/internal/bootstrap/worker.go | modify | Task 5 | RunWorker | 将 Worker 占位 heartbeat 替换为可取消的 Source 调度器 | - |
| backend/cmd/velis-admin/main.go | add | Task 6 | run | 新增本地管理命令入口 | - |
| backend/internal/interfaces/cli/source.go | add | Task 6 | Runner.Run | 实现 source add/list/pause/resume/fetch 协议适配 | - |
| backend/internal/interfaces/http/hertz/handler/article.go | add | Task 6 | Article.List | 实现文章列表参数校验、错误映射和 UTC DTO | - |
| backend/internal/interfaces/http/hertz/router.go | modify | Task 6 | NewServer | 注册 GET /api/v1/articles | - |
| backend/api/openapi/velis.yaml | modify | Task 6 | listArticles | 同步文章分页 API、DTO 与稳定错误契约 | - |
| web/src/api/client.ts | modify | Task 7 | listArticles | 新增不透明游标文章列表客户端 | - |
| web/src/features/article/ArticleList.vue | add | Task 7 | - | 实现加载、空、失败、重试、分页和安全文本卡片 | - |
| web/src/features/article/model.ts | add | Task 7 | safeArticleURL, articleTime | 实现 HTTP(S) 外链防御和发布/收录时间语义 | - |
| web/src/views/LatestView.vue | add | Task 7 | - | 将 /latest 接入真实文章列表 | - |
| backend/test/integration/article_repository_test.go | add | Task 8 | TestSourceAndArticleRepositories | 覆盖真实 PostgreSQL 幂等、事务、分页和租约并发 | - |
| README.md | modify | Task 8 | - | 同步当前能力、Go 安全补丁要求和本地 Source 管理用法 | - |
| code_copilot/changes/article-display-foundation/tasks.md | modify | Task 1 | - | 同步 Task 2 至 Task 8 的实际文件与 Apply 验证结果 | - |

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| make check | 本地 Go 1.26.6 与 Node.js 24；PostgreSQL integration 未设置环境变量时按测试保护跳过 | passed | Go 单测、全包 race、四层依赖、四个进程构建、Vitest、vue-tsc 和 Vite build 全部通过 |
| cd backend && GOCACHE=/tmp/feedvelis-go-cache go vet ./... | 本地 Go 1.26.6 | passed | 全包静态检查通过 |
| VELIS_TEST_DATABASE_URL=<isolated> go test -race -count=1 ./test/integration | 本地 PostgreSQL 17 + pgvector，template0 隔离数据库 | passed | Source 幂等与租约、Article inserted/unchanged/updated、updated_at、不重复分页和过期租约恢复均通过 |
| velis-migrate up; down -steps 2; up; version | template0 隔离 PostgreSQL 数据库 | passed | 迁移 round-trip 成功，最终 version=2 dirty=false；同时修复 public schema_migrations search_path |
| govulncheck ./... | Go 1.26.6；pgx v5.9.2；gofeed v1.4.2；bluemonday v1.0.27 | passed | No vulnerabilities found；初扫暴露的旧 Go 补丁和 pgx 可达漏洞已通过版本升级消除 |
| velis-admin source fetch <controlled source> | 隔离 PostgreSQL + 受控公网 Feed | passed | Atom Go 官方 Feed 插入 10 篇、RSS 2.0 插入 20 篇、JSON Feed 官方示例插入 2 篇；重复 Atom 抓取收敛为 unchanged |
| curl GET /api/v1/articles?limit=2 及第二页 cursor | 本地 Hertz API + 隔离 PostgreSQL | passed | 200、X-Request-ID、UTC 时间、第一页 has_more=true、第二页无重复且 next_cursor=null |
| EXPLAIN (ANALYZE, BUFFERS) 文章列表查询 | 100 Source / 100,000 Article 隔离 PostgreSQL fixture | passed | 使用 articles_list_idx，LIMIT 21 执行时间约 0.034 ms；仅作为本机查询计划基线 |
| docker compose build velis-migrate velis-api velis-worker | Docker，golang:1.26.6-alpine | passed | 三个后端镜像构建成功 |
| OpenAPI YAML 解析与 git diff --check | 本地静态检查 | passed | OpenAPI 文法可解析，工作区差异无空白错误 |

## E000016 — test / start
- 时间：2026-09-08T00:37:52Z
- 状态：`testing` → `testing`
- 原因：开始对 article-display-foundation 的既定实现执行独立 Test 阶段验证；本阶段只记录测试证据，不修改应用代码。
- 输入/输出依据：- / -

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| inspect_change.py code_copilot/changes/article-display-foundation | Spec Copilot workspace v3；HEAD 9fe8a14 | passed | change 状态为 testing，最后事件 E000015，事件链有效且无未关闭 finding |
| git status --short --branch | 本地 Git 工作区 | passed | Apply 实现已由开发者提交为 9fe8a14；仅发现无关未跟踪目录 agnet/，测试阶段保持不触碰 |

## E000017 — test / complete
- 时间：2026-09-08T00:53:37Z
- 状态：`testing` → `reviewing`
- 原因：文章展示基础能力的可重复单元、契约、集成、运行态、安全与构建验证均通过，未发现产品缺陷；未覆盖项已明确保留为剩余风险。
- 输入/输出依据：- / -

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/article-display-foundation/test-spec.md | modify | Task 8 | - | 由生命周期工具写入正式 Test 结果、分层证据、未覆盖项和剩余风险 | - |

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| GOCACHE=/tmp/feedvelis-go-cache go test -count=1 -v ./internal/domain/shared ./internal/domain/source ./internal/domain/article ./internal/application/source ./internal/application/article ./internal/infrastructure/fetcher/httpfeed ./internal/interfaces/cli ./internal/interfaces/http/hertz | 本地 Go 1.26.6；Fetcher 本机 HTTP 用例在允许 127.0.0.1 临时监听的隔离权限下重跑 | passed | URL 规范化、去重、UTF-8 截断、退避、304、暂停状态、游标、三种 Feed、500 条上限、恶意 HTML、SSRF、条件请求、5 MiB 和重定向限制、CLI/API 契约全部通过且最终无跳过 |
| cd web && npm test | Node.js 24 + Vitest 4.1.11 | passed | 2 个测试文件、5 个测试通过；覆盖 API 错误映射、不透明游标、HTTP(S) 无凭据外链和发布/收录时间语义 |
| make check | 本地 Go 1.26.6、Node.js 24；未给集成包注入数据库 URL，因此该命令中的 PostgreSQL 测试按保护逻辑跳过并由独立集成命令补足 | passed | 全包 Go 测试、race、四层依赖测试、四个 cmd 构建、Vitest、vue-tsc 和 Vite production build 全部通过 |
| cd backend && GOCACHE=/tmp/feedvelis-go-cache go vet ./... | 本地 Go 1.26.6 | passed | 全包静态检查通过 |
| cd backend && GOCACHE=/tmp/feedvelis-go-cache go run golang.org/x/vuln/cmd/govulncheck@latest ./... | 允许访问官方 Go 模块代理和漏洞数据库；首次沙箱运行仅因代理端口受限失败，授权联网后同命令成功 | passed | No vulnerabilities found |
| velis-migrate up; velis-migrate -steps 2 down; velis-migrate up; velis-migrate version | PostgreSQL 17 + pgvector；本轮 template0 临时数据库 velis_e16_20260908_test | passed | 空库迁移和两步回滚可重复，最终 version=2 dirty=false |
| VELIS_TEST_DATABASE_URL=<temporary_test_db> GOCACHE=/tmp/feedvelis-go-cache go test -race -count=1 -v ./test/integration | 真实 PostgreSQL 临时数据库；初次数据库名未以 _test 结尾时被测试安全门禁拒绝，改用合规临时库后进入业务断言并通过 | passed | Source 等价 URL 并发幂等、Article inserted/unchanged/updated、unchanged 不改 updated_at、稳定分页、SKIP LOCKED 无重复认领、人工租约冲突和过期恢复均通过 |
| velis-admin source add -url <equivalent URLs> | 本地 CLI + 真实 PostgreSQL 临时数据库 | passed | https://EXAMPLE.com:443/feed#fragment 与 https://example.com/feed 均返回 source_id=1 created=false，验证规范化 URL 幂等 |
| PostgreSQL information_schema/pg_indexes 查询与 Source DELETE 异常块 | 真实 PostgreSQL 临时数据库 | passed | sources.id 和 articles.id 为 identity；sort_at 为 COALESCE 生成列；三个关键索引存在；schema_migrations 位于 public；有文章的 Source 删除被外键 RESTRICT 拒绝 |
| curl GET /api/v1/articles?limit=2；分页期间插入新文章；使用 next_cursor 请求第二页并运行响应断言 | 本地 Hertz API + 真实 PostgreSQL 临时数据库 | passed | 两页 200 且带 X-Request-ID；ID 无重复；第二页不夹入游标之后新增文章；has_more/next_cursor 正确；时间为 UTC；响应不含正文、hash 或抓取状态大字段。首次断言误硬编码 identity ID，改为业务不变量后通过 |
| /tmp/velis-worker-e16 -config configs/config.example.yaml；发送 SIGINT | 本地临时 Worker 二进制 + 所有 Source 已暂停的真实 PostgreSQL 临时数据库 | passed | Worker 正常启动，SIGINT 后记录停止接收新任务并以 0 退出；go run 包装器的首次退出码 1 已通过直接二进制区分 |
| docker compose build velis-migrate velis-api velis-worker | Docker；golang:1.26.6-alpine + alpine:3.23 | passed | 三个后端镜像构建成功 |
| PyYAML 解析 backend/api/openapi/velis.yaml；git diff --check | 本地静态验证 | passed | OpenAPI YAML 可解析，Test 阶段没有修改应用代码，仅生命周期证据文件发生变化 |
| Playwright 浏览器 E2E | 当前仓库 | not-run | 仓库仍没有可运行的 Playwright 配置；以 Vitest、Vue 类型/生产构建和真实 API 运行态验证替代，但不等价于真实浏览器交互 |
| 生产网络 SSRF 验证与公网 Feed 长尾兼容矩阵 | 生产/时变外部环境 | not-run | 本阶段使用固定 RSS 2.0、Atom、JSON Feed fixture 和本地恶意服务保证可重复性；不把本地结果外推为生产网络保证 |

## E000018 — review / start
- 时间：2026-09-08T01:04:13Z
- 状态：`reviewing` → `reviewing`
- 原因：开始对 article-display-foundation 执行只读 Review；由于当前主 Agent 参与了 Apply/Test，实际代码审查将交给未继承实现上下文的独立子 Agent，主 Agent 仅负责生命周期、review basis 校验和结果持久化。
- 输入/输出依据：- / -

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| inspect_change.py code_copilot/changes/article-display-foundation | Spec Copilot workspace v3 | passed | change 状态为 reviewing，最后事件 E000017，测试结果 passed，无未关闭 finding |
| git status --short --branch；git log -4 --oneline | HEAD 9fe8a14，本地工作区 | passed | Apply 实现位于开发者提交 9fe8a14；未提交差异仅为 Test 生命周期证据和无关 agnet/，Review 对应用代码、测试和 Spec 保持只读 |

## E000019 — review / fail
- 时间：2026-09-08T01:23:53Z
- 状态：`reviewing` → `changes-requested`
- 原因：独立只读 Review 在完整冻结 basis 上确认 4 个 must-fix：Source 完成写入缺少租约 fencing、URL 路径规范化过度合并、内容更新覆盖 hidden 状态、截断标志未参与 unchanged 判定；verdict 为 changes-requested。
- 输入/输出依据：sha256-v1:6122620cebad2a797403154e49e64de00946d8bd6183863fb6de1a36deffca23 / -
- Findings：R0-F1, R0-F2, R0-F3, R0-F4

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| code_copilot/changes/article-display-foundation/review.md | add | R0-F1, R0-F2, R0-F3, R0-F4 | - | 持久化独立 Reviewer 的 must-fix、Important、Suggestions、版本绑定证据和测试层级限制 | - |

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| 独立 Reviewer 对 f6d7ab7..9fe8a14 执行只读 Spec/正确性/安全/事务/契约/可维护性审查 | 独立子 Agent /root/independent_review；HEAD 9fe8a14；未参与 Apply/Test | failed | 发现 R0-F1 至 R0-F4 四个可复现 must-fix，建议进入 Fix；应用代码、测试和 Spec 全程未修改 |
| compute_review_basis.py verify . code_copilot/changes/article-display-foundation sha256-v1:6122620cebad2a797403154e49e64de00946d8bd6183863fb6de1a36deffca23 | 本地 Git 工作区；HEAD 9fe8a14 | passed | valid=true；排除 verdict 可变的 card/events/log 后，f6d7ab7..9fe8a14 的其余 64 个实际变更路径全部覆盖 |
| nl -ba 复核 source_repository.go、url.go、article_repository.go、article.go 的 finding 行号 | 冻结 basis 对应工作区内容 | passed | 四个 must-fix 的触发路径和行号与独立 Reviewer 报告一致 |
| E000017 Test 证据复核 | 事件链和 log 摘要；Review 未重跑测试 | passed | Unit/local、PostgreSQL integration、runtime 和构建证据已分层记录；Playwright、生产 SSRF 和公网长尾仍为 not-run |

## E000020 — fix / start
- 时间：2026-09-08T01:39:38Z
- 状态：`changes-requested` → `fixing`
- 原因：进入 Fix cycle 1，限定处理独立 Review 的 R0-F1 至 R0-F4；这些问题均可在既有契约内做最小完整修正，无需变更 Proposal。
- 输入/输出依据：- / -
- Findings：R0-F1, R0-F2, R0-F3, R0-F4

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| inspect_change.py code_copilot/changes/article-display-foundation | Spec Copilot workspace v3 | passed | 状态 changes-requested，failure_source=review，未关闭 finding 为 R0-F1 至 R0-F4，建议下一阶段 fix |
| Review basis 绑定行号与源码定位 | HEAD 9fe8a14；review basis sha256-v1:6122620cebad2a797403154e49e64de00946d8bd6183863fb6de1a36deffca23 | passed | 已定位租约完成写、path.Clean、hidden 状态覆盖和 truncation hash 四个根因；均为实现偏差 |

## E000021 — fix / complete
- 时间：2026-09-08T01:59:28Z
- 状态：`fixing` → `testing`
- 原因：Fix cycle 1 已完成 R0-F1 至 R0-F4 的最小完整修正：Source 完成写入采用租约 fencing 并与文章入库保持原子性，URL 规范化仅移除 dot-segment，文章更新保留 hidden 状态，content_hash 纳入原始内容截断标志。
- 输入/输出依据：- / -
- 已处理 Findings：R0-F1, R0-F2, R0-F3, R0-F4

### 实际改动
| 文件 | 操作 | Task/Finding | 符号/行 | 原因 | 版本依据 |
|---|---|---|---|---|---|
| backend/internal/domain/source/source.go | modify | R0-F1 | Lease, ErrLeaseLost, Source.CurrentLease, Repository | 增加租约 generation 值对象、租约丢失错误和带租约的 Repository 完成写接口。 | - |
| backend/internal/application/source/service.go | modify | R0-F1 | Service, NewService, Service.fetch, Service.fail | 抓取完成与失败路径携带认领租约，并将文章入库和 Source 成功完成写放入同一事务；用实际完成时间校验租约。 | - |
| backend/internal/application/source/service_test.go | modify | R0-F1 | sourceRepositoryFake, transactionManagerFake | 同步租约接口并为应用服务测试提供事务与认领租约替身。 | - |
| backend/internal/bootstrap/feed.go | modify | R0-F1 | buildFeedServices | 为 Source 与 Article 服务装配同一个事务管理器。 | - |
| backend/internal/infrastructure/persistence/postgres/source_repository.go | modify | R0-F1 | SourceRepository.MarkNotModified, SourceRepository.MarkSuccess, SourceRepository.MarkFailure, SourceRepository.executor, leaseWriteResult | 完成写按 Source、允许状态、lease owner、lease expiry 和未过期条件执行，零更新行返回租约失效；事务上下文内复用当前事务。 | - |
| backend/internal/infrastructure/persistence/postgres/transaction.go | modify | R0-F1 | TxManager.WithinTransaction | 事务管理器识别已有事务上下文，使 Source 抓取外层事务与 Article 内层事务安全复用。 | - |
| backend/internal/domain/shared/url.go | modify | R0-F2 | NormalizeHTTPURL, removeDotSegments, removeLastPathSegment | 以 RFC 3986 remove-dot-segments 语义替代 path.Clean，保留重复斜杠、尾斜杠和 percent-encoding。 | - |
| backend/internal/domain/shared/url_test.go | modify | R0-F2 | TestNormalizeHTTPURLRemovesOnlyDotSegments | 覆盖路径尾斜杠、重复斜杠、dot-segment 和编码点号的回归行为。 | - |
| backend/internal/infrastructure/persistence/postgres/article_repository.go | modify | R0-F3 | ArticleRepository.Upsert | 冲突更新不再强制 published，保留文章已有的受控状态。 | - |
| backend/internal/domain/article/article.go | modify | R0-F4 | ContentHash | 将摘要和正文的截断标志纳入可重算 content_hash。 | - |
| backend/internal/domain/article/article_test.go | modify | R0-F4 | TestContentHashIncludesTruncationFlags | 验证任一截断标志变化都会改变 content_hash。 | - |
| backend/test/integration/article_repository_test.go | modify | R0-F1, R0-F3 | TestSourceAndArticleRepositories | 用真实 PostgreSQL 回归旧租约、新租约、pause、过期、hidden 状态保留及 Source 完成失败时文章事务回滚。 | - |

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| GOCACHE=/tmp/feedvelis-go-cache go test -count=1 ./internal/domain/shared ./internal/domain/source ./internal/domain/article ./internal/application/source ./internal/application/article ./internal/infrastructure/persistence/postgres ./internal/architecture | 本地 Go 1.26.6；Fix 开发验证 | passed | URL、租约接口、content hash、Source/Article 应用服务、PostgreSQL 适配器编译和四层依赖检查通过。 |
| cd backend && GOCACHE=/tmp/feedvelis-go-cache go vet ./... | 本地 Go 1.26.6；最终完成时间修正后 | passed | 后端全包静态检查通过。 |
| VELIS_TEST_DATABASE_URL=<velis_fix1_e20b_test> GOCACHE=/tmp/feedvelis-go-cache go test -race -count=1 -v ./test/integration | PostgreSQL 17 + pgvector 临时数据库；迁移 up 后运行，完成后临时库已删除且容器已停止 | passed | 真实数据库下租约 fencing、pause/过期/重新认领、hidden 保留和 Article+Source 原子回滚回归全部通过。 |
| make check | 本地 Go 1.26.6、Node.js 24；在最终 Fix 内容上执行 | passed | gofmt、Go 全包测试、race、四个 cmd 构建、Vitest 5 项、vue-tsc 和 Vite production build 全部通过；未注入数据库 URL 的集成包由独立真实 PostgreSQL 命令补足。 |
| git diff --check | 当前工作区；Git 行尾配置会提示 LF/CRLF 转换 | passed | 退出码为 0，无空白错误；仅出现既有 Git 行尾转换提示。 |
| Spec Copilot formal Test | Fix 完成后的新实现 basis | not-run | 按 Fix 阶段协议只做开发者级验证；旧 Test/Review 已失效，需下一阶段由用户启动正式 Test。 |

## E000022 — test / start
- 时间：2026-09-08T02:04:31Z
- 状态：`testing` → `testing`
- 原因：用户指示 fix 阶段已完成、开始正式 test 阶段；Fix cycle 1 已关闭 R0-F1 至 R0-F4，旧 Test/Review 证据已失效，需在修复后的新 basis 上执行独立正式测试。
- 输入/输出依据：- / -

## E000023 — test / complete
- 时间：2026-09-08T02:09:34Z
- 状态：`testing` → `reviewing`
- 原因：Fix cycle 1 完成后新 basis 上的正式 Test 全部通过：targeted 单测、静态检查、真实 PostgreSQL 集成+race、迁移 round-trip、约束/索引核对与 make check 全量回归均 passed，未发现新缺陷，无需产生 T1-F<n> finding。
- 输入/输出依据：- / -

### 验证证据
| 命令/方式 | 环境 | 结果 | 摘要 |
|---|---|---|---|
| cd backend && GOCACHE=/tmp/feedvelis-go-cache go test -count=1 ./internal/domain/... ./internal/application/... ./internal/infrastructure/... ./internal/interfaces/... ./internal/architecture ./cmd/... | 本地 Go 1.26.6；无 DB/网络依赖 | passed | domain/shared、domain/source、domain/article、application/source、application/article、fetcher/httpfeed、interfaces/cli、interfaces/http/hertz、architecture、cmd/velis-migrate 全部 ok；覆盖 URL 规范化、content_hash、dedupe、退避、SSRF、条件请求/304、三格式解析、畸形输入、sanitizer allowlist、分页契约、Hertz 错误信封。 |
| cd web && npx vitest run | 本地 Node.js 24 | passed | 2 文件 5 项通过：HTTP(S) 原文链接校验、时间回退、ping、ApiError 映射、listArticles 游标。组件级渲染转义无专项测试，如实列入未覆盖。 |
| cd backend && go vet ./... && gofmt -l . && cd .. && git diff --check | 本地 Go 1.26.6；当前工作区 | passed | vet 无告警；gofmt 无差异；diff --check 退出码 0（仅既有 LF/CRLF 行尾提示）。架构依赖测试 TestLayerDependencies 在 targeted 中通过。 |
| docker compose up -d postgres；创建临时库 velis_migrate_test 与 velis_integ_test；VELIS_DATABASE_URL=…/velis_migrate_test go run ./cmd/velis-migrate up/down/up + version；VELIS_TEST_DATABASE_URL=…/velis_integ_test GOCACHE=/tmp/feedvelis-go-cache go test -race -count=1 -v ./test/integration | PostgreSQL 17 + pgvector（Docker Compose）；临时库迁移 up 后执行，完成后临时库已 DROP、容器已 stop | passed | 迁移 round-trip up→version=2 dirty=false、down→version=1、up→version=2 全部 ok；真实库集成测试 PASS 且 race 无告警（租约 fencing、pause/过期/重新认领、hidden 保留、原子回滚、幂等）。 |
| psql \d velis.sources / velis.articles + EXPLAIN 列表查询 | 临时库 velis_integ_test（迁移 up 后） | passed | sources_normalized_feed_url_key UNIQUE、(source_id, dedupe_key) UNIQUE、租约 fencing CHECK、URL/长度/状态枚举 CHECK、FK RESTRICT 存在；列表查询 Index Scan 命中 articles_list_idx。 |
| make check | 本地 Go 1.26.6、Node.js 24；仓库根 | passed | 退出码 0：backend-fmt（无改动）、backend-test 26 包 ok、backend-race（集成包无 DB URL 自动 skip）、四个 cmd 构建、Vitest 5 项、vue-tsc、Vite production build 全部通过。 |

## E000024 — review / start
- 时间：2026-09-08T02:15:24Z
- 状态：`reviewing` → `reviewing`
- 原因：用户明确指示开始 review；前置状态 reviewing（test/complete E000023 通过）。
- 输入/输出依据：- / -

## E000025 — review / complete
- 时间：2026-09-08T02:25:00Z
- 状态：`reviewing` → `verified`
- 原因：R1 审查（Fix cycle 1 之后）无 must-fix：R0-F1 至 R0-F4 修复均正确完整，新增实现（嵌套事务、手动抓取认领、fencing 时间条件）经逐项核验无缺陷；测试证据分层真实（Mock/本地/集成/构建回归），结论形成前 basis 重算并 verify valid=true。
- 输入/输出依据：sha256-v1:d7c9f8a4b125ffd335aec5175a2c72f8b65cd8411c0f58b9fa05d77b1d6d9c9e / -

## E000026 — archive / start
- 时间：2026-09-08T02:38:05Z
- 状态：`verified` → `verified`
- 原因：用户明确指示「archive开始整理相关文档」；前置状态 verified（review/complete E000025 passed）。
- 输入/输出依据：- / -

## E000027 — archive / complete
- 时间：2026-09-08T02:39:31Z
- 状态：`verified` → `archived`
- 原因：Change 已通过正式 Test 与 R1 Review（passed，basis d7c9f8a4），归档前只读重算 verify 仍为 valid=true；归档期间仅整理 knowledge/index.md（不在 review basis 中），提升 4 类已验证且未来可复用的事实，并保留 deferred、未覆盖与剩余风险记录。
- 输入/输出依据：- / -

### 知识沉淀
| 文件 | 关键词 |
|---|---|
| knowledge/index.md | URL 规范化, dot-segment, RFC 3986, 租约 fencing, ErrLeaseLost, content_hash, 去重, hidden 保留, 幂等入库, VELIS_TEST_DATABASE_URL, 集成测试约定, article-display-foundation |
