# 设计图 — 文章展示基础能力

> 本文用于可视化 `spec.md` 已确认的业务契约。若图与文字规格出现冲突，以 `spec.md` 为准，并在进入 Apply 前修正图示。

## 图示进度

| 图示 | 状态 | 确认结果 |
|---|---|---|
| 1. 用例图 | 已完成 | 开发者已确认 |
| 2. 业务流程图 | 已完成 | 开发者已确认 |
| 3. ER 图 | 已完成 | 开发者已确认 |
| 4. 流程时序图 | 已完成 | 开发者已确认 |
| 5. 后端四层实现架构图 | 已完成 | 开发者已确认 |

## 1. 用例图

```mermaid
flowchart LR
    operator["本地运维 / 管理员"]
    clock["系统时钟"]
    reader["匿名阅读者"]
    upstream["外部 Feed / 原文站点"]

    subgraph velis["Velis：文章展示基础能力"]
        direction TB

        addSource(["添加 Source"])
        listSources(["查看 Source"])
        pauseSource(["暂停 Source"])
        resumeSource(["恢复 Source"])
        manualFetch(["手动抓取 Source"])
        scheduledFetch(["定时抓取到期 Source"])

        safeFetch(["安全获取 Feed"])
        parseFeed(["解析 RSS 2.0 / Atom / JSON Feed"])
        sanitizeContent(["限制并清理文章内容"])
        ingestArticles(["幂等保存 / 更新文章"])
        updateFetchState(["更新条件请求、租约与退避状态"])

        listArticles(["分页浏览最新文章"])
        openOriginal(["打开原站文章"])
    end

    operator --> addSource
    operator --> listSources
    operator --> pauseSource
    operator --> resumeSource
    operator --> manualFetch
    clock --> scheduledFetch
    reader --> listArticles
    reader --> openOriginal

    manualFetch -.->|"«include»"| safeFetch
    scheduledFetch -.->|"«include»"| safeFetch
    safeFetch -.->|"«include»"| updateFetchState
    parseFeed -.->|"«extend», HTTP 200"| safeFetch
    parseFeed -.->|"«include»"| sanitizeContent
    parseFeed -.->|"«include»"| ingestArticles

    safeFetch -->|"HTTP(S) Feed 请求"| upstream
    upstream -->|"Feed 响应"| safeFetch
    openOriginal -->|"浏览器跳转"| upstream
```

### 边界说明

- “本地运维 / 管理员”通过 `velis-admin source add/list/pause/resume/fetch` 管理系统级 Source；本次不提供 Source HTTP 写接口。
- “系统时钟”只表示定时触发者，调度和 Worker 仍属于 Velis 内部能力。
- “匿名阅读者”只能浏览文章列表并跳转原站；本次没有账号、订阅、站内文章详情、互动或推荐用例。
- “外部 Feed / 原文站点”位于系统边界外。Velis 仅接受 HTTP(S)，抓取需执行 SSRF、重定向、超时和响应大小限制。
- Feed 返回 HTTP 200 时才扩展执行解析和入库；HTTP 304 只更新抓取状态，不解析文章。
- 用例图表达参与者与系统职责，不表达实际执行先后；抓取分支、失败处理和事务顺序将在业务流程图与流程时序图中展开。

## 2. 业务流程图

