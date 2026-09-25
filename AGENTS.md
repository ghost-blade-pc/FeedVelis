# Repository Guidelines

## 项目结构与模块组织

- `backend/cmd/` 包含 API、Worker、迁移和管理 CLI；`backend/internal/` 按 Domain、Application、Infrastructure、Interfaces 四层组织，`bootstrap/` 负责装配。
- `backend/migrations/` 保存版本化 PostgreSQL 迁移，`backend/api/openapi/velis.yaml` 是 HTTP 契约，`backend/test/` 放真实依赖与端到端测试。
- `web/src/` 是 Vue 3 + TypeScript 应用，测试与实现就近放置；`compose.yaml` 和 `deploy/` 提供本地基础设施。
- `openspec/` 是当前规划与长期规格入口；`code_copilot/` 仅作只读历史证据。实现前查阅 `README.md` 与 `Velis Roadmap.md`。

## 构建、测试与本地开发

- `make check`：执行 gofmt、Go 单测/race/build，以及 Web lint、Vitest 和构建；会改写 Go 格式。
- `make compose-up` / `make compose-down`：构建、启动或停止完整本地环境。`docker compose down -v` 会删除持久卷，谨慎使用。
- `make migrate-up`：对示例配置指向的 PostgreSQL 执行迁移。
- `cd web && npm ci && npm run dev`：启动 Vite 开发服务器。
- `make integration-all`：运行 PostgreSQL、RabbitMQ、OpenSearch 真实依赖测试；先设置对应的 `VELIS_TEST_*`，数据库必须是专用 `_test` 库。

## 编码风格与架构约束

Go 代码以 `gofmt` 为准，包名小写；Vue 组件使用 PascalCase，TypeScript/Vue 延续现有两空格缩进并通过 ESLint。仓库文档、注释、日志和错误消息默认使用简体中文。

保持四层依赖方向：Domain 仅依赖 Domain；Application 依赖 Domain；Infrastructure 和 Interfaces 通过应用端口协作。SQL、HTTP 状态、Redis/AMQP 键及第三方 SDK 类型不得进入领域模型。不要改写已执行迁移；新变更添加递增编号的 `.up.sql`/`.down.sql`。

## 测试规范

Go 使用标准 `testing`，文件命名为 `*_test.go`；Web 使用 Vitest，文件命名为 `*.test.ts`。仓库暂无固定覆盖率阈值，但每项行为变更必须包含成功、失败和边界回归测试。架构改动需运行 `backend/internal/architecture/` 测试；真实依赖测试的 skip 不等于通过。

## Commit 与 Pull Request

近期提交采用 Conventional Commits 风格及简洁中文主题，例如 `feat: 实现OpenSearch`、`fix: 修复默认周期`、`docs: 归档Spec`。一个提交聚焦一个逻辑变化。

PR 应说明范围、风险、关联 issue/OpenSpec change，以及已运行命令和结果；Web 视觉变更附截图。若修改 API、数据库或配置，同时更新 OpenAPI、迁移、示例配置和相关文档。Review 前重新运行 `make check`。不得自动提交、推送、合并、发布或操作生产。

## 安全与配置

配置优先级为默认值 < YAML < 环境变量。真实凭据仅通过 `VELIS_*` 注入，不提交、不写日志；涉及外部账户、生产数据、降级迁移或删除操作时，先明确备份与回滚方案。

<!-- openspec:start -->
## OpenSpec 协作入口

本仓库后续使用 `openspec/` 作为唯一 Spec 工作区，按已安装的 OpenSpec 标准 skill 执行 explore、propose、apply、sync 和 archive。

- 使用 `openspec/config.yaml` 中配置的标准 schema；change 的提案、设计、任务与状态以 `openspec/changes/` 为准，已同步的长期行为规范以 `openspec/specs/` 为准。
- `code_copilot/` 仅保留为迁移前的历史证据；不再于其中创建、继续、修复、Review 或归档 change。
- README 记录当前可用功能，Roadmap 维护项目目标与实施顺序；不用规划中的目标能力冒充已实现事实。

此区块必须与仓库根目录 `CLAUDE.md` 和 `AGENTS.md` 中的对应区块逐字一致；区块外内容分别由项目和客户端维护。
<!-- openspec:end -->
