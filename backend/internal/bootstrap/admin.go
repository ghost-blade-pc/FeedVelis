package bootstrap

import (
	"context"
	"io"
	"log/slog"
	"os"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/config"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/cli"
)

func RunAdmin(ctx context.Context, cfg config.Config, _ *slog.Logger, args []string, stdout, stderr io.Writer) error {
	pool, err := postgres.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer pool.Close()
	feed := buildFeedServices(pool)
	accounts, err := buildAdminService(cfg, pool)
	if err != nil {
		return err
	}
	return cli.New(cli.Options{
		Sources:     feed.sources,
		Accounts:    accounts,
		Stdin:       os.Stdin,
		Stdout:      stdout,
		Stderr:      stderr,
		HiddenInput: cli.TTYPasswordPrompt(stderr),
	}).Run(ctx, args)
}