```mermaid
flowchart TD
    subgraph source_management["1. Source 登记与抓取触发"]
        operator_add(["管理员执行 source add"])
        normalize_source["规范化 Feed URL"]
        valid_source_url{"是否为允许的 HTTP(S) URL？"}
        reject_source["返回稳定参数错误"]
        source_exists{"normalized_feed_url 已存在？"}
        return_existing["幂等返回已有 Source ID"]
        create_source["创建 active Source<br/>next_fetch_at = now"]

        schedule_tick(["30 分钟调度 Tick"])
        claim_sources["按 next_fetch_at 认领最多 20 个 active Source<br/>SKIP LOCKED，写入 2 分钟租约"]
        manual_fetch(["管理员执行 source fetch"])
        manual_status{"Source 状态？"}
        require_resume["paused：要求先执行 source resume"]

        operator_add --> normalize_source --> valid_source_url
        valid_source_url -->|"否"| reject_source
        valid_source_url -->|"是"| source_exists
        source_exists -->|"是"| return_existing
        source_exists -->|"否"| create_source
        schedule_tick --> claim_sources
        manual_fetch --> manual_status
        manual_status -->|"paused"| require_resume
    end

    subgraph safe_fetch["2. 安全抓取与结果分支"]
        request_feed["每次 DNS 解析及重定向均校验目标<br/>携带 ETag / Last-Modified 请求 Feed"]
        fetch_result{"抓取结果？"}
        not_modified["HTTP 304<br/>不解析、不更新 Article.last_seen_at"]
        parse_feed["限制解压后响应为 5 MiB<br/>解析 RSS 2.0 / Atom / JSON Feed"]
        document_valid{"Feed 文档能否解析？"}
        fetch_failure["记录稳定错误码<br/>consecutive_failures + 1"]
        failure_limit{"连续失败达到 5 次？"}
        schedule_backoff["按 1m–6h 指数退避并加抖动<br/>清除租约"]
        mark_degraded["标记 degraded，停止自动认领<br/>保留人工 source fetch 恢复入口"]

        claim_sources --> request_feed
        manual_status -->|"active / degraded"| request_feed
        request_feed --> fetch_result
        fetch_result -->|"HTTP 304"| not_modified
        fetch_result -->|"HTTP 200"| parse_feed
        fetch_result -->|"协议 / SSRF / 超时 / 超限 / HTTP 失败"| fetch_failure
        parse_feed --> document_valid
        document_valid -->|"否"| fetch_failure
        fetch_failure --> failure_limit
        failure_limit -->|"否"| schedule_backoff
        failure_limit -->|"是"| mark_degraded
    end

    subgraph ingest["3. 条目校验、清理与幂等入库"]
        next_item{"读取下一条<br/>最多 500 条"}
        item_available{"还有条目？"}
        validate_item{"canonical URL 为 HTTP(S)，且<br/>能够生成 dedupe_key？"}
        skip_item["跳过该条并累计稳定原因码"]
        normalize_item["规范化字段并按 UTF-8 边界截断 raw 内容"]
        sanitize_item["清理 HTML，生成 plain_text 与 excerpt"]
        calculate_keys["计算 dedupe_key 与 content_hash"]
        article_exists{"source_id + dedupe_key 已存在？"}
        insert_article["事务内新增 Article + ArticleContent<br/>设置 discovered_at / last_seen_at"]
        content_changed{"content_hash 是否变化？"}
        mark_seen["更新 last_seen_at 与允许的非内容元数据<br/>不触碰正文与 Article.updated_at"]
        update_article["事务内更新 Article + ArticleContent<br/>保留 discovered_at"]
        ingest_success["抓取成功：保存条件请求状态<br/>失败计数归零、恢复 active、安排下次抓取、清除租约"]
        keep_old_articles["本次未出现的历史文章保持不变<br/>不自动隐藏或删除"]

        document_valid -->|"是"| next_item
        next_item --> item_available
        item_available -->|"是"| validate_item
        item_available -->|"否"| ingest_success
        validate_item -->|"否"| skip_item --> next_item
        validate_item -->|"是"| normalize_item --> sanitize_item --> calculate_keys --> article_exists
        article_exists -->|"否"| insert_article --> next_item
        article_exists -->|"是"| content_changed
        content_changed -->|"否"| mark_seen --> next_item
        content_changed -->|"是"| update_article --> next_item
        not_modified --> ingest_success
        ingest_success -.-> keep_old_articles
    end

    subgraph article_display["4. 文章列表读取与展示"]
        reader_open(["匿名阅读者打开 /latest"])
        request_articles["Web 请求 GET /api/v1/articles"]
        valid_query{"limit 与版本化 cursor 合法？"}
        invalid_cursor["返回 INVALID_ARGUMENT / INVALID_CURSOR"]
        show_input_error["Web 展示参数错误"]
        list_query["只查询 published Article + Source 小字段<br/>按 sort_at DESC, id DESC 读取 limit + 1"]
        query_result{"查询结果？"}
        query_failed["返回稳定 INTERNAL_ERROR<br/>Web 展示失败与重试"]
        empty_state["Web 展示空状态"]
        render_cards["返回 items / next_cursor / has_more<br/>Web 以文本节点渲染文章卡片"]
        published_at{"source_published_at 有值？"}
        show_published["显示“发布于”"]
        show_discovered["显示“收录于”"]
        open_origin(["点击标题，以安全新窗口打开原站"])

        ingest_success -.->|"文章可读"| list_query
        reader_open --> request_articles --> valid_query
        valid_query -->|"否"| invalid_cursor --> show_input_error
        valid_query -->|"是"| list_query --> query_result
        query_result -->|"数据库失败"| query_failed
        query_result -->|"无文章"| empty_state
        query_result -->|"有文章"| render_cards --> published_at
        published_at -->|"是"| show_published --> open_origin
        published_at -->|"否"| show_discovered --> open_origin
    end
```

