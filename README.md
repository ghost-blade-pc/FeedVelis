# Velis Feed

Velis 是一个可自托管的个人内容聚合与智能阅读平台。本仓库采用 Go、CloudWeGo Hertz、PostgreSQL + pgvector 和 Vue 3，并已具备系统级 Feed 抓取、文章保存与列表展示的基础闭环。

## 当前状态

已经落地：

- `velis-api`、`velis-worker`、`velis-migrate`、`velis-admin` 四个独立进程入口；
- Domain、Application、Infrastructure、Interfaces 四层目录和自动依赖边界测试；
- Hertz `/livez`、`/readyz`、`/api/v1/ping`、`GET /api/v1/articles`、Request ID、统一错误、结构化访问日志和优雅关闭；
- 系统级 Source 管理、RSS 2.0/Atom/JSON Feed 安全抓取、条件请求、租约调度、失败退避以及 Article 幂等入库；
- PostgreSQL 连接池、pgvector 初始迁移、Source/Article 表以及 `golang-migrate` 命令；
- Vue 3 最新文章列表，包含加载、空、失败、重试、游标分页和安全原站外链；
- Docker Compose、CI、Go 竞态测试和前端单元测试入口。

用户订阅关系、站内文章详情、互动、搜索、推荐、Embedding、Redis、RabbitMQ 和 MinIO 不在当前文章基础闭环中；对应目录或 Compose 服务不代表这些业务能力已经完成。

## 目录

```text
backend/        Go 后端模块与数据库迁移
web/            Vue 3 Web 客户端
deploy/         Prometheus 等本地部署配置
compose.yaml    本地完整环境
```

后端 Go module 位于 `backend/`，因此完整 module 路径是：

```text
github.com/ghost-blade-pc/Velis_Feed/backend
```

## 本地启动

要求：Docker Compose v2。首次启动需要下载镜像和构建依赖。

```bash
cp .env.example .env
docker compose up --build -d
docker compose ps
```

访问入口：

- Web：<http://localhost:5173>
- API 存活检查：<http://localhost:8080/livez>
- API 就绪检查：<http://localhost:8080/readyz>
- RabbitMQ 管理页：<http://localhost:15672>
- MinIO Console：<http://localhost:9001>

停止环境：

```bash
docker compose down
```

`docker compose down -v` 会删除本地数据库和对象存储卷，不应在仍需保留数据时执行。

## 本地开发

后端需要 Go 1.26.6 或更高补丁版本，Web 需要 Node.js 24。

```bash
make check
```

只启动 PostgreSQL 并在宿主机运行 API：

```bash
docker compose up -d postgres
make migrate-up
cd backend
go run ./cmd/velis-api -config configs/config.example.yaml
```

登记和管理系统级 Feed：

```bash
cd backend
go run ./cmd/velis-admin -config configs/config.example.yaml source add -url https://example.com/feed.xml
go run ./cmd/velis-admin -config configs/config.example.yaml source list
go run ./cmd/velis-admin -config configs/config.example.yaml source fetch 1
go run ./cmd/velis-admin -config configs/config.example.yaml source pause 1
go run ./cmd/velis-admin -config configs/config.example.yaml source resume 1
```

Source 管理只提供本地 CLI，不暴露 HTTP 写接口。Worker 会认领到期 Source 并执行抓取；Web 的 `/latest` 页面读取匿名只读文章列表，标题直接跳转 HTTP(S) 原站。

另一个终端运行 Web：

```bash
cd web
npm ci
npm run dev
```

## 配置规则

YAML 提供非敏感默认值，`VELIS_*` 环境变量优先。数据库地址等敏感配置不要写入受版本控制的文件。可用配置项见 [backend/configs/config.example.yaml](backend/configs/config.example.yaml)。

本项目暂未添加开源许可证；除非后续明确加入许可证，否则默认保留全部权利。
