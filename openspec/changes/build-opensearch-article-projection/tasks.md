# Tasks

## 1. 数据契约与领域状态机

- [x] 1.1 新增版本化 SQL migration，创建 `search_projection_jobs`、`search_projection_deliveries`、`search_index_rebuilds`、全局 change sequence、约束和认领索引，并通过迁移升级/受保护降级集成测试验证不改写既有迁移且活动状态不会被静默删除
- [x] 1.2 在 Domain/Application 定义投影目标、job、delivery、租约、Bulk item 结果与重建阶段模型，覆盖相同目标 noop、目标变化递增 generation、状态转换和错误分类的无数据库单元测试
- [x] 1.3 定义 `SearchProjectionTargetStore`、任务执行、索引管理和重建所需端口，运行架构依赖测试确认 OpenSearch/SQL/SDK 类型没有进入 Domain 或 Application

## 2. PostgreSQL 收敛槽位与事实读取

- [x] 2.1 实现 PostgreSQL 目标推进仓储，以单行锁和完整目标比较完成创建、noop、generation/change sequence 递增及活动 delivery 重置，并用并发集成测试验证每文章唯一和单调性
- [x] 2.2 实现有界 `FOR UPDATE SKIP LOCKED` 认领、租约恢复、按 `(article, generation, token, index)` fenced 完成/重试/永久失败与 job 汇总状态，并用多 Worker、过期租约和迟到回写集成测试验证
- [x] 2.3 实现批量当前投影读取，联合文章当前修订、来源/作者和 AI 当前选择，拒绝跨 revision 或 generation 不匹配的向量，并用公开、无 AI、generation-only、完整向量及不一致选择测试验证
- [x] 2.4 实现按 article ID 的稳定快照分页、按 change sequence 的增量扫描、公开计数和确定性抽样读取，并用重建期间并发修改集成测试验证无遗漏和无重复水位

## 3. 文章与 AI 事务接入

- [x] 3.1 将 RSS 发布/修订路径接入目标推进，使文章事实、Outbox 与 upsert/tombstone 目标同事务提交，并用 RSS 原子性和重复内容测试验证
- [x] 3.2 将用户创建、编辑、发布、恢复、作者/管理员下架和删除路径接入目标推进，使幂等结果、文章事实、Outbox 与目标同事务提交，并覆盖草稿/离线编辑保持 tombstone、失败全回滚和重试测试
- [x] 3.3 在 generation 当前选择成功切换事务推进无向量目标，在 Embedding 当前选择成功切换事务推进带向量目标，并覆盖重复保存 noop、旧 revision/profile/lease 被拒绝以及推进失败回滚测试
- [x] 3.4 增加无 RabbitMQ、无 OpenSearch 配置下的文章与 AI 回归测试，验证只生成 PostgreSQL 待处理槽位且发布、latest、详情和增强执行保持可用

## 4. OpenSearch 索引与 Bulk 适配器

- [x] 4.1 增加并固定 OpenSearch 3.x 兼容官方 Go 客户端，在 Infrastructure 实现 TLS/认证、超时和端点脱敏，运行配置及错误日志测试确认凭据、query 和路径秘密不泄露
- [x] 4.2 创建版本化严格索引模板，包含 CJK/standard/keyword 字段、身份与可见性字段及固定维度 `knn_vector`，用真实 OpenSearch 3.8.0 测试验证 mapping、schema identity、固定 analyze token 和维度不兼容拒绝
- [x] 4.3 实现物理索引创建、实际 settings/mapping 校验、唯一读写别名初始化和原子切换，覆盖重复初始化、多写索引拒绝、错误 schema 拒绝和切换原子性的真实依赖测试
- [x] 4.4 实现按 physical index、item 数和请求字节切分的 Bulk upsert/tombstone，使用文章 ID 和 `external_gte` generation，并验证重复请求、低版本冲突、持久 tombstone 及重新发布高版本覆盖
- [x] 4.5 严格解析并逐项关联 Bulk 响应，将成功、stale/noop、可重试和永久失败返回 Application；用部分成功、429、映射错误、响应缺项/乱序/非法 JSON 和连接结果未知测试验证只重试失败 delivery

