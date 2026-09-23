package integration

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articleevent"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

func TestOutboxAppendRequiresTransactionAndKeepsFlatColumnsConsistent(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	repository := postgres.NewOutboxRepository(env.pool)
	event, err := articleevent.Published(context.Background(), articleevent.PublicFact{ArticleID: 42, OriginType: "user", RevisionID: 81, RevisionNo: 3, ContentHash: strings.Repeat("a", 64), LockVersion: 7}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Append(context.Background(), event); !errors.Is(err, ports.ErrTransactionRequired) {
		t.Fatalf("事务外 Append=%v", err)
	}
	var count int
	if err := env.pool.QueryRow(context.Background(), `SELECT count(*) FROM velis.outbox_events`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("事务外写入破坏数据: count=%d err=%v", count, err)
	}
	manager := postgres.NewTxManager(env.pool)
	if err := manager.WithinTransaction(context.Background(), func(ctx context.Context) error { return repository.Append(ctx, event) }); err != nil {
		t.Fatal(err)
	}
	stored, err := repository.Get(context.Background(), event.EventID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.EventType != event.EventType || stored.Aggregate != event.Aggregate {
		t.Fatalf("信封不一致: %+v", stored)
	}
	var eventType, aggregateID string
	var version int64
	if err := env.pool.QueryRow(context.Background(), `SELECT event_type,aggregate_id,aggregate_version FROM velis.outbox_events WHERE event_id=$1`, event.EventID).Scan(&eventType, &aggregateID, &version); err != nil {
		t.Fatal(err)
	}
	if eventType != event.EventType || aggregateID != event.Aggregate.ID || version != event.Aggregate.Version {
		t.Fatal("扁平列与信封不一致")
	}
	duplicate := event
	duplicate.EventID = "01993a42-8e80-7a15-87dd-1dd92b6fb0c5"
	if err := manager.WithinTransaction(context.Background(), func(ctx context.Context) error { return repository.Append(ctx, duplicate) }); err == nil {
		t.Fatal("聚合事件唯一约束未生效")
	}
}
