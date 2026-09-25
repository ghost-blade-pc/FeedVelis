package integration

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	enrichmentApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/enrichment"
	projectionApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/searchprojection"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
	searchAdapter "github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/search/opensearch"
)

// projectionE2E 用真实 PostgreSQL + 真实 OpenSearch 装配投影 Worker 链路。
type projectionE2E struct {
	env        *testEnv
	client     *searchAdapter.Client
	writer     projectionApp.DocumentWriter
	repository *postgres.SearchProjectionRepository
	executor   *projectionApp.Executor
	index      string
}

func newProjectionE2E(t *testing.T, env *testEnv, dimensions int) *projectionE2E {
	t.Helper()
	endpoint := strings.TrimSpace(os.Getenv("VELIS_TEST_OPENSEARCH_URL"))
	if endpoint == "" {
		t.Skip("未设置 VELIS_TEST_OPENSEARCH_URL，跳过搜索投影端到端验收")
	}
	prefix := fmt.Sprintf("velis-e2e-%d", time.Now().UnixNano()%1_000_000_000)
	schemaIdentity := fmt.Sprintf("mapping-v1|analyzer-cjk|dims-%d|encoding-v1", dimensions)
	client, err := searchAdapter.New(searchAdapter.Config{
		Endpoints: []string{endpoint}, IndexPrefix: prefix, SchemaVersion: 1,
		SchemaIdentity: schemaIdentity, EmbeddingDimensions: dimensions,
		ConnectTimeout: 5 * time.Second, RequestTimeout: 30 * time.Second,
		BulkMaxItems: 100, BulkMaxBytes: 1 << 20, BulkMaxDocumentChars: 100000,
	})
	if err != nil {
		t.Fatal(err)
	}
	repository := postgres.NewSearchProjectionRepository(env.pool)
	rebuild := projectionApp.NewRebuildService(repository, repository, client, client, repository,
		projectionApp.RebuildPolicy{IndexPrefix: prefix, SchemaVersion: 1, SchemaIdentity: schemaIdentity,
			EmbeddingDimensions: dimensions, SnapshotBatch: 10, RollbackWindow: time.Hour,
			Lease: time.Second, SampleSize: 10, PollInterval: 20 * time.Millisecond}, nil)
	if _, err := rebuild.InitIndex(context.Background()); err != nil {
		t.Fatalf("初始化索引: %v", err)
	}
	state, err := repository.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	stack := &projectionE2E{env: env, client: client, writer: client, repository: repository, index: state.CurrentIndex}
	stack.executor = projectionApp.NewExecutor(repository, repository, repository, client, projectionApp.ExecutorPolicy{
		Lease: time.Minute, BatchSize: 20, MaxAttempts: 3, BackoffMin: time.Second, BackoffMax: time.Minute,
		SchemaVersion: 1, Owner: "e2e-worker"}, nil, nil)
	t.Cleanup(func() {
		_ = client.Delete(context.Background(), state.CurrentIndex)
		_ = client.Close()
	})
	return stack
}

// drain 反复执行投影批次直到没有可认领的槽位。
func (e *projectionE2E) drain(t *testing.T, ctx context.Context) {
	t.Helper()
	for round := 0; round < 50; round++ {
		processed, err := e.executor.ProcessBatch(ctx)
		if err != nil {
			t.Fatalf("投影批次: %v", err)
		}
		if processed == 0 {
			return
		}
	}
	t.Fatal("投影未在有限批次内收敛")
}

// document 读取索引中一篇文档的原始字段，用于核对最终身份。
func (e *projectionE2E) document(t *testing.T, articleID int64) map[string]any {
	t.Helper()
	document, err := e.client.Fetch(context.Background(), e.index, articleID)
	if err != nil {
		t.Fatalf("读取索引文档 %d: %v", articleID, err)
	}
	return map[string]any{
		"generation": document.Generation, "revision": document.RevisionID,
		"lock_version": document.LockVersion, "visible": document.Visible,
		"content_hash": document.ContentHash,
	}
}

func (e *projectionE2E) visibleCount(t *testing.T) int64 {
	t.Helper()
	state, err := e.client.Inspect(context.Background(), e.index)
	if err != nil {
		t.Fatal(err)
	}
	return state.VisibleDocuments
}

func (e *projectionE2E) lagging(t *testing.T) int64 {
	t.Helper()
	lagging, err := e.repository.LaggingCandidateDeliveries(context.Background(), e.index)
	if err != nil {
		t.Fatal(err)
	}
	return lagging
}