### 流程约束说明

- Source 重复添加是幂等成功，不创建重复记录；新 Source 立即进入可抓取状态。
- 自动抓取仅认领 `active` 且到期的 Source；`paused` 不参与调度，`degraded` 只能通过人工抓取成功后恢复。
- HTTP 304 视为一次成功检查，但不解析 Feed，也不更新任何 Article 的 `last_seen_at`。
- 单条无效文章只被跳过，不回滚同批其他合法条目；完整 Feed 无法解析则按抓取失败处理，旧文章保持可用。
- `content_hash` 未变化时只更新仍出现文章的 `last_seen_at`；Feed 中消失的历史文章不自动删除或隐藏。
- 文章列表不读取 `article_contents` 大字段；分页游标由 `(sort_at, id)` 构成，Web 不自行重排。

## 3. ER 图

```mermaid
erDiagram
    SOURCES ||--o{ ARTICLES : "提供"
    ARTICLES ||--|| ARTICLE_CONTENTS : "拥有"

    SOURCES {
        bigint id PK "GENERATED ALWAYS AS IDENTITY"
        text feed_url "NOT NULL，最多 4096 bytes"
        text normalized_feed_url UK "NOT NULL，规范化业务键"
        text site_url "NULL，仅 HTTP(S)"
        varchar title "NOT NULL，最多 500 字符"
        varchar status "active / paused / degraded"
        text etag "NULL，最多 1024 bytes"
        text last_modified "NULL，最多 1024 bytes"
        timestamptz next_fetch_at "NOT NULL"
        timestamptz last_checked_at "NULL"
        timestamptz last_success_at "NULL"
        integer consecutive_failures "NOT NULL，非负"
        varchar last_error_code "NULL，最多 64 字符"
        varchar lease_owner "NULL，最多 128 字符"
        timestamptz lease_expires_at "NULL"
        timestamptz created_at "NOT NULL"
        timestamptz updated_at "NOT NULL"
    }

    ARTICLES {
        bigint id PK "GENERATED ALWAYS AS IDENTITY"
        bigint source_id FK "NOT NULL，ON DELETE RESTRICT"
        char dedupe_key "NOT NULL，SHA-256，char(64)"
        text source_item_id "NULL，最多 2048 bytes"
        text canonical_url "NOT NULL，最多 4096 bytes，仅 HTTP(S)"
        varchar title "NOT NULL，最多 500 字符"
        varchar author_name "NULL，最多 300 字符"
        text excerpt "NOT NULL，最多 1000 字符"
        varchar language "NOT NULL，默认 und"
        timestamptz source_published_at "NULL，上游发布时间"
        timestamptz discovered_at "NOT NULL，首次收录时间"
        timestamptz sort_at "GENERATED STORED，发布时间或收录时间"
        timestamptz source_updated_at "NULL，上游更新时间"
        char content_hash "NOT NULL，SHA-256，char(64)"
        varchar status "published / hidden"
        timestamptz last_seen_at "NOT NULL"
        timestamptz created_at "NOT NULL"
        timestamptz updated_at "NOT NULL"
    }

    ARTICLE_CONTENTS {
        bigint article_id PK, FK "关联 articles，ON DELETE CASCADE"
        text raw_description "NULL，应用层最多保留 256 KiB"
        text raw_content "NULL，应用层最多保留 1 MiB"
        boolean raw_description_truncated "NOT NULL"
        boolean raw_content_truncated "NOT NULL"
        text sanitized_html "NULL，allowlist 清理后的派生物"
        text plain_text "NOT NULL，可重建派生物"
        integer sanitizer_version "NOT NULL，首版为 1"
        timestamptz created_at "NOT NULL"
        timestamptz updated_at "NOT NULL"
    }
```

