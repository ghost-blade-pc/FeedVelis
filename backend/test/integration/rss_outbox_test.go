package integration

import (
	"context"
	"testing"
	"time"

	articleApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	sourceDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/source"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/fetcher/httpfeed"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

func TestRSSIngestWritesPublicEventsInArticleTransaction(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	ctx := context.Background()
	now := fixedNow()
	source, inserted, err := postgres.NewSourceRepository(env.pool).Add(ctx, "https://events.example/feed", "https://events.example/feed", "事件来源", sourceDomain.DefaultFetchInterval, now)
	if err != nil || !inserted {
		t.Fatalf("创建 Source: inserted=%t err=%v", inserted, err)
	}

	id := "event-item"
	ingest := func(at time.Time, content string) articleApp.IngestReport {
		service := articleApp.NewServiceWithOutbox(postgres.NewArticleRepository(env.pool), httpfeed.NewSanitizer(), &articleTestClock{now: at}, postgres.NewTxManager(env.pool), postgres.NewOutboxRepository(env.pool))
		report, ingestErr := service.Ingest(ctx, source.ID, []ports.ParsedItem{{ID: &id, URL: "https://events.example/article", Title: "文章", Content: &content}})
		if ingestErr != nil {
			t.Fatal(ingestErr)
		}
		return report
	}
	if report := ingest(now, "初版"); report.Inserted != 1 {
		t.Fatalf("首次报告: %+v", report)
	}
	if report := ingest(now.Add(time.Minute), "初版"); report.Unchanged != 1 {
		t.Fatalf("无变化报告: %+v", report)
	}
	if report := ingest(now.Add(2*time.Minute), "第二版"); report.Updated != 1 {
		t.Fatalf("修订报告: %+v", report)
	}

	rows, err := env.pool.Query(ctx, `SELECT event_type,aggregate_version FROM velis.outbox_events ORDER BY aggregate_version`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var types []string
	var versions []int64
	for rows.Next() {
		var eventType string
		var version int64
		if err := rows.Scan(&eventType, &version); err != nil {
			t.Fatal(err)
		}
		types = append(types, eventType)
		versions = append(versions, version)
	}
	if len(types) != 2 || types[0] != "article.published.v1" || types[1] != "article.revised.v1" || versions[0] != 1 || versions[1] != 2 {
		t.Fatalf("事件=%v versions=%v", types, versions)
	}
	var lastSeen time.Time
	if err := env.pool.QueryRow(ctx, `SELECT last_seen_at FROM velis.articles WHERE source_id=$1`, source.ID).Scan(&lastSeen); err != nil {
		t.Fatal(err)
	}
	if !lastSeen.Equal(now.Add(2 * time.Minute)) {
		t.Fatalf("last_seen_at=%v", lastSeen)
	}
}
