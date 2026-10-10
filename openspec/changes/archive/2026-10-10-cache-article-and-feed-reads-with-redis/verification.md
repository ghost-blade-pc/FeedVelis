# Redis 读取缓存阶段验收记录

本文保留任务组 1–4 的历史基线与验收，末尾记录任务组 5、6 的推荐首查计划和完整三对象联合验证。没有部署或操作生产；本轮仅启动已有本地测试依赖，故障通过测试进程自有代理注入。

## 环境与清理边界

2026-10-09，本地 Go 1.26.6、PostgreSQL 17、Redis 8、OpenSearch 3.8.0；使用专用 `velis_cache_reads_test` 数据库。连接串解析与 `current_database()` 均检查 `_test` 后缀，迁移及清表仅作用于该库。OpenSearch 用例创建并清理测试独占索引；Redis 使用每次运行的随机命名空间，只登记和删除本次键，禁止全库清空及共享服务停止。数据库不删除，便于复验。

## 关闭缓存基线（业务读取路径变动前）

命令从 `backend/` 执行：

```bash
VELIS_TEST_DATABASE_URL='postgres://velis:velis@127.0.0.1:5432/velis_cache_reads_test?sslmode=disable' \
VELIS_TEST_OPENSEARCH_URL='http://127.0.0.1:9200' \
VELIS_TEST_CACHE_REPORT=/tmp/cache-baseline-1.md \
GOCACHE=/tmp/feedvelis-go-cache go test -count=1 -v ./test/integration -run '^TestCacheReadBaseline$'
```

第二次仅将报告文件改为 `/tmp/cache-baseline-2.md`。两次均通过，固定输入摘要、各路径请求数、数据库端口次数/卡片数/载荷字节、推荐召回和 Embedding 次数完全一致。每个路径的相同用户输出 ID 序列在并发重复请求中一致；不比较含随机 nonce 的加密游标字符串。负载是应用服务调用，耗时包含真实依赖与计数序列化，不含 HTTP/浏览器或建库、投影预热时间；没有改善比例门槛。

### 第一次（17.57s）

固定语料 SHA256: `0a131b8c31b3d38584f2ea7e9e38beecf699608c66da6a7bf15164de18b76418`；1000 篇、两名用户、limit=20、并发 5、每 worker 10 轮、每轮固定 6 请求；固定时钟 `2026-09-19T12:00:00Z`。

| 路径 | 请求数 | P50 ms | P95 ms |
| --- | ---: | ---: | ---: |
| latest_first | 50 | 2.944 | 9.821 |
| latest_next | 50 | 2.382 | 3.578 |
| bm25_assembly | 50 | 10.129 | 44.437 |
| hybrid_assembly | 50 | 12.793 | 20.960 |
| recommend_first | 50 | 23.060 | 41.931 |
| recommend_next | 50 | 3.286 | 5.451 |

卡片数据库端口读取 350 次、返回 26100 卡片、JSON 载荷 12429525 字节；推荐召回 50 次；查询 Embedding 100 次（确定性桩，无收费模型）。画像/排除 SQL 不计入卡片端口计数；PIT、查询及 KNN 次数由固定序列确定：搜索 BM25 100、搜索 KNN 50，推荐 BM25/KNN 各 50。

### 第二次（18.71s）

固定语料 SHA256: `0a131b8c31b3d38584f2ea7e9e38beecf699608c66da6a7bf15164de18b76418`；1000 篇、两名用户、limit=20、并发 5、每 worker 10 轮、每轮固定 6 请求；固定时钟 `2026-09-19T12:00:00Z`。

| 路径 | 请求数 | P50 ms | P95 ms |
| --- | ---: | ---: | ---: |
| latest_first | 50 | 2.547 | 10.854 |
| latest_next | 50 | 2.916 | 5.841 |
| bm25_assembly | 50 | 8.710 | 12.702 |
| hybrid_assembly | 50 | 10.406 | 12.933 |
| recommend_first | 50 | 21.197 | 26.067 |
| recommend_next | 50 | 3.755 | 5.380 |

卡片数据库端口读取 350 次、返回 26100 卡片、JSON 载荷 12429525 字节；推荐召回 50 次；查询 Embedding 100 次（确定性桩，无收费模型）。画像/排除 SQL 不计入卡片端口计数；PIT、查询及 KNN 次数由固定序列确定：搜索 BM25 100、搜索 KNN 50，推荐 BM25/KNN 各 50。

## Redis harness