### 关系与约束说明

- 一个 `source` 可以暂时没有文章，也可以拥有多篇文章；每篇 `article` 必须且只能属于一个 `source`。
- 每篇 `article` 在业务上必须且只能有一条 `article_content`。Article 与 ArticleContent 在同一事务中写入，正文分表使列表查询无需搬运大字段。
- `articles.source_id` 使用 `ON DELETE RESTRICT`：Source 的日常“删除”意图只映射为 `paused`，不能级联删除历史文章。
- `article_contents.article_id` 使用 `ON DELETE CASCADE`，仅服务于未来独立且受控的 Article 物理清理操作；本 change 不提供该操作。
- `articles` 的业务唯一键是组合约束 `UNIQUE(source_id, dedupe_key)`；图中的 `dedupe_key` 不能被理解为全局唯一。
- `sort_at` 是数据库存储生成列：`COALESCE(source_published_at, discovered_at)`，确保发布时间缺失时仍能稳定分页。

### 核心索引与 CHECK

| 表 | 约束或索引 | 用途 |
|---|---|---|
| `velis.sources` | `UNIQUE(normalized_feed_url)` | Source 重复添加时幂等返回已有记录。 |
| `velis.sources` | `(next_fetch_at, id) WHERE status = 'active'` | Worker 按到期时间认领 Source。 |
| `velis.sources` | status、非负失败计数、租约字段成对为空/非空 CHECK | 保护 Source 状态与租约不变量。 |
| `velis.articles` | `UNIQUE(source_id, dedupe_key)` | 并发抓取下的最终去重防线。 |
| `velis.articles` | `(sort_at DESC, id DESC) WHERE status = 'published'` | 最新文章 keyset 分页。 |
| `velis.articles` | `(source_id, last_seen_at DESC)` | 按来源排查最近一次出现时间。 |
| `velis.articles` | status CHECK | 首版只允许 `published / hidden`。 |

### 明确不建的关系

- 本阶段没有用户与订阅关系，因此不创建 `users`、`subscriptions` 或 `source_user_relations`。
- 不创建 `source_fetch_logs`；`sources` 只保存当前抓取状态，历史诊断依赖结构化日志。
- 不创建标签、统计、搜索、Embedding、Revision 或互动相关表。

## 4. 流程时序图

