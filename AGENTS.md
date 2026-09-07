# AGENT.md

This file provides guidance to Codex when working with code in this repository.

## 项目概述

Velis 是可自托管的个人内容聚合与智能阅读平台（RSS/Atom 聚合、Markdown 发布、个性化 Feed）。当前处于 M0 工程骨架阶段：三个进程入口、四层目录、健康检查、迁移与 CI 已落地；各业务领域包目前只有 `doc.go` 占位，尚未实现业务能力。

开发参考文档是 `Velis新项目开发文档.md`（架构蓝图、里程碑 M0–M6、ADR、API 端点清单、测试策略）。README.md 只描述当前已落地状态。实现业务功能前先读开发文档对应章节以及与开发人员进行沟通，最终开发路线可能与开发文档有所出入。

后端 Go module 位于 `backend/`，完整 module 路径 `github.com/ghost-blade-pc/Velis_Feed/backend`。要求 Go 1.26、Node.js 24。

## 常用命令

全部封装在根目录 Makefile：

```bash
make check          # 完整验证：backend-fmt backend-test backend-race backend-build web-test web-build
make backend-test   # cd backend && go test ./...
make backend-race   # go test -race -count=1 ./...
make backend-build  # go build ./cmd/...
make backend-fmt    # gofmt -w 全部 .go 文件（会修改文件）
make web-test       # cd web && npm test（vitest run）
make web-build      # vue-tsc -b && vite build
make migrate-up     # 对本地 PostgreSQL 执行迁移
make migrate-down   # 回退 1 个版本
make compose-up / make compose-down
```

单个测试：

```bash
cd backend && go test ./internal/application/health/ -run TestXxx
cd web && npx vitest run src/api/client.test.ts
```

本地开发（只启动 postgres，宿主机直接跑进程）：

```bash
docker compose up -d postgres
make migrate-up
cd backend && go run ./cmd/velis-api -config configs/config.example.yaml
# 另一个终端
cd web && npm ci && npm run dev    # Vite :5173，代理 /api /livez /readyz 到 localhost:8080
```

迁移命令也可直接使用：`go run ./cmd/velis-migrate [-config <yaml>] [-path migrations] [-steps N] [-version N] <up|down|version|force>`。

CI（`.github/workflows/ci.yml`）在后端跑：gofmt 无差异检查、`go vet ./...`、`go test -race -count=1 ./...`、`go build ./cmd/...`；前端跑 npm ci / test / build。提交前用 `make check` 可本地等价验证。

## 架构

### 进程与部署

模块化单体：一个 Go module，三个独立进程（`backend/cmd/`）：

- `velis-api` — CloudWeGo Hertz HTTP/SSE 服务（:8080）
- `velis-worker` — MQ Consumer、Outbox Relay、抓取调度（当前为占位）
- `velis-migrate` — golang-migrate 迁移命令（SQL 在 `backend/migrations/`）

基础设施：PostgreSQL + pgvector（**唯一**业务/向量持久化库）、Redis（仅缓存与短期在线数据）、RabbitMQ（领域事件与异步任务）、MinIO（对象存储）。`compose.yaml` 提供全部环境；prometheus/grafana 在 `observability` profile 下（`docker compose --profile observability up`）。`docker compose down -v` 会删除数据卷，需保留数据时不要执行。

### 四层架构（强制，由测试保证）

```
Interfaces ──────► Application ──────► Domain
Infrastructure ──► Application / Domain
Bootstrap ───────► 所有层（Composition Root，仅负责装配）
```

- `internal/domain/<module>` — 实体、聚合、领域事件、Repository 抽象。模块：account、source、article、feed、interaction、relation、exposure、recommendation、embedding、shared
- `internal/application/<module>` + `internal/application/ports/` — Use Case、事务边界与跨聚合编排；声明技术端口（`TxManager`、`EventPublisher`、`Cache`、`Embedder`、`VectorIndex`…），由 infrastructure 实现
- `internal/infrastructure/` — 端口实现：persistence/postgres（pgx）、cache/redis、messaging/rabbitmq、objectstore/minio、vector/pgvector、ai/eino、fetcher/httpfeed、clock、observability
- `internal/interfaces/` — 协议适配：http/hertz（handler/middleware/presenter/dto + router.go）、consumer、scheduler、cli
- `internal/bootstrap/` — 每进程的 Composition Root 装配入口

