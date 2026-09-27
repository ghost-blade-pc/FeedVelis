# Tasks

## 1. 配置与应用契约

- [ ] 1.1 在 `config/search.go` 增加默认关闭的 hybrid 配置、候选/超时边界与环境覆盖，核对总预算；以默认值、YAML/环境优先级、1/100/101 候选和非法超时测试验证。
- [ ] 1.2 在 `application/articlesearch` 定义查询 Embedding、KNN 候选及当前向量身份批量读取端口与计划指纹，保持无 SDK/SQL 依赖；运行编译与 `internal/architecture` 测试验证。

## 2. profile 投影与当前身份读取

- [ ] 2.1 增加 `articles.v2.json` 的实际 Embedding profile 字段、版本化编码/指纹/schema identity；以 v1 strict 编码兼容、v2 字段、维度与不可变 schema 测试验证。
- [ ] 2.2 更新 PostgreSQL 搜索投影读取、Bulk 编码与重建校验，将 profile 从实际结果事实读取；以当前修订、失配 generation、无向量、同维度旧 profile 的成功/失败/边界测试验证。
- [ ] 2.3 增加公开卡片及 revision/generation/embedding/profile 身份的批量读取，校验当前选择归属且不逐篇查库；以 PostgreSQL 集成测试验证旧修订、同修订选择变化、空输入、下架和批量次序。

## 3. 查询 Embedding 与 KNN 召回

- [ ] 3.1 在 Eino 基础设施实现专用在线 QueryEmbedder 适配器，复用 provider/model/profile 配置与错误分类，固定一次调用、不写结果表；以 HTTP 模型桩验证输入、有限值/零范数/维度、超时/取消、限流和无重试。
- [ ] 3.2 扩展 OpenSearch 适配器支持当前 PIT 下 KNN、内部精确/profile 过滤和必要身份字段，保留原 BM25 查询权重；以请求体与响应测试覆盖独立召回、稳定 tie-break、非法身份、重复 ID、超量候选和部分分片失败。
- [ ] 3.3 增加实际读索引 schema 检查与 v1/BM25 降级，处理切换后的能力识别；以适配器测试验证 v1/v2、缺失元数据与故障分类，不将检查失败变为核心 readiness 失败。
- [ ] 3.4 在固定 OpenSearch 3.8.0 真实依赖上验证 Lucene KNN+PIT+显式排序及 profile/关键词/主题/来源过滤；固定人工向量分别证明 BM25 和 KNN 的独有召回，记录命令和结果，skip 不计通过。

## 4. 融合与失败隔离

- [ ] 4.1 实现应用层纯 RRF 函数，去重、重新编号、等权与常数 60、时间/ID 全序；以精确手算样例、空路、双路重叠、同分、随机插入顺序和 200 候选边界测试验证确定性。
- [ ] 4.2 实现首查并发 BM25 与语义路径、独立子 deadline、融合前身份复核和失败清理；以 Service 测试验证成功、无模型、无有效向量、Embedding/KNN 各种失败回退 BM25，BM25/数据库失败不返回语义部分页。
- [ ] 4.3 实现旧 KNN 贡献移除、BM25 资格保留和无向量文本命中；以固定测试集验证旧 revision、旧 generation/embedding、不同 profile 均不贡献 RRF，且文本独有文章正常命中。

## 5. 混合游标与分页

- [ ] 5.1 实现 v2 紧凑候选列表的认证加密 codec、HKDF 密钥隔离与查询/计划/模式/绝对 TTL 校验，保留 v1 解码；以 round-trip、篡改、未知版本、错误密钥、重复 ID、非法偏移、过期和最坏 200 条载荷低于 16 KiB 的测试验证。
- [ ] 5.2 实现冻结列表分页、逐页可见性/向量身份复核、预读项位置和耗尽处理；以连续分页测试验证不重不漏、下架间隙、语义身份变化整篇跳过、纯 BM25 返回当前修订、零有效候选和改变 limit。
- [ ] 5.3 验证首查降级模式固定、续页零模型/KNN 调用、索引变化不重排、关闭/修改计划使 v2 失效、旧 v1 继续 BM25；以 Service/HTTP 测试及实际大游标请求验证兼容与错误契约。

## 6. 装配、指标与文档

- [ ] 6.1 更新 API bootstrap 的在线 Embedding 装配/取消回收、示例 YAML、`.env.example` 与 Compose 的 API 配置注入；以 bootstrap/config 测试验证默认 BM25、仅 Embedding 可用、未配置降级与资源关闭。
- [ ] 6.2 扩展 ArticleSearch Observer/Prometheus 的分段耗时、候选、模式、陈旧身份与降级指标；以观测测试验证成功降级计数、低基数词表及日志不包含查询、向量、游标或凭据。
- [ ] 6.3 更新 OpenAPI 搜索说明、README 与 Roadmap 的已实现事实，说明开关、两路上限、有限窗口、profile v2 重建/回滚与 BM25 降级；交付与代码一致的文档，不宣称 recommend 已实现。

## 7. 集成与验收证据

- [ ] 7.1 交付固定语料、人工向量、查询模型桩和期望召回/次序，覆盖中英文、过滤、语义独有、文本独有、无向量和陈旧身份；重复执行相同集合验证结果一致，不以真实模型输出作 CI 断言。
- [ ] 7.2 运行专用 PostgreSQL/OpenSearch 集成测试验证完整混合 HTTP 搜索、v2 在线重建切换、v1 回滚及模型/KNN 故障；明确记录依赖配置、命令、通过或 skip，验收不得以 skip 替代真实通过。
- [ ] 7.3 运行现有 Web 搜索回归与 `make check`（含架构检查），验证搜索页面/加载更多/空结果/不可用继续兼容；记录命令与结果并修复相关失败。
- [ ] 7.4 对照两份 delta spec 的所有场景形成验收记录，明确五项关键验收均有测试证据及每路 100 上限的产品限制；运行 `openspec validate add-hybrid-article-search --strict` 验证产物一致。
