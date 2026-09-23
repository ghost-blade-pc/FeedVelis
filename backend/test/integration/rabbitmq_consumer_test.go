package integration

import (
	"context"
	"os"
	"testing"
	"time"

	articleApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articleevent"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/asynctask"
	idempotencyApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/idempotency"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/content/markdown"
	rabbitAdapter "github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/messaging/rabbitmq"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
	amqp "github.com/rabbitmq/amqp091-go"
)

type reportingProjector struct {
	delegate *asynctask.Service
	outcomes chan asynctask.Outcome
}

func (p reportingProjector) Project(ctx context.Context, event articleevent.Envelope) (asynctask.Outcome, error) {
	outcome, err := p.delegate.Project(ctx, event)
	if err == nil {
		p.outcomes <- outcome
	}
	return outcome, err
}

func TestRabbitMQDuplicateDeliveryUsesInboxWithoutAdvancingTask(t *testing.T) {
	url := os.Getenv("VELIS_TEST_RABBITMQ_URL")
	if url == "" {
		t.Skip("未设置 VELIS_TEST_RABBITMQ_URL")
	}
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx := context.Background()
	now := fixedNow()
	const authorID = "57000000-0000-0000-0000-000000000001"
	if _, err := env.pool.Exec(ctx, `INSERT INTO velis.users
(id,username,nickname,password_hash,role,status,created_at,updated_at)
VALUES($1,'rabbit_author','作者','hash','user','active',$2,$2)`, authorID, now); err != nil {
		t.Fatal(err)
	}
	tx := postgres.NewTxManager(env.pool)
	idempotency := idempotencyApp.NewService(postgres.NewIdempotencyRepository(env.pool), tx, 24*time.Hour)
	articles := articleApp.NewUserService(postgres.NewArticleRepository(env.pool), markdown.NewUserRenderer(), postgres.NewArticleAssetRepository(env.pool),
		idempotency, &articleTestClock{now: now}, articleApp.AssetPolicy{MaxImages: 20, MaxTotalBytes: 50 << 20})
	published, _, err := articles.Create(ctx, articleApp.CreateUserArticleCommand{AuthorUserID: authorID,
		IdempotencyKey: "57000000-0000-0000-0000-000000000011", Title: "RabbitMQ", Markdown: "正文", InitialStatus: articleDomain.StatusPublished})
	if err != nil {
		t.Fatal(err)
	}
	event := mustPublicEvent(t, ctx, articleevent.PublishedType, published.Article, now)

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

	inbox, err := postgres.NewConsumedEventRepository(asynctask.ConsumerName)
	if err != nil {
		t.Fatal(err)
	}
	projector := asynctask.NewService(tx, inbox, postgres.NewAsyncTaskRepository(), func() time.Time { return now })
	reporter := reportingProjector{delegate: projector, outcomes: make(chan asynctask.Outcome, 2)}
	consumerCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- rabbitAdapter.NewConsumer(url, 16, reporter, func(error) bool { return false }).Run(consumerCtx)
	}()
	publisher, err := rabbitAdapter.DialPublisher(url)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	defer publisher.Close()
	for _, want := range []asynctask.Outcome{asynctask.OutcomeApplied, asynctask.OutcomeDuplicate} {
		if _, err := publisher.Publish(ctx, event); err != nil {
			cancel()
			t.Fatal(err)
		}
		select {
		case got := <-reporter.outcomes:
			if got != want {
				t.Fatalf("投影结果=%s want=%s", got, want)
			}
		case <-time.After(5 * time.Second):
			cancel()
			t.Fatal("等待 Consumer 处理超时")
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var consumed, tasks, generation int
	err = env.pool.QueryRow(ctx, `SELECT
(SELECT count(*) FROM velis.consumed_events WHERE consumer_name=$1 AND event_id=$2),
(SELECT count(*) FROM velis.async_tasks WHERE article_id=$3),
(SELECT generation FROM velis.async_tasks WHERE article_id=$3)`, asynctask.ConsumerName, event.EventID, published.Article.ID).Scan(&consumed, &tasks, &generation)
	if err != nil || consumed != 1 || tasks != 1 || generation != 1 {
		t.Fatalf("Inbox/任务状态 consumed=%d tasks=%d generation=%d err=%v", consumed, tasks, generation, err)
	}
}