```mermaid
sequenceDiagram
    autonumber
    actor Trigger as 系统时钟 / 管理员
    participant Entry as Scheduler / velis-admin
    participant SourceUC as Source 管理 / 到期认领应用服务
    participant FetchUC as FetchSource 应用服务
    participant Fetcher as 安全 HTTP Fetcher
    participant FeedSite as 外部 Feed / 原文站点
    participant Processor as Feed Parser / Sanitizer
    participant DB as PostgreSQL
    participant ArticleAPI as Article List API
    participant Web as Web /latest
    actor Reader as 匿名阅读者

    alt 自动调度
        Trigger->>Entry: 系统时钟触发 30 分钟 Tick
        Entry->>SourceUC: FetchDueSources(worker_id, limit=20)
        SourceUC->>DB: 认领到期 active Source<br/>FOR UPDATE SKIP LOCKED，写入 2 分钟租约
        DB-->>SourceUC: 返回已认领的 Source
    else 人工抓取
        Trigger->>Entry: 管理员执行 source fetch
        Entry->>SourceUC: 请求人工抓取指定 Source
        SourceUC->>DB: 读取 active / degraded Source<br/>并取得本次抓取资格
        DB-->>SourceUC: 返回 Source
    end

    loop 对每个 Source 串行执行一次抓取
        SourceUC->>FetchUC: FetchSource(source)
        FetchUC->>Fetcher: Fetch(feed_url, etag, last_modified)
        Fetcher->>Fetcher: 校验 HTTP(S)、端口及 DNS/IP
        Fetcher->>FeedSite: 发起带条件请求头的 HTTP 请求

        opt 每次重定向，最多 5 次
            FeedSite-->>Fetcher: 3xx + Location
            Fetcher->>Fetcher: 重新解析并校验目标 URL 与 DNS/IP
            Fetcher->>FeedSite: 请求校验通过的新地址
        end

        FeedSite-->>Fetcher: HTTP 响应
        Fetcher-->>FetchUC: 304、受限的 200 响应体或稳定错误

        alt HTTP 304 Not Modified
            FetchUC->>DB: 更新 last_checked_at / last_success_at<br/>失败计数归零，安排下次抓取并清除租约
            Note over FetchUC,DB: 不解析 Feed，也不更新 Article.last_seen_at
        else HTTP 200 且 Feed 可解析
            FetchUC->>Processor: 解析 RSS 2.0 / Atom / JSON Feed<br/>响应体不超过 5 MiB，最多返回 500 条
            Processor-->>FetchUC: Feed 元数据与原始条目

            loop 逐条处理
                FetchUC->>FetchUC: 校验 canonical URL，生成 dedupe_key
                alt 条目无效
                    FetchUC->>FetchUC: 跳过并累计稳定原因码
                else 条目有效
                    FetchUC->>Processor: 截断 raw 内容、清理 HTML<br/>生成 plain_text / excerpt
                    Processor-->>FetchUC: 返回受限 raw、sanitized_html、plain_text 与 excerpt
                    FetchUC->>FetchUC: 按固定业务字段计算 content_hash
                    FetchUC->>DB: 原子写入 Article + ArticleContent<br/>以 source_id + dedupe_key 判定身份
                    alt 新文章
                        DB-->>FetchUC: inserted，设置 discovered_at / last_seen_at
                    else 内容发生变化
                        DB-->>FetchUC: updated，保留 discovered_at
                    else 内容未变化
                        DB-->>FetchUC: unchanged，仅更新 last_seen_at<br/>及允许的非内容元数据
                    end
                end
            end

            FetchUC->>DB: 保存 ETag / Last-Modified 和成功状态<br/>恢复 active、安排下次抓取并清除租约
            Note over FetchUC,DB: 本次未出现的历史文章保持不变
        else 抓取、解析或入库失败
            FetchUC->>FetchUC: 计算 1m–6h 指数退避与抖动
            FetchUC->>DB: 记录稳定错误码并增加失败计数<br/>达到 5 次则 degraded，最后清除租约
            Note over FetchUC,DB: 失败不删除已保存文章；degraded 可由人工抓取恢复
        end
    end

    Reader->>Web: 打开 /latest
    Web->>ArticleAPI: GET /api/v1/articles?cursor=&limit=
    ArticleAPI->>ArticleAPI: 校验 limit 与版本化 cursor
    ArticleAPI->>DB: 查询 published Article + Source 小字段<br/>ORDER BY sort_at DESC, id DESC，LIMIT limit + 1
    DB-->>ArticleAPI: 返回文章行
    ArticleAPI-->>Web: items / next_cursor / has_more
    Web-->>Reader: 以文本节点展示文章卡片<br/>显示“发布于”或“收录于”
    Reader->>FeedSite: 点击标题，以安全新窗口跳转原站
```

### 时序职责说明

