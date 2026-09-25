# 验收记录

本文件记录本 change 的可复现验收命令与结果摘要。环境：本地 Compose 的 PostgreSQL 17、RabbitMQ 4.3.6、OpenSearch 3.8.0（`opensearchproject/opensearch:3.8.0`）。OpenSearch 只发布到 `127.0.0.1:9200`。

跑真实依赖测试前必须停掉 `velis-worker` 容器：它会消费与测试相同的队列，导致 RabbitMQ 用例等待消息超时。

```bash
docker compose up -d postgres rabbitmq opensearch   # 不要启动 velis-worker
```

## 命令与结果

| 命令 | 结果 |
| --- | --- |
| `make check` | 通过（gofmt、单测、`-race`、构建、web lint/test/build） |
| `cd backend && go vet ./...` | 无输出 |
| `VELIS_TEST_DATABASE_URL=... make integration-postgres` | 通过（含迁移升级/受保护降级、目标推进、认领与 fencing、投影读取、重建全链路） |
| `VELIS_TEST_RABBITMQ_URL=... make integration-rabbitmq` | 通过 |
| `VELIS_TEST_OPENSEARCH_URL=... make integration-opensearch` | 通过（未设置 URL 时 4 个用例显式 SKIP，不计作通过） |
| `VELIS_TEST_DATABASE_URL=... VELIS_TEST_OPENSEARCH_URL=... make integration-search` | 通过 |

## 端到端验收覆盖

- **8.1** `TestSearchProjectionEndToEndLifecycle`：发布 → generation 先完成（文本可检索、无向量）→ Embedding 完成（向量维度正确）→ 公开修订（revision 与 generation 递增）→ 迟到旧写被版本条件折叠为收敛且不回退文档 → 下架后 tombstone 不可检索 → 重新发布以更高版本恢复可检索。索引中的 `revision_id`、`lock_version`、`projection_generation` 与 `projection_content_hash` 每次都和槽位核对一致。
- **8.2** `TestSearchProjectionSurvivesOpenSearchOutage`：写入端指向已关闭端口模拟停机。文章事务照常提交、详情可读、槽位不被标记成功、索引无文档；恢复后只追赶未完成的投递，积压归零，重复执行不推进 generation。
- **8.3** `TestSearchRebuildConvergesWithConcurrentWrites`：`SnapshotBatch=1` 把快照拉开到多批，写入线程在重建推进期间连续修订、下架、重新发布。最终校验必须通过、候选无落后投递、可见文档数与 PostgreSQL 一致，且逐篇抽样比对 revision、lock version 与内容指纹都收敛到当前事实，之后才允许切换。

## 手工运维链路（真实开发库，73 篇公开文章）

```bash
export VELIS_SEARCH_ENDPOINTS=http://localhost:9200
ADMIN="go run ./cmd/velis-admin -config configs/config.example.yaml"
$ADMIN search index init      # 幂等；重复执行返回现状
$ADMIN search rebuild start   # 快照 73 篇 + 增量 + 校验 → validated
$ADMIN search rebuild status
$ADMIN search rebuild cutover # 原子切换读写别名，打开 24h 回滚窗口
$ADMIN search rebuild rollback
$ADMIN search rebuild cleanup -index <物理索引> -confirm
```

`cutover` 后经 `_cat/aliases` 核对：读别名与写别名都指向候选索引，写别名唯一承担 `is_write_index`，读别名可检索 73 篇文档。`cleanup` 对当前读索引、未知前缀与缺少 `-confirm` 三种情况都明确拒绝；对已放弃且无引用的候选索引成功删除。

## 边界核对（8.4）

- 未注册任何搜索 HTTP API：`internal/interfaces/http/hertz/router.go` 无搜索路由。
- 未装配 Redis 或 recommendation：`internal/bootstrap/` 与 `cmd/` 无相关引用。
- 未使用 PostgreSQL 向量查询：`internal/infrastructure/persistence/postgres/` 无 `<=>` / `<->`。
- 新增直接依赖只有 `github.com/opensearch-project/opensearch-go/v4 v4.7.3`；`go 1.26.6` 与 `backend/Dockerfile` 的 `golang:1.26.6-alpine` 均未改动。

## 实施中发现并修复的缺陷

1. 已增强文章下架会整笔回滚：tombstone 目标不允许携带 AI 结果引用。
2. 过期租约无法重新认领：认领条件要求 delivery 处于 `pending/retry_wait`，而认领后它们是 `running`。
3. 重试会重发已收敛的索引。
4. `_count` 需要 `{"query": …}` 正文，否则文档计数全部失败。
5. 快照未收敛它写入的候选投递，校验永远无法通过。
6. 校验失败后阶段仍停在 `validated`，`cutover` 照常放行。
7. `rollback` 找不到记录：`Active` 把回滚窗口所在的 `serving` 阶段排除了。
8. 租约屏障固定睡眠一整个租约周期；改为轮询真实活动租约。
9. `Inspect` 读取近实时计数，刚写入的文档被读成不存在；改为先刷新。
10. Worker 写入的文档缺少内容指纹，回滚窗口双写后抽样校验必然不匹配。
11. **ABBA 死锁**：重建路径先锁 delivery 再锁 job，与文章写路径的 job → delivery 顺序相反；已统一为 article → job → delivery。
12. catch-up 内层扫描无界，持续写入时永远追不上高水位；已加页数上限，把「仍有落后投递」交给校验阶段显式拒绝。
