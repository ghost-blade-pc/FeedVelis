# Proposal

## Why

I4 已具备文章搜索、本人反馈驱动的推荐和当前公开文章读取，下一阶段可以进入 I5 对话 Agent。先交付独立可验收的私有会话、消息持久化与受控只读工具，让后续模型编排、引用和流式响应建立在明确的权限、并发与资源边界上。

## What Changes

- 提供属于用户账户的私有会话：创建空会话、分页列表、读取详情与历史、手动改名、不可恢复删除；管理员也不能越权读取。
- 提供不可变的用户消息追加与“最近消息首查、游标向前加载”的历史读取；预留服务端 `assistant` 角色，当前阶段不生成自动回复。
- 实现 24 小时写命令幂等、标题版本并发控制、服务端消息序号、事务配额，以及删除后不泄露历史幂等载荷的清理规则。
- 提供应用层内部 `search_articles`、`recommend_articles`、`get_article` 三个有界工具，复用当前公开事实复核、本人推荐和故障降级语义；不新增工具 HTTP 接口。
- 增加默认关闭的 Agent 配置、独立加密游标、PostgreSQL 迁移、OpenAPI、观测与真实依赖验收。
- 本 change 对应 I5 第 1–2 步“会话、消息与工具基础”。模型编排、最终回答引用校验、SSE、上下文记忆预算、Web 连续滚动界面和 Agent 评测集分别留给后续 change。

## Capabilities

### New Capabilities

- `agent-conversations`：私有会话及不可变消息的持久化、所有权、限额、幂等、并发、删除、保留和分页 HTTP 契约。
- `agent-read-tools`：携带服务端调用者身份的内部文章搜索、推荐、纯文本读取，以及当前公开事实、错误与资源预算约束。

### Modified Capabilities

无。工具增加应用层调用方式，不改变既有公开文章、搜索、推荐或 Redis 缓存的 HTTP、排序、可见性及降级要求。

## Impact

- 后端四层增加会话领域模型、应用服务及端口、PostgreSQL 仓储、Hertz Handler 和 bootstrap 装配；复用认证、事务和幂等基础设施。
- 新增 `/api/v1/me/agent/conversations` 及其详情、标题和消息端点，更新 `backend/api/openapi/velis.yaml`。
- 新增递增迁移，保存用户配额状态、会话、消息和不含正文的最小删除标记；使用现有幂等表并对 Agent 成功快照增加删除清理能力。
- 搜索应用增加不保留分页资源的一次查询入口；文章应用增加当前公开修订纯文本读取端口；推荐复用现有应用能力。
- 配置、示例环境、操作文档与测试同步更新。会话持久化只依赖 PostgreSQL，Redis、OpenSearch、RabbitMQ 和模型不成为会话接口的必要依赖。
- 建议 I5 开发分支为 `feat/i5-conversational-agent`，本次 propose 不创建分支。没有既有 API 破坏性变更，也不引入通用工具平台或新的模型供应商依赖。
