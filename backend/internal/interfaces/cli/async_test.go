package cli

import (
	"bytes"
	"context"
	"errors"
	asyncApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/asynctask"
	dlqApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/dlq"
	"strings"
	"testing"
)

type backfillFake struct {
	limit  int
	report asyncApp.BackfillReport
	err    error
}

type replayFake struct {
	limit  int
	report dlqApp.Report
	err    error
}

func (r *replayFake) Run(_ context.Context, limit int) (dlqApp.Report, error) {
	r.limit = limit
	return r.report, r.err
}
func TestAsyncReplayDLQRequiresLimitAndPrintsStats(t *testing.T) {
	fake := &replayFake{report: dlqApp.Report{Confirmed: 2}}
	var output bytes.Buffer
	runner := New(Options{Replay: fake, Stdout: &output})
	if err := runner.Run(context.Background(), []string{"async", "replay-dlq", "-limit", "2"}); err != nil {
		t.Fatal(err)
	}
	if fake.limit != 2 || !strings.Contains(output.String(), "confirmed=2 failed=0") {
		t.Fatalf("limit=%d output=%q", fake.limit, output.String())
	}
	if err := runner.Run(context.Background(), []string{"async", "replay-dlq"}); err == nil {
		t.Fatal("缺少 limit 应失败")
	}
}

func (b *backfillFake) Run(_ context.Context, limit int) (asyncApp.BackfillReport, error) {
	b.limit = limit
	return b.report, b.err
}
func TestAsyncBackfillRequiresBoundedLimitAndPrintsStats(t *testing.T) {
	fake := &backfillFake{report: asyncApp.BackfillReport{Created: 2, Skipped: 1, HasMore: true}}
	var output bytes.Buffer
	runner := New(Options{Backfill: fake, Stdout: &output})
	if err := runner.Run(context.Background(), []string{"async", "backfill-articles", "-limit", "3"}); err != nil {
		t.Fatal(err)
	}
	if fake.limit != 3 || !strings.Contains(output.String(), "created=2 skipped=1 failed=0 remaining=true") {
		t.Fatalf("limit=%d output=%q", fake.limit, output.String())
	}
	for _, args := range [][]string{{"async", "backfill-articles"}, {"async", "backfill-articles", "-limit", "1001"}} {
		if err := runner.Run(context.Background(), args); err == nil {
			t.Fatalf("应拒绝 %+v", args)
		}
	}
}
func TestAsyncBackfillFailureKeepsCompleteStats(t *testing.T) {
	fake := &backfillFake{report: asyncApp.BackfillReport{Created: 1, Failed: 1}, err: errors.New("失败")}
	var output bytes.Buffer
	err := New(Options{Backfill: fake, Stdout: &output}).Run(context.Background(), []string{"async", "backfill-articles", "-limit", "2"})
	if err == nil || !strings.Contains(output.String(), "failed=1") {
		t.Fatalf("output=%q err=%v", output.String(), err)
	}
}

func TestAsyncCommandOutputDoesNotExposePayloadOrCredentials(t *testing.T) {
	secret := "amqp://user:password@example.invalid/vhost"
	fake := &replayFake{report: dlqApp.Report{Failed: 1}, err: errors.New("RabbitMQ 暂时不可用")}
	var stdout, stderr bytes.Buffer
	err := New(Options{Replay: fake, Stdout: &stdout, Stderr: &stderr}).Run(context.Background(), []string{"async", "replay-dlq", "-limit", "1"})
	if err == nil || !strings.Contains(stdout.String(), "confirmed=0 failed=1") {
		t.Fatalf("stdout=%q err=%v", stdout.String(), err)
	}
	combined := stdout.String() + stderr.String()
	for _, forbidden := range []string{secret, "event_id", "payload", "markdown"} {
		if strings.Contains(combined, forbidden) {
			t.Fatalf("命令输出泄露 %q: %q", forbidden, combined)
		}
	}
	if err := New(Options{}).Run(context.Background(), []string{"async", "unknown"}); err == nil || !strings.Contains(err.Error(), "不支持的 async 命令") {
		t.Fatalf("错误消息应为简体中文: %v", err)
	}
}
