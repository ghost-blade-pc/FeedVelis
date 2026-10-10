# Agent 会话与消息基础

提供私有会话、不可变用户消息、手动标题、历史分页和三个内部文章只读工具。无模型自动回复、SSE、Agent Web 页面或定时任务。完整契约见 [OpenAPI](../backend/api/openapi/velis.yaml)，工具见 [只读工具说明](agent-tools.md)。

## 启用与调用

应用迁移后，同时设置 `VELIS_AUTH_ENABLED=true` 与 `VELIS_AGENT_ENABLED=true`。非开发 API 必须提供独立 `VELIS_AGENT_CURSOR_KEY`（Base64解码后恰好32字节），所有副本共享该值；开发 API 缺省生成进程临时密钥，重启使旧游标失效。Worker、CLI、迁移及关闭功能的API不要求该密钥。关闭功能仅撤下路由，历史不丢失，重新开启可读。

以下使用已通过登录取得的 `ACCESS_TOKEN`，所有UUID仅为示例；创建、改名、追加需各自幂等键。同键重复同命令重放原成功状态和正文，合法新操作配额不足返回409。

```bash
curl -X POST http://127.0.0.1:8080/api/v1/me/agent/conversations \
  -H "Authorization: Bearer $ACCESS_TOKEN" -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: 85000000-0000-0000-0000-000000000001' -d '{}'

# 将上一步返回的id写入CONVERSATION_ID。
curl -X POST "http://127.0.0.1:8080/api/v1/me/agent/conversations/$CONVERSATION_ID/messages" \
  -H "Authorization: Bearer $ACCESS_TOKEN" -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: 85000000-0000-0000-0000-000000000002' -d '{"content":"请查找相关文章"}'
curl "http://127.0.0.1:8080/api/v1/me/agent/conversations/$CONVERSATION_ID/messages?limit=20" \
  -H "Authorization: Bearer $ACCESS_TOKEN"
curl -X PATCH "http://127.0.0.1:8080/api/v1/me/agent/conversations/$CONVERSATION_ID" \
  -H "Authorization: Bearer $ACCESS_TOKEN" -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: 85000000-0000-0000-0000-000000000003' -H 'If-Match: "1"' -d '{"title":"阅读计划"}'
curl -X DELETE "http://127.0.0.1:8080/api/v1/me/agent/conversations/$CONVERSATION_ID" \
  -H "Authorization: Bearer $ACCESS_TOKEN"
```

创建/详情/改名直接返回会话对象和表示 `title_version` 的强ETag；追加直接返回消息，角色由服务端固定为user。列表/历史仅接受limit和cursor，默认20、范围1–50，返回items/next_cursor/has_more。列表按活跃时间及UUID实时倒序，跨页不冻结集合；历史选最近批次后按sequence升序返回，下一页加载本批最早序号之前的数据，追加不改变旧边界。游标默认1小时绝对期限，续页和修改limit不续期。省略标题为“新对话”，显式标题TrimSpace后1–100 code point且单行无控制字符；正文只统一CRLF/CR，不修剪或渲染Markdown。

错误顺序为认证、基本格式、归属/删除、幂等、新操作版本/配额。非法正文/JSON/分页为400 VALIDATION_FAILED，游标为400 INVALID_CURSOR；协议头错误分别IDEMPOTENCY_KEY_REQUIRED/INVALID、IF_MATCH_REQUIRED/INVALID；他人/删除/未知会话统一404 AGENT_CONVERSATION_NOT_FOUND；幂等冲突/在飞、标题版本冲突和两类配额超限为409；数据库故障为503 DEPENDENCY_UNAVAILABLE，预期外错误为500 INTERNAL_ERROR。管理员没有所有权豁免。

## 持久化不变量

- `agent_user_state` 按账户保存现存会话计数，所有写入先锁此行；不会跟随登录会话过期。
- `agent_conversations` 保存当前标题、标题版本、消息计数和下个序号；消息追加不修改标题版本，活跃时间只能前进。
- `agent_messages` 通过会话外键确定所有者，只允许 `user`、`assistant` 和正整数序号，同会话序号唯一。仓储没有修改或单条删除消息入口，整会话删除级联物理清除消息。
- `agent_conversation_deletions` 永久保留会话 UUID、账户 UUID、删除时间三个字段，仅供本人重复删除识别，不含标题、正文，不占配额。

历史直到用户主动删除才移除。游标和去重窗口过期不意味着历史过期；删除也不承诺清除已有备份，备份保留遵循部署方生命周期。

## 事务与去重维护

创建、追加、改名、删除统一采用用户状态锁→归属/删除检查→幂等行锁→会话锁。事务内只执行 PostgreSQL 操作。读取历史在同一短只读快照中完成归属检查和消息读取。

Agent 使用独立固定 24 小时幂等服务，三个操作作用域分别为 `agent.conversation.create`、`agent.message.append`、`agent.conversation.rename`；所有成功结果统一关联 `agent_conversation` 和会话 UUID。摘要使用规范化标题/正文、目标会话，以及改名预期版本。重放不续期、不改变计数或活跃时间，版本与配额在归属和重放之后检查。

整会话删除在同事务中清除全部关联成功快照的标题/正文，将结果替换为版本、会话引用及删除终态，物理删除会话/消息、释放配额、写最小标记。创建键未到期且关联会话已删除时不能重新创建；窗口外重用键生成新 UUID。最小标记不依赖去重记录存续。

既有 `DeleteExpiredIdempotency` 只按截止时间分批删除去重行，不获取用户或会话锁，不清理 Agent 历史和永久删除标记。关闭未来 Agent 功能开关也不得删除这些事实。

## 迁移和回退

`000012` 是新增迁移，不改写已执行迁移。下列验证命令只能指向可丢弃的专用 `_test` 库：

```bash
cd backend
export VELIS_TEST_DATABASE_URL='postgres://velis:velis@127.0.0.1:55434/velis_agent_test?sslmode=disable'
GOCACHE=/tmp/feedvelis-go-cache go test -count=1 -v -run '^TestAgentConversation' ./test/integration

# 仅在没有会话、消息、删除标记、Agent 幂等记录时回退一版。
VELIS_DATABASE_URL="$VELIS_TEST_DATABASE_URL" go run ./cmd/velis-migrate -path migrations -steps 1 down
VELIS_DATABASE_URL="$VELIS_TEST_DATABASE_URL" go run ./cmd/velis-migrate -path migrations up
```

空用户状态行可重建，因此不阻止回退。任何 Agent 事实（包括已过期去重记录）均阻止降级；不要删除历史以完成回退，应保留数据并关闭功能，先备份、明确恢复步骤和人工回滚方案。迁移工具的失败可能留下 dirty 状态，核对结构完整性后按现有迁移运维流程处理，不盲目 force。
