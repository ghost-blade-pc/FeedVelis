# 配置参考

配置优先级为内置默认值 < YAML < `VELIS_*` 环境变量，启动时校验。入口见 [config.example.yaml](../backend/configs/config.example.yaml)、[.env.example](../.env.example) 和 [config.go](../backend/internal/infrastructure/config/config.go)。

示例仅供本地开发；真实数据库地址、密码、Token 和模型密钥通过环境变量注入，不提交到仓库。宿主机连接配置应与 Compose 中 PostgreSQL 的实际账户和数据库匹配。

常用环境变量除资产、幂等与 Feed 配置外，还包括 `VELIS_RABBITMQ_URL`、`VELIS_RELAY_*`、`VELIS_CONSUMER_*`、`VELIS_OUTBOX_*` 和 `VELIS_WORKER_METRICS_ADDRESS`。RabbitMQ URL 留空时只禁用 Relay/Consumer，文章事务仍写 Outbox。完整上限及默认值见示例 YAML；对象存储凭据、MQ 凭据和含凭据的代理 URL 不得提交或写入日志。

认证相关环境变量单独见 [authentication.md](authentication.md)。

## 抓取出网与代理

抓取器默认忽略 `HTTP_PROXY`、`HTTPS_PROXY` 与 `ALL_PROXY`，直连时会校验每次 DNS 结果、实际连接、重定向、协议和端口。只有 `VELIS_FEED_PROXY_URL` 会启用专用可信出口代理；此时最终 DNS/IP 安全边界委托给代理，应用无法声称仍能验证最终目标 IP。代理地址可以含凭据，但日志只记录脱敏模式与主机。

## 对象存储端点

MinIO Bucket 必须私有；匿名图片只能经 API 实时核对“当前公开修订引用”后流式读取。预签名 URL 仅用于 15 分钟直传，不能作为公开读取契约。对象存储的三个地址概念不可混用：`assets.endpoint`/`VELIS_ASSET_ENDPOINT` 是 API 与 Worker 使用的内部 `host:port`，`assets.upload_endpoint`/`VELIS_ASSET_UPLOAD_ENDPOINT` 是浏览器可达的完整 HTTP(S) origin，`assets.web_origin`/`VELIS_ASSET_WEB_ORIGIN` 是 Bucket CORS 允许发起上传的精确前端来源。本地 Compose 分别使用 `minio:9000`、`http://localhost:9000` 和 `http://localhost:5173`。

启用对象存储后公共上传端点为必填项，这是一次有意的配置兼容性变更：已有部署升级前必须补充该字段，系统不会回退到内部 DNS 名称，也不会从请求 Host/Origin 推导。生产应使用客户端可解析、可路由且证书有效的 HTTPS 资产入口；反向代理必须原样保留签名时使用的 Host，否则 AWS Signature V4 校验会失败。公共端点不得包含用户信息、查询、片段或路径前缀。

## AI 内容增强

generation 与 Embedding 是两个独立的 OpenAI-compatible profile。某组的 provider/base URL/API Key/model 任一被填写时，其余项必须完整；两组均为空时不装配 AI Worker，也不影响 readiness。API Key 只通过 `VELIS_AI_GENERATION_API_KEY`、`VELIS_AI_EMBEDDING_API_KEY` 注入。

Compose 用户可直接在本地 `.env` 中按 [.env.example](../.env.example) 的 AI 区块取消注释并填写；这些参数只传给 `velis-worker`，无需也不应把真实密钥写入 YAML。Embedding 未配置时仍会生成并公开摘要、关键词和主题。

