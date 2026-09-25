# Design

## Context

文章、不可变修订、公开状态和 AI 当前选择均以 PostgreSQL 为事实源。现有文章写事务会同时写 Outbox，但 RabbitMQ 可以未配置；AI generation 与 Embedding 在独立租约任务中分阶段保存，当前选择切换已由 revision、task generation 和 lease token fencing。当前没有 OpenSearch 客户端、索引、搜索投影任务或重建控制面，`infrastructure/cache/redis`、`application/recommendation` 等目录只有占位说明。

本设计遵守 `specs/article-search-projection/spec.md` 以及对 `unified-articles`、`ai-content-enrichment` 的 delta。搜索索引是可丢弃派生状态，不能成为文章可见性或 AI 结果的事实源；API/RSS/AI 事务只能写 PostgreSQL，不能同步等待 OpenSearch。

## Goals / Non-Goals

**Goals:**

- 让文章变化和 AI 当前选择变化通过每文章唯一、可合并的持久化槽位可靠推进投影。
- 用单调版本、执行前复核、租约和持久 tombstone 同时抵御重复、乱序及迟到外部写。
- 在 Bulk 部分失败时以 `(article, generation, physical index)` 粒度恢复，而不是重跑整个批次。
- 支持不中断当前读索引的全量重建、持续双写、校验、别名切换和有限窗口回滚。
- 为后续 BM25、KNN 和 RRF 固定文档边界，但本 change 不开放查询能力。

**Non-Goals:**

- 不设计搜索 API、相关性评分、查询 Embedding、RRF 或推荐公式。
- 不使用 OpenSearch ingest pipeline、Data Prepper、CDC 或 PostgreSQL 逻辑复制。
- 不把现有 `async_tasks` 扩展成第三个 stage；AI 执行与搜索投影有不同故障边界和重建生命周期。
- 不清理遗留 pgvector 扩展，不在 PostgreSQL 执行向量相似度查询。
- 不提供多集群复制、跨地域切换或自动删除旧物理索引。

## Decisions

### 1. 固定 OpenSearch 3.8.0，使用内置 CJK analyzer

Compose 固定 `opensearchproject/opensearch:3.8.0`，不使用 `latest`；Go 适配器位于 Infrastructure，并固定与 OpenSearch 3.x 兼容的官方客户端版本。开发环境以单节点运行、禁用演示安全配置，只发布到本机端口；非开发环境必须使用 HTTPS 和显式认证配置，凭据只进入 Worker，不进入日志。

首版全文字段使用 OpenSearch 内置 `cjk` analyzer，英文/数字标识另保留 keyword 或 standard 子字段。内置 analyzer 能覆盖中英文混排的基础检索需求，且不需要为 Smart Chinese 或 IK 插件维护与服务版本严格匹配的自定义镜像。I4.2 应以固定中文、英文、URL 和专有名词样例评估 BM25 质量；若需更换分析器，递增 schema version 并重建，不原地改变旧索引语义。

备选方案是 OpenSearch 2.19 LTS 风格分支或带第三方 IK 插件的镜像。前者会在新阶段一开始引入旧主版本，后者增加供应链、镜像构建和升级耦合；本项目当前没有旧索引兼容负担，因此选择当前固定 3.x 版本和内置分析器。

### 2. 版本化物理索引与稳定别名

逻辑资源命名如下：

- 物理索引：`velis-articles-v<schema>-<build-id>`，其中 build ID 为 UTC 时间加随机后缀；
- 读别名：`velis-articles-read`；
- 写别名：`velis-articles-write`，始终只有一个 `is_write_index=true`；
- schema identity：至少包含 mapping version、分析器版本、向量维度和投影编码版本。

索引模板由仓库中的版本化 JSON 资产管理，使用 `dynamic: strict`。核心字段分为：

- 身份与 fencing：`article_id`、`projection_generation`、`lock_version`、`revision_id`、可空的 generation/embedding result ID、`schema_version`；
- 可见性：`visible` 与不可见原因；
- 过滤/展示身份：origin type、source ID、author stable ID、有效发布时间；
- 文本：标题、纯文本、原始 excerpt、AI summary、keywords、topics、source title；
- 向量：可空 `knn_vector`，dimension 固定为配置的 Embedding dimension。