// TestSearchProjectionEndToEndLifecycle 覆盖 8.1：
// 发布、公开修订、generation/Embedding 分阶段完成、下架、迟到旧写与重新发布全链路，
// 确认索引最终身份正确且 tombstone 不可检索。
func TestSearchProjectionEndToEndLifecycle(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx := context.Background()
	now := fixedNow()
	const authorID = "99000000-0000-0000-0000-000000000001"
	seedIntegrationUser(t, env, authorID, "e2e_author", "user", now)
	stack := newProjectionE2E(t, env, 3)
	articles := postgres.NewArticleRepository(env.pool)

	articleID := createPublishedArticle(t, env, authorID, "g", now)
	stack.drain(t, ctx)
	if stack.visibleCount(t) != 1 || stack.lagging(t) != 0 {
		t.Fatalf("发布后索引必须可见且不落后: visible=%d lagging=%d", stack.visibleCount(t), stack.lagging(t))
	}
	job, _ := readProjectionJob(t, env, articleID)
	document := stack.document(t, articleID)
	if document["visible"] != true || document["revision"] != job.RevisionID || document["generation"] != job.Generation {
		t.Fatalf("索引身份必须与槽位一致: doc=%+v job=%+v", document, job)
	}
	if document["content_hash"] == "" {
		t.Fatal("文档必须记录投影内容指纹")
	}
	// 尚无 AI 结果时不得出现增强字段。
	assertIndexedFieldAbsent(t, stack, articleID, "summary")
	assertIndexedFieldAbsent(t, stack, articleID, "vector")

	// generation 先完成：文本增强独立进入索引，向量仍为空。
	enrichment := postgres.NewEnrichmentRepository(env.pool)
	generationProfile := &enrichmentApp.ActiveProfile{Provider: "e2e", Model: "chat", ProfileVersion: "g-e2e",
		WorkflowVersion: "w1", PromptVersion: "p1", StructuredOutput: "prompt", MaxAttempts: 3,
		AuditTokenBudget: 1000, StageBudget: time.Minute}
	embeddingProfile := &enrichmentApp.ActiveProfile{Provider: "e2e", Model: "embed", ProfileVersion: "e-e2e",
		InputVersion: "i1", Dimensions: 3, MaxAttempts: 3, AuditTokenBudget: 1000, StageBudget: time.Minute}
	if _, err := env.pool.Exec(ctx, `UPDATE velis.articles SET status='published',published_at=$2 WHERE id=$1`, articleID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(ctx, `INSERT INTO velis.async_tasks
(id,task_type,aggregate_type,aggregate_id,article_id,revision_id,revision_no,content_hash,status,generation,observed_aggregate_version,next_attempt_at,created_at,updated_at)
VALUES('99000000-0000-0000-0000-0000000000a1','article.enrichment','article',$1::text,$2,$3,1,repeat('c',64),'pending',1,1,$4,$4,$4)`,
		strconv.FormatInt(articleID, 10), articleID, job.RevisionID, now); err != nil {
		t.Fatal(err)
	}
	task, err := enrichment.Claim(ctx, enrichmentApp.ClaimRequest{Owner: "e2e-ai", Lease: time.Minute,
		Generation: generationProfile, Embedding: embeddingProfile, Now: now.Add(time.Second)})
	if err != nil || task == nil {
		t.Fatalf("认领增强任务: %+v err=%v", task, err)
	}
	generation := enrichmentApp.GenerationResult{ID: "99000000-0000-0000-0000-0000000000b1", ArticleID: task.ArticleID,
		RevisionID: task.RevisionID, Profile: *generationProfile, InputHash: strings.Repeat("d", 64),
		Content:     enrichmentApp.GeneratedContent{Summary: "端到端摘要", Keywords: []string{"词"}, Topics: []string{"主题"}},
		GeneratedAt: now}
	if ok, err := enrichment.SaveGeneration(ctx, *task, generation, true, now.Add(2*time.Second)); err != nil || !ok {
		t.Fatalf("保存 generation: ok=%t err=%v", ok, err)
	}
	stack.drain(t, ctx)
	assertIndexedFieldEquals(t, stack, articleID, "summary", "端到端摘要")
	assertIndexedFieldAbsent(t, stack, articleID, "vector")

	// Embedding 随后完成：向量出现且维度正确。
	embedTask, err := enrichment.Claim(ctx, enrichmentApp.ClaimRequest{Owner: "e2e-ai", Lease: time.Minute,
		Generation: generationProfile, Embedding: embeddingProfile, Now: now.Add(3 * time.Second)})
	if err != nil || embedTask == nil || embedTask.Stage != "embedding" {
		t.Fatalf("认领 embedding: %+v err=%v", embedTask, err)
	}
	embedding := enrichmentApp.EmbeddingResult{ID: "99000000-0000-0000-0000-0000000000b2", GenerationResultID: generation.ID,
		ArticleID: embedTask.ArticleID, RevisionID: embedTask.RevisionID, Profile: *embeddingProfile,
		InputHash: strings.Repeat("e", 64), Vector: []float64{0.1, 0.2, 0.3}, GeneratedAt: now}
	if ok, err := enrichment.SaveEmbedding(ctx, *embedTask, embedding, now.Add(4*time.Second)); err != nil || !ok {
		t.Fatalf("保存 embedding: ok=%t err=%v", ok, err)
	}
	stack.drain(t, ctx)
	assertIndexedVectorDimension(t, stack, articleID, 3)

	// 公开修订：索引必须指向新修订且版本递增。
	before := stack.document(t, articleID)
	markdown, html := "端到端修订", "<p>端到端修订</p>"
	if _, _, err := articles.UpdateUserRevision(ctx, articleID, authorID, job.LockVersion,
		articleDomain.RevisionData{Title: "端到端修订", Markdown: &markdown, SanitizedHTML: &html, PlainText: "端到端修订",
			Excerpt: "端到端修订", Language: "zh-CN", ContentHash: strings.Repeat("j", 64), SanitizerVersion: 1},
		now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	stack.drain(t, ctx)
	revised, _ := readProjectionJob(t, env, articleID)
	after := stack.document(t, articleID)
	if after["revision"] != revised.RevisionID || after["generation"].(int64) <= before["generation"].(int64) {
		t.Fatalf("修订后索引必须指向新修订且版本递增: before=%+v after=%+v", before, after)
	}

	// 迟到旧写：低于当前 generation 的 upsert 必须被版本条件拒绝，文档不得回退。
	late := projectionApp.BulkItem{PhysicalIndex: stack.index, Document: projectionApp.Document{
		ArticleID: articleID, Generation: revised.Generation - 1, LockVersion: 1, RevisionID: job.RevisionID,
		SchemaVersion: 1, Visible: true, OriginType: "user", Title: "迟到旧写", PlainText: "迟到旧写"}}
	outcomes, err := stack.writer.Bulk(ctx, []projectionApp.BulkItem{late})
	if err != nil {
		t.Fatal(err)
	}
	if !outcomes[0].Result.Applied() {
		t.Fatalf("陈旧写入必须折叠为收敛而不是失败: %+v", outcomes[0])
	}
	if current := stack.document(t, articleID); current["revision"] != after["revision"] {
		t.Fatalf("迟到旧写不得回退文档: %+v", current)
	}

	// 下架：tombstone 不可检索但保留身份。
	if _, err := articles.SetArticleState(ctx, articleID, revised.LockVersion, articleDomain.StatusOffline,
		func() *articleDomain.OfflineReason { reason := articleDomain.OfflineByAdmin; return &reason }(), nil, nil, nil,
		now.Add(6*time.Second)); err != nil {
		t.Fatal(err)
	}
	stack.drain(t, ctx)
	if stack.visibleCount(t) != 0 {
		t.Fatalf("下架后必须不可检索: visible=%d", stack.visibleCount(t))
	}
	tombstone := stack.document(t, articleID)
	if tombstone["visible"] != false {
		t.Fatalf("下架后必须是 tombstone: %+v", tombstone)
	}

	// 重新发布：更高版本必须越过 tombstone 恢复可检索。
	republished, err := articles.SetArticleState(ctx, articleID, revised.LockVersion+1, articleDomain.StatusPublished,
		nil, nil, nil, nil, now.Add(7*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	stack.drain(t, ctx)
	if stack.visibleCount(t) != 1 {
		t.Fatalf("重新发布必须恢复可检索: visible=%d", stack.visibleCount(t))
	}
	final := stack.document(t, articleID)
	if final["visible"] != true || final["lock_version"] != republished.LockVersion {
		t.Fatalf("重新发布后的索引身份错误: %+v (lock_version=%d)", final, republished.LockVersion)
	}
}

// TestSearchProjectionSurvivesOpenSearchOutage 覆盖 8.2：
// OpenSearch 停机时文章事务照常完成、任务保留，恢复后只追赶未完成的投递。
func TestSearchProjectionSurvivesOpenSearchOutage(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx := context.Background()
	now := fixedNow()
	const authorID = "99000000-0000-0000-0000-000000000002"
	seedIntegrationUser(t, env, authorID, "outage_author", "user", now)
	stack := newProjectionE2E(t, env, 3)

	// 注入停机：写入端指向一个已关闭的端口，读取与状态仍走真实索引。
	dead := newDeadWriter(t)
	outage := projectionApp.NewExecutor(stack.repository, stack.repository, stack.repository, dead,
		projectionApp.ExecutorPolicy{Lease: time.Minute, BatchSize: 20, MaxAttempts: 2,
			BackoffMin: time.Second, BackoffMax: time.Minute, SchemaVersion: 1, Owner: "outage-worker"}, nil, nil)

	articleID := createPublishedArticle(t, env, authorID, "k", now)
	if _, err := outage.ProcessBatch(ctx); err != nil {
		t.Fatalf("停机时投影批次不得让事务失败: %v", err)
	}
	// 文章事实照常可用，槽位保留待处理，投递进入可重试状态。
	detail, err := postgres.NewArticleRepository(env.pool).GetPublished(ctx, articleID)
	if err != nil || detail.Item.Title == "" {
		t.Fatalf("停机时文章读取必须可用: %+v err=%v", detail.Item, err)
	}
	job, _ := readProjectionJob(t, env, articleID)
	if job.Status == "succeeded" {
		t.Fatalf("停机时槽位不得被标记成功: %+v", job)
	}
	if stack.visibleCount(t) != 0 {
		t.Fatalf("停机时索引不得出现文档: %d", stack.visibleCount(t))
	}

	// 恢复：真实写入端只追赶尚未完成的 delivery，积压归零。
	stack.drain(t, ctx)
	if stack.visibleCount(t) != 1 || stack.lagging(t) != 0 {
		t.Fatalf("恢复后必须收敛: visible=%d lagging=%d", stack.visibleCount(t), stack.lagging(t))
	}
	converged, _ := readProjectionJob(t, env, articleID)
	if converged.Status != "succeeded" {
		t.Fatalf("恢复后槽位必须收敛: %+v", converged)
	}
	// 已收敛的投递不会被重复投递。
	before := converged.Generation
	if _, err := stack.executor.ProcessBatch(ctx); err != nil {
		t.Fatal(err)
	}
	if after, _ := readProjectionJob(t, env, articleID); after.Generation != before {
		t.Fatalf("重复执行不得推进 generation: %d → %d", before, after.Generation)
	}
}

// newDeadWriter 返回一个连接已关闭端口的写入端，用于注入 OpenSearch 停机。
func newDeadWriter(t *testing.T) projectionApp.DocumentWriter {
	t.Helper()
	client, err := searchAdapter.New(searchAdapter.Config{
		Endpoints: []string{"http://127.0.0.1:1"}, IndexPrefix: "velis-outage", SchemaVersion: 1,
		SchemaIdentity: "mapping-v1|analyzer-cjk|dims-3|encoding-v1", EmbeddingDimensions: 3,
		ConnectTimeout: time.Second, RequestTimeout: 2 * time.Second,
		BulkMaxItems: 10, BulkMaxBytes: 1 << 20, BulkMaxDocumentChars: 100000,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func assertIndexedFieldEquals(t *testing.T, stack *projectionE2E, articleID int64, field, want string) {
	t.Helper()
	got := indexedField(t, stack, articleID, field)
	if got != want {
		t.Fatalf("索引字段 %s = %v，期望 %q", field, got, want)
	}
}

func assertIndexedFieldAbsent(t *testing.T, stack *projectionE2E, articleID int64, field string) {
	t.Helper()
	if value := indexedField(t, stack, articleID, field); value != nil {
		t.Fatalf("索引字段 %s 不应存在: %v", field, value)
	}
}

func assertIndexedVectorDimension(t *testing.T, stack *projectionE2E, articleID int64, dimension int) {
	t.Helper()
	value := indexedField(t, stack, articleID, "vector")
	vector, ok := value.([]any)
	if !ok || len(vector) != dimension {
		t.Fatalf("索引向量维度错误: %v", value)
	}
}

// indexedField 读取索引文档的原始字段；字节级断言必须走真实索引而不是适配器的投影模型。
func indexedField(t *testing.T, stack *projectionE2E, articleID int64, field string) any {
	t.Helper()
	raw, err := stack.client.RawDocument(context.Background(), stack.index, articleID)
	if err != nil {
		t.Fatalf("读取原始文档 %d: %v", articleID, err)
	}
	return raw[field]
}
