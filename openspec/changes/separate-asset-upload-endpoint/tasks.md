# Tasks

## 1. 配置契约

- [x] 1.1 在资产配置中加入 `upload_endpoint`/`VELIS_ASSET_UPLOAD_ENDPOINT`，实现启用时必填及 HTTP(S) 完整 origin 校验，并通过配置默认值、YAML、环境变量优先级和非法 URL 单元测试验证。
- [x] 1.2 将内部 endpoint 和公共上传 endpoint 传入对象存储装配，同时验证未启用资产时仍可局部降级、启用但缺少公共 endpoint 时配置加载明确失败。

## 2. 双端点预签名

- [x] 2.1 扩展 MinIO 客户端构造以支持显式 region，并通过单元测试验证设置 region 后生成预签名 URL 不需要访问签名客户端的公共 endpoint。
- [x] 2.2 重构对象存储适配器：对象操作继续使用内部客户端，预签名通过内部客户端解析并缓存 region、再以公共 endpoint 离线签名；用不同 authority 的测试验证 URL 使用公共 Host、签名后未做字符串改写且错误不泄露签名 URL。
- [x] 2.3 更新现有 Store、API E2E 和测试夹具的构造参数，并运行对象存储及资产应用/接口测试，确认确认、探测、读取、删除、幂等重放和局部降级行为未回归。

## 3. 部署与契约说明

- [x] 3.1 更新 Compose 和 `.env.example`，为容器内操作配置 `minio:9000`、为宿主机浏览器上传配置 `http://localhost:9000`，并用 `docker compose config` 验证最终环境展开正确。
- [x] 3.2 更新后端示例 YAML、OpenAPI 的 `upload_url` 描述和 README，说明内部 endpoint、公共上传 endpoint、Web Origin、生产 HTTPS/Host 保留要求及升级兼容性，并核对示例不包含真实凭据。

## 4. 跨网络回归验证

- [x] 4.1 扩充真实 MinIO 测试，使用不同的内部与公共 endpoint 完成预签名 PUT 后再经内部客户端 Stat/Probe，并保留允许/拒绝 Origin 的 CORS 断言；未配置外部依赖时必须明确跳过。
- [x] 4.2 扩充可复现 HTTP 闭环，从宿主机调用 Compose API 创建资产、使用返回的 `localhost` URL 上传并确认，断言响应不含 `minio:9000` 且图片可由授权 API 读取。
- [x] 4.3 运行 `make check`、`cd backend && GOCACHE=/tmp/feedvelis-go-cache go vet ./...` 以及启用 PostgreSQL + MinIO 的相关集成测试，并记录通过结果与任何明确跳过项。