索引文档不保存 Markdown、清洗 HTML、模型调用审计或内部错误。后续查询必须固定过滤 `visible=true`。管理命令在初始化或重建前通过 mapping/settings 读取验证实际 schema identity，不能只信任索引名称。

### 3. `search_projection_jobs` 是每文章唯一的目标槽位

新增 migration 创建：

1. `search_projection_jobs`
   - `article_id` 主键；
   - 当前完整目标：`action(upsert|tombstone)`、article lock version、revision ID、可空 generation/embedding result ID；
   - 单调 `generation` 与全局单调 `change_seq`；
   - `pending|running|retry_wait|succeeded|failed` 状态、attempt、下次时间和限长错误；
   - lease owner/token/expiry、创建/目标变化/完成时间。
2. `search_projection_deliveries`
   - 主键 `(article_id, physical_index)`；
   - 当前要求的 job generation、状态、attempt、退避和最近结果；
   - 外键关联 job，并限制为当前服务索引、活动重建索引或回滚索引。
3. `search_index_rebuilds`
   - 一次仅一个活动记录；保存目标 schema/index、阶段、文章扫描水位、启动 change sequence、增量追赶水位、校验报告、当前/前一索引、切换与回滚截止时间。

`change_seq` 使用 PostgreSQL sequence 在目标真正变化时分配；相同目标重复推进不取新序列、不增加 generation。它用于重建增量范围，不能用 `updated_at` 代替，因为数据库时间可能相同，任务完成也会修改更新时间。

每次目标变化会使该 job 的所有活动 delivery 指向新 generation 并进入 pending。一个 job 只有在全部活动 delivery 达到该 generation 后才成功。这样既保留“每文章一个收敛槽位”，又能在重建双写时精确记住哪个物理索引失败，重试时只选择未完成 delivery。

备选方案是复用 `async_tasks`、为每次变化追加队列行，或仅依赖内存 Bulk 重试。复用会耦合 AI profile 状态机；追加日志会无界增长且仍需压缩；只用内存无法跨进程崩溃保留部分成功证据。

### 4. 文章与 AI 事务通过同一 PostgreSQL projector 推进目标

Application 定义不含 SQL 或 OpenSearch 类型的 `SearchProjectionTargetStore` 端口。PostgreSQL 实现提供“从已锁定的当前事实构造并 upsert 目标”的操作，调用者不自行拼目标版本：

- 文章创建、发布、公开修订、恢复、下架和删除事务，在保存文章、Outbox 与幂等结果的同一事务内调用；
- 草稿或离线编辑维持 tombstone 目标，不产生公开 upsert；
- `SaveGeneration` 和 `SaveEmbedding` 只有在 current selection 实际成功切换时，才在同一 fenced 事务内调用；
- RSS 和用户投稿共享同一 repository helper，避免遗漏某一种来源路径。

如果 OpenSearch 未配置，槽位仍照常推进。这样 RabbitMQ 中断不会令搜索目标永久缺失，也不需要创建新的跨服务集成事件。OpenSearch 写入完全由独立 Worker 在事务提交后执行。

### 5. Worker 总是在执行前重读当前投影

Worker 以 `FOR UPDATE SKIP LOCKED` 有界认领有待处理 delivery 的 job，提交短事务后，批量读取这些文章的当前投影：

1. 对每篇文章核对 job generation 和完整目标身份；
2. 若数据库当前事实已不同，使用短事务推进同一槽位并丢弃已构造的旧操作；
3. 若公开，生成严格映射的 upsert 文档；若不公开，生成 `visible=false` 的最小 tombstone 文档；
4. 按 physical index 和请求体字节上限切分 Bulk；
5. 按 item 更新对应 delivery，最后派生 job 总状态。

外部调用期间不持有行锁。完成与失败更新必须同时匹配 article ID、job generation、lease token、physical index 和 delivery generation。任务超过最大自动尝试后进入 failed，但后续目标变化会增加 generation 并重新激活；管理 CLI 也提供有界的失败任务重试。

