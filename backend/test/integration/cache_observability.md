# 缓存指标与接入约定

API 的 latest、搜索与推荐当前卡片读取共用缓存观察者，Worker 使用同一配置执行提交后首页失效；现有 `/metrics` registry 注册这些指标，序列只在实际观察发生后出现。推荐首查计划与共享卡片跨操作使用同一请求预算。缓存不单独改变推荐 mode/degraded。

| 指标 | 单位 | 标签与语义 |
| --- | --- | --- |
| `velis_cache_entries_total` | 条目 | `object=card/latest/recommend`，`result=hit/miss/corrupt`；卡片按输入身份逐项计数，latest/plan 一次读取一个对象；损坏条目同时计入 miss 和 corrupt |
| `velis_cache_failures_total` | 操作 | `object`、`operation=get/set/invalidate`、`result=bypass/failure`、`reason=invalid/transport/timeout/budget/canceled/none`；一次 MGET 故障计一次绕过，不按 100 个键放大；回填及失效失败按操作计数 |
| `velis_cache_fallback_batches_total` | 实际回源批次或计划重建次数 | `object`；由应用编排在实际批量回源后调用 `Observer.Fallback`，card/latest 一次实际数据库批量请求算一批（失败也计）；recommend 一次首查计划重建尝试计一批，可能包含召回、Embedding、多批当前事实和排除查询，不能当作一条 SQL；不把 miss 数或 Redis 绕过数冒充回源 |
| `velis_cache_operation_duration_seconds` | 秒 | `object`、`operation`、受控 `reason`；记录每次批量调用耗时，包括连接、池等待和响应；预算绕过也记录短耗时；Histogram 的 count 是操作数 |

例如 MGET 三项中一项命中、一项不存在、一项损坏：hit +1、miss +2、corrupt +1；随后一次批量加载缺失片段，fallback_batches +1。损坏是数据未命中，不触发请求级传输故障绕过。传输失败或超时使该请求后续所有缓存操作直接绕过；回填错误不会改变已成功的事实源读取。

调用者必须在请求入口调用 `Cache.NewRequest(ctx)`，并把返回 context 传给计划、latest、卡片、回填等操作。再次调用会保留已有预算，不能在批次或子用例重建额度。未携带预算的操作直接绕过；提交后失效须在最外层提交后创建独立有界预算，该动作同步执行并受进程生命周期和独立截止时间限制，不继承原请求预算。

预算默认单次 50ms、累计 100ms，以 Redis 操作实际耗时扣减，不扣除业务依赖耗时；每次子 context 取单次与剩余额度最小值，父 deadline 自动约束，父取消仍返回原取消错误。在线命令和连接均不自动重试。绝对 PXAT 到期时间避免网络延迟或迟到回填延长旧载荷寿命。

指标只接受固定枚举标签，未知标签拒绝。日志只包含 request_id、对象类型、操作、受控结果/原因；不记录 SDK 原错误、Redis 地址或凭据、缓存键、用户身份、画像、游标或正文。适配器的数据/故障日志不打印底层异常字符串。
