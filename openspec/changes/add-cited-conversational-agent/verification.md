# 有引用对话 Agent 阶段验收

日期：2026-10-11。范围是 I5 第 3–4 步后端：回合接受/配额、证据与投影、Eino 取证后独立成文、异步执行、取消、SSE 与固定阅读评测子集。没有新增 Web 对话页面，没有自动提交、推送、同步长期规格、归档或部署业务服务。

## 实际执行环境

- Go 1.26.6、Linux/WSL2、Intel Core i7-14700HX；Eino 固定 v0.7.13。
- Docker 中现有 PostgreSQL 17（pgvector 镜像）、Redis 8、OpenSearch 3.8.0；仅恢复这三个基础设施容器。API/Worker/Web 业务容器保持停止。
- 全部数据库变更与破坏性测试只在 `velis_agent_runs_test` 执行；故障使用自有代理、连接池、事务触发器。没有操作生产或业务数据库。
- 实际 nginx 1.29.8 使用已有 `velis-velis-web:latest` 镜像启动隔离测试容器，挂载仓库 nginx 配置，测试结束删除容器。
- 全部生成调用为本机 HTTP 确定性桩，使用合成材料与测试专用假凭据，未调用收费 Provider。

## 执行命令与结果

| 命令 | 实际结果 |
| --- | --- |
| `make check` | 退出 0；gofmt、Go 单测/race/build、architecture、Web lint/Vitest/build 通过；Web 17 文件、63 用例 |
| `make integration-agent-runs` | 退出 0；真实依赖套件顶层 43 / 含子用例 123 通过，0 fail、0 skip；模型/HTTP/TCP/指标/OpenAPI 套件顶层 12 / 含子用例 36 通过，0 fail、0 skip |
| 受影响读取套件，见下方筛选 | 顶层 8 / 含子用例 13 通过，0 fail、0 skip |
| `go test ./internal/infrastructure/ai/eino -run '^$' -bench '^BenchmarkReadingLocalRun$' -benchmem -benchtime=5x` | 5 次测量，45.172011 ms/op、668273 B/op、4783 allocs/op |
| `openspec validate add-cited-conversational-agent --strict` | 通过 |
| `git diff --check`、旧迁移与 HEAD 比较 | 通过；000001–000012 未改写 |

联合验收注入 `VELIS_TEST_DATABASE_URL`（专用 `_test` 库）、`VELIS_TEST_REDIS_ADDRESS=127.0.0.1:6379`、`VELIS_TEST_OPENSEARCH_URL=http://127.0.0.1:9200`、`VELIS_TEST_NGINX_IMAGE=velis-velis-web:latest`，并设置 `VELIS_AGENT_TEST_ARTIFACT_DIR=/tmp/feedvelis-agent-acceptance`。入口对缺少任何依赖变量、测试失败、任何 skip 或零测试均非零退出，已由契约测试验证。

受影响既有测试实际命令：

```bash
cd backend
GOCACHE=/tmp/feedvelis-go-cache go test -count=1 -race -json ./test/integration \
  -run '^TestCacheReadJointFault|^TestCacheReadLatest|^TestCacheReadRecommendation|^TestArticleSearch|^TestArticleRecommendation|^TestHybrid' \
  > /tmp/feedvelis-agent-affected-reads.json
python3 ../scripts/check-go-test-json.py /tmp/feedvelis-agent-affected-reads.json
```

常规 `make check` 未注入真实依赖变量，不能视作真实依赖测试通过。相同环境的常规 Go JSON 结果收集到 219 个 skip（含子用例），完整名称见 [default-test-skips.md](default-test-skips.md)：integration 194、Redis 适配器 7、RabbitMQ 6、OpenSearch 6、MinIO 5、Redis testkit 1。本阶段所需 Agent 联合套件和受影响读取套件另外真实执行，均零 skip。未重跑不受本变更影响的真实 RabbitMQ/MinIO 套件。

## 生命周期与数据库证据

真实 PostgreSQL 测试覆盖：双执行器只取得一个 owner、不重复启动同一回合；账户两个/会话一个活动上限；最后助手名额预留；幂等接受响应丢失、失败回滚；accepted/running/终态取消和取消/成功竞争；片段、来源、关联及事件每阶段故障回滚；助手、成功状态与终态事件原子提交；最终提交响应丢失后仅查询原回合；租约失效及原 deadline 固定；授权撤销、账户禁用、会话删除；连接池先关闭后的租约兜底；24 小时有界保留与最小删除标记。迟到 owner 无法续租、写块或成功提交，running 从不自动重跑。

事件日志验证接受与运行状态、受控工具进度、来源、片段和唯一终态的顺序；125 个并发进度事务达到 127 条非终态上界后拒绝额外事件，仍能提交第 128 条终态；512 KiB 字节边界保留终态 4 KiB。序号连续唯一，实际 JSONB 字节纳入预算。流只读取已提交事实。