`VELIS_TEST_REDIS_ADDRESS=127.0.0.1:6379 GOCACHE=/tmp/feedvelis-go-cache go test -count=1 -race -v ./test/testkit/redistest`：通过，实际连接 Redis，验证其他随机命名空间的 sentinel 保留、拒绝登记外部前缀、20ms deadline 限制代理慢响应、断连失败和恢复成功。harness 仅调用精确键 DEL，无 FLUSHALL/FLUSHDB/SCAN 或容器停止命令。

## 基础层实现与验证（任务组 2）

- Application 新增卡片不可变片段、latest 候选页、推荐计划及共享请求预算端口，关闭时使用 Disabled。Domain/Application 没有 Redis 键、SQL 或客户端 SDK；架构技术纯净测试新增 Redis SDK 禁用前缀。
- 客户端固定为 `github.com/redis/go-redis/v9 v9.22.0`，选型依据为 [Redis 官方 Go 客户端说明](https://redis.io/docs/latest/develop/clients/go/)。命令及连接重试关闭，ContextTimeoutEnabled 开启，连接池/单批次/对象大小均有界。
- 三类对象独立 v1 格式与环境 namespace，严格 JSON、身份/字段校验、校验摘要及 created_at/expires_at；Redis 端先检查类型/STRLEN，再执行有界 MGET；SET pipeline 使用 PXAT，首页 DEL 枚举 limit 1..50，不扫描键。命中不续期，过期回填跳过。
- 缓存请求共享同一个预算，默认单次 50ms/累计 100ms；跨操作与批次扣减实际调用时间，父 deadline 约束，首次传输失败及预算耗尽后绕过，父取消错误保留。回填、失效的 Redis 错误吞为受控指标，不改变业务成功。
- 默认关闭，默认 < YAML < 环境变量；凭据不从 YAML 读取，格式化脱敏。API 与 Worker 使用同一配置构造函数及受管理 Close 生命周期，没有启动 Ping 或 Redis readiness 依赖。Compose 开发示例显式开启，可用环境变量关闭。
- 指标按条目、操作、实际数据库回源批次和秒分别计数，只允许固定枚举标签。部分命中及真实超时/回填绕过/失效传输失败分类已验证；日志不打印底层 SDK 异常、键、身份或正文。详见 [缓存可观测性](../../../backend/test/integration/cache_observability.md)。

## 任务组 1、2 的验证命令与结果

| 命令 | 结果与范围 |
| --- | --- |
| `make check` | 通过：gofmt、Go 全包单测/race/build、Web lint、Vitest 17 文件/62 测试、Web 构建；普通 Go 检查没有配置真实依赖，skip 不计为真实通过 |
| `cd backend && GOCACHE=/tmp/feedvelis-go-cache go test ./internal/bootstrap ./internal/infrastructure/config ./internal/infrastructure/observability ./internal/application/articlecache ./internal/architecture` | 通过：静态配置、优先级、默认关闭/启用不可达、readiness、父取消、预算、指标单位与标签、架构边界 |
| `VELIS_TEST_REDIS_ADDRESS=127.0.0.1:6379 make integration-redis` | 通过：实际 Redis harness、部分命中、未知版本、摘要/身份、非法字段、错误类型、超限、绝对寿命、迟到回填、50 首页失效、用户计划隔离、清空回填、慢响应/断连/恢复 |
| `cd backend && VELIS_TEST_REDIS_ADDRESS=127.0.0.1:6379 GOCACHE=/tmp/feedvelis-go-cache go test -count=1 -race -v ./internal/infrastructure/cache/redis ./test/testkit/redistest` | 通过：实际 Redis 与代理 race 复验，包含部分命中指标、受控故障分类及多个批次共享预算；无 skip |
| `cd backend && GOCACHE=/tmp/feedvelis-go-cache go test ./test/integration -run '^TestCacheReadMakeEntrypointsRequireEnvironment$'` | 通过：Redis 入口缺地址，以及联合入口分别缺 PG/Redis/OpenSearch 时均明确失败，失败发生在执行 go test 之前 |
| `openspec validate cache-article-and-feed-reads-with-redis --strict` | 通过 |
| `git diff --check` | 通过 |

最终联合验收从仓库根目录执行：

```bash
VELIS_TEST_DATABASE_URL='postgres://velis:velis@127.0.0.1:5432/velis_cache_reads_test?sslmode=disable' \
VELIS_TEST_REDIS_ADDRESS=127.0.0.1:6379 \
VELIS_TEST_OPENSEARCH_URL=http://127.0.0.1:9200 \
make integration-cache-reads
```

通过，无 skip。真实执行 Redis 上述全部测试，以及：

- `TestArticleRecommendationPostgresOpenSearch`：通过。
- `TestArticleSearchEndToEnd`：通过。
- `TestCacheReadMakeEntrypointsRequireEnvironment`：通过。
- `TestCacheReadBaseline`：通过，语料摘要与原两轮一致，仍为 350 次卡片读取/26100 卡片/12429525 JSON 字节/50 次推荐召回/100 次确定性 Embedding。
- `TestHybridArticleSearchHTTPRebuildAndRollback`：通过。
- `TestHybridIdentityAndActualProfile`：通过。

最初受限沙箱中的 make check 因现有测试无法监听本地端口失败；允许测试监听端口后完整复验通过，最终代码再次完整复验通过。该环境限制没有被记作代码通过，也没有跳过所需测试。

## 任务组 1、2 完成时的边界（历史记录）

上一轮完成并勾选 1.1–1.3、2.1–2.5，整体 8/27。缓存客户端和配置已在入口装配，但业务 latest/搜索/recommend 尚未调用缓存，回源批次指标端口尚待共享读取编排接入。没有用基础层适配器测试冒充当前事实快照、提交后失效、候选补页或推荐缓存端到端验收；任务组 3–6 保持未完成，I4.6 不更新为完成。没有执行归档、规格同步、提交、推送或部署。

## 任务组 3、4：当前卡片与 latest 接入（2026-10-09）

本轮完成 3.1–3.4、4.1–4.5，共 9 项，累计 17/27。API 启用缓存时，latest、BM25/混合搜索和推荐当前身份读取共享一个 Redis 客户端、请求预算及卡片编排；Worker 使用同一配置执行 RSS 提交后首页失效。默认关闭时入口保持原读取路径；详情始终直接读取 PostgreSQL。推荐首查计划缓存和推荐专用 latest 补位优化尚未实施，5、6 组保持待办。

| 范围 | 实现及验收证据 |
| --- | --- |
| 3.1 当前事实和缺失片段 | `article_read.go` 分开读取动态公开事实与不可变片段，验证修订/generation/Embedding/profile 归属；`TestCacheReadPostgresFactsAndFragmentsMatchOriginal` 在真实 PostgreSQL 比较原卡片/身份契约、RSS/用户来源、昵称与无增强状态 |
| 3.2 每批快照 | `WithinReadSnapshot` 使用 READ ONLY REPEATABLE READ，复用已有事务时禁止公开缓存读写；真实并发切换 generation/修订仍得到完整旧视图，真实只读快照拒绝写入，写事务内新修订可读但不会填入缓存；失败快照不回填、数据库错误不被命中隐藏 |
| 3.3 批量卡片 | 单测覆盖全缺失、全命中、部分缺失、损坏身份、输入顺序、205 条跨三批、500 上界、下架、动态昵称及数据库/缓存故障；事实查询每批一次，缺失片段每批最多一次，无逐文章查询 |
| 3.4 搜索与推荐装配 | `TestArticleSearchEndToEnd`、`TestHybridArticleSearchHTTPRebuildAndRollback`、`TestArticleRecommendationPostgresOpenSearch` 在 Redis 已配置时接入真实共享卡片；搜索代理断连后 BM25/混合次序保持原契约；旧 KNN 身份过滤、当前 BM25 卡片、重建/回滚及冻结推荐续页通过 |
| 4.1 latest ID 页 | PostgreSQL 只读取 ID/有效发布时间；Redis 实测 limit/规范化时区位置隔离、空候选页、未知版本、热点命中不续期和原始绝对寿命；位置改变在同一快照中整批按原请求边界重查 |
| 4.2 有界补页 | 单测覆盖同时间 ID 全序、下架间隙、有效 lookahead 不消费、500 候选上限的短页/空页及稳定推进；真实 PostgreSQL/Redis 覆盖预热后下架、昵称与排序变化、损坏、只清理本次键、断连及恢复 |
| 4.3 提交后动作 | 最外层成功提交后按 actionID 去重执行；事务单测及真实 PostgreSQL 验证嵌套复用、回滚、Commit 失败和一次执行；回调失败不改变已提交结果 |
| 4.4 写入失效 | 真实 RSS 外层回滚无失效/无业务泄漏，多条写入一次失效，全部 1–50 首页键删除，RSS 不变写入及用户幂等重放不重复失效；用户发布/编辑/删除和管理员下架/恢复通过；失效故障不改变业务/Outbox 原子性，迟到旧回填仅获得原到期剩余寿命 |
| 4.5 HTTP 与文档 | OpenAPI/README 明确候选约 5 秒窗口、当前事实复核和扫描上限后依游标推进；HTTP 回归验证空 items 仍可返回 has_more=true、next_cursor，续请求接受既有 v2 格式，非法 limit/游标仍返回原错误；Web 空页保留加载按钮，连续空页及短页按服务端水位继续 |

提交后失效同步执行，保留请求关联信息但解除原请求取消和预算，创建默认 100ms 独立有界额度，并受进程生命周期约束；不创建失效后台任务。只读快照异常清理另有 1 秒截止时间，避免父请求取消后无限占用连接。回源批次按实际批量数据库请求计数，失败请求也计一批。

从仓库根目录运行以下联合入口，结果通过且无 skip：

```bash
VELIS_TEST_DATABASE_URL='postgres://velis:velis@127.0.0.1:5432/velis_cache_reads_test?sslmode=disable' \
VELIS_TEST_REDIS_ADDRESS=127.0.0.1:6379 \
VELIS_TEST_OPENSEARCH_URL=http://127.0.0.1:9200 \
make integration-cache-reads
```

入口实际执行 Redis harness/适配器及上述全部 `TestCacheRead*`、真实搜索/混合搜索/语义身份/推荐回归。关闭缓存基线仍使用原路径，固定语料 SHA256 与前两轮一致，仍为 350 次卡片端口读取、26100 卡片、12429525 JSON 字节、50 次推荐召回和 100 次确定性 Embedding。本轮 P50/P95（ms）分别为 latest 首页 2.315/10.019、续页 1.907/2.595、BM25 5.352/7.505、混合装配 6.637/8.326、推荐首查 14.049/17.310、推荐续页 2.641/3.789；这些数值只用于基线复现，不代表缓存性能改善。关闭/冷/热/损坏/故障/恢复的完整比较保留给 6.3。

新增代码及真实依赖 race 复验在 `backend/` 执行，结果通过且无 skip：

```bash
VELIS_TEST_DATABASE_URL='postgres://velis:velis@127.0.0.1:5432/velis_cache_reads_test?sslmode=disable' \
VELIS_TEST_REDIS_ADDRESS=127.0.0.1:6379 \
GOCACHE=/tmp/feedvelis-go-cache go test -count=1 -race -v \
  ./internal/application/articleread ./internal/infrastructure/persistence/postgres \
  ./internal/infrastructure/cache/redis ./test/integration \
  -run 'TestSharedRead|TestLatest|TestInvalidation|TestAfterCommit|TestReadSnapshot|TestRedisLatest|TestCacheRead(Postgres|Snapshot|Existing|Latest|RSS|UserAdmin)'
```

该筛选还执行了既有 latest 索引回退/恢复测试，仅作用于专用 `_test` 库且恢复至当前迁移版本。未修改历史迁移、未新增迁移；harness 自动迁移及测试清表继续使用 `_test` 双重保护。

`make check` 最终复验通过：Go 单测、race、build，架构及 OpenAPI/HTTP 契约回归，Web lint、17 个文件/63 个 Vitest 测试和构建。普通检查未配置真实依赖，其 skip 不算真实验收，真实通过来自上述独立入口。最初受限沙箱中现有 HTTP 大游标测试无法监听端口；允许本地测试监听后完整复验通过，没有跳过用例。

`openspec validate cache-article-and-feed-reads-with-redis --strict` 与 `git diff --check` 通过。所有测试只清理本次 Redis 键与测试独占索引，保留专用数据库，没有停止共享依赖或操作生产。第 5、6 组和 Roadmap I4.6 完成状态保持待办；未提交、推送、归档或同步长期规格。

## 任务组 5、6：推荐首查计划与联合验证（2026-10-09）

推荐首查在 plan/page 间缓存最多 200 个冻结排序项，包含真实语义降级标志、硬排除前原候选 ID 与排除签名，绝对 TTL 默认 30 秒。身份由服务端认证的真实用户 ID、规范化当前画像及完整排序/召回/Embedding 配置指纹组成；同画像用户不跨身份命中。原候选排除签名变化重建计划，覆盖无标签文章撤销及自然过期；RankV1 仍先硬排除后来源打散。每次新首查建立独立时间、expiry 和 offset，续页仅使用原加密客户端状态，不读取计划、不重新召回或生成向量。卡片公开事实和语义身份、本人有效排除每页复核。已有写事务禁止计划及卡片公开缓存读写。

匿名/无正向画像继续冷启动；专用 latest 查询保留本人排除、skip、StartedAt 和键集位置，只查 ID 后在同一短快照装配当前卡片，不复用公共 latest 候选页。latest_fallback 与依赖失败不写入计划；失败回退后的新首查在搜索恢复时重新召回。成功但语义不可用的计划可以保留真实语义降级标志至 30 秒到期。Redis 故障不会单独改变 mode/degraded，PostgreSQL 故障仍返回既有依赖错误。

| 任务 | 可追溯验收 |
| --- | --- |
| 5.1 | `TestProfileAndConfigurationFingerprint`、`TestPlanIdentityIsolationAndFreshIndependentPages`；JSON map 顺序及 nil/empty 规范化、所有排序证据及有效配置变化、同画像身份隔离 |
| 5.2 | `TestPlanExclusionsWithdrawalExpiryAndProfileChange`、`TestPlanRejectsInvalidMembersAndPreservesFailureSemantics`、真实 `TestCacheReadRecommendationPlanIsolationExclusionsAndContinuation`；成员/重复/超 200 边界、绝对寿命、无标签撤销与 SQL 实际过期、硬排除原集合 |
| 5.3 | `TestPlanHitCurrentVisibilityAndSemanticIdentity`、独立首查单测、真实推荐及联合故障矩阵；暖后当前公开/修订/AI/负反馈、新首查独立水位、清空后有效续页不召回 |
| 5.4 | `TestPlanColdStartAndParentCancellation`、故障/恢复单测、真实专用 latest 与原查询逐项比较、`TestArticleRecommendationPostgresOpenSearch`；搜索失败回退及恢复、原游标错误、匿名/无画像、Redis 不改变模式 |
| 5.5 | 推荐单测与真实 PostgreSQL/OpenSearch 用例显式执行且通过；Redis 已配置时同时接入真实首查计划与共享卡片；README/OpenAPI/指标说明更新，30 秒排序窗口与 2 分钟客户端游标分离 |
| 6.1 | `TestCacheReadComparisonAndJointRecovery`、真实推荐及联合故障矩阵；只清空本次精确键，三对象逐渐重填并出现 hit，各状态六路径结果一致、同画像用户隔离、清缓存续页稳定 |
| 6.2 | `TestCacheReadJointFaultMatrixAndRealPostgresFailure`；三对象错误类型/损坏/断连/200ms 延迟/恢复，暖后编辑/AI 切换/下架/反馈，真实 SET/提交后 DEL 失败不改变成功结果；自有 PostgreSQL 代理故障时暖 card/latest/search/recommend 均不能返回旧公开数据；35ms 分段响应跨对象预算 wall=101.065ms、spent=100ms |
| 6.3 | 下方六状态固定负载；同语料摘要、每路径 50 请求，各用户输出逐一比较，记录延迟、SQL 端口载荷、快照、召回/Embedding 与指标，不设置改善比例门槛 |

真实依赖起初已停止，第一次连接被拒绝，未记为通过。只启动已有 `velis-postgres-1`、`velis-redis-1`、`velis-opensearch-1` 本地容器，保留持久卷；未启动 API、Worker、迁移或生产应用。故障均由进程自有 TCP 代理完成，没有停止共享服务。一次六状态比较暴露测试首查留下的 PIT 在多轮中积累；现已在测量结束后逐个关闭本次 BM25 游标的 PIT，关闭耗时不计入请求延迟，不关闭其他客户端上下文。最终复验通过。测试过期 SQL 类型和 generation 唯一身份 fixture 修正后均重跑通过。

### 同固定负载的六状态比较

从 `backend/` 执行：

```bash
VELIS_TEST_DATABASE_URL='postgres://velis:velis@127.0.0.1:5432/velis_cache_reads_test?sslmode=disable' \
VELIS_TEST_REDIS_ADDRESS=127.0.0.1:6379 \
VELIS_TEST_OPENSEARCH_URL=http://127.0.0.1:9200 \
VELIS_TEST_CACHE_COMPARISON_REPORT=/tmp/cache-comparison.md \
GOCACHE=/tmp/feedvelis-go-cache go test -count=1 -v ./test/integration \
  -run 'TestCacheReadRecommendation|TestCacheReadComparison'
```

通过，无 skip，19.302s。冷/恢复表示起始为空且负载中自然回填；热/损坏在测量前预热，损坏只覆盖本次已登记键。并发冷启动没有 singleflight，会重复回源。固定业务时钟复现原语料排序，测试包装器仅把计划 CreatedAt 映射为真实请求开始，避免历史业务时间使真实 Redis TTL 立即过期。性能测试没有向收费模型发请求。

JSON 字节是各应用端口类型的序列化诊断量，包含空字段，不是 PostgreSQL 网络字节；不同结构的总量不能直接视为节省比例，画像和有效排除 SQL 未计入卡片端口统计。快照端口占用包含 begin/查询/缓存/commit 和连接池排队，是连接实际占用的近似上界，各并发请求求和而非 wall time。热计划仍需实时画像与排除；热卡片仍需每批事实 SQL，跨请求没有数据库快照。

本机热计划把推荐召回 50 次降至 0，查询 Embedding 100 次降至 50（剩余为混合搜索）；片段回源为 0，但事实仍读取 300 批。相比关闭，latest、搜索与推荐续页有快照/Redis 的额外延迟，推荐首查减少召回；故障时需要事实 + 全部片段回源，SQL 批次/载荷增加。数据量很小且依赖均本机，不能外推为线上吞吐或通用改善比例。

固定语料 SHA256: `0a131b8c31b3d38584f2ea7e9e38beecf699608c66da6a7bf15164de18b76418`；1000 篇、两名用户、limit=20、并发5、每 worker10轮、六请求。

### disabled

| 路径 | 请求数 | P50 ms | P95 ms |
| --- | ---: | ---: | ---: |
| latest_first | 50 | 1.962 | 8.680 |
| latest_next | 50 | 2.041 | 4.931 |
| bm25_assembly | 50 | 5.613 | 8.229 |
| hybrid_assembly | 50 | 7.029 | 9.758 |
| recommend_first | 50 | 14.579 | 20.367 |
| recommend_next | 50 | 2.609 | 4.769 |

原卡片端口：350 次/26100 行/12429525 JSON字节；事实：0 次/0 行/0 JSON字节；缺失片段：0 次/0 行/0 JSON字节；候选ID：0 次/0 行/0 JSON字节。快照 0 次，端口占用总耗时 0.000 ms；推荐召回 50 次；Embedding 100 次。


### cold

| 路径 | 请求数 | P50 ms | P95 ms |
| --- | ---: | ---: | ---: |
| latest_first | 50 | 3.729 | 12.412 |
| latest_next | 50 | 3.495 | 8.197 |
| bm25_assembly | 50 | 9.453 | 15.634 |
| hybrid_assembly | 50 | 11.894 | 14.026 |
| recommend_first | 50 | 10.453 | 27.915 |
| recommend_next | 50 | 6.048 | 8.428 |

原卡片端口：0 次/0 行/0 JSON字节；事实：310 次/22100 行/12414550 JSON字节；缺失片段：25 次/1000 行/324945 JSON字节；候选ID：10 次/210 行/11305 JSON字节。快照 310 次，端口占用总耗时 1636.795 ms；推荐召回 10 次；Embedding 60 次。

- `velis_cache_entries_total{object=card,result=hit}` = 21100
- `velis_cache_entries_total{object=card,result=miss}` = 1000
- `velis_cache_entries_total{object=latest,result=hit}` = 90
- `velis_cache_entries_total{object=latest,result=miss}` = 10
- `velis_cache_entries_total{object=recommend,result=hit}` = 40
- `velis_cache_entries_total{object=recommend,result=miss}` = 10
- `velis_cache_fallback_batches_total{object=card}` = 25
- `velis_cache_fallback_batches_total{object=latest}` = 10
- `velis_cache_fallback_batches_total{object=recommend}` = 10

### hot

| 路径 | 请求数 | P50 ms | P95 ms |
| --- | ---: | ---: | ---: |
| latest_first | 50 | 3.415 | 4.387 |
| latest_next | 50 | 3.236 | 4.099 |
| bm25_assembly | 50 | 9.224 | 18.345 |
| hybrid_assembly | 50 | 10.539 | 17.152 |
| recommend_first | 50 | 8.834 | 11.918 |
| recommend_next | 50 | 5.685 | 7.658 |

原卡片端口：0 次/0 行/0 JSON字节；事实：300 次/21100 行/11851700 JSON字节；缺失片段：0 次/0 行/0 JSON字节；候选ID：0 次/0 行/0 JSON字节。快照 300 次，端口占用总耗时 1455.736 ms；推荐召回 0 次；Embedding 50 次。

- `velis_cache_entries_total{object=card,result=hit}` = 21100
- `velis_cache_entries_total{object=latest,result=hit}` = 100
- `velis_cache_entries_total{object=recommend,result=hit}` = 50

### corrupt

| 路径 | 请求数 | P50 ms | P95 ms |
| --- | ---: | ---: | ---: |
| latest_first | 50 | 3.599 | 6.742 |
| latest_next | 50 | 3.270 | 5.260 |
| bm25_assembly | 50 | 8.726 | 10.796 |
| hybrid_assembly | 50 | 11.212 | 13.022 |
| recommend_first | 50 | 9.632 | 24.864 |
| recommend_next | 50 | 5.933 | 7.570 |

原卡片端口：0 次/0 行/0 JSON字节；事实：310 次/22100 行/12414550 JSON字节；缺失片段：25 次/1000 行/324945 JSON字节；候选ID：10 次/210 行/11305 JSON字节。快照 310 次，端口占用总耗时 1483.184 ms；推荐召回 10 次；Embedding 60 次。

- `velis_cache_entries_total{object=card,result=corrupt}` = 1000
- `velis_cache_entries_total{object=card,result=hit}` = 21100
- `velis_cache_entries_total{object=card,result=miss}` = 1000
- `velis_cache_entries_total{object=latest,result=corrupt}` = 10
- `velis_cache_entries_total{object=latest,result=hit}` = 90
- `velis_cache_entries_total{object=latest,result=miss}` = 10
- `velis_cache_entries_total{object=recommend,result=corrupt}` = 10
- `velis_cache_entries_total{object=recommend,result=hit}` = 40
- `velis_cache_entries_total{object=recommend,result=miss}` = 10
- `velis_cache_fallback_batches_total{object=card}` = 25
- `velis_cache_fallback_batches_total{object=latest}` = 10
- `velis_cache_fallback_batches_total{object=recommend}` = 10

### failure

| 路径 | 请求数 | P50 ms | P95 ms |
| --- | ---: | ---: | ---: |
| latest_first | 50 | 3.451 | 4.079 |
| latest_next | 50 | 3.465 | 4.312 |
| bm25_assembly | 50 | 8.669 | 9.845 |
| hybrid_assembly | 50 | 10.227 | 11.596 |
| recommend_first | 50 | 18.444 | 23.867 |
| recommend_next | 50 | 5.811 | 6.583 |

原卡片端口：0 次/0 行/0 JSON字节；事实：350 次/26100 行/14665950 JSON字节；缺失片段：350 次/26100 行/8468200 JSON字节；候选ID：100 次/2100 行/113050 JSON字节。快照 350 次，端口占用总耗时 1302.549 ms；推荐召回 50 次；Embedding 100 次。

- `velis_cache_entries_total{object=card,result=miss}` = 26100
- `velis_cache_entries_total{object=latest,result=miss}` = 100
- `velis_cache_entries_total{object=recommend,result=miss}` = 50
- `velis_cache_failures_total{object=card,operation=get,reason=budget,result=bypass}` = 200
- `velis_cache_failures_total{object=card,operation=get,reason=transport,result=bypass}` = 150
- `velis_cache_failures_total{object=card,operation=set,reason=budget,result=failure}` = 350
- `velis_cache_failures_total{object=latest,operation=get,reason=transport,result=bypass}` = 100
- `velis_cache_failures_total{object=latest,operation=set,reason=budget,result=failure}` = 100
- `velis_cache_failures_total{object=recommend,operation=get,reason=transport,result=bypass}` = 50
- `velis_cache_failures_total{object=recommend,operation=set,reason=budget,result=failure}` = 50
- `velis_cache_fallback_batches_total{object=card}` = 350
- `velis_cache_fallback_batches_total{object=latest}` = 100
- `velis_cache_fallback_batches_total{object=recommend}` = 50

### recovery

| 路径 | 请求数 | P50 ms | P95 ms |
| --- | ---: | ---: | ---: |
| latest_first | 50 | 3.757 | 7.518 |
| latest_next | 50 | 3.446 | 5.045 |
| bm25_assembly | 50 | 9.101 | 16.751 |
| hybrid_assembly | 50 | 11.338 | 17.954 |
| recommend_first | 50 | 10.507 | 25.126 |
| recommend_next | 50 | 6.027 | 8.568 |

原卡片端口：0 次/0 行/0 JSON字节；事实：310 次/22100 行/12414550 JSON字节；缺失片段：25 次/1000 行/324945 JSON字节；候选ID：10 次/210 行/11305 JSON字节。快照 310 次，端口占用总耗时 1559.251 ms；推荐召回 10 次；Embedding 60 次。

- `velis_cache_entries_total{object=card,result=hit}` = 21100
- `velis_cache_entries_total{object=card,result=miss}` = 1000
- `velis_cache_entries_total{object=latest,result=hit}` = 90
- `velis_cache_entries_total{object=latest,result=miss}` = 10
- `velis_cache_entries_total{object=recommend,result=hit}` = 40
- `velis_cache_entries_total{object=recommend,result=miss}` = 10
- `velis_cache_fallback_batches_total{object=card}` = 25
- `velis_cache_fallback_batches_total{object=latest}` = 10
- `velis_cache_fallback_batches_total{object=recommend}` = 10


### 最终检查命令与边界

| 命令 | 结果 |
| --- | --- |
| `make check` | 通过：gofmt、Go 全包单测/race/build（含架构与 HTTP/OpenAPI）、Web lint、17 文件/63 Vitest 测试、Web 构建。普通检查未注入真实依赖，其 skip 不当作真实验收 |
| `cd backend && GOCACHE=/tmp/feedvelis-go-cache go test ./internal/application/recommendation ./internal/architecture` | 通过：指纹、非法计划、200/201 边界、真实语义降级标志、已有事务计划绕过、缓存/搜索故障恢复、身份/游标及架构边界 |
| `VELIS_TEST_REDIS_ADDRESS=127.0.0.1:6379 make integration-redis`（由联合入口实际调用） | 通过：真实 Redis harness/适配器，没有 skip，包含原绝对 TTL、错误类型、部分命中、预算与恢复 |
| 三个测试依赖变量如上述示例，`make integration-cache-reads` | 通过：真实 PostgreSQL/Redis/OpenSearch，无 skip；明确执行 `TestArticleRecommendationPostgresOpenSearch`、`TestArticleSearchEndToEnd`、`TestHybridArticleSearchHTTPRebuildAndRollback`、`TestHybridIdentityAndActualProfile`、全部 `TestCacheRead*`，含基线、三对象比较与联合矩阵 |
| 下方 race 联合推荐命令（相同三依赖环境） | 通过，无 skip；新增断言明确观察暖计划 hit 后 PostgreSQL 当前事实失败，真实画像 SQL 失败也不被暖计划掩盖，真实回填/失效传输失败均有指标；预算 wall=100.990ms、spent=100ms |
| 相同三依赖环境，`cd backend && GOCACHE=/tmp/feedvelis-go-cache go test -count=1 -race -v ./test/integration -run '^TestCacheReadComparisonAndJointRecovery$'` | 通过，无 skip，28.555s；新增共享客户端、指标计数与命名空间登记在并发六状态负载下无数据竞争；不将 race 耗时作为性能比较 |
| `openspec validate cache-article-and-feed-reads-with-redis --strict` | 通过 |
| `git diff --check` | 通过 |

race 联合推荐命令从 `backend/` 执行，并设置上述三个测试依赖环境变量：

```bash
GOCACHE=/tmp/feedvelis-go-cache go test -count=1 -race -v ./test/integration \
  -run 'TestCacheReadJointFault|TestCacheReadRecommendation|TestArticleRecommendationPostgresOpenSearch'
```

任务 6.5 对照已通过的联合结果更新 README、OpenAPI、示例 YAML、Compose API Embedding 输入版本、测试及指标说明和 Roadmap I4.6。默认关闭，配置优先级默认 < YAML < 环境变量；关闭只需 `VELIS_CACHE_ENABLED=false`，不需要删除缓存或业务数据，运行时 Redis 故障自动有界回源且不影响 readiness。推荐首查默认绝对 30 秒，客户端推荐游标默认绝对 2 分钟，两者独立；每页当前事实/排除校验保持。三类对象版本/身份/大小/预算有界；凭据仅环境注入，指标没有用户/键/画像/游标/内容标签，日志不打印底层异常或凭据。

没有新增或改写迁移。测试 harness 的既有自动迁移与清表仅作用于 `_test` 双重保护的专用库。Redis 只 DEL 本次拥有键，OpenSearch 只清理测试独占索引，专用数据库保留；仅本地依赖启动，没有停止共享依赖、生产操作、部署、提交、推送、归档或长期规格同步。

本轮完成 5.1–5.5、6.1–6.5 共 10 项，整体 27/27。change 保持未归档；任务状态仅在实现和对应验收完成后更新，并以 CLI apply 返回的 done/progress 逐项复核。
