package scheduler

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	accountApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/account"
)

type recordingRunner struct {
	mu     sync.Mutex
	calls  int
	result accountApp.CleanupResult
	err    error
	notify chan struct{}
}

func (r *recordingRunner) Run(context.Context) (accountApp.CleanupResult, error) {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	if r.notify != nil {
		select {
		case r.notify <- struct{}{}:
		default:
		}
	}
	return r.result, r.err
}

func (r *recordingRunner) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func TestCleanupSchedulerLogsCounts(t *testing.T) {
	var buffer bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buffer, nil))
	runner := &recordingRunner{result: accountApp.CleanupResult{Sessions: 2, Tokens: 3, Failures: 4, Blocks: 5}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		NewCleanup(runner, logger, time.Hour).Run(ctx)
		close(done)
	}()
	waitForCalls(t, runner, 1)
	cancel()
	<-done

	logged := buffer.String()
	for _, want := range []string{"认证数据清理完成", "operation=cleanup", "result=success",
		"deleted_sessions=2", "deleted_refresh_tokens=3", "deleted_failures=4", "deleted_blocks=5", "duration_ms="} {
		if !strings.Contains(logged, want) {
			t.Fatalf("日志缺少 %q：%s", want, logged)
		}
	}
}

func TestCleanupSchedulerRepeatsAndToleratesFailure(t *testing.T) {
	var buffer bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buffer, nil))
	runner := &recordingRunner{err: errors.New("数据库不可用"), notify: make(chan struct{}, 8)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		NewCleanup(runner, logger, 20*time.Millisecond).Run(ctx)
		close(done)
	}()
	waitForCalls(t, runner, 3)
	cancel()
	<-done

	if !strings.Contains(buffer.String(), "认证数据清理失败") {
		t.Fatalf("失败应记录日志：%s", buffer.String())
	}
	if !strings.Contains(buffer.String(), "result=failure") {
		t.Fatalf("失败日志应带 result=failure：%s", buffer.String())
	}
}

func waitForCalls(t *testing.T, runner *recordingRunner, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if runner.callCount() >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待清理调用超时：期望 %d 次，实际 %d 次", want, runner.callCount())
}
