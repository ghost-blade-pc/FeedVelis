package health

import (
	"context"
	"errors"
	"testing"
)

type checkerFunc func(context.Context) error

func (f checkerFunc) Ping(ctx context.Context) error { return f(ctx) }

func TestReady(t *testing.T) {
	service := NewService(checkerFunc(func(context.Context) error { return nil }))
	if err := service.Ready(context.Background()); err != nil {
		t.Fatalf("Ready() error = %v", err)
	}

	service.SetDraining(true)
	if err := service.Ready(context.Background()); !errors.Is(err, ErrDraining) {
		t.Fatalf("Ready() error = %v, want ErrDraining", err)
	}
}

func TestReadyPropagatesDependencyFailure(t *testing.T) {
	want := errors.New("database unavailable")
	service := NewService(checkerFunc(func(context.Context) error { return want }))
	if err := service.Ready(context.Background()); !errors.Is(err, want) {
		t.Fatalf("Ready() error = %v, want wrapped dependency error", err)
	}
}
