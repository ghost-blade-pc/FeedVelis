package cli

import (
	"bytes"
	"context"
	"errors"
	asyncApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/asynctask"
	dlqApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/dlq"
	enrichmentApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/enrichment"
	"strings"
	"testing"
)

type backfillFake struct {
	limit  int
	report asyncApp.BackfillReport
	err    error
}

type aiBackfillFake struct {
	request enrichmentApp.BackfillRequest
	report  enrichmentApp.BackfillReport
	runs    int
}

func (f *aiBackfillFake) Run(_ context.Context, request enrichmentApp.BackfillRequest) (enrichmentApp.BackfillReport, error) {
	f.runs++
	f.request = request
	return f.report, nil
}

func TestAIBackfillRequiresBoundedExplicitSelectionAndSupportsDryRun(t *testing.T) {
	fake := &aiBackfillFake{report: enrichmentApp.BackfillReport{Created: 2, Skipped: 1, HasMore: true}}
	var output bytes.Buffer
	runner := New(Options{AIBackfill: fake, AIGenerationProfile: "g-v1", AIEmbeddingProfile: "e-v1", Stdout: &output})
	err := runner.Run(context.Background(), []string{"ai", "backfill", "-stage", "all", "-mode", "missing-only", "-limit", "3", "-dry-run"})
	if err != nil || !fake.request.DryRun || fake.request.Limit != 3 || !strings.Contains(output.String(), "created=2 skipped=1 failed=0 has-more=true dry-run=true") {
		t.Fatalf("request=%+v output=%q err=%v", fake.request, output.String(), err)
	}
	if fake.request.Order != "oldest" {
		t.Fatalf("默认顺序=%q", fake.request.Order)
	}
	if err := runner.Run(context.Background(), []string{"ai", "backfill", "-stage", "all", "-mode", "missing-only"}); err == nil {
		t.Fatal("缺少范围选择器应拒绝")
	}
}

func TestAIBackfillSupportsArticleNewestAndConfirmedAll(t *testing.T) {
	fake := &aiBackfillFake{}
	runner := New(Options{AIBackfill: fake, AIGenerationProfile: "g-v2", AIEmbeddingProfile: "e-v2"})
	ctx := context.Background()
	if err := runner.Run(ctx, []string{"ai", "backfill", "-stage", "generation", "-mode", "missing-only", "-article-id", "324"}); err != nil || fake.request.ArticleID != 324 {
		t.Fatalf("article request=%+v err=%v", fake.request, err)
	}
	if err := runner.Run(ctx, []string{"ai", "backfill", "-stage", "generation", "-mode", "missing-only", "-limit", "5", "-order", "newest"}); err != nil || fake.request.Order != "newest" || fake.request.Limit != 5 {
		t.Fatalf("newest request=%+v err=%v", fake.request, err)
	}
	if err := runner.Run(ctx, []string{"ai", "backfill", "-stage", "all", "-mode", "missing-only", "-all", "-confirm-all"}); err != nil || !fake.request.All || !fake.request.ConfirmAll {
		t.Fatalf("all request=%+v err=%v", fake.request, err)
	}
	if err := runner.Run(ctx, []string{"ai", "backfill", "-stage", "all", "-mode", "missing-only", "-all", "-dry-run"}); err != nil || !fake.request.All || !fake.request.DryRun {
		t.Fatalf("all dry-run request=%+v err=%v", fake.request, err)
	}
}

func TestAIBackfillRejectsUnsafeSelectorCombinationsBeforeCallingService(t *testing.T) {
	cases := [][]string{
		{"ai", "backfill", "-stage", "all", "-mode", "missing-only"},
		{"ai", "backfill", "-stage", "all", "-mode", "missing-only", "-article-id", "1", "-limit", "2"},
		{"ai", "backfill", "-stage", "all", "-mode", "missing-only", "-article-id", "1", "-order", "newest"},
		{"ai", "backfill", "-stage", "all", "-mode", "missing-only", "-limit", "2", "-order", "random"},
		{"ai", "backfill", "-stage", "all", "-mode", "missing-only", "-all"},
		{"ai", "backfill", "-stage", "all", "-mode", "missing-only", "-limit", "2", "-confirm-all"},
	}
	for _, args := range cases {
		fake := &aiBackfillFake{}
		err := New(Options{AIBackfill: fake, AIGenerationProfile: "g", AIEmbeddingProfile: "e"}).Run(context.Background(), args)
		if err == nil || fake.runs != 0 {
			t.Fatalf("args=%v runs=%d err=%v", args, fake.runs, err)
		}
	}
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