- `Scheduler / velis-admin` 只负责协议适配和触发 Application；到期认领、指定 Source 读取和抓取资格判定由 Source Application Service 负责，Interfaces 不直接访问 PostgreSQL。
- `FetchDueSources` 将已认领 Source 逐个交给 `FetchSource`；抓取编排、成功/失败判定由 `FetchSource` 应用服务负责。
- Fetcher 是唯一可以访问 Feed 网络地址的组件，并在初始请求及每次重定向前重新执行 URL、DNS 和目标 IP 校验；Parser 不联网。
- Parser/Sanitizer 负责格式解析和内容派生，不直接写数据库；Article 与 ArticleContent 的一致写入由 PostgreSQL Repository 事务保证。
- 数据库唯一约束是并发去重的最终防线。重复执行同一 Source 抓取应得到相同 Article 身份，并安全收敛到 inserted、updated 或 unchanged。
- 列表 API 只读取 Article 与 Source 小字段，不读取 ArticleContent；Web 使用服务端顺序和游标，不在客户端重新排序。
- 图中省略了日志与指标调用；它们只能记录 Source ID、稳定结果码、耗时和计数，不记录 Feed 正文或完整 URL query。

## 5. 后端四层实现架构图

```mermaid
flowchart LR
    operator["本地管理员"]
    clock["系统时钟"]
    web["Web /latest"]
    feedSite["外部 Feed 站点"]
    postgres[("PostgreSQL")]

    subgraph entrypoints["进程入口与 Bootstrap / Composition Root"]
        direction TB
        adminProcess["velis-admin<br/>新增入口与装配"]
        workerProcess["velis-worker<br/>修改 Worker 装配"]
        apiProcess["velis-api<br/>修改 API 装配"]
    end

    subgraph interfaces["Interfaces"]
        direction TB
        cli["interfaces/cli<br/>Source add / list / pause / resume / fetch"]
        scheduler["interfaces/scheduler<br/>定时 Tick 与优雅停止"]
        articleHTTP["interfaces/http/hertz<br/>Article Handler / DTO / Presenter / Router"]
    end

    subgraph application["Application"]
        direction TB
        sourceManagement["application/source<br/>SourceManagement<br/>添加、查询、暂停、恢复"]
        fetchDue["application/source<br/>FetchDueSources<br/>认领到期 Source"]
        fetchSource["application/source<br/>FetchSource<br/>单 Source 抓取编排与状态收敛"]
        ingestArticles["application/article<br/>IngestExternalArticles<br/>规范化、去重与事务入库"]
        listArticles["application/article<br/>ListArticles<br/>游标校验与只读列表"]
        technicalPorts["application/ports<br/>FeedFetcher / FeedParser / ContentSanitizer<br/>TxManager / Clock"]
    end

    subgraph domain["Domain"]
        direction TB
        sourceDomain["domain/source<br/>Source / Status / FetchState / Lease<br/>状态、退避与租约不变量"]
        sourceRepository["domain/source<br/>SourceRepository"]
        articleDomain["domain/article<br/>Article / ArticleContent<br/>DedupeKey / ContentHash / 更新判定"]
        articleRepository["domain/article<br/>ArticleRepository"]
    end

    subgraph infrastructure["Infrastructure"]
        direction TB
        sourcePG["persistence/postgres<br/>SourceRepository Adapter"]
        articlePG["persistence/postgres<br/>ArticleRepository Adapter / 查询 / 原子 Upsert"]
        txPG["persistence/postgres<br/>TxManager Adapter"]
        safeFetcher["fetcher/httpfeed<br/>安全 HTTP Fetcher"]
        feedParser["fetcher/httpfeed<br/>gofeed Parser Adapter"]
        sanitizer["fetcher/httpfeed<br/>bluemonday Sanitizer Adapter"]
        systemClock["clock<br/>System Clock Adapter"]
        telemetry["observability<br/>结构化日志与指标"]
    end

    operator --> adminProcess --> cli
    clock --> workerProcess --> scheduler
    web --> apiProcess --> articleHTTP

    cli --> sourceManagement
    cli --> fetchSource
    scheduler --> fetchDue
    articleHTTP --> listArticles

    sourceManagement --> sourceDomain
    sourceManagement --> sourceRepository
    fetchDue --> sourceDomain
    fetchDue --> sourceRepository
    fetchDue --> fetchSource
    fetchSource --> sourceDomain
    fetchSource --> technicalPorts
    fetchSource --> ingestArticles
    fetchSource --> sourceRepository
    ingestArticles --> articleDomain
    ingestArticles --> articleRepository
    ingestArticles --> technicalPorts
    listArticles --> articleRepository

    sourcePG -. "实现" .-> sourceRepository
    articlePG -. "实现" .-> articleRepository
    txPG -. "实现" .-> technicalPorts
    safeFetcher -. "实现 FeedFetcher" .-> technicalPorts
    feedParser -. "实现 FeedParser" .-> technicalPorts
    sanitizer -. "实现 ContentSanitizer" .-> technicalPorts
    systemClock -. "实现 Clock" .-> technicalPorts

    safeFetcher --> feedSite
    sourcePG --> postgres
    articlePG --> postgres
    txPG --> postgres
    fetchSource -. "记录结果" .-> telemetry

    adminProcess -. "构造并注入" .-> sourceManagement
    workerProcess -. "构造并注入" .-> fetchDue
    apiProcess -. "构造并注入" .-> listArticles
    adminProcess -. "装配 Adapter" .-> infrastructure
    workerProcess -. "装配 Adapter" .-> infrastructure
    apiProcess -. "装配 Adapter" .-> infrastructure
```