## 5. 投影 Worker 与可观测性

- [x] 5.1 实现投影执行器的认领、执行前批量事实复核、陈旧目标再推进、按 delivery 调用 Bulk 及 fenced 结果写回，并用 fake 适配器覆盖进程恢复、迟到完成和双索引部分失败
- [x] 5.2 将投影执行器装配为独立受监管 Worker 组件；验证未配置时禁用、配置后故障重连且 OpenSearch 故障不会停止 Feed、AI、Relay 或清理组件
- [x] 5.3 增加待处理/失败数量、最老年龄、索引延迟、Bulk item 分类、重试、陈旧执行、重建阶段和别名版本指标及关联日志，并用观测测试确认标签有界且不包含正文、HTML、向量或凭据
- [x] 5.4 提供带数量上限的失败 delivery 重试管理命令，验证只重新激活精确范围、活动新 generation 不被旧失败覆盖且输出报告可审计

## 6. 可恢复索引重建

- [x] 6.1 实现重建状态仓储和 `search index init`、`search rebuild start|status` 命令，限制单一活动重建、校验精确 schema/index 身份并为候选索引注册第二 delivery；用重复 start 和冲突重建测试验证
- [x] 6.2 实现可恢复 snapshot 阶段，按持久 article ID 水位写入候选索引且不写存量不可见文章；通过中途退出后 resume 和同批更新的真实依赖测试验证
- [x] 6.3 实现 change sequence catch-up、注册前租约屏障和落后 delivery 检测，验证重建期间发布、修订、AI 切换、下架及 Worker 在途请求最终都收敛到候选索引
- [x] 6.4 实现公开文档计数、无落后 delivery 和确定性抽样身份/内容哈希校验，持久化校验报告并通过人为漏文档、旧 revision 和错误向量身份测试确认拒绝切换
- [x] 6.5 实现 `cutover` 的单次别名原子切换、回滚窗口持续双写和 `rollback`，通过切换边界并发更新与真实别名测试验证新旧索引均不落后
- [x] 6.6 实现显式放弃候选与 `cleanup` 安全检查，拒绝删除读写别名、活动 delivery、回滚窗口或未知前缀引用的索引，并以精确目标测试验证不会误删

## 7. 配置、开发环境与运维文档

- [x] 7.1 增加 Search 配置默认值、YAML 和完整 `VELIS_*` 覆盖及边界校验，覆盖 endpoint/TLS、schema、维度、Bulk 上限、租约、退避、尝试次数和回滚窗口测试
- [x] 7.2 在 `compose.yaml` 增加固定 `opensearchproject/opensearch:3.8.0` 单节点服务、健康检查和持久卷，并验证干净 Compose 环境能够健康启动且不会把 `latest` 当作版本
- [x] 7.3 增加明确要求测试 URL 的 OpenSearch 集成 Make target 和 CI/本地命令，验证依赖未配置时明确报告 skip、配置后执行真实 template/Bulk/alias/rebuild 测试
- [x] 7.4 更新 README、示例配置和 CLI 帮助，记录初始化、重建、观察积压、故障恢复、切换、回滚、安全 cleanup 及“OpenSearch 不是事实源”的运维边界，并核对命令可复现

## 8. 全链路验收

- [x] 8.1 在真实 PostgreSQL、RabbitMQ 与 OpenSearch 下验证发布、公开修订、generation/Embedding 分阶段完成、下架、迟到旧写和重新发布链路，确认索引最终身份正确且 tombstone 不可见
- [x] 8.2 注入 OpenSearch 停机、超时、Bulk 部分失败和 Worker 中断，验证文章/AI 事务继续完成、任务保留、恢复后只追赶未完成 delivery 且积压指标归零
- [x] 8.3 在重建 snapshot 和 catch-up 期间持续修改文章与 AI 选择，验证校验、切换、回滚窗口双写和显式清理，并保存可复现的测试命令与结果摘要
- [x] 8.4 运行 `make check`、Go race、架构测试和全部相关真实依赖测试，修复格式与 vet 问题并确认本 change 未注册搜索 HTTP API、未引入 Redis/recommend、未使用 PostgreSQL 向量查询
