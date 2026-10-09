# Tasks

## 1. 画像与推荐输入

- [x] 1.1 扩展反馈画像样本的当前修订关键词读取、去重与有界权重，验证画像单测覆盖重复阅读、收藏、负反馈、缺失增强、下架及跨用户隔离。
- [x] 1.2 增加推荐首查配置、独立游标密钥与预算校验，更新示例配置；验证配置测试覆盖默认值、非法边界、生产共享密钥和开发临时密钥。

## 2. 候选与排序

- [x] 2.1 增加有界 OpenSearch 关键词/主题候选端口，复用读别名与公开过滤；验证查询构造测试覆盖空词项、候选上限、非法/部分响应和超时。
- [x] 2.2 接入可选查询 Embedding/KNN、向量身份复核与 RRF，验证测试覆盖 BM25/KNN 两路、单路为空、语义故障、旧修订向量及候选去重。
- [x] 2.3 实现排序版本 1 的固定打分、有效时间 tie-break、负反馈排除与逐步来源打散，验证纯函数测试覆盖收藏/阅读影响、权重上限、同分、无 AI/Source 和同来源连续候选。

## 3. 分页、补位与故障

- [x] 3.1 实现独立推荐 v1 认证加密游标，冻结候选顺序、模式、身份、排序版本、过期时间与 latest 位置；验证游标测试覆盖篡改、过期、跨用户、跨匿名/登录、版本变化和长度上限。
- [x] 3.2 实现 PostgreSQL 当前公开批量复核、文章级排除重查和 latest 键集补位；验证用例测试覆盖下架、续页新负反馈、候选不足、无信号冷启动、无重复及已冻结顺序。
- [x] 3.3 编排首查故障边界：OpenSearch 整体故障转 latest，语义单路故障保留词项候选，PostgreSQL/画像读取失败返回依赖错误；验证故障注入测试与模式、降级标志、最小原因一致。

## 4. HTTP、Web 与观测

- [x] 4.1 装配匿名/可选认证推荐路由，确保 `/articles/recommend` 先于 `/:id`；验证 HTTP 测试覆盖无凭证、有效/失效凭证、认证关闭、参数错误、游标错误和响应字段。
- [x] 4.2 为 Web 增加 recommend 客户端、类型、latest/recommend 切换、原因展示与降级提示；验证 Vitest 覆盖翻页、模式/账户切换取消旧请求、游标失效恢复、空页和请求失败。
- [x] 4.3 增加固定低基数推荐指标与受控日志，验证观测测试覆盖召回/过滤/补位/降级计数且不输出画像、向量、游标或正文。

## 5. 契约与完整验收

- [x] 5.1 更新 OpenAPI、README、Roadmap 与配置说明，验证文档明确 `mode`、`degraded`、原因枚举、候选上限、游标期限、冷启动与无缓存边界。
- [x] 5.2 用真实 PostgreSQL/OpenSearch 验证登录信号影响排序、BM25/KNN 为空、下架不泄露、稳定分页及 OpenSearch 故障回退；记录真实依赖测试命令和结果，skip 不计为通过。
- [x] 5.3 运行架构测试与 `make check`，核对四层依赖和 Web/Go 全量检查均通过；记录命令及任何环境限制。

### 验证记录

- 2026-09-28：专用 `velis_recommend_test` PostgreSQL 库与本机 OpenSearch 3.8.0，执行 `VELIS_TEST_DATABASE_URL=postgres://velis:***@127.0.0.1:5432/velis_recommend_test?sslmode=disable VELIS_TEST_OPENSEARCH_URL=http://127.0.0.1:9200 GOCACHE=/tmp/velis-go-cache go test -count=1 -v -run '^TestArticleRecommendationPostgresOpenSearch$' ./test/integration`，结果 PASS；无 skip。覆盖两名登录用户不同偏好、投影未追赶时 BM25 空集、v2 KNN 空集、下架与新增负反馈、冻结续页、实际连接故障转 latest。
- 2026-09-28：再次执行上述真实依赖测试，结果 PASS；执行 `make check`，Go 全量单测（包含 `backend/internal/architecture/` 四层依赖测试）、race、构建，以及 Web lint、61 个 Vitest 测试与构建全部通过。执行 `openspec validate add-recommend-article-feed` 和 `git diff --check` 均通过。真实依赖测试使用本机专用测试库与 OpenSearch，未使用生产数据。
