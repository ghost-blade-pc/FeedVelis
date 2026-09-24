package integration

import (
	"context"
	"os"
	"testing"
	"time"

	articleApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/asynctask"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/enrichment"
	idempotencyApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/idempotency"
	relayApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/relay"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
	einoAdapter "github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/ai/eino"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/content/markdown"
	rabbitAdapter "github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/messaging/rabbitmq"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
	amqp "github.com/rabbitmq/amqp091-go"
)

func TestReliableAsyncPipelineAccumulatesWithoutMQAndCatchesUpAfterRecovery(t *testing.T) {
	url := os.Getenv("VELIS_TEST_RABBITMQ_URL")
	if url == "" {
		t.Skip("未设置 VELIS_TEST_RABBITMQ_URL")
	}
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	now := fixedNow()
	const authorID = "59000000-0000-0000-0000-000000000001"
	if _, err := env.pool.Exec(ctx, `INSERT INTO velis.users
(id,username,nickname,password_hash,role,status,created_at,updated_at)
VALUES($1,'e2e_author','作者','hash','user','active',$2,$2)`, authorID, now); err != nil {
		t.Fatal(err)
	}
	connection, err := amqp.Dial(url)
	if err != nil {
		t.Fatal(err)
	}
	channel, err := connection.Channel()
	if err != nil {
		t.Fatal(err)
	}
	if err := rabbitAdapter.DeclareTopology(channel); err != nil {
		t.Fatal(err)
	}
	if _, err := channel.QueuePurge(rabbitAdapter.ConsumerQueue, false); err != nil {
		t.Fatal(err)
	}
	if _, err := channel.QueuePurge(rabbitAdapter.DeadQueue, false); err != nil {
		t.Fatal(err)
	}
	_ = channel.Close()
	_ = connection.Close()

	tx := postgres.NewTxManager(env.pool)
	idempotency := idempotencyApp.NewService(postgres.NewIdempotencyRepository(env.pool), tx, 24*time.Hour)
	articleRepository := postgres.NewArticleRepository(env.pool)
	service := articleApp.NewUserServiceWithOutbox(articleRepository, markdown.NewUserRenderer(), postgres.NewArticleAssetRepository(env.pool),
		idempotency, &articleTestClock{now: now}, articleApp.AssetPolicy{MaxImages: 20, MaxTotalBytes: 50 << 20}, postgres.NewOutboxRepository(env.pool))
	published, _, err := service.Create(ctx, articleApp.CreateUserArticleCommand{AuthorUserID: authorID,
		IdempotencyKey: "59000000-0000-0000-0000-000000000011", Title: "可靠异步", Markdown: "正文", InitialStatus: articleDomain.StatusPublished})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rabbitAdapter.DialPublisher("amqp://guest:guest@127.0.0.1:1/"); err == nil {
		t.Fatal("不可达 MQ 端点应连接失败")
	}
	if _, err := articleRepository.GetPublished(ctx, published.Article.ID); err != nil {
		t.Fatalf("MQ 不可用不应影响文章读取: %v", err)
	}
	beforeAI, err := articleRepository.GetPublished(ctx, published.Article.ID)
	if err != nil || beforeAI.Item.Enhancement != nil {
		t.Fatalf("发布后应立即可读且增强为空: item=%+v err=%v", beforeAI.Item, err)
	}
	relayRepository := postgres.NewRelayRepository(env.pool)
	stats, err := relayRepository.PendingStats(ctx)
	if err != nil || stats.Pending != 1 {
		t.Fatalf("MQ 不可用时 Outbox pending=%d err=%v", stats.Pending, err)
	}

	publisher, err := rabbitAdapter.DialPublisher(url)
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()
	relay := relayApp.NewService(relayRepository, publisher, relayApp.Config{Owner: "e2e-worker", BatchSize: 10, PublishWindow: 2,
		Lease: time.Minute, ConfirmTimeout: 3 * time.Second, BackoffMin: time.Second, BackoffMax: time.Minute})
	if err := relay.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	stats, err = relayRepository.PendingStats(ctx)
	if err != nil || stats.Pending != 0 {
		t.Fatalf("恢复后 Outbox pending=%d err=%v", stats.Pending, err)
	}
	inbox, err := postgres.NewConsumedEventRepository(asynctask.ConsumerName)
	if err != nil {
		t.Fatal(err)
	}
	projector := asynctask.NewService(tx, inbox, postgres.NewAsyncTaskRepository(), func() time.Time { return now })
	consumerCtx, stopConsumer := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- rabbitAdapter.NewConsumer(url, 16, projector, func(error) bool { return false }).Run(consumerCtx)
	}()
	for ctx.Err() == nil {
		var status string
		err = env.pool.QueryRow(ctx, `SELECT status FROM velis.async_tasks WHERE article_id=$1`, published.Article.ID).Scan(&status)
		if err == nil {
			if status != "pending" {
				t.Fatalf("任务状态=%s", status)
			}
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if ctx.Err() != nil {
		t.Fatal("等待恢复后异步任务超时")
	}
	stopConsumer()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	workflow, err := einoAdapter.NewWorkflow(ctx, e2eChatModel{})
	if err != nil {
		t.Fatal(err)
	}
	gen := &enrichment.ActiveProfile{Provider: "deterministic", Model: "chat-stub", ProfileVersion: "g-rabbit-e2e", WorkflowVersion: "w1", PromptVersion: "p1", MaxAttempts: 2, AuditTokenBudget: 1000, StageBudget: time.Second}
	embed := &enrichment.ActiveProfile{Provider: "deterministic", Model: "embed-stub", ProfileVersion: "e-rabbit-e2e", InputVersion: "i1", Dimensions: 3, MaxAttempts: 2, AuditTokenBudget: 1000, StageBudget: time.Second}
	executor := enrichment.NewExecutor(postgres.NewEnrichmentRepository(env.pool), workflow, einoAdapter.NewEmbeddingAdapter(e2eEmbedder{}, 3), gen, embed,
		enrichment.ExecutorPolicy{Lease: time.Minute, GenerationBackoffMin: time.Second, GenerationBackoffMax: time.Minute, EmbeddingBackoffMin: time.Second, EmbeddingBackoffMax: time.Minute, ChunkChars: 1000, MaxChunks: 8, SingleInputChars: 12000, MaxCalls: 9, Concurrency: 2, MaxOutputTokens: 1000, OutputLimits: enrichment.OutputLimits{SummaryChars: 1000, KeywordCount: 12, TopicCount: 5, LabelChars: 64}, EmbeddingBodyChars: 12000}, nil, nil)
	for range 2 {
		processed, runErr := executor.ProcessOne(ctx, "rabbit-e2e-worker")
		if runErr != nil || !processed {
			t.Fatalf("AI 闭环 processed=%t err=%v", processed, runErr)
		}
	}
	afterAI, err := articleRepository.GetPublished(ctx, published.Article.ID)
	if err != nil || afterAI.Item.Enhancement == nil || afterAI.Item.Enhancement.Summary != "E2E AI 摘要" {
		t.Fatalf("AI 结果未进入公开读取: item=%+v err=%v", afterAI.Item, err)
	}
	var vectorCount int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.ai_embedding_results WHERE article_id=$1 AND dimensions=3`, published.Article.ID).Scan(&vectorCount); err != nil || vectorCount != 1 {
		t.Fatalf("向量未持久化 count=%d err=%v", vectorCount, err)
	}
	edited, _, err := service.Update(ctx, articleApp.UpdateUserArticleCommand{AuthorUserID: authorID, ArticleID: published.Article.ID,
		ExpectedVersion: published.Article.LockVersion, IdempotencyKey: "59000000-0000-0000-0000-000000000012", Title: "可靠异步二版", Markdown: "新正文"})
	if err != nil {
		t.Fatal(err)
	}
	afterEdit, err := articleRepository.GetPublished(ctx, edited.Article.ID)
	if err != nil || afterEdit.Item.Enhancement != nil || afterEdit.Item.Excerpt != "新正文" {
		t.Fatalf("编辑后旧增强应立即隐藏: item=%+v err=%v", afterEdit.Item, err)
	}
}
