# 阅读回答与证据协议

本文描述 `add-cited-conversational-agent` 的证据与投影实现。模型执行、HTTP 回合与 SSE 仍以任务清单的完成状态为准。

工具的实际保留结果转换为本轮材料，每项有随机 UUID `evidence_id`、article/revision 身份、取得时间、`summary` 或 `body` 类型和截断标记。只将交给成文模型的文本登记到证据集合；输入预算裁掉的尾部和候选不能引用。历史引用需要重新读取当前文章后获得新证据。证据文本不出现在引用 DTO 中，不建立永久正文镜像。

成文阶段只接受 UTF-8 NDJSON，字段和顺序固定，拒绝重复键、未知字段、null、非法代理项、重复片段 ID、空引用和尾随记录：

```jsonl
{"type":"header","version":1,"kind":"answer"}
{"type":"block","id":"b1","text":"文章说明租约用于限制执行权。","evidence_ids":["82000000-0000-0000-0000-000000000010"]}
{"type":"finish"}
```

`block` 还可带 `quotes:[{"evidence_id":"…","text":"原文"}]`，每条引文必须属于该块引用且逐字出现在实际提供的文本中。正文细节任务要求正文证据；摘要不能替代正文读取。任何分片可以在 UTF-8 字符和 JSON 中间拆开，完整行通过校验后才提交片段。单行上限 64 KiB，累计原始输出上限 256 KiB，回答默认上限 8000 个 Unicode code point、32 块、10 个来源；达到上限明确失败。最后一行可没有换行，但必须有 finish 并正常结束流。Provider 的工具结束、输出截断或非法尾部不能当作 finish。

应用回答固定为 `version,kind,blocks,references,cards,redacted`。`answer` 每块都有本轮引用；`no_results` 需要实际检索或推荐成功且确实无可用结果；`clarification` 用于任务缺失、指代不明、材料不足或未启用能力。后两种由服务端固定模板生成，模型不能通过选择类型掩盖依赖失败或夹带文章介绍。

卡片的标题、作者、来源、站内路径和可选 RSS 原文链接来自可信读取，模型不能提交这些字段。引用按首次出现给文章修订分配编号，同一修订的摘要和正文复用编号。保留工具的摘要来源、截断、推荐 mode/degraded/degrade_reason 和真实推荐原因。首次输出各片段之前与完整提交之前，批量复核 PostgreSQL 当前公开性、修订和精确关键词/主题/来源条件；下架返回 `AGENT_EVIDENCE_UNAVAILABLE`，修订改变返回 `AGENT_EVIDENCE_STALE`，事实源故障关闭输出。

`partial` 表示回答尚未完整成功，可能已有合法片段；失败尾部不能把这些片段变成成功助手消息。`redacted` 表示读取时有引用失效：整个相关块替换为“相关内容暂不可用”，移除该块引文及引用 ID，隐藏失效引用和卡片。未失效块仍可保留，编号无需重新排序。历史、回合详情、上下文和事件使用同一投影规则；持久化正文、消息 ID、sequence 和创建时间不变。每个块事件持有独立引用关系，续读不依赖旧客户端来源缓存。

契约测试读取以下完整示例：

- [正常文章回答](../backend/internal/application/agentorchestration/testdata/answer.json)
- [筛选条件澄清](../backend/internal/application/agentorchestration/testdata/clarification.json)
- [引用失效后的回答](../backend/internal/application/agentorchestration/testdata/redacted.json)

身份、结构和逐字引文校验不能证明自由文本的所有断言正确。阅读评测必须另报语义支持程度，并区分实际取得的摘要和正文；不能把合法引用比例作为回答正确率。
