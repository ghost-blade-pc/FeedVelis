# Velis Feed

Velis 是一个可自托管的个人内容聚合与智能阅读平台。本仓库当前按照《Velis 新项目开发文档》建立 M0 工程骨架，采用 Go、CloudWeGo Hertz、PostgreSQL + pgvector、Redis、RabbitMQ、MinIO 和 Vue 3。

## 当前状态

已经落地：

- `velis-api`、`velis-worker`、`velis-migrate` 三个独立进程入口；
- Domain、Application、Infrastructure、Interfaces 四层目录和自动依赖边界测试；
- Hertz `/livez`、`/readyz`、`/api/v1/ping`、Request ID、统一错误、结构化访问日志和优雅关闭；
- PostgreSQL 连接池、pgvector 初始迁移以及 `golang-migrate` 命令；
- Vue 3、TypeScript、Vite、Pinia、Vue Router 的最小可构建 Web 客户端；
- Docker Compose、CI、Go 竞态测试和前端单元测试入口。

Redis、RabbitMQ、MinIO、推荐、Embedding 和业务领域包目前只有基础设施或目录占位，不代表业务能力已经完成。

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

后端需要 Go 1.26，Web 需要 Node.js 24。

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

另一个终端运行 Web：

```bash
cd web
npm ci
npm run dev
```

## 配置规则

YAML 提供非敏感默认值，`VELIS_*` 环境变量优先。数据库地址等敏感配置不要写入受版本控制的文件。可用配置项见 [backend/configs/config.example.yaml](backend/configs/config.example.yaml)。

本项目暂未添加开源许可证；除非后续明确加入许可证，否则默认保留全部权利。