### 6. 使用外部版本和持久 tombstone 防止迟到复活

每次 OpenSearch index 操作以 job `generation` 作为单文章外部版本，使用 `external_gte` 语义。同 generation 的未知结果可以安全重放，更低 generation 会被拒绝。

下架和删除不立即调用物理 `_delete`，而是写入只含身份、版本和 `visible=false` 的 tombstone。原因是 OpenSearch 对已物理删除文档的版本墓碑有有限保留期；若墓碑被回收，极迟到的旧 create 可能重新建立文档。持久 tombstone 加上 Worker 执行前 PostgreSQL 复核，能在整个物理索引生命周期内拒绝旧版本并保证未来查询过滤不可见内容。

全量重建不会把不可见文章写入候选索引，但增量追赶中出现的下架仍写 tombstone。旧物理索引只在退出回滚窗口、没有活动 delivery 且管理员明确指定后才物理清理。因此“移除”在服务语义上是不可检索，而不是要求当前物理索引立即无记录。

备选方案是 OpenSearch `_delete` 加外部版本。它节省少量存储，但不能给本 change 要求的长期迟到防护，故不采用。

### 7. Bulk 分类在适配器边界完成，应用层决定状态

Infrastructure 适配器将每个 Bulk item 映射为统一结果：

- success：`2xx`，以及 tombstone 已达到同版或更高版；
- stale/noop：版本冲突且远端版本不低于请求版本；
- retryable：连接结果未知、`408`、`429`、`502/503/504` 等临时失败；
- permanent：严格映射失败、文档过大、非法请求或不受支持的响应。

响应缺 item、item 数量不符、重复 ID 无法关联或响应 JSON 非法时，整批未能可靠关联的操作按 retryable 处理。应用层只为 retryable delivery 设置指数退避和抖动，只把 permanent delivery 标记失败；其他 delivery 独立完成。请求构造设置最大 item 数、最大正文字符、最大请求字节和总超时，日志只记录分类、index、article/task 身份及限长错误。

### 8. 重建采用快照扫描、持久增量序列和全程双写

CLI 分成 `search index init`、`search rebuild start|resume|status|cutover|rollback|cleanup`。所有可能创建、切换或删除索引的命令要求精确 schema/index 身份；cleanup 额外要求显式确认且拒绝别名引用目标。

重建协议：

1. `start` 创建目标物理索引和 rebuild 记录，记下 `start_change_seq`，然后把候选索引注册为每个变化 job 的第二活动 delivery；从此新目标由正常 Worker 双写当前与候选索引。
2. snapshot 阶段按 `article_id` 升序、固定 batch 从 PostgreSQL 读取当前公开投影并直接写候选索引，持久化最后文章 ID。每个文档仍使用该文章当前 job generation 作为外部版本。
3. catch-up 阶段扫描 `change_seq > start_change_seq` 的 job；因为槽位只保留最新目标，扫描时直接读取当前事实并确保候选 delivery 达到最新 generation。重复扫描直到高水位没有落后 delivery。
4. validate 阶段比较 PostgreSQL 公开数与候选 `visible=true` 数、确认无落后 delivery，并对确定性抽样文章比较投影身份和内容哈希。校验报告持久化。
5. `cutover` 再次确认没有落后 delivery，然后用一次 aliases API 把 read/write 别名从旧索引切至候选。rebuild 状态进入 rollback window，Worker 继续同时写新旧两个索引。
6. rollback window 内，`rollback` 可原子切回旧索引，双写保证其未落后。窗口结束后先停止旧索引 delivery，再确认没有当前租约引用旧索引；cleanup 只能由显式命令删除旧索引。

在候选注册与可能已在途的 Worker 之间存在短暂竞态：注册后，重建必须等待注册前已发放的租约过期或完成，并由 catch-up 扫描覆盖相关 job，才允许进入 validate。切换后继续双写整个回滚窗口，也吸收了切换边界上的迟到完成。

备选方案是暂停写入完成最终追赶，或使用 PostgreSQL 与 OpenSearch 间不可实现的原子提交。暂停会阻塞文章与 AI 事务；双写加持久 delivery 在可用性和复杂度之间更适合当前单集群规模。

