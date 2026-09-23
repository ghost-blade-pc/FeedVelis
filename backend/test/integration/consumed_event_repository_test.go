package integration

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articleevent"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/asynctask"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

func TestConsumedEventDeduplicationIsPerConsumerAndRollbackSafe(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	manager := postgres.NewTxManager(env.pool)
	event, err := articleevent.Published(context.Background(), articleevent.PublicFact{ArticleID: 42, OriginType: "user", RevisionID: 81, RevisionNo: 3, ContentHash: strings.Repeat("a", 64), LockVersion: 7}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	first, _ := postgres.NewConsumedEventRepository(asynctask.ConsumerName)
	second, _ := postgres.NewConsumedEventRepository("audit-projector.v1")
	if _, err := first.Start(context.Background(), event); !errors.Is(err, ports.ErrTransactionRequired) {
		t.Fatalf("事务外 Start=%v", err)
	}
	for _, repository := range []*postgres.ConsumedEventRepository{first, second} {
		if err := manager.WithinTransaction(context.Background(), func(ctx context.Context) error {
			fresh, e := repository.Start(ctx, event)
			if e == nil && !fresh {
				t.Fatal("首次应写入")
			}
			return e
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := manager.WithinTransaction(context.Background(), func(ctx context.Context) error {
		fresh, e := first.Start(ctx, event)
		if fresh {
			t.Fatal("重复事件不应再次写入")
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
	rollbackEvent := event
	rollbackEvent.EventID = "01993a42-8e80-7a16-87dd-1dd92b6fb0c6"
	want := errors.New("rollback")
	if err := manager.WithinTransaction(context.Background(), func(ctx context.Context) error {
		if fresh, e := first.Start(ctx, rollbackEvent); e != nil || !fresh {
			t.Fatalf("rollback 首次=%t err=%v", fresh, e)
		}
		return want
	}); !errors.Is(err, want) {
		t.Fatal(err)
	}
	if err := manager.WithinTransaction(context.Background(), func(ctx context.Context) error {
		fresh, e := first.Start(ctx, rollbackEvent)
		if e == nil && !fresh {
			t.Fatal("回滚后必须可重试")
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
}
