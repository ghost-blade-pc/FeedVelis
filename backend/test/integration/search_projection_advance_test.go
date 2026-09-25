package integration

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	articleApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/article"
	enrichmentApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/enrichment"
	idempotencyApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/idempotency"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	accountDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
	sourceDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/source"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/content/markdown"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/fetcher/httpfeed"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

const testPhysicalIndex = "velis-articles-v1-20260919t120000z-aaaaaa"

type projectionJobRow struct {
	Action             string
	Generation         int64
	ChangeSeq          int64
	LockVersion        int64
	RevisionID         int64
	GenerationResultID *string
	EmbeddingResultID  *string
	Status             string
}

// seedSearchIndexState 建立当前服务索引，使 delivery 可以被写入。
func seedSearchIndexState(t *testing.T, env *testEnv, index string) {
	t.Helper()
	_, err := env.pool.Exec(context.Background(), `INSERT INTO velis.search_index_state
(read_alias,write_alias,current_index,schema_version,schema_identity,updated_at)
VALUES('velis-articles-read','velis-articles-write',$1,1,'mapping-v1|cjk-v1|dim-3|encoding-v1',now())
ON CONFLICT (id) DO UPDATE SET current_index=EXCLUDED.current_index,rollback_index=NULL,rollback_deadline=NULL,updated_at=EXCLUDED.updated_at`, index)
	if err != nil {
		t.Fatal(err)
	}
}

