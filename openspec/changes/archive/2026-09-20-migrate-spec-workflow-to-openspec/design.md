# Design

## Context

仓库根的 `AGENTS.md` 与 `CLAUDE.md` 目前含有逐字一致的 Spec Copilot 受管区块，它要求任务先进入 `code_copilot/` 阶段协议。OpenSpec 已在仓库根完成初始化，标准 schema 为 `spec-driven`，Codex 与 Claude 的 OpenSpec skill 也已生成。迁移时必须保留现有未提交业务改动，并不得改写已归档 change 的证据链。

## Goals / Non-Goals

**Goals:**

- 让所有后续 Spec change 只使用 OpenSpec 入口。
- 让根级项目文档与 I1 已归档、I2 待开始的当前事实一致。
- 在不破坏 Spec Copilot 历史文件的前提下，明确它们不再是当前执行入口。

**Non-Goals:**

- 不为 OpenSpec 添加任务分级、自定义 schema、artifact 规则或 operation guidance。
- 不把历史 Spec Copilot change 转换成 OpenSpec change，也不删除其事件、日志、Review 报告或知识文档。
- 不在本 change 中设计或实现 I2。

## Decisions

### 1. 单一未来工作流：OpenSpec

`AGENTS.md` 与 `CLAUDE.md` 将删除 Spec Copilot 受管区块，改为简洁且逐字一致的 OpenSpec 协作入口。该入口只指向 `openspec/`、已安装的标准 skill 与 OpenSpec artifact，不另行定义分级或阶段规则。

替代方案是同时保留两套活跃入口，但这会让 change 状态与授权边界继续冲突，因此不采用。

### 2. `code_copilot/` 原地冻结为历史证据

不删除或批量搬迁 `code_copilot/`。只在其 `README.md` 顶部加入显眼的历史状态说明，声明停止新建、继续、迁移或归档其 change，并指向 `openspec/`。历史文件内部当时的状态和表述保持原样。

### 3. 保留但取代 `i1-status-documentation-sync`

该 change 保留 `ready` card、events 与 log 的原始字节，不再调用旧 Runner 推进它。`code_copilot/README.md` 的冻结声明将明确记录它由 `migrate-spec-workflow-to-openspec` 取代；其中有用的 I1 文档同步范围由本 change 重新承担。

替代方案是删除该目录或手改 card 为非协议状态；前者丢失证据，后者破坏事件链语义，因此都不采用。

### 4. 路线图同步以归档证据为准

Roadmap 会将 I1 标记为已完成并链接 `account-access-foundation`，将 I2 标记为下一阶段。已有 RSS 抓取与阅读仍只是 I4 可复用基础，因为统一发布、审核和可靠异步尚未实现。I1 已接受的改密、找回、注销、设备会话和管理员 HTTP 等限制继续保留。

## Risks / Trade-offs

- [历史 `ready` change 容易被误认为仍可执行] → 在 `code_copilot/README.md`、根协作入口和 Roadmap 中统一声明其已冻结，且新工作只读取历史证据。
- [Roadmap 状态更新被误读为 I1 无限制完成] → 在阶段表和当前基线中同时保留 README 已列出的能力边界。
- [两个 Agent 入口再次漂移] → 对 `AGENTS.md` 和 `CLAUDE.md` 的 OpenSpec 区块做逐字比较验证。

## Migration Plan

1. 替换 `AGENTS.md` 与 `CLAUDE.md` 的协作入口，并保持两者一致。
2. 更新 README 导航、Roadmap 阶段状态和文档维护规则。
3. 在 `code_copilot/README.md` 冻结旧工作区，记录被取代的未执行提案。
4. 通过文本检索、区块对比、OpenSpec 验证和 Git diff 核对迁移结果。

如需回滚，只回退本 change 对上述文档的改动；OpenSpec 初始化资产和原有 `code_copilot/` 历史不在本 change 中删除。
