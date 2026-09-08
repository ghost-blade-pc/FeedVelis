package bootstrap

import (
	cryptorand "crypto/rand"
	"math/big"

	articleApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/article"
	sourceApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/source"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/clock"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/fetcher/httpfeed"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

type feedServices struct {
	articles *articleApp.Service
	sources  *sourceApp.Service
}

func buildFeedServices(pool *pgxpool.Pool) feedServices {
	articleRepository := postgres.NewArticleRepository(pool)
	sourceRepository := postgres.NewSourceRepository(pool)
	sanitizer := httpfeed.NewSanitizer()
	clockValue := clock.System{}
	articleService := articleApp.NewService(articleRepository, sanitizer, clockValue, postgres.NewTxManager(pool))
	sourceService := sourceApp.NewService(sourceRepository, httpfeed.NewFetcher(), httpfeed.NewParser(), articleService, clockValue, randomJitter)
	return feedServices{articles: articleService, sources: sourceService}
}

func randomJitter() float64 {
	value, err := cryptorand.Int(cryptorand.Reader, big.NewInt(401))
	if err != nil {
		return 0
	}
	return float64(value.Int64()-200) / 1000
}
