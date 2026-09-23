package outbox

import (
	"context"
	"time"
)

type Stats struct {
	Pending   int64
	OldestAge time.Duration
}
type Maintenance interface {
	DeletePublishedBefore(context.Context, time.Time, int) (int64, error)
	PendingStats(context.Context) (Stats, error)
}

type Cleanup struct {
	store     Maintenance
	retention time.Duration
	batch     int
	interval  time.Duration
	now       func() time.Time
}

func NewCleanup(store Maintenance, retention time.Duration, batch int, interval time.Duration, now func() time.Time) *Cleanup {
	if now == nil {
		now = time.Now
	}
	return &Cleanup{store: store, retention: retention, batch: batch, interval: interval, now: now}
}
func (c *Cleanup) RunOnce(ctx context.Context) (int64, error) {
	return c.store.DeletePublishedBefore(ctx, c.now().UTC().Add(-c.retention), c.batch)
}
func (c *Cleanup) Run(ctx context.Context) error {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		if _, err := c.RunOnce(ctx); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
