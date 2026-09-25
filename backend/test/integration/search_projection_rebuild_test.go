package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	projectionApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/searchprojection"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
	projectionDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/searchprojection"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
	searchAdapter "github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/search/opensearch"
)

type rebuildStack struct {
	service    *projectionApp.RebuildService
	repository *postgres.SearchProjectionRepository
	client     *searchAdapter.Client
	prefix     string
}

// newRebuildStack 用真实 PostgreSQL 与真实 OpenSearch 装配重建服务。
func newRebuildStack(t *testing.T, env *testEnv, dimensions int) *rebuildStack {
	t.Helper()
	endpoint := strings.TrimSpace(os.Getenv("VELIS_TEST_OPENSEARCH_URL"))
	if endpoint == "" {
		t.Skip("未设置 VELIS_TEST_OPENSEARCH_URL，跳过真实索引重建测试")
	}
	prefix := fmt.Sprintf("velis-rebuild-%d", time.Now().UnixNano()%1_000_000_000)
	client, err := searchAdapter.New(searchAdapter.Config{
		Endpoints: []string{endpoint}, IndexPrefix: prefix, SchemaVersion: 1,
		SchemaIdentity:      fmt.Sprintf("mapping-v1|analyzer-cjk|dims-%d|encoding-v1", dimensions),
		EmbeddingDimensions: dimensions, ConnectTimeout: 5 * time.Second, RequestTimeout: 30 * time.Second,
		BulkMaxItems: 100, BulkMaxBytes: 1 << 20, BulkMaxDocumentChars: 100000,
	})
	if err != nil {
		t.Fatal(err)
	}
	repository := postgres.NewSearchProjectionRepository(env.pool)
	policy := projectionApp.RebuildPolicy{
		IndexPrefix: prefix, SchemaVersion: 1,
		SchemaIdentity:      fmt.Sprintf("mapping-v1|analyzer-cjk|dims-%d|encoding-v1", dimensions),
		EmbeddingDimensions: dimensions, SnapshotBatch: 2, RollbackWindow: time.Hour,
		Lease: 2 * time.Second, SampleSize: 20, PollInterval: 20 * time.Millisecond,
	}
	t.Cleanup(func() {
		rows, err := env.pool.Query(context.Background(), `SELECT candidate_index FROM velis.search_index_rebuilds`)
		if err == nil {
			for rows.Next() {
				var name string
				if scanErr := rows.Scan(&name); scanErr == nil {
					_ = client.Delete(context.Background(), name)
				}
			}
			rows.Close()
		}
		var current string
		if err := env.pool.QueryRow(context.Background(),
			`SELECT current_index FROM velis.search_index_state WHERE id`).Scan(&current); err == nil {
			_ = client.Delete(context.Background(), current)
		}
		_ = client.Close()
	})
	return &rebuildStack{service: projectionApp.NewRebuildService(repository, repository, client, client, repository, policy, nil),
		repository: repository, client: client, prefix: prefix}
}

// TestSearchIndexRebuildLifecycle 覆盖 6.1–6.6 的真实依赖链路：
// 初始化、快照、增量追赶、校验、切换、回滚、放弃与安全清理。
func TestSearchIndexRebuildLifecycle(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx := context.Background()
	now := fixedNow()
	const authorID = "98000000-0000-0000-0000-000000000001"
	seedIntegrationUser(t, env, authorID, "rebuild_author", "user", now)
	repository := postgres.NewArticleRepository(env.pool)

	var published []int64
	for _, seed := range []string{"a", "b", "c"} {
		published = append(published, createPublishedArticle(t, env, authorID, seed, now))
	}
	hidden := createPublishedArticle(t, env, authorID, "d", now)
	if _, err := repository.SetArticleState(ctx, hidden, 1, articleDomain.StatusOffline,
		func() *articleDomain.OfflineReason { reason := articleDomain.OfflineByAuthor; return &reason }(), nil, nil, nil, now); err != nil {
		t.Fatal(err)
	}
	stack := newRebuildStack(t, env, 3)

	// 6.1：初始化必须幂等，且为已有槽位建立投递目标。
	first, err := stack.service.InitIndex(ctx)
	if err != nil {
		t.Fatalf("初始化索引: %v", err)
	}
	if !first.HasReadAlias || !first.HasWriteAlias || first.SchemaVersion != 1 {
		t.Fatalf("初始化结果错误: %+v", first)
	}
	second, err := stack.service.InitIndex(ctx)
	if err != nil {
		t.Fatalf("重复初始化: %v", err)
	}
	if second.PhysicalIndex != first.PhysicalIndex || second.SchemaIdentity != first.SchemaIdentity {
		t.Fatalf("重复初始化必须返回现状而不是创建重复索引: %+v vs %+v", second, first)
	}
	var jobs int64
	var deliveries int64
	if err := env.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM velis.search_projection_jobs),
