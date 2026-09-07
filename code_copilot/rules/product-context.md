# 产品与决策上下文

> 仅用于 `initial`、`scaffolded` 或仍有关键产品决策的项目。确认后再把假设转为事实。

## 用户确认

- 当前仓库的基础工程框架已经完成，后续项目任务开始使用 Spec 工作区管理。
- 目标用户与问题：以仓库权威开发文档为当前已记录产品定义；若具体 change 要改变该定义，需由开发者重新确认。
- 核心场景与可观察结果：订阅/发布内容，经采集、去重与分发形成可浏览、可检索、可解释且可降级的个人 Feed。
- MVP：以 `Velis新项目开发文档.md` 的“首版范围”和 M1–M6 验收为当前范围来源；Bootstrap 不改变其实施顺序或确认状态。
- 非目标：不做开放互联网爬虫、视频/私信/直播、在线模型训练、独立向量数据库、通用 Agent/MCP 平台、大量首版微服务或 Kubernetes/多地域部署。
- 成功信号：以开发文档“首版成功标准”和各里程碑验收为准；目录或占位文件存在不算业务能力完成。
- 功能与非功能约束：Feed 与内容消费是主体；推荐与 Embedding 可降级；PostgreSQL 是业务事实源；Redis 只保存可丢弃、可重建状态；核心路径需具备自动化验证与可观测入口。

## 仓库事实

| 事实 | 路径、符号、配置或测试证据 | 影响 |
|---|---|---|
| 当前为 M0 工程骨架 | `README.md`、`backend/cmd/`、`web/src/` | 新业务能力必须经 change 提案，不能把占位目录当成既有实现。 |
| 后端采用四层依赖边界 | `backend/internal/architecture/dependencies_test.go#TestLayerDependencies` | 新代码的包位置与 import 必须满足自动化边界测试。 |
| PostgreSQL/pgvector 是当前持久化基础 | `backend/migrations/000001_initialize_platform.up.sql`、`compose.yaml` | schema、索引、迁移、回滚和数据一致性属于显式契约。 |
| 现有 HTTP 只落地健康与 ping | `backend/internal/interfaces/http/hertz/router.go#NewServer` | 开发文档中的其余端点是规划，不是当前可用 API。 |
| Web 是可构建最小客户端 | `web/package.json`、`web/src/` | 页面、状态与业务 feature 仍需逐项实现和验收。 |

## 假设、建议与待决策

| 类型 | 内容 | 依据 | 开发者决定 |
|---|---|---|---|
| 待决策 | 下一个业务 change 的目标、范围、优先级与验收标准 | M0 后仍有 M1–M6 多个候选能力 | pending |
| 待决策 | 哪些纯文档/格式类改动可列入免协议清单 | `rules/project-context.md` 当前未预授权 | pending |

## 方案取舍

| 候选 | 适配性 | 实现与迁移成本 | 扩展与运维 | 风险与退出 | 结论 |
|---|---|---|---|---|---|
| TODO: 由具体 change 提供有实质差异的候选 | 与对应业务目标一并评估 | 在 change 中量化 | 在 change 中量化 | 在 change 中说明回滚与退出 | pending |

通用能力按“项目已有实现 → 企业内部能力 → 成熟开源方案 → 自研”调查，但不把顺序当作强制选型。新依赖或服务还需评估 License、安全与供应链、升级兼容和维护所有权。

## ADR 候选

- 当前开发文档已列出 ADR-001 至 ADR-005；具体 change 若要改变这些选择，必须显式提出兼容、迁移、运维与退出影响并由开发者确认。
