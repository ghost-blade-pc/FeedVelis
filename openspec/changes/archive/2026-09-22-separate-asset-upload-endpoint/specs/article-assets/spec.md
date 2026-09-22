# Spec Delta

## MODIFIED Requirements

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
