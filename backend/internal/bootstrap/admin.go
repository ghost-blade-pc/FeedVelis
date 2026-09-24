package bootstrap

import (
	"context"
	"io"
	"log/slog"
	"os"

	asyncApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/asynctask"
	enrichmentApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/enrichment"
	sourceApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/source"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/clock"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/config"
	rabbitAdapter "github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/messaging/rabbitmq"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/cli"
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
		Stdin:       os.Stdin,
		Stdout:      stdout,
		Stderr:      stderr,
		HiddenInput: cli.TTYPasswordPrompt(stderr),
	}).Run(ctx, args)
}
