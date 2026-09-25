package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	projectionApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/searchprojection"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
	projectionDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/searchprojection"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

const (
	testGenerationID = "97000000-0000-0000-0000-000000000001"
	testSupersededID = "97000000-0000-0000-0000-000000000002"
	testEmbeddingID  = "97000000-0000-0000-0000-000000000003"
)

func insertGenerationResult(t *testing.T, env *testEnv, id, revision string, articleID, revisionID int64) {
	t.Helper()
	_, err := env.pool.Exec(context.Background(), `INSERT INTO velis.ai_generation_results
(id,article_id,revision_id,provider,model,profile_version,workflow_version,prompt_version,generation_input_hash,input_truncated,summary,keywords,topics,generated_at)
VALUES($1,$2,$3,'stub','chat','g-v1','w-v1',$4,repeat('a',64),false,'AI 摘要',ARRAY['关键词','词二'],ARRAY['主题'],now())`,
		id, articleID, revisionID, revision)
	if err != nil {
		t.Fatal(err)
	}
}

func insertEmbeddingResult(t *testing.T, env *testEnv, id, generationResultID string, articleID, revisionID int64) {
	t.Helper()
	_, err := env.pool.Exec(context.Background(), `INSERT INTO velis.ai_embedding_results
(id,article_id,revision_id,generation_result_id,provider,model,profile_version,embedding_input_version,embedding_input_hash,dimensions,vector,generated_at)
VALUES($1,$2,$3,$4,'stub','embed','e-v1','i-v1',repeat('b',64),3,ARRAY[0.25,0.5,0.75]::real[],now())`,
		id, articleID, revisionID, generationResultID)
	if err != nil {
		t.Fatal(err)
	}
}

func setCurrentSelection(t *testing.T, env *testEnv, articleID, revisionID int64, generationID, embeddingID *string) {
	t.Helper()
	var generationProfile, embeddingProfile *string
	if generationID != nil {
		value := "g-v1"
		generationProfile = &value
	}
	if embeddingID != nil {
		value := "e-v1"
		embeddingProfile = &value
	}
	_, err := env.pool.Exec(context.Background(), `INSERT INTO velis.ai_current_selections
(article_id,revision_id,generation_result_id,embedding_result_id,generation_profile_version,embedding_profile_version,updated_at)
VALUES($1,$2,$3,$4,$5,$6,now())
ON CONFLICT (article_id) DO UPDATE SET revision_id=EXCLUDED.revision_id,generation_result_id=EXCLUDED.generation_result_id,
embedding_result_id=EXCLUDED.embedding_result_id,generation_profile_version=EXCLUDED.generation_profile_version,
embedding_profile_version=EXCLUDED.embedding_profile_version,updated_at=EXCLUDED.updated_at`,
		articleID, revisionID, generationID, embeddingID, generationProfile, embeddingProfile)
	if err != nil {
		t.Fatal(err)
	}
}

func readOneProjection(t *testing.T, env *testEnv, articleID int64) projectionApp.CurrentProjection {
	t.Helper()
	projections, err := postgres.NewSearchProjectionRepository(env.pool).Current(context.Background(), []int64{articleID})
	if err != nil || len(projections) != 1 {
		t.Fatalf("读取投影: %+v err=%v", projections, err)
	}
	return projections[0]
}

