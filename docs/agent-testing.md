# Agent 回合测试与阅读评测

默认测试只调用本机确定性模型桩，不读取生产生成凭据，不调用收费模型。测试数据均为合成内容。`make check` 包含 Go 单测、race、构建及 Web lint/测试/构建；真实依赖测试在缺环境时会 skip，因此不能用该命令代替联合验收。

## 联合验收

准备专用、可清理的 `_test` PostgreSQL 数据库和已经启动的 Redis/OpenSearch。测试不能指向业务库；同一测试库不要并行运行不同套件。nginx 使用本机已缓存的 Web 镜像，每次启动独立容器并自动删除，挂载仓库 `web/nginx.conf`，不启动或部署已有业务服务。

```bash
export VELIS_TEST_DATABASE_URL='postgres://测试用户:测试密码@127.0.0.1:5432/velis_agent_runs_test?sslmode=disable'
export VELIS_TEST_REDIS_ADDRESS='127.0.0.1:6379'
export VELIS_TEST_OPENSEARCH_URL='http://127.0.0.1:9200'
export VELIS_TEST_NGINX_IMAGE='velis-velis-web:latest'
export VELIS_AGENT_TEST_ARTIFACT_DIR='/tmp/velis-agent-acceptance'
make integration-agent-runs
```

四项依赖变量缺少任何一项均在测试前非零退出；镜像缺失不会自动拉取。数据库测试再次校验 `_test`。入口以 `-count=1 -race -json` 运行会话、回合、证据和三个只读工具真实联合测试，以及模型协议、HTTP、真实 TCP、指标与 OpenAPI 契约。`scripts/check-go-test-json.py` 对任何 skip、失败或零测试非零退出；输出分别统计顶层用例和含子用例数量。JSON 记录包含调用数、实际 nginx 分片时刻、心跳与慢写释放证据。

Docker Desktop 使用端口映射和 host.docker.internal 连接暂时监听的测试 API；Linux 通过 host-gateway。测试代理只发布 localhost 端口。Redis/OpenSearch/数据库故障均用测试自有连接代理或连接池模拟，不停止共享服务、不删除业务缓存。OpenSearch 索引和 Redis 键按测试登记范围清理。

受影响的既有读取链路可另运行：

```bash
cd backend
GOCACHE=/tmp/feedvelis-go-cache go test -count=1 -race -json ./test/integration \
  -run '^TestCacheReadJointFault|^TestCacheReadLatest|^TestCacheReadRecommendation|^TestArticleSearch|^TestArticleRecommendation|^TestHybrid' \
  > /tmp/velis-agent-affected-reads.json
python3 ../scripts/check-go-test-json.py /tmp/velis-agent-affected-reads.json
```

## 固定阅读评测子集

[reading-eval.json](../backend/internal/infrastructure/ai/eino/testdata/reading-eval.json) 固定十个案例，覆盖搜索解释、结构化追问重新取证、推荐 latest_fallback、真实空结果、显式硬条件、反馈上下文隔离、材料注入、写工具注入、伪造证据与合法身份但错误断言。模型桩根据协议观察继续决策，引用 ID 从当轮保留材料动态取得；绝不预先复用上一回合证据。

```bash
cd backend
GOCACHE=/tmp/feedvelis-go-cache go test -race -count=1 -v \
  ./internal/infrastructure/ai/eino -run '^TestReadingFixedEvaluationSubset$'
GOCACHE=/tmp/feedvelis-go-cache go test ./internal/infrastructure/ai/eino \
  -run '^$' -bench '^BenchmarkReadingLocalRun$' -benchmem -benchtime=5x
```

每项分别报告引用身份和固定材料的语义支持标注，以及模型/工具调用数。十项契约通过不表示真实模型语义正确率 100%。七个文章回答中六个有支持标注，一个刻意构造的错误断言仍通过身份校验；另一个伪造引用被拒绝，写工具注入被拒绝，空结果返回服务端模板。标注只针对固定材料和句子，不是通用语义判定器；真实用户问题和 Provider 泛化仍需下一阶段完整产品评测。

benchmark 记录本机桩的总耗时、分配字节和次数，包含桩固定分批等待，不能解释为真实 Provider 延迟或生产容量。默认资源上限、事件上限、实际 TCP 慢写与双执行器竞争另有回归测试。完整结果见[阶段验收记录](../openspec/changes/archive/2026-10-12-add-cited-conversational-agent/verification.md)。

## 下一 Web change 与可选真实模型冒烟

复用[回合接口](agent-runs.md)、[完整协议夹具](../backend/test/fixtures/agent-runs/protocol.json)、成功/澄清/redacted 回答夹具和已验证的 nginx 路由。重连必须先用详情替换快照，EOF 查询原回合，取消是独立操作。当前没有新增 Web 对话 UI。

收费 Provider 冒烟未运行，也不是默认测试前提。若后续显式授权，独立记录 provider/model、profile/prompt 版本、日期、实际 usage 完整性、费用和人工语义评判；凭据只通过环境注入，报告不得包含真实对话或密钥。不要把确定性桩的成功率当作真实模型质量。