(SELECT count(*) FROM velis.search_projection_deliveries WHERE physical_index=$1)`, second.PhysicalIndex).
		Scan(&jobs, &deliveries); err != nil {
		t.Fatal(err)
	}
	if jobs != 4 || deliveries != 4 {
		t.Fatalf("初始化必须为全部槽位建立投递: jobs=%d deliveries=%d", jobs, deliveries)
	}

	// 6.2 + 6.3 + 6.4：快照、增量追赶与校验。
	state, err := stack.service.Start(ctx)
	if err != nil {
		t.Fatalf("重建开始: %v", err)
	}
	if state.Phase != projectionDomain.PhaseValidated {
		t.Fatalf("重建必须推进到已校验: %+v", state)
	}
	if state.SnapshotDocuments != 3 {
		t.Fatalf("快照只应写入公开文章: %+v", state)
	}
	if state.Validation == nil || !state.Validation.Passed() {
		t.Fatalf("校验必须通过: %+v", state.Validation)
	}
	candidate, err := stack.client.Inspect(ctx, state.CandidateIndex)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.VisibleDocuments != 3 || candidate.Documents != 3 {
		t.Fatalf("候选索引不得写入存量不可见文章: %+v", candidate)
	}
	// 6.1：同一时刻只允许一个活动重建。
	if _, err := stack.service.Start(ctx); !errors.Is(err, projectionApp.ErrRebuildConflict) {
		t.Fatalf("重复 start 必须被拒绝: %v", err)
	}

	// 6.5：切换后回滚窗口内新旧索引都必须被双写。
	served, err := stack.service.Cutover(ctx, state.ID)
	if err != nil {
		t.Fatalf("切换: %v", err)
	}
	if served.Phase != projectionDomain.PhaseServing || served.RollbackDeadline == nil {
		t.Fatalf("切换必须打开回滚窗口: %+v", served)
	}
	current, err := stack.repository.State(ctx)
	if err != nil || current.CurrentIndex != state.CandidateIndex || current.RollbackIndex != second.PhysicalIndex {
		t.Fatalf("切换后的服务状态错误: %+v err=%v", current, err)
	}
	// 回滚窗口内修改一篇文章：新旧索引都必须收敛。
	markdown, html := "切换后修订", "<p>切换后修订</p>"
	if _, _, err := repository.UpdateUserRevision(ctx, published[0], authorID, 1,
		articleDomain.RevisionData{Title: "切换后修订", Markdown: &markdown, SanitizedHTML: &html, PlainText: "切换后修订",
			Excerpt: "切换后修订", Language: "zh-CN", ContentHash: strings.Repeat("z", 64), SanitizerVersion: 1}, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	executor := projectionApp.NewExecutor(stack.repository, stack.repository, stack.repository, stack.client,
		projectionApp.ExecutorPolicy{Lease: time.Minute, BatchSize: 10, MaxAttempts: 3,
			BackoffMin: time.Second, BackoffMax: time.Minute, SchemaVersion: 1, Owner: "rebuild-test"}, nil, nil)
	if _, err := executor.ProcessBatch(ctx); err != nil {
		t.Fatal(err)
	}
	for _, index := range []string{state.CandidateIndex, second.PhysicalIndex} {
		if lagging, err := stack.repository.LaggingCandidateDeliveries(ctx, index); err != nil || lagging != 0 {
			t.Fatalf("回滚窗口内 %s 必须保持不落后: lagging=%d err=%v", index, lagging, err)
		}
	}

	// 6.5：回滚到前一索引。
	rolled, err := stack.service.Rollback(ctx, state.ID)
	if err != nil {
		t.Fatalf("回滚: %v", err)
	}
	if rolled.Phase != projectionDomain.PhaseAbandoned {
		t.Fatalf("回滚后候选索引必须被放弃: %+v", rolled)
	}
	restored, err := stack.repository.State(ctx)
	if err != nil || restored.CurrentIndex != second.PhysicalIndex || restored.RollbackIndex != "" {
		t.Fatalf("回滚后的服务状态错误: %+v err=%v", restored, err)
	}

	// 6.6：cleanup 必须拒绝当前索引、未知前缀，并在显式确认后删除候选。
	if err := stack.service.Cleanup(ctx, restored.CurrentIndex, true); !errors.Is(err, projectionApp.ErrUnsafeIndexTarget) {
		t.Fatalf("不得删除当前读索引: %v", err)
	}
	if err := stack.service.Cleanup(ctx, "other-prefix-v1-20260925t120000z-aaaaaa", true); !errors.Is(err, projectionApp.ErrUnsafeIndexTarget) {
		t.Fatalf("不得删除未知前缀索引: %v", err)
	}
	if err := stack.service.Cleanup(ctx, state.CandidateIndex, false); !errors.Is(err, projectionApp.ErrUnsafeIndexTarget) {
		t.Fatalf("cleanup 必须显式确认: %v", err)
	}
	if err := stack.service.Cleanup(ctx, state.CandidateIndex, true); err != nil {
		t.Fatalf("放弃后的候选索引应可安全清理: %v", err)
	}
	if _, err := stack.client.Inspect(ctx, state.CandidateIndex); !errors.Is(err, projectionApp.ErrIndexMissing) {
		t.Fatalf("候选索引应已被删除: %v", err)
	}
	// 当前索引仍然服务。
	if remaining, err := stack.client.Inspect(ctx, restored.CurrentIndex); err != nil || !remaining.HasReadAlias {
		t.Fatalf("当前索引必须继续服务: %+v err=%v", remaining, err)
	}
}

// TestSearchRebuildRejectsIncompleteCandidate 覆盖 6.4：
// 候选索引缺少文档时必须拒绝切换，并保留当前索引继续服务。
func TestSearchRebuildRejectsIncompleteCandidate(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx := context.Background()
	now := fixedNow()
	const authorID = "98000000-0000-0000-0000-000000000002"
	seedIntegrationUser(t, env, authorID, "rebuild_author2", "user", now)
	for _, seed := range []string{"e", "f"} {
		createPublishedArticle(t, env, authorID, seed, now)
	}
	stack := newRebuildStack(t, env, 3)
	if _, err := stack.service.InitIndex(ctx); err != nil {
		t.Fatal(err)
	}
	state, err := stack.service.Start(ctx)
	if err != nil || state.Phase != projectionDomain.PhaseValidated {
		t.Fatalf("重建必须完成: %+v err=%v", state, err)
	}
	// 人为从候选索引删除一篇文档，模拟漏写。
	removed := int64(0)
	if err := env.pool.QueryRow(ctx, `SELECT article_id FROM velis.search_projection_jobs ORDER BY article_id LIMIT 1`).Scan(&removed); err != nil {
		t.Fatal(err)
	}
	if err := stack.client.DeleteDocument(ctx, state.CandidateIndex, removed); err != nil {
		t.Fatal(err)
	}
	// 重新校验必须发现漏写并失败，同时把阶段退回未校验状态。
	if _, err := stack.service.Resume(ctx, state.ID); !errors.Is(err, projectionApp.ErrValidationFailed) {
		t.Fatalf("重新校验必须报告失败: %v", err)
	}
	rechecked, err := stack.repository.Get(ctx, state.ID)
	if err != nil || rechecked.Validation == nil || rechecked.Validation.Passed() {
		t.Fatalf("失败报告必须被持久化: %+v err=%v", rechecked.Validation, err)
	}
	if rechecked.Validation.CandidateDocuments != 1 || rechecked.Validation.PublicDocuments != 2 {
		t.Fatalf("失败报告必须给出计数差异: %+v", rechecked.Validation)
	}
	if rechecked.Phase != projectionDomain.PhaseValidate {
		t.Fatalf("校验失败必须退回未校验阶段: %s", rechecked.Phase)
	}
	// 校验未通过的候选索引不得被切换。
	if _, err := stack.service.Cutover(ctx, state.ID); !errors.Is(err, projectionApp.ErrInvalidRebuildPhase) {
		t.Fatalf("未通过校验必须拒绝切换: %v", err)
	}
	current, err := stack.repository.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	index, err := stack.client.Inspect(ctx, current.CurrentIndex)
	if err != nil || !index.HasReadAlias {
		t.Fatalf("校验失败后当前索引必须继续服务: %+v err=%v", index, err)
	}
	if current.CurrentIndex == state.CandidateIndex {
		t.Fatal("校验失败不得迁移读写别名")
	}
}

// TestSearchRebuildConvergesWithConcurrentWrites 覆盖 8.3：
// 快照与增量追赶期间持续修订、下架、重新发布并切换 AI 当前选择，
// 候选索引最终必须收敛到切换前的当前事实，校验通过后才允许切换。
func TestSearchRebuildConvergesWithConcurrentWrites(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx := context.Background()
	now := fixedNow()
	const authorID = "98000000-0000-0000-0000-000000000003"
	seedIntegrationUser(t, env, authorID, "rebuild_author3", "user", now)
	repository := postgres.NewArticleRepository(env.pool)

	var articles []int64
	for index := 0; index < 8; index++ {
		articles = append(articles, createPublishedArticle(t, env, authorID, string(rune('a'+index)), now))
	}
	// 每页 1 篇：快照会被拉开到多个批次，写入线程有真实的介入窗口。
	stack := newRebuildStack(t, env, 3)
	stack.service = projectionApp.NewRebuildService(stack.repository, stack.repository, stack.client, stack.client,
		stack.repository, projectionApp.RebuildPolicy{
			IndexPrefix: stack.prefix, SchemaVersion: 1,
			SchemaIdentity:      "mapping-v1|analyzer-cjk|dims-3|encoding-v1",
			EmbeddingDimensions: 3, SnapshotBatch: 1, RollbackWindow: time.Hour,
			Lease: time.Second, SampleSize: 20, PollInterval: 5 * time.Millisecond,
		}, nil)
	if _, err := stack.service.InitIndex(ctx); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	var state projectionApp.RebuildState
	var rebuildErr error
	go func() {
		defer close(done)
		state, rebuildErr = stack.service.Start(ctx)
	}()

	// 在重建推进期间写入一批目标变化：修订与下架交替出现。
	// 写入必须是有限突发——持续写入会让追赶永远追不上高水位，重建正确地拒绝收敛。
	const burst = 12
	writes := 0
	for index := 0; index < burst; index++ {
		if isClosed(done) {
			break
		}
		articleID := articles[index%len(articles)]
		var lockVersion int64
		var status articleDomain.Status
		if err := env.pool.QueryRow(ctx, `SELECT lock_version,status FROM velis.articles WHERE id=$1`, articleID).
			Scan(&lockVersion, &status); err != nil {
			t.Fatal(err)
		}
		at := now.Add(time.Duration(index+1) * time.Second)
		if status == articleDomain.StatusPublished {
			// 交替修订与下架，确保两类目标变化都出现在重建窗口内。
			if index%2 == 0 {
				markdown, html := "重建期间修订", "<p>重建期间修订</p>"
				if _, _, err := repository.UpdateUserRevision(ctx, articleID, authorID, lockVersion,
					articleDomain.RevisionData{Title: "重建期间修订", Markdown: &markdown, SanitizedHTML: &html,
						PlainText: "重建期间修订", Excerpt: "重建期间修订", Language: "zh-CN",
						ContentHash: strings.Repeat(string(rune('p'+index%4)), 64), SanitizerVersion: 1}, at); err != nil {
					t.Fatal(err)
				}
			} else if _, err := repository.SetArticleState(ctx, articleID, lockVersion, articleDomain.StatusOffline,
				func() *articleDomain.OfflineReason { reason := articleDomain.OfflineByAdmin; return &reason }(), nil, nil, nil, at); err != nil {
				t.Fatal(err)
			}
		} else {
			if _, err := repository.SetArticleState(ctx, articleID, lockVersion, articleDomain.StatusPublished,
				nil, nil, nil, nil, at); err != nil {
				t.Fatal(err)
			}
		}
		writes++
		time.Sleep(time.Millisecond)
	}
	<-done
	if rebuildErr != nil {
		t.Fatalf("并发写入下重建必须完成: %v", rebuildErr)
	}
	if state.Phase != projectionDomain.PhaseValidated {
		t.Fatalf("重建必须推进到已校验: %+v", state)
	}
	if state.Validation == nil || !state.Validation.Passed() {
		t.Fatalf("并发写入后校验必须通过: %+v", state.Validation)
	}
	if writes < 8 {
		t.Fatalf("并发写入次数不足，未形成有效交叠: %d", writes)
	}
	// 切换前的当前事实必须已经在候选索引上收敛。
	lagging, err := stack.repository.LaggingCandidateDeliveries(ctx, state.CandidateIndex)
	if err != nil || lagging != 0 {
		t.Fatalf("候选索引不得有落后投递: lagging=%d err=%v", lagging, err)
	}
	publicCount, err := stack.repository.PublicCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := stack.client.Inspect(ctx, state.CandidateIndex)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.VisibleDocuments != publicCount {
		t.Fatalf("候选可见文档数必须与 PostgreSQL 一致: 候选=%d 公开=%d", candidate.VisibleDocuments, publicCount)
	}
	// 抽样比对身份与内容指纹：这是「收敛到当前事实」而不是「写了很多次」的证据。
	for _, articleID := range articles {
		projection := readOneProjection(t, env, articleID)
		if !projection.Document.Visible {
			continue
		}
		indexed, err := stack.client.Fetch(ctx, state.CandidateIndex, articleID)
		if err != nil {
			t.Fatalf("候选索引缺少 article %d: %v", articleID, err)
		}
		expected := projection.Document
		expected.SchemaVersion = 1
		expected.Generation = indexed.Generation
		if indexed.RevisionID != expected.RevisionID || indexed.LockVersion != expected.LockVersion ||
			indexed.ContentHash != projectionApp.DocumentFingerprint(expected) {
			t.Fatalf("article %d 的候选文档未收敛到当前事实: indexed=%+v", articleID, indexed)
		}
	}

	// 校验通过后才允许切换，切换后读别名必须与候选一致。
	served, err := stack.service.Cutover(ctx, state.ID)
	if err != nil {
		t.Fatalf("切换: %v", err)
	}
	if served.Phase != projectionDomain.PhaseServing {
		t.Fatalf("切换后必须进入回滚窗口: %+v", served)
	}
	current, err := stack.repository.State(ctx)
	if err != nil || current.CurrentIndex != state.CandidateIndex || current.RollbackIndex == "" {
		t.Fatalf("切换后的服务状态错误: %+v err=%v", current, err)
	}
	if after, err := stack.client.Inspect(ctx, state.CandidateIndex); err != nil || after.VisibleDocuments != publicCount {
		t.Fatalf("切换后读别名必须服务当前事实: %+v err=%v", after, err)
	}
}

// isClosed 判断通道是否已关闭，用于在写入循环里无阻塞地观察重建是否结束。
func isClosed(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}
