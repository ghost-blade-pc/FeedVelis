package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

type WorkerComponent struct {
	Name string
	Run  func(context.Context) error
}
type Supervisor struct {
	components      []WorkerComponent
	logger          *slog.Logger
	restartDelay    time.Duration
	shutdownTimeout time.Duration
}

func NewSupervisor(components []WorkerComponent, logger *slog.Logger, restartDelay, shutdownTimeout time.Duration) *Supervisor {
	return &Supervisor{components: components, logger: logger, restartDelay: restartDelay, shutdownTimeout: shutdownTimeout}
}

func (s *Supervisor) Run(ctx context.Context) error {
	var wait sync.WaitGroup
	for _, component := range s.components {
		component := component
		wait.Add(1)
		go func() { defer wait.Done(); s.runComponent(ctx, component) }()
	}
	done := make(chan struct{})
	go func() { wait.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
	}
	timer := time.NewTimer(s.shutdownTimeout)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		return fmt.Errorf("Worker 组件未在 %s 内停止", s.shutdownTimeout)
	}
}

func (s *Supervisor) runComponent(ctx context.Context, component WorkerComponent) {
	for {
		if ctx.Err() != nil {
			return
		}
		err := component.Run(ctx)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			err = fmt.Errorf("组件意外退出")
		}
		s.logger.Error("Worker 组件退出，将独立重启", "component", component.Name, "error", err)
		timer := time.NewTimer(s.restartDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