func readProjectionJob(t *testing.T, env *testEnv, articleID int64) (projectionJobRow, bool) {
	t.Helper()
	var row projectionJobRow
	err := env.pool.QueryRow(context.Background(), `SELECT action::text,generation,change_seq,article_lock_version,revision_id,
generation_result_id::text,embedding_result_id::text,status::text
FROM velis.search_projection_jobs WHERE article_id=$1`, articleID).Scan(&row.Action, &row.Generation, &row.ChangeSeq,
		&row.LockVersion, &row.RevisionID, &row.GenerationResultID, &row.EmbeddingResultID, &row.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return projectionJobRow{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return row, true
}

func seedIntegrationUser(t *testing.T, env *testEnv, id, username, role string, now time.Time) {
	t.Helper()
	_, err := env.pool.Exec(context.Background(), `INSERT INTO velis.users
(id,username,nickname,password_hash,role,status,created_at,updated_at)
VALUES ($1,$2,$2,'hash',$3,'active',$4,$4)`, id, username, role, now)
	if err != nil {
		t.Fatal(err)
	}
}

func seedRSSSource(t *testing.T, env *testEnv, now time.Time) int64 {
	t.Helper()
	source, inserted, err := postgres.NewSourceRepository(env.pool).Add(context.Background(),
		"https://projection.example/feed", "https://projection.example/feed", "投影来源", sourceDomain.DefaultFetchInterval, now)
	if err != nil || !inserted {
		t.Fatalf("创建 Source: inserted=%t err=%v", inserted, err)
	}
	return source.ID
}

// TestSearchProjectionTargetIsUniqueAndMonotonicUnderConcurrency 覆盖 2.1：
// 同一文章的并发公开状态变化必须收敛为唯一槽位，generation 与 change sequence 严格单调。
func TestSearchProjectionTargetIsUniqueAndMonotonicUnderConcurrency(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx := context.Background()
	now := fixedNow()
	seedSearchIndexState(t, env, testPhysicalIndex)
	const authorID = "92000000-0000-0000-0000-000000000001"
	seedIntegrationUser(t, env, authorID, "projection_writer", "user", now)

	repository := postgres.NewArticleRepository(env.pool)
	markdown, html := "并发正文", "<p>并发正文</p>"
	created, err := repository.CreateUserArticle(ctx, authorID, articleDomain.RevisionData{
		Title: "并发文章", Markdown: &markdown, SanitizedHTML: &html, PlainText: "并发正文", Excerpt: "并发正文",
		Language: "zh-CN", ContentHash: strings.Repeat("a", 64), SanitizerVersion: 1}, true, now)
	if err != nil || created.Status != articleDomain.StatusPublished {
		t.Fatalf("创建公开文章: %+v err=%v", created, err)
	}
	first, ok := readProjectionJob(t, env, created.ID)
	if !ok || first.Action != "upsert" || first.Generation != 1 {
		t.Fatalf("首次公开必须建立 generation 1 的 upsert 槽位: %+v", first)
	}

	// 每个写入者反复切换公开状态；乐观并发冲突时重试，每次成功切换都必须让槽位前进一格。
	const writers, perWriter = 6, 4
	var wait sync.WaitGroup
	conflicts := make([]int, writers)
	failures := make([]error, writers)
	for slot := range writers {
		wait.Add(1)
		go func(slot int) {
			defer wait.Done()
			done := 0
			for attempt := 0; attempt < 500 && done < perWriter; attempt++ {
				var lockVersion int64
				var status articleDomain.Status
				if err := env.pool.QueryRow(ctx, `SELECT lock_version,status FROM velis.articles WHERE id=$1`, created.ID).Scan(&lockVersion, &status); err != nil {
					failures[slot] = err
					return
				}
				target, reason := articleDomain.StatusOffline, articleDomain.OfflineByAuthor
				if status == articleDomain.StatusOffline {
					target, reason = articleDomain.StatusPublished, articleDomain.OfflineReason("")
				}
				var reasonPointer *articleDomain.OfflineReason
				if target == articleDomain.StatusOffline {
					reasonPointer = &reason
				}
				offlineAt := now
				if _, err := repository.SetArticleState(ctx, created.ID, lockVersion, target, reasonPointer, nil, &offlineAt, nil, now); err != nil {
					if errors.Is(err, articleDomain.ErrVersionConflict) {
						conflicts[slot]++
						continue
					}
					failures[slot] = err
					return
				}
				done++
			}
			if done != perWriter {
				failures[slot] = fmt.Errorf("并发重试未完成: done=%d conflicts=%d", done, conflicts[slot])
			}
		}(slot)
	}
	wait.Wait()
	for slot, err := range failures {
		if err != nil {
			t.Fatalf("并发写入者 %d: %v", slot, err)
		}
	}

	// 每篇文章只允许一个槽位。
	var rows int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.search_projection_jobs WHERE article_id=$1`, created.ID).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("每文章唯一槽位: rows=%d err=%v", rows, err)
	}
	job, _ := readProjectionJob(t, env, created.ID)
	if want := int64(writers*perWriter + 1); job.Generation != want {
		t.Fatalf("每次成功切换都必须递增 generation: generation=%d，期望 %d", job.Generation, want)
	}
	if job.LockVersion != job.Generation {
		t.Fatalf("槽位必须与文章版本同步: lock_version=%d generation=%d", job.LockVersion, job.Generation)
	}
	var currentRevision int64
	var currentStatus articleDomain.Status
	if err := env.pool.QueryRow(ctx, `SELECT current_revision_id,status FROM velis.articles WHERE id=$1`, created.ID).Scan(&currentRevision, &currentStatus); err != nil {
		t.Fatal(err)
	}
	if job.RevisionID != currentRevision {
		t.Fatalf("槽位必须指向当前修订: %d vs %d", job.RevisionID, currentRevision)
	}
	wantAction := "upsert"
	if currentStatus != articleDomain.StatusPublished {
		wantAction = "tombstone"
	}
	if job.Action != wantAction {
		t.Fatalf("槽位动作必须跟随最终公开状态 %s: %+v", currentStatus, job)
	}
	// 全部 delivery 都必须被拉回当前 generation，否则旧索引会被漏写。
	var lagging int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.search_projection_deliveries
WHERE article_id=$1 AND (required_generation<>$2 OR status<>'pending')`, created.ID, job.Generation).Scan(&lagging); err != nil || lagging != 0 {
		t.Fatalf("目标变化必须重置活动 delivery: lagging=%d err=%v", lagging, err)
	}
	// 事实完全相同时必须是不取新序列的 noop。
	if changed, err := postgres.NewSearchProjectionRepository(env.pool).Advance(ctx, created.ID, now); err != nil || changed {
		t.Fatalf("相同事实必须为 noop: changed=%t err=%v", changed, err)
	}
	after, _ := readProjectionJob(t, env, created.ID)
	if after.Generation != job.Generation || after.ChangeSeq != job.ChangeSeq {
		t.Fatalf("noop 不得修改槽位: before=%+v after=%+v", job, after)
	}
}

// TestSearchProjectionTargetFollowsRSSAndUserLifecycle 覆盖 3.1 与 3.2：
// 文章事实、Outbox 与投影目标同事务提交，草稿与下架保持 tombstone。
func TestSearchProjectionTargetFollowsRSSAndUserLifecycle(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx := context.Background()
	now := fixedNow()
	seedSearchIndexState(t, env, testPhysicalIndex)
	sourceID := seedRSSSource(t, env, now)
	repository := postgres.NewArticleRepository(env.pool)

	// 3.1：RSS 首次发布、无变化、公开修订；事实、Outbox 与投影目标同事务提交。
	itemID := "projection-item"
	sanitizer := httpfeed.NewSanitizer()
	ingest := func(at time.Time, content string) articleApp.IngestReport {
		t.Helper()
		service := articleApp.NewServiceWithOutbox(repository, sanitizer, &articleTestClock{now: at},
			postgres.NewTxManager(env.pool), postgres.NewOutboxRepository(env.pool))
		report, err := service.Ingest(ctx, sourceID, []ports.ParsedItem{{
			ID: &itemID, URL: "https://projection.example/article", Title: "投影文章", Content: &content}})
		if err != nil {
			t.Fatal(err)
		}
		return report
	}
	if report := ingest(now, "第一版"); report.Inserted != 1 {
		t.Fatalf("RSS 首次发布: %+v", report)
	}
	var articleID, revisionID int64
	if err := env.pool.QueryRow(ctx, `SELECT id,current_revision_id FROM velis.articles WHERE source_id=$1`, sourceID).Scan(&articleID, &revisionID); err != nil {
		t.Fatal(err)
	}
	rssJob, _ := readProjectionJob(t, env, articleID)
	if rssJob.Action != "upsert" || rssJob.Generation != 1 || rssJob.RevisionID != revisionID {
		t.Fatalf("RSS 发布目标: %+v", rssJob)
	}
	if report := ingest(now.Add(time.Minute), "第一版"); report.Unchanged != 1 {
		t.Fatalf("重复内容: %+v", report)
	}
	if same, _ := readProjectionJob(t, env, articleID); same.Generation != 1 {
		t.Fatalf("重复内容不得推进 generation: %+v", same)
	}
	if report := ingest(now.Add(2*time.Minute), "第二版"); report.Updated != 1 {
		t.Fatalf("RSS 修订: %+v", report)
	}
	var revisedRevision int64
	if err := env.pool.QueryRow(ctx, `SELECT current_revision_id FROM velis.articles WHERE id=$1`, articleID).Scan(&revisedRevision); err != nil {
		t.Fatal(err)
	}
	if revisedJob, _ := readProjectionJob(t, env, articleID); revisedJob.Generation != 2 || revisedJob.RevisionID != revisedRevision {
		t.Fatalf("RSS 修订必须指向新修订: %+v", revisedJob)
	}
	// Outbox 与投影目标在同一事务里：事件数必须与公开变化次数一致。
	var events int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.outbox_events WHERE aggregate_id=$1`, strconv.FormatInt(articleID, 10)).Scan(&events); err != nil || events != 2 {
		t.Fatalf("RSS Outbox 事件数 = %d err=%v", events, err)
	}

	// 3.2：用户文章草稿、发布、编辑、下架、恢复、删除。
	const authorID = "93000000-0000-0000-0000-000000000001"
	const adminID = "93000000-0000-0000-0000-000000000002"
	seedIntegrationUser(t, env, authorID, "projection_author", "user", now)
	seedIntegrationUser(t, env, adminID, "projection_admin", "admin", now)
	service := newUserArticleStack(t, env, now)
	adminService := articleApp.NewAdminService(repository, idempotencyApp.NewService(postgres.NewIdempotencyRepository(env.pool), postgres.NewTxManager(env.pool), 24*time.Hour), &articleTestClock{now: now})

	draft, _, err := service.Create(ctx, articleApp.CreateUserArticleCommand{AuthorUserID: authorID,
		IdempotencyKey: "93000000-0000-0000-0000-000000000011", Title: "草稿", Markdown: "草稿正文", InitialStatus: articleDomain.StatusDraft})
	if err != nil {
		t.Fatal(err)
	}
	draftJob, ok := readProjectionJob(t, env, draft.Article.ID)
	if !ok || draftJob.Action != "tombstone" || draftJob.Generation != 1 {
		t.Fatalf("草稿必须保持 tombstone 目标: %+v", draftJob)
	}
	if _, _, err := service.Publish(ctx, articleApp.UserArticleStateCommand{AuthorUserID: authorID, ArticleID: draft.Article.ID,
		ExpectedVersion: draft.Article.LockVersion, IdempotencyKey: "93000000-0000-0000-0000-000000000012"}); err != nil {
		t.Fatal(err)
	}
	publishedJob, _ := readProjectionJob(t, env, draft.Article.ID)
	if publishedJob.Action != "upsert" || publishedJob.Generation != 2 {
		t.Fatalf("发布必须收敛为 upsert 目标: %+v", publishedJob)
	}

	edit, _, err := service.Update(ctx, articleApp.UpdateUserArticleCommand{AuthorUserID: authorID, ArticleID: draft.Article.ID,
		ExpectedVersion: publishedJob.LockVersion, IdempotencyKey: "93000000-0000-0000-0000-000000000013", Title: "公开修订", Markdown: "新的公开正文"})
	if err != nil {
		t.Fatal(err)
	}
	editedJob, _ := readProjectionJob(t, env, draft.Article.ID)
	if editedJob.Action != "upsert" || editedJob.Generation != 3 || editedJob.RevisionID != edit.Article.RevisionID {
		t.Fatalf("公开编辑: %+v", editedJob)
	}

	offlined, _, err := service.Offline(ctx, articleApp.UserArticleStateCommand{AuthorUserID: authorID, ArticleID: draft.Article.ID,
		ExpectedVersion: editedJob.LockVersion, IdempotencyKey: "93000000-0000-0000-0000-000000000014"})
	if err != nil {
		t.Fatal(err)
	}
	offlineJob, _ := readProjectionJob(t, env, draft.Article.ID)
	if offlineJob.Action != "tombstone" || offlineJob.Generation != 4 || offlineJob.GenerationResultID != nil {
		t.Fatalf("作者下架必须收敛为 tombstone: %+v", offlineJob)
	}
	// 下架期间作者继续编辑：仍必须是 tombstone，不得产生可公开的 upsert 目标。
	offlineEdit, _, err := service.Update(ctx, articleApp.UpdateUserArticleCommand{AuthorUserID: authorID, ArticleID: draft.Article.ID,
		ExpectedVersion: offlined.Article.LockVersion, IdempotencyKey: "93000000-0000-0000-0000-000000000015", Title: "离线编辑", Markdown: "离线正文"})
	if err != nil {
		t.Fatal(err)
	}
	offlineEditedJob, _ := readProjectionJob(t, env, draft.Article.ID)
	if offlineEditedJob.Action != "tombstone" || offlineEditedJob.Generation != 5 || offlineEditedJob.RevisionID != offlineEdit.Article.RevisionID {
		t.Fatalf("离线编辑必须保持 tombstone: %+v", offlineEditedJob)
	}

	// 管理员恢复公开。
	if _, _, err := adminService.Restore(ctx, articleApp.AdminArticleCommand{Actor: articleApp.AdminActor{UserID: adminID, Role: accountDomain.RoleAdmin},
		ArticleID: draft.Article.ID, ExpectedVersion: offlineEdit.Article.LockVersion, IdempotencyKey: "93000000-0000-0000-0000-000000000016"}); !errors.Is(err, articleDomain.ErrNotFound) {
		t.Fatalf("作者下架的文章必须对管理员不可见: %v", err)
	}
	restored, _, err := service.Publish(ctx, articleApp.UserArticleStateCommand{AuthorUserID: authorID, ArticleID: draft.Article.ID,
		ExpectedVersion: offlineEdit.Article.LockVersion, IdempotencyKey: "93000000-0000-0000-0000-000000000017"})
	if err != nil {
		t.Fatal(err)
	}
	restoredJob, _ := readProjectionJob(t, env, draft.Article.ID)
	if restoredJob.Action != "upsert" || restoredJob.Generation != 6 {
		t.Fatalf("重新发布必须恢复 upsert 目标: %+v", restoredJob)
	}

	// 管理员下架后作者不能自行恢复。
	if _, _, err := adminService.Offline(ctx, articleApp.AdminArticleCommand{Actor: articleApp.AdminActor{UserID: adminID, Role: accountDomain.RoleAdmin},
		ArticleID: draft.Article.ID, ExpectedVersion: restored.Article.LockVersion, IdempotencyKey: "93000000-0000-0000-0000-000000000018"}); err != nil {
		t.Fatal(err)
	}
	adminOfflineJob, _ := readProjectionJob(t, env, draft.Article.ID)
	if adminOfflineJob.Action != "tombstone" || adminOfflineJob.Generation != 7 {
		t.Fatalf("管理员下架: %+v", adminOfflineJob)
	}

	deleted, _, err := service.Delete(ctx, articleApp.UserArticleStateCommand{AuthorUserID: authorID, ArticleID: draft.Article.ID,
		ExpectedVersion: adminOfflineJob.LockVersion, IdempotencyKey: "93000000-0000-0000-0000-000000000019"})
	if err != nil {
		t.Fatal(err)
	}
	deletedJob, _ := readProjectionJob(t, env, draft.Article.ID)
	if deletedJob.Action != "tombstone" || deletedJob.Generation != 8 || deletedJob.RevisionID != deleted.Article.RevisionID {
		t.Fatalf("删除必须保留 tombstone 目标: %+v", deletedJob)
	}
}

// TestSearchProjectionTargetRollsBackWithArticleTransaction 覆盖 3.2 的失败回滚：
// 投影槽位无法持久化时，文章事实、修订、Outbox 与幂等结果必须一起回滚。
func TestSearchProjectionTargetRollsBackWithArticleTransaction(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx := context.Background()
	now := fixedNow()
	seedSearchIndexState(t, env, testPhysicalIndex)
	const authorID = "94000000-0000-0000-0000-000000000001"
	seedIntegrationUser(t, env, authorID, "rollback_author", "user", now)
	service := newUserArticleStack(t, env, now)

	if _, err := env.pool.Exec(ctx, `CREATE FUNCTION velis.test_fail_projection_advance() RETURNS trigger
LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION '注入故障：投影目标推进失败'; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(ctx, `CREATE TRIGGER test_fail_projection_advance BEFORE INSERT OR UPDATE
ON velis.search_projection_jobs FOR EACH ROW EXECUTE FUNCTION velis.test_fail_projection_advance()`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = env.pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS test_fail_projection_advance ON velis.search_projection_jobs;
DROP FUNCTION IF EXISTS velis.test_fail_projection_advance()`)
	})

	_, _, err := service.Create(ctx, articleApp.CreateUserArticleCommand{AuthorUserID: authorID,
		IdempotencyKey: "94000000-0000-0000-0000-000000000011", Title: "会失败的文章", Markdown: "正文", InitialStatus: articleDomain.StatusPublished})
	if err == nil {
		t.Fatal("投影目标写入失败必须让整个用例失败")
	}
	var articles, revisions, events, operations int
	if err := env.pool.QueryRow(ctx, `SELECT
(SELECT count(*) FROM velis.articles WHERE author_user_id=$1),
(SELECT count(*) FROM velis.article_versions v JOIN velis.articles a ON a.id=v.article_id WHERE a.author_user_id=$1),
(SELECT count(*) FROM velis.outbox_events),
(SELECT count(*) FROM velis.idempotency_operations WHERE actor_user_id=$1)`, authorID).Scan(&articles, &revisions, &events, &operations); err != nil {
		t.Fatal(err)
	}
	if articles != 0 || revisions != 0 || events != 0 || operations != 0 {
		t.Fatalf("失败必须整体回滚: articles=%d revisions=%d events=%d operations=%d", articles, revisions, events, operations)
	}

	// 故障排除后同一幂等键必须可以安全重试。
	if _, err := env.pool.Exec(ctx, `DROP TRIGGER test_fail_projection_advance ON velis.search_projection_jobs`); err != nil {
		t.Fatal(err)
	}
	created, _, err := service.Create(ctx, articleApp.CreateUserArticleCommand{AuthorUserID: authorID,
		IdempotencyKey: "94000000-0000-0000-0000-000000000011", Title: "会失败的文章", Markdown: "正文", InitialStatus: articleDomain.StatusPublished})
	if err != nil {
		t.Fatalf("重试: %v", err)
	}
	retriedJob, ok := readProjectionJob(t, env, created.Article.ID)
	if !ok || retriedJob.Action != "upsert" || retriedJob.Generation != 1 {
		t.Fatalf("重试后的目标: %+v", retriedJob)
	}
}

// TestSearchProjectionTargetFollowsAISelectionSwitch 覆盖 3.3：
// generation 与 Embedding 当前选择切换各自在同一事务推进目标，重复保存与迟到结果不推进。
func TestSearchProjectionTargetFollowsAISelectionSwitch(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	seedAIUpgradeTasks(t, env)
	ctx := context.Background()
	now := time.Now().UTC()
	seedSearchIndexState(t, env, testPhysicalIndex)
	repository := postgres.NewEnrichmentRepository(env.pool)
	gen := &enrichmentApp.ActiveProfile{Provider: "stub", Model: "chat", ProfileVersion: "g-v1", WorkflowVersion: "w-v1",
		PromptVersion: "p-v1", StructuredOutput: "prompt", MaxAttempts: 3, AuditTokenBudget: 100, StageBudget: time.Minute}
	embed := &enrichmentApp.ActiveProfile{Provider: "stub", Model: "embed", ProfileVersion: "e-v1", InputVersion: "i-v1",
		Dimensions: 2, MaxAttempts: 3, AuditTokenBudget: 100, StageBudget: time.Minute}

	task, err := repository.Claim(ctx, enrichmentApp.ClaimRequest{Owner: "projection-worker", Lease: time.Minute, Generation: gen, Embedding: embed, Now: now})
	if err != nil || task == nil {
		t.Fatalf("认领 generation 任务: %+v err=%v", task, err)
	}
	result := enrichmentApp.GenerationResult{ID: "93000000-0000-0000-0000-0000000000a1", ArticleID: task.ArticleID, RevisionID: task.RevisionID,
		Profile: *gen, InputHash: strings.Repeat("d", 64), Content: enrichmentApp.GeneratedContent{Summary: "摘要", Keywords: []string{"词"}, Topics: []string{"主题"}}, GeneratedAt: now}
	if ok, err := repository.SaveGeneration(ctx, *task, result, true, now.Add(time.Second)); err != nil || !ok {
		t.Fatalf("保存 generation: ok=%t err=%v", ok, err)
	}
	afterGeneration, ok := readProjectionJob(t, env, task.ArticleID)
	if !ok || afterGeneration.Action != "upsert" || afterGeneration.Generation != 1 {
		t.Fatalf("generation 切换必须推进无向量目标: %+v", afterGeneration)
	}
	if afterGeneration.GenerationResultID == nil || *afterGeneration.GenerationResultID != result.ID || afterGeneration.EmbeddingResultID != nil {
		t.Fatalf("generation 目标必须带新结果且向量为空: %+v", afterGeneration)
	}
	// 迟到执行者无法再次切换：旧 token 已被 fencing 拒绝，目标保持不变。
	if ok, err := repository.SaveGeneration(ctx, *task, result, true, now.Add(2*time.Second)); err != nil || ok {
		t.Fatalf("迟到 generation 应被拒绝: ok=%t err=%v", ok, err)
	}
	if unchanged, _ := readProjectionJob(t, env, task.ArticleID); unchanged.Generation != 1 {
		t.Fatalf("被拒绝的结果不得推进目标: %+v", unchanged)
	}

	embedTask, err := repository.Claim(ctx, enrichmentApp.ClaimRequest{Owner: "projection-worker", Lease: time.Minute, Generation: gen, Embedding: embed, Now: now.Add(2 * time.Second)})
	if err != nil || embedTask == nil || embedTask.Stage != "embedding" {
		t.Fatalf("认领 embedding 任务: %+v err=%v", embedTask, err)
	}
	embedding := enrichmentApp.EmbeddingResult{ID: "93000000-0000-0000-0000-0000000000a2", GenerationResultID: result.ID,
		ArticleID: embedTask.ArticleID, RevisionID: embedTask.RevisionID, Profile: *embed, InputHash: strings.Repeat("e", 64), Vector: []float64{0.25, 0.75}, GeneratedAt: now}
	if ok, err := repository.SaveEmbedding(ctx, *embedTask, embedding, now.Add(3*time.Second)); err != nil || !ok {
		t.Fatalf("保存 embedding: ok=%t err=%v", ok, err)
	}
	afterEmbedding, _ := readProjectionJob(t, env, task.ArticleID)
	if afterEmbedding.Generation != 2 || afterEmbedding.EmbeddingResultID == nil || *afterEmbedding.EmbeddingResultID != embedding.ID {
		t.Fatalf("Embedding 切换必须推进带向量目标: %+v", afterEmbedding)
	}
	// 旧任务代次的结果不能再次切换当前选择，因此也不能推进目标。
	if ok, err := repository.SaveEmbedding(ctx, *embedTask, embedding, now.Add(4*time.Second)); err != nil || ok {
		t.Fatalf("迟到 embedding 应被拒绝: ok=%t err=%v", ok, err)
	}
	if final, _ := readProjectionJob(t, env, task.ArticleID); final.Generation != 2 {
		t.Fatalf("重复保存不得推进目标: %+v", final)
	}
}

func newUserArticleStack(t *testing.T, env *testEnv, now time.Time) *articleApp.UserService {
	t.Helper()
	return articleApp.NewUserServiceWithOutbox(postgres.NewArticleRepository(env.pool), markdown.NewUserRenderer(),
		postgres.NewArticleAssetRepository(env.pool),
		idempotencyApp.NewService(postgres.NewIdempotencyRepository(env.pool), postgres.NewTxManager(env.pool), 24*time.Hour),
		&articleTestClock{now: now}, articleApp.AssetPolicy{MaxImages: 20, MaxTotalBytes: 50 << 20},
		postgres.NewOutboxRepository(env.pool))
}
