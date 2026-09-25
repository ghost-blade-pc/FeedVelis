package bootstrap

import (
	"context"
	"io"
	"log/slog"
	"os"

	asyncApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/asynctask"
	enrichmentApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/enrichment"
	projectionApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/searchprojection"
	sourceApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/source"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/clock"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/config"
	rabbitAdapter "github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/messaging/rabbitmq"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
	searchAdapter "github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/search/opensearch"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/cli"
	"github.com/jackc/pgx/v5/pgxpool"
)

func RunAdmin(ctx context.Context, cfg config.Config, logger *slog.Logger, args []string, stdout, stderr io.Writer) error {
	pool, err := postgres.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer pool.Close()
	feed := buildFeedServices(pool, cfg.Feed.ProxyURL)
	content := buildContentServices(cfg, pool, logger)
	adminSources := sourceApp.NewAdminService(postgres.NewSourceRepository(pool),
		postgres.NewFetchRunRepository(pool), feed.sources, content.idempotency, clock.System{})
	accounts, err := buildAdminService(cfg, pool)
	if err != nil {
		return err
	}
	backfill := asyncApp.NewBackfillService(postgres.NewBackfillRepository(pool), postgres.NewOutboxRepository(pool), postgres.NewTxManager(pool), nil)
	aiBackfill := enrichmentApp.NewAIBackfillService(postgres.NewEnrichmentBackfillRepository(pool))
	// 未配置 OpenSearch 时不装配搜索管理命令，命令本身会明确拒绝执行。
	searchService, closeSearch, err := buildSearchAdmin(cfg, pool)
	if err != nil {
		return err
	}
	if closeSearch != nil {
		defer closeSearch()
	}
	return cli.New(cli.Options{
		Sources:    cli.SourceServices{Service: feed.sources, Admin: adminSources},
		Accounts:   accounts,
		Backfill:   backfill,
		Replay:     rabbitAdapter.DLQReplay{URL: cfg.RabbitMQ.URL},
		AIBackfill: aiBackfill,
		AIGenerationProfile: func() string {
			if cfg.AI.Generation.Profile.Enabled() {
				return cfg.AI.Generation.Profile.ProfileVersion
			}
			return ""
		}(),
		AIEmbeddingProfile: func() string {
			if cfg.AI.Embedding.Profile.Enabled() {
				return cfg.AI.Embedding.Profile.ProfileVersion
			}
			return ""
		}(),
		Search:      searchService,
		Stdin:       os.Stdin,
		Stdout:      stdout,
		Stderr:      stderr,
		HiddenInput: cli.TTYPasswordPrompt(stderr),
	}).Run(ctx, args)
}

// buildSearchAdmin 装配本地搜索管理命令；未配置 OpenSearch 时返回 nil 服务。
func buildSearchAdmin(cfg config.Config, pool *pgxpool.Pool) (cli.SearchService, func(), error) {
	if !cfg.Search.Enabled() {
		return nil, nil, nil
	}
	client, err := searchAdapter.New(searchClientConfig(cfg))
	if err != nil {
		return nil, nil, err
	}
	repository := postgres.NewSearchProjectionRepository(pool)
	rebuild := projectionApp.NewRebuildService(repository, repository, client, client, repository,
		projectionApp.RebuildPolicy{
			IndexPrefix: cfg.Search.IndexPrefix, SchemaVersion: cfg.Search.SchemaVersion,
			SchemaIdentity: cfg.Search.SchemaIdentity(), EmbeddingDimensions: cfg.Search.EmbeddingDimensions,
			SnapshotBatch: cfg.Search.Rebuild.SnapshotBatch, RollbackWindow: cfg.Search.Rebuild.RollbackWindowD,
			Lease: cfg.Search.Worker.Lease, SampleSize: cfg.Search.Rebuild.SampleSize,
			PollInterval: cfg.Search.Worker.Poll,
		}, nil)
	return projectionApp.NewAdmin(projectionApp.NewRetryService(repository), rebuild),
		func() { _ = client.Close() }, nil
}