| 配置 | 默认值 | 硬边界/说明 |
| --- | --- | --- |
| generation timeout / stage budget | 30s / 2m | 单次 1s-5m；阶段预算不小于单次且不超过 15m |
| structured output mode | prompt | `prompt\|json_object\|json_schema`；只在 Provider 明确兼容时启用后两者 |
| output token 参数名 | max_tokens | `max_tokens\|max_completion_tokens`；Provider 忽略参数名时输出上限静默失效，需按实测选择 |
| single input / chunk chars | 12000 / 6000 | 1000-100000 / 500-single input，按 Unicode 字符计 |
| Map summary / repair input chars | 800 / 16000 | 64-4000 / 1000-100000，防止中间与纠正输入无界 |
| max chunks / concurrency / calls | 8 / 2 / 9 | 1-64 / 1-8 / 1-65，调用数至少覆盖 map + reduce |
| generation attempts / backoff | 3 / 5s-5m | 尝试 1-5；退避 1s-1h 且有界抖动 |
| output / audit Token | 3072 / 20000 | 输出 64-8192；审计预算不小于输出且最多 1000000 |
| summary / keyword / topic / label | 1000 / 12 / 5 / 64 字符 | summary 最多 4000；关键词 1-12、主题 1-5、标签最多 128 |
| Embedding timeout / stage budget | 20s / 30s | dimensions 启用时必填且为 1-65536；`request_dimensions=false` 时只校验、不发送参数 |
| Embedding input / attempts / audit Token | 12000 / 3 / 10000 | 输入 1000-100000；尝试 1-5；审计预算最多 1000000 |
| Worker lease / poll / batch | 2m / 1s / 8 | lease 10s-10m；poll 100ms-1m；batch 1-100 |

## 搜索投影

OpenSearch 是可选依赖。`search.endpoints` 为空时投影 Worker 不装配、API 搜索返回 `503 SEARCH_UNAVAILABLE`：文章发布、RSS 抓取、latest、详情与 AI 增强全部照常工作，只是 PostgreSQL 里保留一份待处理槽位。非开发环境必须使用 HTTPS 并显式提供 `search.username` 与 `search.password`；凭据注入 API 与 Worker，日志只记录脱敏后的协议与主机。

Compose 已内置固定 `opensearchproject/opensearch:3.8.0` 单节点服务，默认把 API 与 Worker 指向它。把 `VELIS_SEARCH_ENDPOINTS` 显式写成空值即可关闭投影与查询而不影响其它组件。

| 配置 | 默认值 | 硬边界/说明 |
| --- | --- | --- |
| endpoints | 空（未配置） | 最多 8 个；不得携带凭据、查询或路径 |
| index_prefix | velis-articles | 2-64 位小写标识；读写别名由它派生为 `-read` / `-write` |
| schema_version | 1 | 1-100；与映射、分析器、维度和投影编码共同构成 schema 身份 |
| embedding_dimensions | 1024 | 1-65536；启用 Embedding profile 时必须与 `ai.embedding.dimensions` 一致 |
| connect / request timeout | 5s / 30s | 1s-1m / 1s-5m |
| query timeout / PIT keep-alive | 5s / 2m | 100ms-30s / 30s-10m；查询超时独立于投影写入超时 |
| candidate batch / request maximum | 100 / 500 | 单批 1-500；单请求最多检查 1-5000 个候选且不得小于单批 |
| bulk_max_items / bytes / document chars | 500 / 5 MiB / 65536 | 1-10000 / 1KiB-64MiB / 1000-4MiB；超限文档单独永久失败 |
| worker lease / poll / batch | 2m / 1s / 20 | lease 10s-10m 且必须大于 request timeout；poll 100ms-1m；batch 1-500 |
| worker attempts / backoff | 5 / 2s-5m | 尝试 1-20；退避 1s-1h |
| rebuild snapshot batch / rollback window / sample | 500 / 24h / 200 | 批 1-10000；窗口 1m-30d；抽样 1-10000 |

搜索游标签名键只能通过 `VELIS_SEARCH_CURSOR_KEY` 注入，值为 Base64，解码后至少 32 字节；YAML 中的同名字段会被忽略。development/test 未设置时会为当前进程生成临时键，重启后旧游标失效；production 未设置时搜索局部禁用。多副本部署必须为所有 API 实例配置同一个持久密钥，否则游标跨实例不可用。

