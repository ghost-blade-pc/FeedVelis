// Package health 提供进程存活与就绪状态用例。
package health

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
)

var ErrDraining = errors.New("服务正在关闭")

type Service struct {
	checker  ports.HealthChecker
	draining atomic.Bool
}

func NewService(checker ports.HealthChecker) *Service {
	return &Service{checker: checker}
}

func (s *Service) SetDraining(value bool) {
	s.draining.Store(value)
}

func (s *Service) Ready(ctx context.Context) error {
	if s.draining.Load() {
		return ErrDraining
	}
	if err := s.checker.Ping(ctx); err != nil {
		return fmt.Errorf("必要依赖不可用: %w", err)
	}
	return nil
}