### 9. 配置、监督和健康语义

新增 `Search` 配置组，至少包含 endpoints、认证/TLS、index prefix、schema version、embedding dimensions、连接与请求超时、Bulk item/byte 上限、Worker batch/lease/poll、最大尝试与退避、回滚窗口。配置继续遵循默认值 < YAML < `VELIS_*`；URL 日志必须去除 userinfo、query 和 path 中的秘密。

OpenSearch 未配置时 Worker 组件不装配，API readiness 不依赖它。配置完整但 OpenSearch 启动失败时，搜索投影作为可重启的非核心受监管组件反复恢复，不能终止 Feed、AI 或 Outbox 清理组件；这一点与现有 Supervisor 的组件故障隔离方式保持一致。后续搜索 API 自己定义搜索不可用响应，本 change 不改变 `/readyz`。

### 10. 验证分层

- Domain/Application 单元测试：目标相等判断、generation/change sequence、状态转换、租约 fencing、结果分类和重建状态机；
- PostgreSQL 集成测试：文章/AI 原子推进、并发 upsert、重复 noop、认领恢复、delivery 双写和稳定增量扫描；
- OpenSearch 真实依赖测试：模板、CJK analyze、严格映射、向量维度、外部版本、tombstone、Bulk 部分失败桩与别名原子切换；
- 端到端故障测试：OpenSearch 停机/恢复、请求结果未知、Worker 中断、重建期间修订/下架/AI 切换、校验失败、切换及回滚；
- 默认 CI 使用确定性 fake 验证状态机，不把未配置真实 OpenSearch 的 skip 当作通过；独立 Make target 明确要求测试 URL。

## Risks / Trade-offs

- [CJK bigram 对专有名词和分词精度有限] → I4.2 建立固定相关性样例；更换分析器通过新 schema 和重建完成，不污染现有索引。
- [每次文章或 AI 发布事务多写一个 PostgreSQL 槽位] → 只对目标真正变化分配 generation/sequence，使用单行 upsert 和必要索引，不在事务内访问网络。
- [持久 tombstone 占用索引空间] → 以“不泄露和不复活”为优先；物理空间通过创建新索引和安全淘汰旧索引回收。
- [重建双写增加 Bulk 量并使任务完成依赖两个索引] → 限制一次一个活动重建，按 delivery 独立退避；候选长期故障可显式放弃而不影响当前服务索引。
- [向量维度写入 mapping 后不可变] → schema identity 包含维度；Embedding profile 改维度必须创建新索引并重建。
- [OpenSearch 与 PostgreSQL 无分布式事务，任务可能已写成功但回写失败] → 文档 ID 固定、external_gte 幂等、结果未知保留 delivery 重试。
- [单槽位会丢弃中间状态历史] → 中间状态不是业务事实；重建只需要最终当前状态，审计依赖结构化指标与有限诊断而非无限任务日志。

## Migration Plan

1. 新增版本化 migration 创建 jobs、deliveries、rebuilds 和 sequence；down migration 仅在无活动重建且表为空时允许，避免静默丢失诊断状态。
2. 部署理解新表的 API/Worker/CLI，但保持 OpenSearch 默认未配置；验证文章和 AI 写路径只新增本地任务且原有核心路径回归通过。
3. 在 Compose 加入固定 3.8.0 服务和持久卷，配置开发认证边界；运行 `search index init` 建立首个物理索引和别名。
4. 用重建 CLI 将存量公开文章写入首个索引，完成快照、追赶和校验；观察积压、Bulk 分类、索引延迟和文档抽样。
5. 启用持续 Worker，进行发布、修订、generation/Embedding 切换、下架、OpenSearch 中断和恢复演练。
6. 回滚应用时先停用投影 Worker；旧应用忽略新增表，文章主链路继续可用。不得因应用回滚执行 down migration或删除 OpenSearch 索引。
7. 若索引发布失败，保持或切回旧别名并放弃候选；只有显式 cleanup 且目标不被别名与 delivery 引用时才删除候选。
