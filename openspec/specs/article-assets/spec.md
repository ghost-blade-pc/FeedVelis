# article-assets Specification

## Purpose

定义用户投稿图片从私有对象上传、确认、文章版本引用到匿名受控读取和清理的完整生命周期，同时确保资产故障不会扩大为内容主链路故障。

## Requirements

### Requirement: 图片通过私有对象存储两阶段上传

系统 SHALL 先创建归属于当前用户的 `pending` 资产，并返回使用显式公共上传端点签发、可由 Web 客户端访问的短期预签名上传信息，再由确认接口通过内部对象存储端点验证实际对象后转为 `ready`；公共上传端点必须作为签名内容的一部分且不得从客户端可控的请求 Host 或 Origin 推导，Bucket 和对象键不得公开成为匿名读取契约。

#### Scenario: 创建上传资产
- **WHEN** 登录用户在配额内创建图片资产
- **THEN** 系统 SHALL 返回资产 ID、固定对象键所需的预签名上传信息和 15 分钟有效期，且对象键形如 `article-assets/{user_id}/{asset_id}/original`
- **AND** `upload_url` 的 scheme 和 authority SHALL 与配置的公共上传端点一致，不得暴露只在服务端网络内可达的内部端点

#### Scenario: 内外网络端点不同
- **WHEN** 服务端通过内部端点访问对象存储且 Web 客户端通过不同的公共端点上传
- **THEN** Web 客户端 SHALL 能以返回的预签名信息完成 PUT，确认接口 SHALL 继续通过内部端点验证同一对象

#### Scenario: 公共上传端点配置缺失或无效
- **WHEN** 对象存储已启用但公共上传端点缺失，或不是不含用户信息、查询、片段及额外路径的完整 HTTP(S) URL
- **THEN** 系统 SHALL 拒绝该配置，不得签发使用内部端点或客户端请求信息替代的上传 URL

#### Scenario: 确认已上传对象
- **WHEN** 资产所有者确认一个实际存在且满足策略的上传对象
- **THEN** 系统 SHALL 以对象存储中的实际元数据和文件签名为准，将资产置为 `ready` 并保存可信类型、大小、尺寸和校验信息

#### Scenario: 确认不存在或不合规对象
- **WHEN** 对象不存在、超限、类型不符或图片签名无效
- **THEN** 系统 SHALL 拒绝确认且资产不得成为可引用状态

### Requirement: 图片格式与资源额度受限

系统 SHALL 只接受 JPEG、PNG、WebP；单文件最大 10 MiB、宽高各不超过 8192、总像素不超过 4000 万，每篇文章最多 20 张且引用总大小不超过 50 MiB，每位用户默认总额度为 1 GiB并最多同时拥有 20 个 `pending` 资产。

#### Scenario: 文件满足限制
- **WHEN** 上传图片的签名、类型、大小、尺寸、像素和用户额度均满足限制
- **THEN** 系统 SHALL 允许确认并计入用户资产额度

#### Scenario: 任一限制被突破
- **WHEN** 文件或文章引用超过任一格式、大小、尺寸、像素、数量或额度限制
- **THEN** 系统 SHALL 返回明确校验或额度错误，且不得发布引用无效资产的文章

#### Scenario: 原始元数据被保留
- **WHEN** 合规图片包含 EXIF 或其他原始元数据
- **THEN** I2 SHALL 不重新编码或剥离元数据，Web SHALL 提示该隐私边界

### Requirement: 资产归属和内容版本引用不可绕过

文章保存或发布 SHALL 只接受作者本人拥有且为 `ready` 的 `asset:<uuid>`；资产首次引用时绑定一篇文章，可被该文章后续修订复用但不得跨文章复用，引用关系 SHALL 绑定具体内容修订。

#### Scenario: 首次绑定资产
- **WHEN** 作者保存或发布引用本人未绑定 `ready` 资产的文章修订
- **THEN** 系统 SHALL 在文章事务中将资产绑定该文章并建立修订引用

