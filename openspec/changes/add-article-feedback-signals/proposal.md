# Proposal

## Why

I4.1–I4.3 已提供公开文章检索，但系统尚无用户阅读、收藏和明确负反馈事实，后续 recommend 无法按真实偏好排序。先交付最小反馈闭环，可以在推荐实现前积累登录用户信号，同时把身份隔离、重复请求和抑制语义固定下来。

## What Changes

- 新增登录用户对公开文章的阅读、收藏和“不感兴趣”反馈；匿名阅读保持现有公开读取行为，不创建跨会话身份或画像。
- 提供阅读上报、收藏设置/取消、负反馈设置/取消和本人文章反馈状态读取 API，明确认证、可见性、幂等及错误语义。
- 阅读按固定时间窗口保存必要事实并限制画像权重；收藏和负反馈作为每用户每文章唯一状态，重复提交不累加。
- 负反馈首版只明确作用于文章，保留 180 天；聚合端口提供文章排除集和有界主题/来源负向证据，供后续 recommend 在排序时解释抑制原因。
- 在 Web 文章详情和文章卡片提供登录态交互、状态同步与失败恢复；不改变匿名 latest/详情和搜索的公开契约。
- 新增版本化 PostgreSQL 迁移、行为测试、OpenAPI 与实施后的 README/Roadmap 更新。
- 本 change 不实现 recommend Feed、Redis 个性化缓存、社交计数或匿名跟踪。

## Capabilities

### New Capabilities

- `article-feedback`：登录用户文章反馈的持久化、HTTP/Web 交互、阅读限权及推荐画像读取契约。

### Modified Capabilities

无。现有公开文章读取与搜索要求保持不变；反馈通过独立的本人资源 API 提供。

## Impact

- 后端：新增反馈 Domain/Application、PostgreSQL 仓储及迁移、Hertz 本人资源路由/DTO、bootstrap 装配和推荐画像读取端口。
- Web：文章详情、latest/搜索复用卡片、认证会话和反馈 API 客户端；未登录用户仍可阅读公开文章。
- 契约与运行：新增受保护的 `/api/v1/me/articles/{article_id}/...` 端点；反馈事实仅依赖 PostgreSQL，不依赖 OpenSearch、RabbitMQ、Redis 或模型；认证关闭时不注册反馈端点。
