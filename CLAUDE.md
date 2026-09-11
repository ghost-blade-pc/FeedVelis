# CLAUDE.md

## 项目导航

- [README](README.md)：当前功能、开发现状、启动与验证方式。
- [Velis Roadmap](<Velis Roadmap.md>)：唯一项目目标、技术决策、实施顺序与完成标准；实现前读取对应阶段，未决细节在 change 中定稿。
- `backend/api/openapi/velis.yaml`、`backend/migrations/`：当前 API 与数据库契约，随实现更新。
- `code_copilot/`：执行协议与历史证据；不另立产品路线，不把旧 change 的范围当成当前全项目目标。

## 开发约束

- 后端 module 位于 `backend/`，路径为 `github.com/ghost-blade-pc/Velis_Feed/backend`；工具版本与命令以 `backend/go.mod`、`web/package.json`、根 Makefile 和 CI 为准。
- 保留 Domain、Application、Infrastructure、Interfaces 四层及 bootstrap 装配。Domain 仅依赖 Domain；Application 依赖 Application/Domain；Infrastructure 依赖 Infrastructure/Application/Domain；Interfaces 依赖 Interfaces/Application/Domain。bootstrap 与 architecture 豁免。
- `backend/internal/architecture/dependencies_test.go` 检查层级 import；模块之间通过应用接口或事件协作，不直接修改对方数据。业务规则应能在无 DB、网络和框架的测试中验证。
- SQL、HTTP 状态、Redis Key、AMQP Routing Key、第三方 SDK 类型与协议 JSON 映射不进入领域模型；适配器留在 Infrastructure/Interfaces。不要把目标约束描述成现有代码已全部满足。
- HTTP 中间件顺序保持 RequestID → Recovery → AccessLog；Handler 校验、鉴权、调用用例、统一错误响应；契约细节见 Roadmap 与 OpenAPI。
- 配置优先级为默认值 < YAML < 环境变量；真实凭据不提交、不写日志。当前敏感配置入口为 `VELIS_*`，真实外部账户与生产操作需有明确授权。
- 仓库文档、代码注释、日志与错误消息默认简体中文。保留当前 Vue/TypeScript 前端，不为目录形式重建框架。
- 采用版本化 SQL 迁移，不改写已执行迁移。下迁移/force、生产数据修复及删除数据前明确目标、备份与回滚；`docker compose down -v` 会删除持久卷。
- `make check` 会执行 gofmt 并修改文件；CI 还执行 go vet。真实 PostgreSQL 测试使用专用 `_test` 库并清表；跳过不等于通过。具体命令和验证限制见 README。
- 不自动 commit、push、merge、发布、部署、采购或操作生产。用户已有明确授权的同一动作不重复请求许可。
- 仓库暂无开源许可证，默认保留全部权利。

<!-- spec-copilot:start schema=3 -->
## Spec Coding 协作入口

本仓库采用 `code_copilot/` Spec Coding 工作流。开始处理项目任务前，先读取 `code_copilot/manifest.json`、`code_copilot/README.md` 和 `code_copilot/rules/project-context.md`；当前项目事实、规则和 change 状态以该工作区为准，不在本区块重复维护。

- 创建、升级、修复或校准工作区时，使用可用的 `spec-copilot-bootstrap` Skill。
- 执行或恢复具体 change 时，使用可用的 `spec-copilot-runner` Skill，并遵守工作区中的状态、验证和审查协议。
- 若工作区缺失、协议无效或所需 Skill 不可用，停止推测性写入并向用户报告所缺入口。

此受管区块必须与仓库根目录 `CLAUDE.md` 和 `AGENTS.md` 中的对应区块逐字一致；区块外内容分别由项目和客户端维护。
<!-- spec-copilot:end -->