BM25 查询固定使用读别名和可见文档，标题、关键词、主题、摘要、正文的权重依次降低；`keyword`、`topic`、`source_id` 是精确筛选。分页通过 PIT 与 `search_after` 保持快照，PIT 过期或游标无效时返回受控错误，客户端应从第一页重试。OpenSearch 候选始终由 PostgreSQL 批量复核当前公开状态、当前修订和当前 AI 选择后才返回，因此索引延迟或迟到文档不会泄露已下架内容。查询身份只需读别名上的搜索与 PIT 权限，不应授予索引写入或管理权限；开启混合搜索时还需要读取实际读索引 mapping 元数据的权限；无需授予索引写入或别名切换权限。

### 混合搜索开关与回滚

混合搜索默认关闭（`search.query.hybrid.enabled=false` / `VELIS_SEARCH_QUERY_HYBRID_ENABLED=false`）。开启前保留 v1 索引，设置 `search.schema_version=2`，通过已有的 `search rebuild start` / `search rebuild cutover` 流程在线重建并切换 v2；不要原地修改 v1 mapping。v2 的 `embedding_profile_version` 来自实际 Embedding 结果，不会把同维度旧 profile 标为当前 profile。API 与 Worker 注入相同的 `VELIS_AI_EMBEDDING_PROVIDER/BASE_URL/API_KEY/MODEL/PROFILE_VERSION/DIMENSIONS/REQUEST_DIMENSIONS`；API 只需 Embedding 配置，不依赖 generation。

首查默认每路召回 100 条（可配 1-100），KNN 的 k 与其上限一致，等权 RRF 常数固定为 60；无向量文章仍可文本命中。整个搜索默认 5 秒，Embedding/KNN 各默认 1 秒（可配 100ms-2s，且小于总预算），模型只调用一次、不重试、不写结果表。模型未配置、v1 读索引、无有效向量或语义失败时保留正常 BM25 排序。混合开关开启后包括 BM25 降级都冻结有限候选，最多两路 100 条的去重并集（最多 200 条）；不能遍历全部匹配。游标为 AES-256-GCM 认证加密 v2，使用独立 HKDF 派生键，绝对 TTL 2 分钟；续页零模型/KNN 调用，当前下架或语义身份变化会跳过候选，纯文本项返回当前公开修订。最大 16 KiB 游标需要部署入口支持，访问日志不得记录 URL 查询串。旧 v1 BM25 游标继续原 PIT 计划。

回滚先关闭混合开关，让客户端对失效 v2 游标从第一页重查；需要时用既有 `search rebuild rollback` 切回保留的 v1 索引，继续 BM25。关闭开关恢复原有 BM25 深分页；不删除旧索引、不重算向量，不涉及数据库降级迁移。查询计划或游标键变化也要求重新查询。

搜索采用独立的局部降级边界：OpenSearch 正常且游标键有效时 `/api/v1/search/articles` 可用；端点未配置、production 缺少游标键或 OpenSearch 查询失败时，仅该接口返回带 `Retry-After` 的 `503 SEARCH_UNAVAILABLE`。latest、详情、投稿、抓取及 `/readyz` 不把搜索作为核心依赖；PostgreSQL 复核失败时搜索同样不返回部分结果。

## Redis 读取缓存

可选 Redis 读取缓存覆盖三类可重建对象：版本化卡片片段、latest ID 候选页、用户隔离的推荐首查排序计划。文章写入提交后尽力失效首页；推荐 latest 补位使用保留本人排除、冻结候选 skip 和首查 StartedAt 的专用 ID 查询，再共享卡片装配。没有数据库迁移或 HTTP 字段变化。

每批先在短 `READ ONLY REPEATABLE READ` 快照读取当前公开状态、修订、AI 选择及实际 Embedding/profile，再批量读缓存，只批量加载缺失片段，结束快照后回填。来源、昵称和排序字段总是来自当前数据库；编辑、AI 切换及下架无需等待卡片过期。写事务中的读取复用事务视图并绕过公开缓存；PostgreSQL 失败仍返回原错误，文章详情直接读取 PostgreSQL。搜索排序及语义身份过滤保持原行为，Redis 故障只导致有界回源。

