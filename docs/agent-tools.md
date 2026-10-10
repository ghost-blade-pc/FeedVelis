# Agent 内部只读文章工具

应用入口为 `application/agenttools.Service` 的 Search/Recommend/Get（强类型）及对应 JSON 适配方法；没有工具 HTTP、CLI、MCP 服务或执行记录表。调用者账户身份通过独立 `Caller` 参数传入，不能从模型 JSON 获取。后续编排必须先确认本人会话；工具本身不依赖会话ID，也不生成回答或写入消息。

| 工具 | JSON输入 | 固定边界 |
| --- | --- | --- |
| search_articles | `{"q":"Go","keyword":"后端","topic":"技术","source_id":1,"limit":5}` | q必需，规范化1–200 code point；keyword/topic可选1–64；source_id正int64；limit省略5、范围1–10 |
| recommend_articles | `{"limit":5}` | 仅接受limit，无自由文本；按可信账户读取本人画像与有效排除 |
| get_article | `{"article_id":1,"max_chars":4000}` | 正int64文章ID必需；max_chars省略4000、范围1–8000 code point |

JSON拒绝未知或重复字段、尾随JSON、null、错误类型、非法UTF-8、NUL和未配对Unicode代理项。类型化入口与JSON入口共享语义校验；非法输入不访问依赖。身份不能匿名回退，伪造user_id/conversation_id、游标、SQL或抓取URL均被拒绝。

搜索与公开查询共用规范化、精确筛选、BM25/混合召回、排序和最终批量当前事实复核。一批结果不生成续页游标；一次查询会在成功/失败/取消后释放自身最新PIT，使用独立最多250ms清理上下文，失败由有限keep_alive兜底且可观测。一次混合查询在总deadline内顺序完成BM25与可选语义分支，避免取消后仍有并行分支遗留PIT身份；原HTTP的并行召回与分页入口保持可用。推荐复用本人计划与最终排除，保留cold_start/personalized/latest_fallback、degraded、可空degrade_reason和最小recommendation_reason，无正向画像时仍排除本人负反馈。

ArticleRef含article_id、revision_id、title、origin_type、source、author、path、original_url、summary、summary_source、metadata_truncated。source只含公开id/title；author为站内id/nickname或RSS显示name；path为`/articles/{id}`，RSS原文链接可空。summary_source为model/extractive/excerpt。摘要最多1000 code point，标题/来源/作者显示字段最多256，裁剪设置metadata_truncated。搜索为items/truncated，推荐再含模式及原因，正文再含content/truncated。不返回分数、画像权重、向量、Prompt、凭据或内部任务字段。

卡片与修订身份在最终事实装配时一并取得，不另查revision拼接旧卡片。Redis及索引不是事实源，命中仍复核当前公开性；当前增强缺失使用当前excerpt，旧修订摘要不能沿用。正文单条SQL读取当前公开修订与持久化plain_text，并按Unicode前缀截取；草稿/下架/删除/不存在统一ARTICLE_NOT_FOUND，作者或管理员也不能越权。

每次业务执行默认5秒，调用者更短deadline优先；取消传递至依赖，工具层不重试。成功紧凑JSON默认最多128KiB，按实际序列化字节计算，先执行展示字段上限，再缩减正文前缀或移除尾部条目，保留身份、链接和相对顺序并标记截断。truncated可能表示候选/扫描、条数或输出预算用尽，不保证检索穷尽。空items表示成功空结果，不能替代错误。

稳定工具错误为VALIDATION_FAILED、ARTICLE_NOT_FOUND、SEARCH_UNAVAILABLE、DEPENDENCY_UNAVAILABLE、TOOL_TIMEOUT、TOOL_CANCELED、INTERNAL_ERROR，不携带底层响应。搜索整体故障明确失败，单独语义失败沿用BM25降级；推荐搜索故障在事实源正常时latest回退，事实或排除集故障拒绝返回。主动取消优先TOOL_CANCELED，总预算耗尽为TOOL_TIMEOUT。

执行不会更改会话、消息、阅读、收藏、负反馈、画像或文章事实。只允许既有缓存填充、临时PIT和固定低基数观测副作用，不保存逐次工具调用记录。指标仅固定工具/操作、结果、降级、截断、数量与耗时，日志不记录查询、正文、画像、键、游标、PIT或凭据。查询Embedding仅复用既有预算，测试使用确定性桩，不调用付费生成模型。

专用真实验收入口为 `make integration-agent-tools`，必须同时提供 `VELIS_TEST_DATABASE_URL`（专用`_test`库）、`VELIS_TEST_OPENSEARCH_URL` 和 `VELIS_TEST_REDIS_ADDRESS`，缺少任意变量立即非零退出。测试管理自己的随机Redis namespace和搜索索引，自有代理注入故障，不停止共享服务、不清空整个Redis。
