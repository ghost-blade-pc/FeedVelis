# 文章事件 v1 契约

本目录定义 `article.published.v1`、`article.revised.v1`、`article.offlined.v1` 与 `article.deleted.v1`。所有对象均拒绝未知字段；事件只传递稳定标识、聚合版本和内容哈希，不包含 Markdown、HTML、纯文本或数据库快照。

- `event_id`：UUIDv7；`trace_id`：32 位小写十六进制。
- `occurred_at`：带 `Z` 的 UTC RFC 3339 时间。
- `aggregate.type` 固定为 `article`，版本取文章 `lock_version`。
- 发布/修订载荷引用当前修订；下架/删除载荷引用状态变化时的当前修订。
- v1 不原地增加字段；不兼容变更发布新的事件重大版本。

`fixtures/valid` 是编码兼容样例，`fixtures/invalid` 是 Consumer 必须永久拒绝的样例。Go 契约测试直接读取这些文件。