latest ID 候选正常情况下约 5 秒收敛，命中不续期。发布、恢复、下架、删除及 RSS 写入在最外层提交后失效所有合法 limit 的首页；回滚不执行，故障不改变已提交结果，续页靠绝对 TTL 收敛。每次 latest 最多检查 500 个候选，水位包含已检查的下架项，但不消费有效 lookahead。达到上限可能返回短页或空页且 `has_more=true`；客户端应使用 `next_cursor` 继续，不能根据 `items` 数量判断结束；Web 空页也保留“加载更多”。该窗口不承诺自动刷新已显示页面，也不提供跨请求数据库快照。

默认 `cache.enabled=false`，宿主机启用时设置 `VELIS_CACHE_ENABLED=true` 和 `VELIS_CACHE_REDIS_ADDRESS=127.0.0.1:6379`。Compose 开发示例显式启用，地址为 `redis:6379`；设置 `VELIS_CACHE_ENABLED=false` 即可关闭，不需要删除 Redis 键或业务数据。配置优先级为默认 < YAML < `VELIS_*` 环境变量，API/Worker 必须使用相同 namespace；空 namespace 按环境派生 `velis:<app.environment>`。Redis 运行时不可达不会阻止启动，readiness 仍只依赖 PostgreSQL。

| 配置 / 环境变量后缀（统一加 `VELIS_CACHE_`） | 默认 | 启用时约束 |
| --- | --- | --- |
| `enabled` / `ENABLED` | false | 布尔值 |
| `redis.address` / `REDIS_ADDRESS` | 需配置 | host:port，不能嵌入凭据 |
| `redis.database` / `REDIS_DATABASE` | 0 | 非负 |
| `redis.tls` / `REDIS_TLS` | false | TLS 最低 1.2，验证服务端证书 |
| `namespace` / `NAMESPACE` | `velis:<environment>` | 1-96 位字母、数字、冒号、下划线、连字符 |
| `card_ttl` / `CARD_TTL` | 5m | 1s-1h |
| `latest_ttl` / `LATEST_TTL` | 5s | 1s-5s |
| `recommend_ttl` / `RECOMMEND_TTL` | 30s | 1s-30s |
| `operation_timeout` / `OPERATION_TIMEOUT` | 50ms | 5ms-100ms，不能大于总预算 |
| `request_budget` / `REQUEST_BUDGET` | 100ms | 10ms-200ms |
| `pool_size` / `POOL_SIZE` | 10 | 1-100 |
| `batch_size` / `BATCH_SIZE` | 100 | 1-100 |

凭据仅由 `VELIS_CACHE_REDIS_USERNAME`、`VELIS_CACHE_REDIS_PASSWORD` 注入，YAML 中的凭据字段不会生效。适配器关闭自动命令/连接重试，缓存操作共享请求剩余预算且受父 deadline 限制，传输失败后本请求直接绕过；回填/失效失败只记录受控分类。卡片最多 64KiB，计划最多 128KiB；绝对到期时间从原始读取开始计算，命中不续期。指标单位及接入约定见[缓存可观测性](../backend/test/integration/cache_observability.md)，基线及本阶段验收见[缓存验证记录](../openspec/changes/archive/2026-10-10-cache-article-and-feed-reads-with-redis/verification.md)。

## 推荐 Feed

`GET /api/v1/articles/recommend` 默认每页 20 条（1-50），匿名及没有有效正向关键词/主题画像的用户按 PostgreSQL latest 冷启动，返回 `mode=cold_start`、`degraded=false`。登录用户的当前修订关键词/主题、去重阅读和收藏影响排序；有效“不感兴趣”仅硬排除对应文章。每路最多召回 100 条，去重后冻结最多 200 条；候选不足用 latest 补位，OpenSearch 整体故障时 `mode=latest_fallback`。响应的 `degraded` 标记降级，文章只返回 `keyword_match`、`topic_match`、`similar_content`、`recent`、`latest_fallback` 之一作为原因。Web 默认 latest，可切换推荐并在游标失效时从第一页重试。

