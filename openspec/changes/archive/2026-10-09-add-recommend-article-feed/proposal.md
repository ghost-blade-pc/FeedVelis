# Proposal

## Why

搜索、AI 增强与最小文章反馈已交付，但用户仍只能按时间或显式查询发现文章。I4.5 需要把现有召回与反馈接成可用的 recommend Feed，同时保证搜索故障时阅读入口仍可用。

## What Changes

- 新增匿名可访问、登录后个性化的 `GET /api/v1/articles/recommend`，返回公开文章卡片、最小推荐原因、实际模式与降级状态。
- 使用有界的关键词/主题与可选语义候选，结合阅读、收藏、负反馈、新鲜度和来源打散形成版本化确定性排序；无信号用户使用可预测的冷启动顺序。
- 以独立版本化游标冻结推荐计划与有界候选；候选不足或 OpenSearch 不可用时衔接 latest，并保持分页去重、身份隔离和下架过滤。
- 扩展现有反馈画像端口，提供当前公开文章 AI 关键词的有界偏好证据，供推荐排序使用。
- Web 增加 latest/recommend 切换、推荐原因和明确降级提示；补齐 OpenAPI、配置、运行文档与行为测试。

## Capabilities

### New Capabilities

- `article-recommendation`: 推荐 Feed 的身份、召回与排序、冷启动、稳定分页、降级、可见性、解释和 Web 行为。

### Modified Capabilities

- `article-feedback`: 推荐画像增加有界关键词偏好证据，并维持现有信号隔离、保留期和文章级负反馈语义。

## Impact

- 后端推荐应用服务、OpenSearch 候选适配、PostgreSQL 当前文章读取与反馈画像、HTTP 路由及装配、固定低基数观测。
- Web Feed 页面、API 客户端和类型；实现时同步更新 `backend/api/openapi/velis.yaml`、示例配置、README 与 Roadmap。
- 首版不引入在线学习模型或新的业务事实表；Redis 推荐缓存作为独立后续工作。