分层边界由 `internal/architecture/dependencies_test.go` 用 go/parser 解析 import 强制检查，违反即测试失败：domain 只能 import domain；application 只能 import application/domain；infrastructure 可 import infrastructure/application/domain；interfaces 可 import interfaces/application/domain；bootstrap 与 architecture 豁免。任何层都不得反向依赖 interfaces。

领域对象中不出现 SQL、JSON、HTTP 状态码、Redis Key、AMQP Routing Key 或第三方 SDK 类型（Hertz DTO、GORM Model、pgvector 类型只能出现在 infrastructure/interfaces 层）。

### HTTP 层约定

- 中间件固定顺序：RequestID → Recovery → AccessLog（`internal/interfaces/http/hertz/router.go`）
- Handler 标准流程：绑定校验 → 取调用者身份 → 调 Use Case → 映射错误与响应（presenter 统一 JSON 信封）
- API 约定（开发文档 §9）：Base URL `/api/v1`；JSON 字段 snake_case；列表统一 `items`/`next_cursor`/`has_more` 游标分页；错误返回稳定业务码 `{"error": {code, message, request_id, details}}`，不泄露 SQL/堆栈；写接口支持 `Idempotency-Key`；所有响应带 `X-Request-ID`
- OpenAPI 契约：`backend/api/openapi/velis.yaml`，变更需同步

### 配置

`internal/infrastructure/config/config.go`：内置默认值 → YAML 文件 → `VELIS_*` 环境变量覆盖 → 统一校验。YAML 只放非敏感默认值（`backend/configs/config.example.yaml`），数据库地址等敏感配置只能走环境变量、不得提交。`configs/policies.example.yaml` 是占位。

### 前端

`web/`：Vue 3 + TypeScript + Vite + Pinia + Vue Router。测试用 Vitest（匹配 `src/**/*.test.ts`）。开发时 Vite 代理 `/api`、`/livez`、`/readyz` 到 localhost:8080；生产由 `web/nginx.conf` 同样代理到 velis-api 并回退 `index.html`。`src/features/` 按业务组织，`src/types/` 与后端 DTO 对应。`web/e2e/` 是 Playwright 占位，尚未实现。

## 关键架构决策（开发文档 §20 ADR）

- 先模块化单体，出现独立扩缩容证据后再抽 Kitex 微服务
- PostgreSQL 是唯一持久化业务/向量库，Redis 只存可丢弃、可重建的数据，Redis 故障回源 PostgreSQL
- 核心写同步提交 + 同事务 Outbox，计数/通知/Embedding 等派生效果异步处理（MQ 短暂故障不影响事实一致性）
- 推荐可解释且可降级：Embedding 只是召回/特征通道之一，Provider 故障时发布与基础 Feed 仍可用
- 四层按依赖组织而非目录命名装饰——业务规则必须能在无 DB、无网络、无框架下测试

## 惯例

- 代码注释、日志与错误消息使用中文（与现有代码一致）
- 开发文档 §19 定义了模块完成标准：业务规则在 Domain/Application、迁移可回滚、覆盖正常/边界/权限/幂等/故障场景、外部调用有超时/取消/重试边界、无数据竞争与 goroutine 泄漏
- 仓库暂无开源许可证，默认保留全部权利
<!-- spec-copilot:start schema=3 -->
## Spec Coding 协作入口

本仓库采用 `code_copilot/` Spec Coding 工作流。开始处理项目任务前，先读取 `code_copilot/manifest.json`、`code_copilot/README.md` 和 `code_copilot/rules/project-context.md`；当前项目事实、规则和 change 状态以该工作区为准，不在本区块重复维护。

- 创建、升级、修复或校准工作区时，使用可用的 `spec-copilot-bootstrap` Skill。
- 执行或恢复具体 change 时，使用可用的 `spec-copilot-runner` Skill，并遵守工作区中的状态、验证和审查协议。
- 若工作区缺失、协议无效或所需 Skill 不可用，停止推测性写入并向用户报告所缺入口。

此受管区块必须与仓库根目录 `CLAUDE.md` 和 `AGENTS.md` 中的对应区块逐字一致；区块外内容分别由项目和客户端维护。
<!-- spec-copilot:end -->