| 配置 | 默认值 | 硬边界/说明 |
| --- | --- | --- |
| `recommend.first_query_timeout` | 5s | 500ms-30s；仅限制推荐首查 |
| `recommend.bm25_candidates` / `knn_candidates` | 各 100 | 各 1-100；最多冻结 200 条 |
| `recommend.cursor_ttl` | 2m | 30s-10m；绝对到期，不因翻页续期 |
| `VELIS_RECOMMEND_CURSOR_KEY` | 开发进程临时生成 | 独立于搜索密钥，仅从环境读取；Base64 解码后至少 32 字节；production 必填且各 API 实例相同 |

推荐游标是独立的认证加密 v1 格式，绑定匿名/登录身份与排序版本；续页不重新召回或生成向量，但仍复核公开状态和本人有效负反馈。启用 Redis 后，首查排序计划使用服务端认证的真实用户 ID、规范化当前画像指纹及排序/召回/Embedding 配置指纹隔离，默认绝对 TTL 30 秒，命中不续期。窗口只冻结候选排序；每次首查重新建立时间和消费状态，每页仍复核当前公开状态、语义身份和本人有效排除。硬排除前原候选集合的签名变化会重算，包含无标签文章负反馈撤销及自然过期。匿名/无正向画像不使用计划缓存，latest_fallback 和依赖失败不写入计划；搜索恢复后，曾故障回退的新首查可重新召回。有效计划中的真实语义降级标志可保留至该 30 秒窗口结束。Redis 故障不单独改变 mode/degraded，清缓存不影响有效客户端游标的续页。当前身份和卡片使用共享读取缓存；PostgreSQL 读取失败返回 `503 DEPENDENCY_UNAVAILABLE`，不会直接返回搜索投影。新的候选进入新首查时仍受约 30 秒排序窗口约束；已有 latest 补位按首查站内发布时间水位及键集继续。

## Agent 会话与内部工具

Agent默认关闭，必须同时开启auth才注册路由。配置优先级仍为默认值 < YAML < 环境变量，专用游标密钥只接受环境注入。完整行为及启用示例见 [会话操作](agent-conversations.md) 和 [工具契约](agent-tools.md)。

| YAML | 默认值 | 范围 | 环境变量 |
| --- | --- | --- | --- |
| agent.enabled | false | bool | VELIS_AGENT_ENABLED |
| agent.max_conversations_per_user | 50 | 1–500 | VELIS_AGENT_MAX_CONVERSATIONS_PER_USER |
| agent.max_messages_per_conversation | 200 | 1–2000 | VELIS_AGENT_MAX_MESSAGES_PER_CONVERSATION |
| agent.max_message_chars | 4000 | 1–16000 code point | VELIS_AGENT_MAX_MESSAGE_CHARS |
| agent.cursor_ttl | 1h | 1m–24h绝对期限 | VELIS_AGENT_CURSOR_TTL |
| agent.tools.timeout | 5s | 100ms–10s | VELIS_AGENT_TOOL_TIMEOUT |
| agent.tools.max_output_bytes | 131072 | 65536–1048576字节 | VELIS_AGENT_TOOL_MAX_OUTPUT_BYTES |
| 不接受YAML密钥 | 无 | Base64解码后恰好32字节 | VELIS_AGENT_CURSOR_KEY |

固定24h幂等、标题100字符、分页20/50、工具结果5/10、正文工具4000/8000和PIT清理250ms不另增配置。非开发API启用会话且缺密钥时启动失败；开发API可生成进程临时密钥，重启后旧游标失效。其他进程不新增密钥要求，也不新增readiness依赖。降低配额不删除存量，允许读取、删除和原成功重放。
