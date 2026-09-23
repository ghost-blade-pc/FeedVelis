package relay

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articleevent"
)

type storeFake struct {
	items      []ClaimedEvent
	confirmed  int
	failed     int
	failCode   string
	confirmErr error
}

func (s *storeFake) Claim(context.Context, string, int, time.Duration) ([]ClaimedEvent, error) {
	return s.items, nil
}
func (s *storeFake) Confirm(context.Context, string, string) (bool, error) {
	s.confirmed++
	return s.confirmErr == nil, s.confirmErr
}
func (s *storeFake) Fail(_ context.Context, _, _, code, _ string, _ time.Duration) (bool, error) {
	s.failed++
	s.failCode = code
	return true, nil
}

type publisherFake struct {
	result PublishResult
	err    error
	wait   bool
}

func (p publisherFake) Publish(ctx context.Context, _ articleevent.Envelope) (PublishResult, error) {
	if p.wait {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return p.result, p.err
}

func relayTestItem() ClaimedEvent {
	event, _ := articleevent.Deleted(context.Background(), 42, 81, 7, time.Now())
	return ClaimedEvent{Event: event, LeaseToken: "token", Attempts: 1}
}
func relayConfig() Config {
	return Config{Owner: "worker", BatchSize: 10, PublishWindow: 2, Lease: time.Minute, ConfirmTimeout: 5 * time.Millisecond, ScanInterval: time.Second, BackoffMin: time.Second, BackoffMax: time.Minute}
}

func TestRelayPublishOutcomesRemainRetryable(t *testing.T) {
	tests := []struct {
		name      string
		publisher publisherFake
		code      string
	}{
		{"mandatory return", publisherFake{result: PublishReturned}, "unroutable"}, {"Nack", publisherFake{result: PublishNacked}, "nack"}, {"超时", publisherFake{wait: true}, "confirm_timeout"}, {"发布错误", publisherFake{err: errors.New("connection")}, "publish_error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &storeFake{items: []ClaimedEvent{relayTestItem()}}
			err := NewService(store, test.publisher, relayConfig()).RunOnce(context.Background())
			if err == nil || store.failed != 1 || store.confirmed != 0 || store.failCode != test.code {
				t.Fatalf("failed=%d confirmed=%d code=%s err=%v", store.failed, store.confirmed, store.failCode, err)
			}
		})
	}
}

func TestRelayConfirmedAndWritebackFailure(t *testing.T) {
	store := &storeFake{items: []ClaimedEvent{relayTestItem()}}
	if err := NewService(store, publisherFake{result: PublishConfirmed}, relayConfig()).RunOnce(context.Background()); err != nil || store.confirmed != 1 {
		t.Fatalf("confirmed=%d err=%v", store.confirmed, err)
	}
	store = &storeFake{items: []ClaimedEvent{relayTestItem()}, confirmErr: errors.New("db down")}
	if err := NewService(store, publisherFake{result: PublishConfirmed}, relayConfig()).RunOnce(context.Background()); err == nil || store.failed != 0 {
		t.Fatalf("回写失败应保留租约恢复: %+v err=%v", store, err)
	}
}

func TestRelayCancellationLeavesLeaseForRecovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := &storeFake{items: []ClaimedEvent{relayTestItem()}}
	err := NewService(store, publisherFake{wait: true}, relayConfig()).RunOnce(ctx)
	if !errors.Is(err, context.Canceled) || store.failed != 0 || store.confirmed != 0 {
		t.Fatalf("store=%+v err=%v", store, err)
	}
}