// TestSearchProjectionReadsOnlyCurrentFacts 覆盖 2.3：
// 投影只使用当前修订的当前 AI 选择，跨 revision 或跨 generation 的向量必须被忽略。
func TestSearchProjectionReadsOnlyCurrentFacts(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	seedAIUpgradeTasks(t, env)
	ctx := context.Background()

	// 公开但完全没有 AI 结果。
	projection := readOneProjection(t, env, 7101)
	if !projection.Document.Visible || projection.Target.Action != projectionDomain.ActionUpsert {
		t.Fatalf("公开文章必须是 upsert 目标: %+v", projection)
	}
	if projection.Document.Title == "" || projection.Document.PlainText == "" {
		t.Fatalf("文本投影不得依赖 AI 结果: %+v", projection.Document)
	}
	if projection.Document.Summary != "" || len(projection.Document.Vector) != 0 ||
		projection.Document.GenerationResultID != "" || projection.Document.EmbeddingResultID != "" {
		t.Fatalf("无 AI 结果时不得保留增强字段: %+v", projection.Document)
	}
	if projection.Target.GenerationResultID != nil || projection.Target.EmbeddingResultID != nil {
		t.Fatalf("无 AI 结果时目标不带结果引用: %+v", projection.Target)
	}

	// generation-only：文本增强可独立进入索引，向量仍为空。
	generation := testGenerationID
	insertGenerationResult(t, env, generation, "p1", 7101, 7111)
	setCurrentSelection(t, env, 7101, 7111, &generation, nil)
	projection = readOneProjection(t, env, 7101)
	if projection.Document.Summary != "AI 摘要" || len(projection.Document.Keywords) != 2 ||
		len(projection.Document.Vector) != 0 || projection.Document.EmbeddingResultID != "" {
		t.Fatalf("generation-only 投影错误: %+v", projection.Document)
	}
	if projection.Target.GenerationResultID == nil || *projection.Target.GenerationResultID != generation {
		t.Fatalf("目标必须带当前 generation 身份: %+v", projection.Target)
	}

	// 完整向量：Embedding 依赖当前 generation 选择。
	embedding := testEmbeddingID
	insertEmbeddingResult(t, env, embedding, generation, 7101, 7111)
	setCurrentSelection(t, env, 7101, 7111, &generation, &embedding)
	projection = readOneProjection(t, env, 7101)
	if len(projection.Document.Vector) != 3 || projection.Document.Vector[2] != 0.75 || projection.InconsistentSelection {
		t.Fatalf("完整向量投影错误: %+v", projection)
	}
	if projection.Document.EmbeddingResultID != embedding {
		t.Fatalf("文档必须记录向量身份: %+v", projection.Document)
	}

	// 不一致选择：当前 generation 已被替换，但 Embedding 仍依赖旧 generation。
	superseded := testSupersededID
	insertGenerationResult(t, env, superseded, "p2", 7101, 7111)
	setCurrentSelection(t, env, 7101, 7111, &superseded, &embedding)
	projection = readOneProjection(t, env, 7101)
	if !projection.InconsistentSelection {
		t.Fatal("跨 generation 的向量必须被标记为不一致")
	}
	if len(projection.Document.Vector) != 0 || projection.Document.EmbeddingResultID != embedding {
		t.Fatalf("不一致时不得写入向量，但必须保留当前选择身份: %+v", projection.Document)
	}
	if projection.Target.EmbeddingResultID == nil || *projection.Target.EmbeddingResultID != embedding {
		t.Fatalf("目标身份必须与写路径推进时一致: %+v", projection.Target)
	}

	// 下架：tombstone 目标不携带任何 AI 结果引用，文档不可检索。
	if _, err := postgres.NewArticleRepository(env.pool).SetArticleState(ctx, 7101, 1, articleDomain.StatusOffline,
		func() *articleDomain.OfflineReason { reason := articleDomain.OfflineByAdmin; return &reason }(), nil, nil, nil, fixedNow()); err != nil {
		t.Fatalf("管理员下架: %v", err)
	}
	projection = readOneProjection(t, env, 7101)
	if projection.Document.Visible || projection.Target.Action != projectionDomain.ActionTombstone {
		t.Fatalf("下架后必须是 tombstone: %+v", projection)
	}
	if projection.Target.GenerationResultID != nil || projection.Target.EmbeddingResultID != nil {
		t.Fatalf("tombstone 目标不得携带 AI 结果引用: %+v", projection.Target)
	}
	if projection.Document.Title != "" || projection.Document.Summary != "" || len(projection.Document.Vector) != 0 {
		t.Fatalf("tombstone 不得保留检索内容: %+v", projection.Document)
	}
	if projection.Document.InvisibleReason != "offline" {
		t.Fatalf("tombstone 必须记录不可见原因: %+v", projection.Document)
	}
}

