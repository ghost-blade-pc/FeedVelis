package integration

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	articleApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articleevent"
	idempotencyApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/idempotency"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/content/markdown"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

var errInjectedArticleTransaction = errors.New("注入的文章事务故障")

type failingOutbox struct{}

func (failingOutbox) Append(context.Context, articleevent.Envelope) error {
	return errInjectedArticleTransaction
}
func (failingOutbox) Get(context.Context, string) (articleevent.Envelope, error) {
	return articleevent.Envelope{}, errInjectedArticleTransaction
}
func (failingOutbox) ExistsAggregateEvent(context.Context, string, int64, string) (bool, error) {
	return false, errInjectedArticleTransaction
}

type failingArticleAssets struct{}

func (failingArticleAssets) BindArticleAssets(context.Context, ports.ArticleAssetBinding) error {
	return errInjectedArticleTransaction
}
func (failingArticleAssets) ListRevisionAssetIDs(context.Context, int64) ([]string, error) {
	return nil, nil
}

type failingSettleRepository struct {
	delegate idempotencyApp.Repository
}

func (r failingSettleRepository) Begin(ctx context.Context, identity idempotencyApp.Identity, digest [32]byte, now, expiresAt time.Time) (idempotencyApp.Record, bool, error) {
	return r.delegate.Begin(ctx, identity, digest, now, expiresAt)
}
func (failingSettleRepository) Succeed(context.Context, idempotencyApp.Identity, int, json.RawMessage, string, string, time.Time) error {
	return errInjectedArticleTransaction
}

func TestUserArticleTransactionRollsBackOnInjectedFailures(t *testing.T) {
	tests := []struct {
		name     string
		markdown string
		build    func(*testEnv) (*idempotencyApp.Service, ports.ArticleAssets, ports.Outbox)
	}{
		{
			name: "Outbox append 失败",
			build: func(env *testEnv) (*idempotencyApp.Service, ports.ArticleAssets, ports.Outbox) {
				return newArticleIdempotency(env, postgres.NewIdempotencyRepository(env.pool)), postgres.NewArticleAssetRepository(env.pool), failingOutbox{}
			},
		},
		{
			name:     "资产绑定失败",
			markdown: "![图片](asset:55000000-0000-0000-0000-000000000099)",
			build: func(env *testEnv) (*idempotencyApp.Service, ports.ArticleAssets, ports.Outbox) {
				return newArticleIdempotency(env, postgres.NewIdempotencyRepository(env.pool)), failingArticleAssets{}, postgres.NewOutboxRepository(env.pool)
			},
		},
		{
			name: "幂等结果落账失败",
			build: func(env *testEnv) (*idempotencyApp.Service, ports.ArticleAssets, ports.Outbox) {
				repository := failingSettleRepository{delegate: postgres.NewIdempotencyRepository(env.pool)}
				return newArticleIdempotency(env, repository), postgres.NewArticleAssetRepository(env.pool), postgres.NewOutboxRepository(env.pool)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			env := newTestEnv(t)
			env.resetArticles(t)
			env.resetAccounts(t)
			ctx := context.Background()
			now := fixedNow()
			const authorID = "55000000-0000-0000-0000-000000000001"
			if _, err := env.pool.Exec(ctx, `INSERT INTO velis.users
(id,username,nickname,password_hash,role,status,created_at,updated_at)
VALUES($1,'atomic_author','作者','hash','user','active',$2,$2)`, authorID, now); err != nil {
				t.Fatal(err)
			}
			idempotency, assets, outbox := test.build(env)
			service := articleApp.NewUserServiceWithOutbox(postgres.NewArticleRepository(env.pool), markdown.NewUserRenderer(), assets,
				idempotency, &articleTestClock{now: now}, articleApp.AssetPolicy{MaxImages: 20, MaxTotalBytes: 50 << 20}, outbox)
			articleMarkdown := test.markdown
			if articleMarkdown == "" {
				articleMarkdown = "正文"
			}
			_, _, err := service.Create(ctx, articleApp.CreateUserArticleCommand{
				AuthorUserID: authorID, IdempotencyKey: "55000000-0000-0000-0000-000000000011",
				Title: "原子事务", Markdown: articleMarkdown, InitialStatus: articleDomain.StatusPublished,
			})
			if !errors.Is(err, errInjectedArticleTransaction) {
				t.Fatalf("错误=%v", err)
			}
			assertArticleTransactionEmpty(t, env, ctx)
		})
	}
}

func newArticleIdempotency(env *testEnv, repository idempotencyApp.Repository) *idempotencyApp.Service {
	return idempotencyApp.NewService(repository, postgres.NewTxManager(env.pool), 24*time.Hour)
}

func assertArticleTransactionEmpty(t *testing.T, env *testEnv, ctx context.Context) {
	t.Helper()
	var articles, revisions, references, operations, events int
	err := env.pool.QueryRow(ctx, `SELECT
(SELECT count(*) FROM velis.articles),
(SELECT count(*) FROM velis.article_versions),
(SELECT count(*) FROM velis.article_asset_references),
(SELECT count(*) FROM velis.idempotency_operations),
(SELECT count(*) FROM velis.outbox_events)`).Scan(&articles, &revisions, &references, &operations, &events)
	if err != nil {
		t.Fatal(err)
	}
	if articles != 0 || revisions != 0 || references != 0 || operations != 0 || events != 0 {
		t.Fatalf("事务未完整回滚: articles=%d revisions=%d references=%d operations=%d events=%d",
			articles, revisions, references, operations, events)
	}
}
