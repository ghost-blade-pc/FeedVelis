# Proposal

## Why

当前对象存储配置同时承担服务端内部访问和浏览器预签名直传两个网络角色；Docker Compose 中生成的 `minio:9000` 上传地址只在容器网络内可达，导致资产创建成功后浏览器直传必然失败。需要显式分离内部访问端点和公共上传端点，使同一套资产协议在本地、测试和生产的不同网络拓扑下都能工作。

## What Changes

- **BREAKING**：为启用的资产存储增加必填、完整 URL 形式的公共上传端点配置，专用于生成浏览器可访问且 Host 已正确参与签名的预签名 PUT URL；已有部署升级时必须显式补充该配置。
- 保留现有内部对象存储端点，继续供 Bucket 校验、确认探测、授权读取和清理使用；禁止通过字符串替换已签名 URL 或从请求 Host/Origin 推导公共端点。
- 在对象存储适配器中分别处理内部操作与公共地址签名，并通过内部连接取得签名所需的 Bucket region，避免要求 API 容器能够访问公共端点。
- 更新 Compose、示例配置和运维文档：本地内部端点使用 `minio:9000`，公共上传端点使用 `http://localhost:9000`，生产可配置独立 HTTPS 资产域名。
- 增加跨网络视角的配置、适配器和真实 MinIO 回归验证，确保返回的 URL 使用公共地址、浏览器侧 PUT 可成功且服务端仍通过内部地址确认对象。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `article-assets`：预签名上传信息必须使用显式配置、由 Web 客户端可达的公共上传端点，同时服务端对象操作继续使用内部端点。

## Impact

- 后端资产配置、环境变量映射、校验与安全日志。
- MinIO 客户端和对象存储适配器的签名职责、region 获取及错误降级。
- Docker Compose、`.env.example`、后端示例 YAML、README 和资产测试。
- `POST /api/v1/me/assets` 的响应结构保持不变，但 `upload_url` 的主机与协议改为公共上传端点；不修改数据库结构、前端上传协议或匿名图片读取契约。
