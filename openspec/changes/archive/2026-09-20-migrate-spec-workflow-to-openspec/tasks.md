# Tasks

## 1. 切换协作入口

- [x] 1.1 将 `AGENTS.md` 和 `CLAUDE.md` 的 Spec Copilot 入口替换为标准 OpenSpec 入口，并通过提取区块后的 `cmp` 验证两份文档逐字一致。
- [x] 1.2 更新两份文档的项目导航，将 `openspec/` 标记为当前规范与 change 入口、`code_copilot/` 标记为只读历史证据，并用 `rg` 验证不再指示调用 Spec Copilot skill。

## 2. 冻结旧工作区

- [x] 2.1 在 `code_copilot/README.md` 顶部声明工作区已冻结为历史证据、禁止新建或推进 change 并指向 `openspec/`，然后用 `rg` 确认三项信息均可查找。
- [x] 2.2 在冻结声明中记录 `i1-status-documentation-sync` 已由 `migrate-spec-workflow-to-openspec` 取代，并通过改动前后的 SHA-256 比较确认该旧 change 的 card、events 和 log 没有被修改。

## 3. 同步项目现状文档

- [x] 3.1 更新 `README.md` 的项目摘要、目录现状与文档分工，并用 `rg` 确认账户闭环、OpenSpec 当前入口和 Spec Copilot 历史定位都已明确。
- [x] 3.2 更新 `Velis Roadmap.md` 的基线、I1/I2 状态、下一阶段与文档维护规则，并用定向检索确认当前入口不再将 I1 写为待实施，同时保留 I1 限制与 I4 未完成边界。

## 4. 验证迁移结果

- [x] 4.1 运行 `openspec validate migrate-spec-workflow-to-openspec --type change --strict`、Markdown 矛盾词检索与 `git diff --check`，检查 OpenSpec artifact 有效、根文档口径一致且无空白错误。
- [x] 4.2 检查最终 diff 只包含授权的协作与项目文档及 OpenSpec artifact，确认未改动应用代码、运行配置或 Spec Copilot 历史 change 文件。
