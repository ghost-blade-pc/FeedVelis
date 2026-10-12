# Agent 回合数据、接受与配额

本文记录回合事实、接受、助手预留、删除和保留期规则。完整生成执行器、引用投影和 HTTP/SSE 已接入，见[回合接口](agent-runs.md)。

状态图、事务锁顺序和端口职责见 [开发约束](agent-development.md)，当前会话接口见 [会话说明](agent-conversations.md)。

## 接受与消息额度

接受关联本人会话的最新 user 消息，不重复追加问题。命令仅允许 `message_id`、可选正整数 `context_article_id`、可选 `constraints:{keyword,topic,source_id}` 和可选 UUID `retry_of`。严格拒绝重复/未知字段、null、非法 UTF-8、未配对 Unicode 转义及非预期类型；总输入上限 16 KiB，过滤条件沿用搜索规范化规则。

账户状态锁先于接受幂等、会话及回合锁。接受事务同时保存回合、一个助手预留和固定 24 小时成功快照。相同键与规范化命令重放原 accepted 快照，即使当前已失败或停止接受；异命令返回 `IDEMPOTENCY_KEY_REUSED`。详情才表示当前回合状态。事务失败不留下回合、预留或占位；提交响应丢失后重试原键，不以新键重新收费执行。

每账户最多两个活动回合，每会话最多一个。消息总额度计入已保存 user/assistant 与活动助手预留；`message_count` 和序号只表示已保存消息。活动期间新追加返回 `AGENT_RUN_IN_PROGRESS`，旧追加成功重放、读取和改名仍可用。失败、取消、超时或整会话删除释放预留。

只有最后一个消息名额时，仍可保存 user，但之后没有助手额度，接受返回 `AGENT_MESSAGE_LIMIT_EXCEEDED`。预留不是占位消息，失败不会消耗序号。`WithAssistantReservation` 为最终保存提供同事务的当前授权、执行权与当前配额检查；降低上限后不删除旧历史，也不能凭旧预留突破新上限。共享数据库的副本必须使用一致配额，不支持不同副本混用上限或无协调热更新。

同消息最多接受三次且最多成功一次。已有回合后必须通过 `retry_of` 指向最新 failed/canceled/timed_out 回合，原当前文章及约束保持不变。活动、成功、过时 retry_of、修改约束和第四次接受均拒绝；24 小时去重到期不清除这些业务事实。

## 查询与游标

列表按 `(created_at,id)` 倒序，默认 20、范围 1–50；同时间使用 UUID 稳定排序。列表仅返回状态、消息关联、创建/结束时间、partial 与安全错误，不包含用户问题、原模型输出、登录会话、执行权或约束。读取不领取回合、不启动生成。

回合列表与会话/消息共用独立 Agent 游标密钥，但使用单独认证用途 `run-list`，同时绑定账户与会话。默认绝对期限沿用会话 1 小时配置，续页可改变 limit，不能续期；跨账户、会话、用途、篡改和过期游标均拒绝。

详情读取 accepted、running、终态摘要、用量及 partial；回答始终经过统一当前可见性投影，事实源故障返回 `DEPENDENCY_UNAVAILABLE`，不能旁路返回原文。成功助手历史使用同一投影器，旧 user/assistant 消息保持兼容。

## 数据与保留期

`000013_create_agent_runs` 新增：

| 表 | 内容与约束 |
| --- | --- |
| agent_runs | 本人会话/用户消息关联、六态、最多三次尝试、原登录会话、deadline、执行权、助手预留和回答/用量摘要；每会话一个活动、每消息一个成功 |
| agent_run_evidence | 本轮 article 证据身份、修订、摘要/正文类型、工作内容、卡片与必要引文核验信息 |
| agent_run_blocks | 有序片段及显式引文；每回合片段位置唯一 |
| agent_run_block_evidence | 片段到本轮证据的有序关系；复合外键拒绝跨回合引用 |
| agent_run_events | 五类业务事件及正序号；不允许模型内部事件或 stream.error |

成功回答、片段及证据随会话保留；所有终态事件在 24 小时后清除，失败片段、工作证据和卡片同期清除，仅保留无工作正文的失败摘要、关联输入身份及原约束（用于显式重试）。用户消息仍按原永久历史保留。保留期从 `finished_at` 开始，不从最后订阅、重放或清理时间重新计算。

