# 有引用回合与流式接入

会话启用后提供回合 API；生成还需独立开启 `VELIS_AGENT_GENERATION_ENABLED` 并配置兼容模型，见[配置参考](configuration.md)。所有端点使用 Bearer，本人归属没有管理员豁免。Web 对话页面留给下一 change。

| 方法与相对路径 | 行为 |
| --- | --- |
| `POST /me/agent/conversations/{id}/runs` | 关联已保存的最新 user 消息，预留助手额度，返回固定 202 快照及 Location |
| `GET /me/agent/conversations/{id}/runs` | 默认 20、范围 1–50 的摘要分页；独立加密 cursor |
| `GET /me/agent/conversations/{id}/runs/{run_id}` | 当前状态、answer、usage；读取不启动生成 |
| `POST /me/agent/conversations/{id}/runs/{run_id}/cancel` | 显式停止，终态重复调用仍返回 200；无正文和查询参数 |
| `GET /me/agent/conversations/{id}/runs/{run_id}/events` | 读取原回合的持久事件；不领取或重新生成 |

路径以 `/api/v1` 为前缀。创建必须携带单一 UUID `Idempotency-Key` 和 JSON：

```json
{"message_id":"33000000-0000-4000-8000-000000000001","constraints":{"keyword":"Go"}}
```

输入最大 16 KiB，拒绝未知/重复字段、null 和未知查询参数。可选字段为 `context_article_id`、`constraints:{keyword,topic,source_id}`、`retry_of`。接受幂等 24 小时重放最初的 accepted/202，当前结果以详情为准；失败重试使用新键和最新失败回合的 `retry_of`，最多三次且只能成功一次。配额、最后消息名额及删除规则见[数据说明](agent-run-data.md)。

SSE 使用 `fetch`，因为浏览器原生 EventSource 不支持自定义 Bearer 头。禁止把令牌放入 URL。每个持久事件有 `id: v1:<run_uuid>:<positive_sequence>`，JSON 的 `version=1`、`run_id`、`sequence` 与位置一致，event 为 `run.status`、`tool.status`、`answer.sources`、`answer.block` 或 `run.terminal`。来源先于其片段，只有数据库提交后的事件才会出现。不会输出模型 Token、推理或工具原始参数。

单一 `Last-Event-ID` 返回该位置之后的事件；跨回合、非法、重复头或未来位置返回 400 `INVALID_AGENT_EVENT_ID`。授权和归属先检查。终态最后位置已消费返回 204；终态 24 小时后的日志返回 410 `AGENT_EVENTS_EXPIRED`，此时用详情恢复。五类持久事件最多 128 条、512 KiB，始终为终态预留一条及 4 KiB。

客户端流程：保存 user → 用固定幂等键接受 → 保存 run_id → 读取详情作为快照 → 订阅该回合。重连或刷新访问令牌后，先重新读取详情并**替换**本地文本、引用和卡片，然后携带最后位置续读、按完整事件 ID 去重。每批都会复核当前来源；相同 ID 可能变为 redacted，不能长期保留旧正文。`partial=true` 表示未成功的工作片段；`redacted=true` 表示来源失效，详见[证据协议](agent-evidence.md)。

```javascript
// 伪码中的 replaceSnapshot、applyEvent 和 parseSSE 由客户端实现。
const detail = await fetch(runURL, {
  headers: { Authorization: `Bearer ${accessToken}` }
}).then(r => r.json());
replaceSnapshot(detail); // 每次重连都替换，不能只追加旧文本
const headers = { Authorization: `Bearer ${accessToken}` };
if (lastEventID) headers['Last-Event-ID'] = lastEventID;
const response = await fetch(`${runURL}/events`, { headers, signal });
if (response.status === 204) return;
if (response.status === 410) return replaceSnapshot(await readDetail());
if (!response.ok) throw await response.json();
for await (const event of parseSSE(response.body)) {
  if (event.type === 'stream.error') break;
  if (!seen.has(event.id)) {
    applyEvent(event);
    seen.add(event.id);
    lastEventID = event.id;
  }
}
replaceSnapshot(await readDetail()); // EOF 不是成功，查询原回合决定是否续读
```

读取流期间不使用 HTTP 请求超时取消生成。客户端 AbortController、关闭页面、九十秒订阅期限及网络 EOF 都只结束订阅；停止按钮单独调用 cancel。访问令牌自然到期关闭订阅，刷新后继续原回合；注销、账户禁用或会话删除才撤销执行授权。瞬态 `stream.error` 只包含版本及安全错误码，没有 id，不持久化，不是回合终态。

无业务事件时每十秒注释心跳，单次订阅最多九十秒，写阻塞最多五秒，实例最多 64 个、同一账户在单实例最多 4 个订阅，超额 429。nginx 对专用 events 路由关闭响应缓冲和缓存，读写代理期限 100 秒。服务端响应 `Cache-Control: no-store`、`X-Accel-Buffering: no`。

下一 Web change 可以复用 [OpenAPI](../backend/api/openapi/velis.yaml)、[完整协议夹具 protocol.json](../backend/test/fixtures/agent-runs/protocol.json)、[回答 JSON 夹具](../backend/internal/application/agentorchestration/testdata/answer.json)及 `TestAgentRunHTTPContract`、`TestAgentRunSSEReplayProjectionAndPositions`、`TestAgentRunNginxModelStreamingAndReconnect`。客户端不能按自由文本解析来源，也不能把服务器失败模板展示为成功回答。恢复与兼容回退见[执行运维](agent-run-operations.md)。
