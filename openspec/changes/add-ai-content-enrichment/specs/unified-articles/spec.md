# Spec Delta

## ADDED Requirements

### Requirement: 匿名文章读取返回当前修订的可选增强结果

`GET /api/v1/articles` 和 `GET /api/v1/articles/{id}` SHALL 为每篇公开文章返回可为空的 `enhancement` 对象；非空对象 SHALL 只来自该文章当前修订选中的成功生成结果，并包含摘要、关键词、主题和生成时间，不得包含向量、Prompt、provider、model、Token、错误或内部任务状态。

#### Scenario: 当前修订具有增强结果
- **WHEN** 匿名调用者读取的公开文章当前修订已选中成功增强结果
- **THEN** latest 项与详情 SHALL 返回相同的非空 `enhancement`，其中摘要、关键词、主题均为经过服务端约束的纯文本

#### Scenario: 当前修订尚无增强结果
- **WHEN** 模型未配置、任务尚未完成、生成失败或仅旧修订具有结果
- **THEN** API SHALL 返回 `enhancement: null`，保留原始 `excerpt` 与其他既有字段并正常完成请求

#### Scenario: 文章编辑产生新修订
- **WHEN** 已展示增强结果的文章切换到新的当前修订且新修订结果尚未成功
- **THEN** 后续 latest 与详情读取 SHALL 立即返回 `enhancement: null`，不得继续展示旧修订摘要或标签

#### Scenario: 同一修订升级 profile
- **WHEN** 当前修订的新 Prompt 或 model 结果尚未成功
- **THEN** API SHALL 继续返回该修订最近选中的成功结果，并在新结果原子切换后返回新内容

#### Scenario: 增强结果异步到达
- **WHEN** 一篇公开文章在发布后完成增强
- **THEN** 后续读取 MAY 从 null 变为非空 `enhancement`，但文章有效发布时间、latest 排序和游标语义 SHALL 保持不变

### Requirement: Web 优先展示增强内容并明确降级

latest 与文章详情 Web SHALL 在当前修订存在增强结果时优先展示 AI 摘要、关键词和主题，并明确标识这些内容由 AI 生成；增强结果为空时 SHALL 展示现有 `excerpt` 作为降级，且不得因 AI 状态阻止正文阅读。

#### Scenario: latest 展示增强结果
- **WHEN** latest 项包含非空 `enhancement`
- **THEN** Web SHALL 使用 AI 摘要替代卡片中的原始截断摘要，并以可区分的标签展示关键词和主题

#### Scenario: 详情展示增强结果
- **WHEN** 文章详情包含非空 `enhancement`
- **THEN** Web SHALL 在正文之外展示 AI 摘要、关键词、主题和 AI 生成标识，且不得把这些字段解释为作者原文

#### Scenario: 缺少增强结果时降级
- **WHEN** latest 项或详情的 `enhancement` 为 null
- **THEN** Web SHALL 继续展示原始 `excerpt` 和正文，不显示错误占位、无限加载或阻塞阅读

#### Scenario: 模型文本包含 HTML
- **WHEN** 摘要、关键词或主题包含 HTML 特殊字符或类似标记的文本
- **THEN** Web SHALL 按普通文本转义渲染，不得作为 HTML 注入页面
