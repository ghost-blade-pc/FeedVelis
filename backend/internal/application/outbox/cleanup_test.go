package outbox

import (
	"context"
	"testing"
	"time"
)

type maintenanceFake struct {
	before time.Time
	limit  int
}

func (m *maintenanceFake) DeletePublishedBefore(_ context.Context, b time.Time, l int) (int64, error) {
	m.before = b
	m.limit = l
	return 3, nil
}
func (*maintenanceFake) PendingStats(context.Context) (Stats, error) { return Stats{}, nil }
func TestCleanupUsesRetentionAndBatch(t *testing.T) {
	now := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	store := &maintenanceFake{}
	count, err := NewCleanup(store, 168*time.Hour, 500, time.Hour, func() time.Time { return now }).RunOnce(context.Background())
	if err != nil || count != 3 || store.limit != 500 || !store.before.Equal(now.Add(-168*time.Hour)) {
		t.Fatalf("count=%d before=%v limit=%d err=%v", count, store.before, store.limit, err)
	}
}