### 图例与依赖约束

- 实线箭头表示运行时调用；“实现”虚线表示 Infrastructure Adapter 实现内层声明的抽象；“构造并注入”虚线只表示 Bootstrap 装配，不代表业务层反向依赖。
- Interfaces 只能调用 Application，不直接访问 PostgreSQL、Fetcher 或第三方库。Application 组织用例与事务，并依赖 Domain 和抽象端口；Infrastructure 依赖内层契约并提供实现。
- Repository 抽象随聚合放在 `domain/source`、`domain/article`；Fetcher、Parser、Sanitizer、事务和时钟属于技术能力，抽象放在 `application/ports`。
- `application/source` 可以调用 `application/article` 的入库用例完成跨模块编排；Article Domain 不依赖 Source Application，只通过 `source_id` 保持归属关系。
- Bootstrap 是 Composition Root，不是第五个业务层；它可以同时依赖四层，但只负责构造、注入、进程生命周期和优雅关闭。

### 分层改动清单

| 层/配套范围 | 包 | 主要组件或接口 | 职责 | 变更类型 |
|---|---|---|---|---|
| Domain | `internal/domain/source` | `Source`、`Status`、`FetchState`、`Lease`、`SourceRepository` | 维护 Source 身份、状态切换、租约、成功/失败收敛、退避和下次抓取时间等不变量；声明 Source 持久化抽象 | 在占位包中新增业务实现，并更新包说明 |
| Domain | `internal/domain/article` | `Article`、`ArticleContent`、`DedupeKey`、`ContentHash`、更新判定、`ArticleRepository` | 维护外部文章稳定身份、内容版本判定、首次收录与最近出现语义；声明 Article/Content 一致持久化和列表读取抽象 | 在占位包中新增业务实现，并更新包说明 |
| Application | `internal/application/source` | `SourceManagement`、`FetchDueSources`、`FetchSource` Command/Result/View | 编排 Source 添加、查询、暂停、恢复、认领和单次抓取；调用抓取/解析端口与 Article 入库用例；统一更新成功、304、失败和 degraded 状态 | 在占位包中新增用例实现 |
| Application | `internal/application/article` | `IngestExternalArticles`、`ListArticles`、游标模型、Article Card View | 将标准化条目转换为领域输入，计算稳定身份与内容变化，控制 Article/Content 事务；提供不读取正文的稳定分页查询 | 在占位包中新增命令、查询和只读模型 |
| Application | `internal/application/ports` | `FeedFetcher`、`FeedParser`、`ContentSanitizer`、`TxManager`、`Clock` | 隔离网络、第三方 Feed/HTML 库、事务与系统时间；端口输入输出只使用项目自有类型 | 修改现有 ports 范围，新增技术端口 |
| Infrastructure | `internal/infrastructure/persistence/postgres` | Source Repository Adapter、Article Repository Adapter、列表 Query、原子 Upsert、TxManager Adapter | 使用显式 `velis.` schema 实现唯一约束、租约认领、Article/Content 原子写入和 keyset 查询；把 pgx 类型限制在 Infrastructure | 扩展现有 PostgreSQL 包；保留现有连接池能力 |
| Infrastructure | `internal/infrastructure/fetcher/httpfeed` | Safe HTTP Fetcher、gofeed Parser Adapter、bluemonday Sanitizer Adapter | 实现条件请求、SSRF/DNS/重定向/超时/大小限制，解析三种 Feed，并生成受限 raw、sanitized HTML 与纯文本 | 在占位包中新增 Adapter 实现 |
| Infrastructure | `internal/infrastructure/clock` | System Clock Adapter | 为调度、租约、退避和测试提供可替换时间源 | 在占位包中新增 Adapter 实现 |
| Infrastructure | `internal/infrastructure/observability` | Fetch/Ingest 结构化日志与指标记录 | 记录稳定结果码、耗时和计数；禁止正文和完整 URL query 进入日志或指标标签 | 修改现有观测能力 |
| Interfaces | `internal/interfaces/cli` | Source Commands 与输入/输出映射 | 解析 `source add/list/pause/resume/fetch` 参数、调用 Application、映射退出码；不承载业务规则或数据库访问 | 在占位包中新增 CLI Adapter |
| Interfaces | `internal/interfaces/scheduler` | Tick Runner、取消与优雅停止 | 按周期触发 `FetchDueSources`，传递 Worker 身份和取消信号；不直接认领数据库记录 | 在占位包中新增 Scheduler Adapter |
| Interfaces | `internal/interfaces/http/hertz` | Article Handler、DTO、Presenter、Router | 绑定并校验 `cursor/limit`、调用 `ListArticles`、输出统一 JSON 信封和稳定错误码 | 修改现有 Hertz HTTP 包 |
| Composition Root | `internal/bootstrap` | API、Worker、Admin 装配 | 创建 Repository 和技术 Adapter，注入 Application 与 Interfaces，管理连接池和进程生命周期 | 修改 API/Worker 装配，新增 Admin 装配能力 |
| 进程入口 | `backend/cmd` | `velis-admin` | 加载现有配置与日志，启动受控本地管理 CLI | 新增进程入口；现有 API/Worker 入口按装配需要做最小修改 |
| 数据与契约 | `backend/migrations`、`backend/api/openapi`、配置 | Source/Article/Content migration、Article List OpenAPI、抓取/调度配置 | 建立可回滚 schema、公开只读列表契约，并提供安全抓取和调度参数 | 新增 migration，修改 OpenAPI 与配置 |
| Web | `web/src` | Article API Client、类型、`features/article`、`/latest` View | 请求文章列表、展示文本卡片和安全原站链接；不进入后端四层 | 修改现有占位页面并新增文章展示能力 |

### 明确不修改的模块

- `domain/application` 下的 account、feed、interaction、relation、exposure、recommendation、embedding 不参与本次闭环；不为未来能力预建跨模块抽象。
- Redis、RabbitMQ/Outbox、MinIO、pgvector、Eino 适配器不接入本次运行链路；Worker 直接调用 Application Service。
- 本次不新增 Source HTTP 写接口、Article 详情接口、用户鉴权、订阅关系、互动、推荐、搜索或站内正文渲染。
- 上表锁定包、主要组件/接口、职责和变更类型；具体 `.go` 文件、结构体字段、函数签名与文件级实施顺序留到 `tasks.md` 细化和 Apply 阶段决定。
