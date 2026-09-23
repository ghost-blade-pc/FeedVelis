package integration

import (
	"context"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
	"testing"
	"time"
)

func TestOutboxCleanupNeverDeletesPending(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	ctx := context.Background()
	_, err := env.pool.Exec(ctx, `INSERT INTO velis.outbox_events(event_id,event_type,aggregate_type,aggregate_id,aggregate_version,envelope,occurred_at,created_at,next_attempt_at,published_at) VALUES
('01993a42-8e80-7a11-87dd-1dd92b6fb0c1','article.deleted.v1','article','42',1,'{"event_id":"01993a42-8e80-7a11-87dd-1dd92b6fb0c1","event_type":"article.deleted.v1","aggregate":{"type":"article","id":"42","version":1}}',now(),now()-interval '8 days',now(),now()-interval '8 days'),
('01993a42-8e80-7a12-87dd-1dd92b6fb0c2','article.deleted.v1','article','43',1,'{"event_id":"01993a42-8e80-7a12-87dd-1dd92b6fb0c2","event_type":"article.deleted.v1","aggregate":{"type":"article","id":"43","version":1}}',now(),now()-interval '30 days',now(),NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	repo := postgres.NewRelayRepository(env.pool)
	count, err := repo.DeletePublishedBefore(ctx, time.Now().Add(-7*24*time.Hour), 500)
	if err != nil || count != 1 {
		t.Fatalf("deleted=%d err=%v", count, err)
	}
	stats, err := repo.PendingStats(ctx)
	if err != nil || stats.Pending != 1 {
		t.Fatalf("stats=%+v err=%v", stats, err)
	}
}