SSE 的真实 TCP/数据库测试覆盖跨实例重放、非法/重复/未来/跨回合位置、终态末位置 204、清理后 410、注销/删除/访问令牌到期、安全无位置 stream.error、断线后继续完成。来源下架后同一位置返回 redacted，详情与助手历史同样隐藏整块，持久化原文及分页边界不变。

## 实际 nginx、背压与资源

最后完整联合验收的代理分片观测：首片段 0.514602415 秒，第二片段 1.039274975 秒，成功终态 10.563940721 秒。两个片段可见时数据库仍为 running；中途断开并在重新读取详情后按原位置续读，收到第二片段和十秒心跳，再允许模型 finish。原回合最终 succeeded，唯一助手消息、两个片段；模型调用恰好 2 次，当前文章工具调用计入预算。另一次显式终态取证记录：数据库 succeeded、已提交事件 8、片段 2、模型请求 2、工具调用 1、助手 1；首片段 0.518796476 秒、第二片段 1.040818560 秒、终态 10.565862435 秒。完整响应头包含 `text/event-stream; charset=utf-8`、`Cache-Control: no-store`、`Server: nginx/1.29.8`、`nosniff`。直连 Hertz 的 `X-Accel-Buffering: no` 已由测试验证；nginx 正常消费该控制头。

实际慢 TCP 客户端不读取，缩小收发缓冲后写入阻塞约 5.0168 秒即释放订阅，另一个连接可并行读取。心跳实测间隔 10.00044 秒，订阅初始 deadline 为 89.999988861 秒。账户四条并行连接正常，第五条返回 429；实例 64 条额度与完全释放由单元测试验证。HTTP 断线仅释放订阅，原模型回合完成；访问令牌自然到期同样不撤销执行授权。

资源测试另验证默认 4 次模型/6 次工具、整批先预留、缺 usage 不补零、超额不再调用、实际输入字节及 NDJSON 64 KiB 单行/256 KiB 累计边界。benchmark 包含本地桩约 40 ms 固定分批等待，只描述该夹具的耗时与分配，不代表真实模型延迟、生产吞吐或全站容量；没有运行全站压测或 GC 优化实验。

## 联合故障与恢复

`TestAgentRunGenerationFaultIsolationAndDependencyRecovery` 分别在生成关闭和独立模型 503 下运行 warm → Redis/OpenSearch 自有代理同时断连 → 恢复。每阶段 PostgreSQL readiness 均 ready，三篇公开文章可读、核心会话可查询/追加。依赖断连时搜索明确不可用，推荐转 latest_fallback；恢复后 personalized 返回。PIT 数始终为 0，没有泄漏。生成关闭时模型调用为 0；模型故障回合安全 failed/AGENT_MODEL_UNAVAILABLE、预留释放，桩恢复后显式 retry 成功，总模型请求为 2。暖缓存不绕过当前授权或数据库故障；既有缓存修订/AI 选择/下架及推荐排除回归通过。

## 固定阅读评测与交接

[十项固定评测](../../../backend/internal/infrastructure/ai/eino/testdata/reading-eval.json) 契约全部通过。七个文章回答有合法本轮引用身份，其中六个有固定材料支持标注，一个刻意错误的断言仍通过身份校验；伪造证据和写工具注入被拒绝，真正空结果使用服务端模板。引用身份与语义支持分别输出，不能把身份校验解释为完整语义证明，不能把确定性桩的通过率解释为真实模型质量。

下一 Web change 复用 [OpenAPI](../../../backend/api/openapi/velis.yaml)、[客户端接入](../../../docs/agent-runs.md)、[协议夹具](../../../backend/test/fixtures/agent-runs/protocol.json)、成功/澄清/redacted 回答夹具及真实 nginx 路由。关键约定是重连先用详情替换快照、按完整事件位置去重、EOF 查询原回合、显式 cancel 独立调用、partial 与 redacted 分开。Web 体验和完整产品评测尚未实施；笔记、外部来源、写作产物和长期偏好需各自定义新工具权限、副作用和证据类型。

收费模型冒烟未运行。若另行明确授权，应单独记录 Provider/model、profile/prompt 版本、日期、实际 usage 完整性、费用及人工语义评判，凭据仅环境注入。独立生成默认关闭，模型不纳入 readiness。

本机原始证据：`/tmp/feedvelis-agent-acceptance/{integration,protocol}.json`、`/tmp/feedvelis-agent-affected-reads.json`、`/tmp/feedvelis-agent-make-check.log`、`/tmp/feedvelis-agent-benchmark.log`、`/tmp/feedvelis-agent-nginx-terminal.log`。这些临时文件用于本次复核；可移植的执行命令和固定夹具已写入仓库文档。
