# Proposal

## Why

Velis 当前文章模型只容纳强制关联 Source 和外链的 RSS 内容，缺少用户草稿、图文投稿、稳定站内发布时间、内容版本和管理员 HTTP 管理闭环。I2 需要在不引入 MQ、Redis、搜索或模型的前提下，把 RSS 自动发布与用户主动发布统一到同一公开内容池，并为后续异步增强提供可靠的文章版本边界。

## What Changes

- 将 RSS 专用文章结构演进为区分 `rss`/`user` 来源的统一文章聚合，采用 `draft`、`published`、`offline`、`deleted` 生命周期、不可变内容版本、固定站内发布时间与乐观锁。
- 增加登录用户的 Markdown 草稿、直接发布、立即公开编辑、作者下架/重新发布和软删除能力；管理员只能全局下架与恢复，不获得读取草稿、编辑或删除投稿的权限。
- 对全部 I2 写接口统一应用 `Idempotency-Key`，对既有资源修改叠加 `If-Match`，覆盖响应丢失、重复提交和并发冲突。
- 增加私有 MinIO 图片资产的预签名直传、服务端确认、版本引用、配额、孤儿清理和受控匿名流式读取；MinIO 故障只使资产能力降级。
- 增加管理员 Source API/Web，支持新增、来源级抓取周期、暂停、恢复、同步手动抓取和最近抓取历史；继续复用租约、fencing、条件请求和失败退避。
- 强化 Feed SSRF 边界：默认忽略环境代理，仅允许通过专用配置显式启用可信出口代理。
- **BREAKING**：匿名文章响应由必填 `source`/`canonical_url` 演进为区分 RSS 来源和站内作者的联合 `origin`；分页改为固定 `published_at` 与文章 ID。
- 使用新增版本化迁移保留历史 RSS 文章 ID，将现有内容回填为 version 1、以 `discovered_at` 回填站内发布时间，并在降级会丢失新模型数据时拒绝 down migration。
- 增加投稿、资产、Source 管理和联合 latest 的最小 Vue Web 页面与确定性测试；更新 OpenAPI、README 和运行配置。

## Capabilities

### New Capabilities

- `unified-articles`: RSS 与用户投稿的统一文章身份、版本、状态、权限、幂等写入、历史迁移、匿名 latest/详情和作者 Web 工作流。
- `article-assets`: 私有图片的预签名上传、确认、归属与版本引用、配额、匿名受控读取、清理和局部降级。
- `source-administration`: 管理员 Source API/Web、来源级周期、抓取历史、手动抓取并发语义和可信网络出口边界。

### Modified Capabilities

无。当前 `openspec/specs/` 尚无已同步能力规格。

## Impact

- 后端领域、应用、PostgreSQL 仓储、事务、Hertz 路由/中间件/DTO、Worker 调度和 bootstrap 装配将扩展；保持既有四层依赖边界。
- 新增只前滚的版本化 SQL 迁移、MinIO/S3 兼容适配器、Markdown 渲染器和相关配置；不改写现有迁移。
- `backend/api/openapi/velis.yaml` 的文章响应发生破坏性变更，并新增作者、资产和管理员 Source 契约及错误码。
- Vue/TypeScript Web 增加本人文章、Markdown 编辑、图片上传和 Source 管理页面，并适配联合来源展示。
- Compose/示例配置补齐私有 Bucket 和精确 CORS 所需参数；PostgreSQL 仍是业务事实来源，MinIO 是局部可降级依赖。
- I2 不接入 RabbitMQ、Redis、OpenSearch、Outbox Relay、AI/模型或推荐逻辑。
