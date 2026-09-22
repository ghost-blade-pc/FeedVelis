# Design

## Context

见 [proposal.md](proposal.md)。当前 `AssetConfig.Endpoint` 以 `host:port` 配合 `UseTLS` 同时创建对象操作客户端和预签名客户端；MinIO SDK 会把该 endpoint 的 Host 纳入 AWS Signature V4。Compose 中 API 只能通过 `minio:9000` 访问 MinIO，而宿主机浏览器只能通过发布端口 `localhost:9000` 访问，因此现有上传凭证在浏览器网络中不可达。

资产应用服务已经把数据库内的 `pending` 身份创建与事务外的预签名分开，API 响应模型也已经包含完整 `upload_url`，所以本次无需修改领域模型、数据库或 HTTP 响应结构。Bucket 必须继续保持私有，匿名读取仍由 API 实时授权代理。

## Goals / Non-Goals

**Goals:**

- 明确区分服务端对象操作地址和 Web 客户端直传地址，并让两者在不同网络命名空间中协作。
- 保证公共 Host 在签名生成时就参与 Signature V4，而不是签名后改写 URL。
- 通过内部连接解析 Bucket region，公共上传端点不需要从 API 容器可达。
- 对缺失或危险的公共端点配置尽早失败，并保持对象存储运行时故障的局部降级语义。

**Non-Goals:**

- 不改为 API 中转上传，不增加 multipart、断点续传、缩略图或图片转码。
- 不改变资产配额、生命周期、确认、匿名读取或幂等协议。
- 不支持带路径前缀的 S3 反向代理端点，也不从 HTTP 请求动态发现外部地址。
- 不迁移或主动删除现有 `pending` 记录；既有清理策略继续处理失败操作留下的孤儿资产。

## Decisions

### 1. 公共上传端点使用独立的完整 URL 配置

在 `AssetConfig` 增加 `upload_endpoint`，环境变量为 `VELIS_ASSET_UPLOAD_ENDPOINT`。它使用完整 HTTP(S) URL，例如本地 `http://localhost:9000`、生产 `https://assets.example.com`；现有 `endpoint` 与 `use_tls` 保持服务端内部连接语义。

启用对象存储时 `upload_endpoint` 必填。配置加载 SHALL 要求 scheme 仅为 `http` 或 `https`、Host 非空，且不得包含 userinfo、query、fragment 或除可规范化根斜杠之外的路径。公共端点是部署者声明的信任边界，不从请求 `Host`、`Origin`、转发头或内部 endpoint 自动推导。

这是有意的配置兼容性变更：静默回退到内部 endpoint 会重现当前故障，并可能把内部 DNS 名称泄露给客户端。资产未启用时该字段可以为空；字段非法属于确定性的启动配置错误，而非可恢复的 MinIO 运行时故障。

备选方案是增加另一组 `host:port + use_tls` 字段，但完整 URL 更直接表达浏览器实际访问的 scheme 和 authority，也减少两字段组合不一致。

### 2. 内部客户端执行对象操作，公共签名客户端只离线签名

`Store` 保留内部 MinIO 客户端，用于 Bucket 私有性校验、region 查询、HEAD、受限探测、流式读取和删除。预签名时先通过内部客户端取得 Bucket region，再以公共 URL 拆出的 host、scheme、同一凭据和该 region 构造签名客户端；显式 region 使 MinIO SDK 生成 URL 时不向公共端点执行 location 请求。

首次成功解析的 region 和对应不可变签名客户端可在 `Store` 内并发安全地缓存；region 查询或签名失败继续映射为稳定的资产存储不可用错误，且不得记录带签名查询参数的 URL。Bucket region 被视为部署期稳定属性，进程重启后重新发现。

不能先用内部 endpoint 签名再替换 Host，因为 Signature V4 的 `host` 是签名头；也不让公共签名客户端直接探测 region，因为公共地址可能只对浏览器或外部负载均衡网络可达。

### 3. API 契约保持结构兼容，只改变 `upload_url` 的 authority

`POST /api/v1/me/assets` 继续返回 `AssetUpload`，其方法、headers、15 分钟期限与幂等语义不变。`upload_url` 的 scheme 和 authority 必须来自公共上传端点，对象路径仍由服务端固定生成。

前端继续按服务端返回值直接执行 XHR PUT，无需了解内部 endpoint，也不负责 URL 改写。当前网络错误与同一次操作重试逻辑保留；修复后的同一凭证在有效期内可以正常重试。

### 4. Compose 显式提供内外两种地址

Compose 后端环境继续设置 `VELIS_ASSET_ENDPOINT=minio:9000`，并新增默认 `VELIS_ASSET_UPLOAD_ENDPOINT=http://localhost:9000`。MinIO 保持发布 `9000` 端口，并继续用 `VELIS_ASSET_WEB_ORIGIN` 配置精确 CORS。

`.env.example`、示例 YAML 与 README 同时说明三个不同概念：内部 endpoint、公共上传 endpoint、允许发起跨域请求的 Web Origin。生产部署必须把公共 endpoint 指向客户端可解析、可路由且证书有效的 MinIO/S3 入口。

### 5. 回归验证必须跨越网络视角

配置测试覆盖 YAML/环境变量优先级、URL 约束以及启用时必填。适配器测试使用不同的内部和公共 authority，断言签名 URL 采用公共 authority，并实际 PUT 后通过内部客户端完成 Stat/Probe；测试还应证明公共签名不要求 API 侧连接公共 endpoint。

真实 Compose/HTTP 验证从宿主机调用运行在容器中的 API 创建资产，使用响应 URL 上传，再调用确认接口，覆盖 `minio:9000` 与 `localhost:9000` 的真实分离。现有 CORS 正反来源验证继续保留。未设置真实 MinIO/数据库测试环境时仍必须明确报告跳过，不能把跳过计作通过。

## Risks / Trade-offs

- [新增必填配置会使未迁移部署启动失败] → 在示例、README 和迁移说明中给出本地与生产取值，Compose 提供安全的开发默认值。
- [公共 endpoint、反向代理 Host 或 TLS 证书不一致会导致签名失败] → 对 URL 做严格校验，并用跨网络 E2E 验证实际 PUT；文档强调代理必须保留签名所用 Host。
- [region 获取失败会在数据库已创建 `pending` 身份后使资产创建响应失败] → 保持现有事务外签名与幂等重试设计；同一幂等键重试复用资产身份，过期孤儿由既有任务清理。
- [缓存的 region 在对象存储运行期被修改后失效] → 把 region 视为部署期不变量，变更后重启 API；不为罕见的热切换增加复杂缓存失效协议。
- [公共上传入口扩大 MinIO 的网络暴露面] → Bucket 保持私有，仅短期 Signature V4 PUT 可写固定对象键，CORS 继续限制精确 Web Origin，匿名读取仍不能直连对象存储。

## Migration Plan

1. 在部署配置中新增公共上传 endpoint；本地 Compose 使用 `http://localhost:9000`，生产使用客户端可达的 HTTPS 资产入口。
2. 发布包含新配置校验和双端点签名适配器的 API/Worker/Web 配置。配置校验在进程接受流量前完成，不需要数据库迁移。
3. 用真实浏览器来源完成创建、PUT、确认和读取闭环，并核对返回 URL 不包含内部服务名。
4. 回滚应用时同时恢复旧配置格式；数据库和现有对象不需要回滚。回滚后若内部 endpoint 对浏览器不可达，图片上传会恢复为旧故障，因此优先前滚修复。