#### Scenario: 跨文章复用资产
- **WHEN** 作者尝试在另一篇文章引用已绑定资产
- **THEN** 系统 SHALL 拒绝请求且不得创建不完整修订

#### Scenario: 引用其他用户或未确认资产
- **WHEN** 投稿引用其他用户资产、`pending` 资产或已删除资产
- **THEN** 系统 SHALL 拒绝保存或发布并不得泄露该资产的私有元数据

### Requirement: 匿名资产读取由 API 实时授权

系统 SHALL 通过 API 对私有 MinIO 对象进行流式代理；匿名 `GET`/`HEAD` 仅在资产被某篇文章的当前 `published` 修订引用时成功，草稿、下架和删除内容不得授权匿名读取。

#### Scenario: 读取公开文章图片
- **WHEN** 匿名请求当前已发布修订引用的有效资产
- **THEN** API SHALL 流式返回对象、可信 `Content-Type`、ETag、`X-Content-Type-Options: nosniff` 和安全的 `Content-Disposition`，且不得把完整对象缓冲进内存

#### Scenario: 文章变为不可见
- **WHEN** 引用资产的文章被下架或删除
- **THEN** 后续匿名资产请求 SHALL 立即被拒绝，即使私有对象尚未物理清理

#### Scenario: 作者预览草稿图片
- **WHEN** 资产所有者携带有效身份预览自己的草稿所引用图片
- **THEN** 系统 SHALL 允许读取而不扩大匿名可见性

### Requirement: 资产生命周期可清理且不破坏有效引用

系统 SHALL 清理超过 24 小时的 `pending` 对象和超过 7 天仍未绑定文章的 `ready` 对象；下架不得删除资产，文章软删除 SHALL 立即撤销读取并由可重试的本地任务删除不再被保留引用需要的对象。

#### Scenario: 清理孤儿上传
- **WHEN** 清理任务发现过期待确认或长期未绑定的资产
- **THEN** 系统 SHALL 幂等删除对象并记录资产删除状态，删除失败 SHALL 可在后续周期重试

#### Scenario: 下架文章仍保留资产
- **WHEN** 作者或管理员下架文章
- **THEN** 系统 SHALL 保留其当前及历史修订资产，不得把下架误当成删除

#### Scenario: 软删除文章
- **WHEN** 文章被软删除
- **THEN** 数据库权限 SHALL 立即阻止资产公开读取，对象删除 SHALL 与文章事务解耦并可重试

### Requirement: 资产写入遵循统一幂等协议

资产创建和确认 SHALL 要求 `Idempotency-Key`，成功结果默认保存 24 小时；同一规范化请求 SHALL 重放同一资产业务结果，不得重复计费或创建对象身份。

#### Scenario: 重试创建资产
- **WHEN** 用户以相同幂等键重试相同资产创建请求
- **THEN** 系统 SHALL 返回同一资产 ID，并可为仍处于 `pending` 的资产生成新的有效上传凭证而不创建第二条资产记录

#### Scenario: 重试确认资产
- **WHEN** 用户以相同幂等键重试已成功确认的同一资产
- **THEN** 系统 SHALL 返回首次确认结果且不得重复增加额度

### Requirement: MinIO 是局部可降级依赖

对象存储不可用 SHALL 只影响资产创建、确认和图片字节读取，不得阻断 RSS、latest、文章 HTML、Source 管理或不含图片的用户投稿；全局 readiness 不得仅因 MinIO 故障失败。

#### Scenario: MinIO 故障时读取文章
- **WHEN** MinIO 不可用但 PostgreSQL 可用
- **THEN** latest 和文章详情 SHALL 正常返回，图片端点 SHALL 返回 `503 ASSET_UNAVAILABLE`

#### Scenario: MinIO 故障时发布纯文本
- **WHEN** 用户发布不引用图片的有效稿件
- **THEN** 发布 SHALL 成功且不得尝试把 MinIO 作为事务硬依赖
