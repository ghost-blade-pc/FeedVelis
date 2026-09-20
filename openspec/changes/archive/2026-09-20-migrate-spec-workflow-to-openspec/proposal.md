# Proposal

## Why

项目已初始化 OpenSpec，但仓库协作入口仍强制使用 `code_copilot` 的严格阶段与事件链，且 Roadmap 仍把已归档的 I1 账户与权限写为待实施。需要将后续 Spec 工作流统一迁移到 OpenSpec，同时校准当前项目状态。

## What Changes

- 将 `AGENTS.md` 和 `CLAUDE.md` 的协作入口从 Spec Copilot 替换为标准 OpenSpec 工作流，两份文档保持一致。
- 更新 `README.md` 的项目摘要、目录导航与文档分工，表明账户闭环已实现，OpenSpec 是后续规范与 change 入口。
- 更新 `Velis Roadmap.md` 的当前基线、I1 状态、下一阶段与文档维护规则；明确 I2 是下一项业务，不将已有 RSS 基础能力冒充为 I4 完成。
- 将 `code_copilot/` 标记为只读历史证据；后续不再于其中创建或推进 change。
- 保留 `code_copilot/changes/i1-status-documentation-sync/` 的现有字节与事件链，在历史导航中记录它已被本 OpenSpec change 取代，不再执行。
- 不引入自定义分级规则，保留 `openspec/config.yaml` 的标准 `spec-driven` schema。

## Capabilities

### New Capabilities

无。这是协作工具与文档迁移，不改变产品行为；change 通过 `skip_specs: true` 明确不生成产品能力规范。

### Modified Capabilities

无。

## Impact

- 受影响文档：`AGENTS.md`、`CLAUDE.md`、`README.md`、`Velis Roadmap.md`、`code_copilot/README.md`。
- 受影响工作流：未来的提案、实施、规范同步和归档使用 OpenSpec；`code_copilot` 仅供历史查阅。
- 不修改应用代码、API、数据库、运行配置、测试行为或历史 Spec Copilot 证据文件。
