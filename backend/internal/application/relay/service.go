package relay

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"
)

type Config struct {
	Owner          string
	BatchSize      int
	PublishWindow  int
	Lease          time.Duration
	ConfirmTimeout time.Duration
	ScanInterval   time.Duration
	BackoffMin     time.Duration
	BackoffMax     time.Duration
	Jitter         func() float64
}

type Service struct {
	store     OutboxStore
	publisher Publisher
	config    Config
}

func NewService(store OutboxStore, publisher Publisher, config Config) *Service {
	return &Service{store: store, publisher: publisher, config: config}
}

func (s *Service) Run(ctx context.Context) error {
	ticker := time.NewTicker(s.config.ScanInterval)
	defer ticker.Stop()
	for {
		if err := s.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (s *Service) RunOnce(ctx context.Context) error {
	claimed, err := s.store.Claim(ctx, s.config.Owner, s.config.BatchSize, s.config.Lease)
	if err != nil {
		return err
	}
	window := s.config.PublishWindow
	if window < 1 {
		window = 1
	}
	semaphore := make(chan struct{}, window)
	var wg sync.WaitGroup
	var firstErr error
	var lock sync.Mutex
	for _, item := range claimed {
		if ctx.Err() != nil {
			break
		}
		semaphore <- struct{}{}
		wg.Add(1)
		go func(item ClaimedEvent) {
			defer wg.Done()
			defer func() { <-semaphore }()
			if publishErr := s.publishOne(ctx, item); publishErr != nil {
				lock.Lock()
				if firstErr == nil {
					firstErr = publishErr
				}
				lock.Unlock()
			}
		}(item)
	}
	wg.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return firstErr
}

func (s *Service) publishOne(ctx context.Context, item ClaimedEvent) error {
	publishCtx, cancel := context.WithTimeout(ctx, s.config.ConfirmTimeout)
	defer cancel()
	result, err := s.publisher.Publish(publishCtx, item.Event)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil && result == PublishConfirmed {
		ok, writeErr := s.store.Confirm(ctx, item.Event.EventID, item.LeaseToken)
		if writeErr != nil {
			return fmt.Errorf("confirm 后回写失败: %w", writeErr)
		}
		if !ok {
			return errors.New("confirm 回写被 fencing 拒绝")
		}
		return nil
	}
	code := "publish_error"
	message := "发布失败"
	if errors.Is(publishCtx.Err(), context.DeadlineExceeded) {
		code = "confirm_timeout"
		message = "等待 confirm 超时"
	} else if result == PublishReturned {
		code = "unroutable"
		message = "消息不可路由"
	} else if result == PublishNacked {
		code = "nack"
		message = "Broker Nack"
	}
	delay := s.backoff(item.Attempts)
	ok, writeErr := s.store.Fail(ctx, item.Event.EventID, item.LeaseToken, code, message, delay)
	if writeErr != nil {
		return writeErr
	}
	if !ok {
		return errors.New("失败回写被 fencing 拒绝")
	}
	return fmt.Errorf("%s", message)
}

func (s *Service) backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	factor := math.Pow(2, float64(attempt-1))
	delay := time.Duration(float64(s.config.BackoffMin) * factor)
	if delay > s.config.BackoffMax {
		delay = s.config.BackoffMax
	}
	jitter := 0.0
	if s.config.Jitter != nil {
		jitter = s.config.Jitter()
	}
	if jitter < -0.2 {
		jitter = -0.2
	}
	if jitter > 0.2 {
		jitter = 0.2
	}
	delay = time.Duration(float64(delay) * (1 + jitter))
	if delay < time.Second {
		delay = time.Second
	}
	if delay > 60*time.Second {
		delay = 60 * time.Second
	}
	return delay
}