内部 `Service.Cleanup` 每次最多 100 个回合，整个批次位于同一事务。候选先无锁读取，之后按账户→会话→回合加锁；清理失败回滚全部删除及过期标记。API supervisor 每秒触发有界维护，生成关闭时仍维护。原去重清理继续按 `expires_at` 删除到期键，不删除回合业务事实或永久删除标记。

删除沿账户锁在一个事务内清除会话、消息、回合、证据、片段、引用关系、事件和关联成功幂等私密快照。四类操作均转成原三字段 `version,conversation_id,deleted` 结果；会话删除表仍只有 `conversation_id,user_id,deleted_at`。旧接受重放返回会话不存在；外键拒绝删除后的迟到片段，失败注入会回滚整个删除。

以下只读查询可核对计数，不读取私密正文：

```sql
SELECT status, count(*) AS runs,
       count(*) FILTER (WHERE assistant_reserved) AS reservations
FROM velis.agent_runs GROUP BY status ORDER BY status;

SELECT count(*) AS expired_work
FROM velis.agent_run_blocks b JOIN velis.agent_runs r ON r.id=b.run_id
WHERE r.status <> 'succeeded' AND r.finished_at <= clock_timestamp()-interval '24 hours';

SELECT table_name, count(*) AS columns
FROM information_schema.columns
WHERE table_schema='velis' AND table_name='agent_conversation_deletions'
GROUP BY table_name;
```

清理后的失败工作计数应为 0，删除标记列数始终为 3。成功片段不属于失败工作查询范围。

## 迁移、备份与兼容回退

仅新增 `000013`，不改写 `000001`–`000012`。真实环境执行迁移前准备数据库备份及恢复步骤；本阶段仅在独立专用 `_test` 数据库验证，没有部署运行中的 API、Worker 或 Web。

down 仅允许没有新回合、助手预留、助手关联及新接受去重事实时执行；过期但未清理的接受记录仍阻止回退。不要通过删用户数据让 down 成功。标准兼容回退为停止新接受、保留表与历史；关闭 generation 并收敛活动回合，具体兼容门槛见[执行运维](agent-run-operations.md)。

存在生成助手事实时，禁止直接回退到不理解预留和可见性投影的旧二进制。关闭生成不等于可以撤销 schema；保留支持新数据的代码和表，才能保持读取与隐私边界。迁移保护错误不是删除数据的建议，也不能盲目 force 迁移版本。

## 本阶段验证

测试会清理专用 `_test` 库，只能使用可丢弃的测试数据。本次使用 Docker PostgreSQL 的独立 `velis_agent_runs_test`，没有使用应用库 `velis`。命令中的连接信息为本地开发示例：

```bash
cd backend
GOCACHE=/tmp/feedvelis-go-cache go test -race \
  ./internal/domain/agentrun ./internal/domain/agentevidence \
  ./internal/application/agentrun ./internal/application/agentconversation \
  ./internal/application/strictjson ./internal/architecture

VELIS_TEST_DATABASE_URL='postgres://velis:velis@127.0.0.1:5432/velis_agent_runs_test?sslmode=disable' \
GOCACHE=/tmp/feedvelis-go-cache go test -race -count=1 -v \
  -run '^TestAgent(Run|Conversation)' ./test/integration
```

真实数据库用例覆盖 up、空事实 down/up、有事实拒绝 down、归属/角色/关联约束、严格命令、最新用户消息、显式三次重试、接受快照、24 小时窗口、结算失败回滚、最外层提交响应丢失、两个服务实例的账户/会话/最后助手名额竞争、旧追加重放、降低配额后保存回调拒绝、稳定分页与游标隔离、成功保留与失败清理、清理失败回滚和删除竞争。删除查询确认五类回合表与消息都无残留，幂等快照与删除标记各保留三字段。

早期迁移、删除与保留期测试使用构造夹具；完整执行、模型协议、真实依赖及 nginx 联合验收见[测试说明](agent-testing.md)和[阶段记录](../openspec/changes/add-cited-conversational-agent/verification.md)。未调用收费模型，未提交、推送或部署业务服务。