// TestSearchProjectionSnapshotWatermarkAndIncrementalScan 覆盖 2.4：
// 快照按 article ID 稳定分页且只读公开文章，增量按 change sequence 无遗漏、无重复水位。
func TestSearchProjectionSnapshotWatermarkAndIncrementalScan(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx := context.Background()
	now := fixedNow()
	const authorID = "97000000-0000-0000-0000-000000000010"
	seedIntegrationUser(t, env, authorID, "snapshot_author", "user", now)
	repository := postgres.NewArticleRepository(env.pool)
	projection := postgres.NewSearchProjectionRepository(env.pool)

	var published []int64
	for _, seed := range []string{"a", "b", "c", "d", "e"} {
		published = append(published, createPublishedArticle(t, env, authorID, seed, now))
	}
	hidden := createPublishedArticle(t, env, authorID, "f", now)
	if _, err := repository.SetArticleState(ctx, hidden, 1, articleDomain.StatusOffline,
		func() *articleDomain.OfflineReason { reason := articleDomain.OfflineByAuthor; return &reason }(), nil, nil, nil, now); err != nil {
		t.Fatal(err)
	}

	// 快照分页：只读公开文章，按 article ID 升序，无遗漏、无重复。
	seen := make([]int64, 0, len(published))
	watermark := int64(0)
	for page := 0; ; page++ {
		if page > 10 {
			t.Fatal("快照分页未在有限页内结束")
		}
		items, hasMore, err := projection.SnapshotPage(ctx, projectionApp.SnapshotRequest{AfterArticleID: watermark, Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range items {
			if item.ArticleID <= watermark {
				t.Fatalf("水位必须严格前进: id=%d watermark=%d", item.ArticleID, watermark)
			}
			if item.ArticleID == hidden {
				t.Fatal("快照不得写入存量不可见文章")
			}
			watermark = item.ArticleID
			seen = append(seen, item.ArticleID)
		}
		if !hasMore {
			break
		}
	}
	if len(seen) != len(published) {
		t.Fatalf("快照分页遗漏文章: seen=%v want=%v", seen, published)
	}
	for index := range published {
		if seen[index] != published[index] {
			t.Fatalf("快照顺序必须按 article ID 升序: %v", seen)
		}
	}

	// 公开计数与确定性抽样。
	var wantCount int64
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.articles WHERE status='published'`).Scan(&wantCount); err != nil {
		t.Fatal(err)
	}
	if count, err := projection.PublicCount(ctx); err != nil || count != wantCount {
		t.Fatalf("公开计数 = %d，期望 %d，err=%v", count, wantCount, err)
	}
	sampled, err := projection.Sample(ctx, 3)
	if err != nil || len(sampled) != 3 {
		t.Fatalf("抽样: %+v err=%v", sampled, err)
	}
	again, err := projection.Sample(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	for index := range sampled {
		if sampled[index].ArticleID != again[index].ArticleID {
			t.Fatalf("抽样必须可重复: %v vs %v", sampled, again)
		}
		if sampled[index].ArticleID == hidden {
			t.Fatal("抽样只应包含公开文章")
		}
	}

	// 增量扫描：水位单调前进，重新扫描同一水位不会重复返回。
	first, err := projection.SinceChange(ctx, projectionApp.ChangeRequest{AfterChangeSeq: 0, Limit: 3})
	if err != nil || len(first.Jobs) != 3 || !first.HasMore {
		t.Fatalf("增量首页: %+v err=%v", first, err)
	}
	for index := 1; index < len(first.Jobs); index++ {
		if first.Jobs[index].ChangeSeq <= first.Jobs[index-1].ChangeSeq {
			t.Fatalf("增量必须按 change sequence 递增: %+v", first.Jobs)
		}
	}
	rest, err := projection.SinceChange(ctx, projectionApp.ChangeRequest{AfterChangeSeq: first.HighWaterSeq, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	overlap := map[int64]bool{}
	for _, job := range first.Jobs {
		overlap[job.ArticleID] = true
	}
	for _, job := range rest.Jobs {
		if overlap[job.ArticleID] {
			t.Fatalf("水位之后不得重复返回同一槽位: %d", job.ArticleID)
		}
	}
	if len(first.Jobs)+len(rest.Jobs) != len(published)+1 {
		t.Fatalf("增量扫描遗漏槽位: %d + %d vs %d", len(first.Jobs), len(rest.Jobs), len(published)+1)
	}

	// 快照扫过之后发生的修改必须落在更高的 change sequence 上，从而被增量追赶覆盖。
	markdown, html := "重建期间修订", "<p>重建期间修订</p>"
	if _, _, err := repository.UpdateUserRevision(ctx, published[0], authorID, 1,
		articleDomain.RevisionData{Title: "重建期间修订", Markdown: &markdown, SanitizedHTML: &html,
			PlainText: "重建期间修订", Excerpt: "重建期间修订", Language: "zh-CN",
			ContentHash: strings.Repeat("z", 64), SanitizerVersion: 1}, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	tail, err := projection.SinceChange(ctx, projectionApp.ChangeRequest{AfterChangeSeq: rest.HighWaterSeq, Limit: 100})
	if err != nil || len(tail.Jobs) != 1 || tail.Jobs[0].ArticleID != published[0] {
		t.Fatalf("重建期间修改必须进入增量水位之后: %+v err=%v", tail, err)
	}
	if tail.HighWaterSeq <= rest.HighWaterSeq {
		t.Fatalf("水位必须单调前进: %d -> %d", rest.HighWaterSeq, tail.HighWaterSeq)
	}
	if job, _ := readProjectionJob(t, env, published[0]); job.Generation != tail.Jobs[0].Generation {
		t.Fatalf("增量扫描必须返回最新 generation: %+v vs %d", job, tail.Jobs[0].Generation)
	}
}
