package bootstrap

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

func TestSupervisorRestartsFailedComponentWithoutStoppingOthers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	var failing, healthy atomic.Int32
	components := []WorkerComponent{
		{Name: "mq", Run: func(context.Context) error { failing.Add(1); return errors.New("断连") }},
		{Name: "feed", Run: func(ctx context.Context) error { healthy.Add(1); <-ctx.Done(); return nil }},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := NewSupervisor(components, logger, time.Millisecond, time.Second).Run(ctx); err != nil {
		t.Fatal(err)
	}
	if failing.Load() < 2 || healthy.Load() != 1 {
		t.Fatalf("failing=%d healthy=%d", failing.Load(), healthy.Load())
	}
}
func TestSupervisorReportsBoundedShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	release := make(chan struct{})
	defer close(release)
	component := WorkerComponent{Name: "stuck", Run: func(context.Context) error { <-release; return nil }}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	done := make(chan error, 1)
	go func() {
		done <- NewSupervisor([]WorkerComponent{component}, logger, time.Millisecond, 5*time.Millisecond).Run(ctx)
	}()
	time.Sleep(time.Millisecond)
	cancel()
	if err := <-done; err == nil {
		t.Fatal("应报告关闭超时")
	}
}
