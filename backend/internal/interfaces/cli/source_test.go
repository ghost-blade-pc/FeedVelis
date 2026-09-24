package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	sourceApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/source"
	sourceDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/source"
)

type cliSourceService struct {
	addedURL      string
	addedInterval time.Duration
	pausedID      int64
	fetchedID     int64
	fetchedForce  bool
	historyLimit  int
	sources       []sourceDomain.Source
	runs          []sourceDomain.FetchRun
}

func (s *cliSourceService) AddWithInterval(_ context.Context, rawURL string, interval time.Duration) (sourceDomain.Source, bool, error) {
	// 与真实用例一致地校验周期，否则 CLI 传 0 之类的缺陷会被假实现掩盖。
	if err := sourceDomain.ValidateFetchInterval(interval); err != nil {
		return sourceDomain.Source{}, false, err
	}
	s.addedURL, s.addedInterval = rawURL, interval
	return sourceDomain.Source{ID: 9, FetchInterval: interval, LockVersion: 1}, true, nil
}

func (s *cliSourceService) List(context.Context) ([]sourceDomain.Source, error) {
	return s.sources, nil
}

func (s *cliSourceService) History(_ context.Context, command sourceApp.HistoryCommand) (sourceApp.HistoryPage, error) {
	s.historyLimit = command.Limit
	return sourceApp.HistoryPage{Items: s.runs}, nil
}
func (s *cliSourceService) Get(context.Context, int64) (sourceDomain.Source, error) {
	return sourceDomain.Source{ID: 7, LockVersion: 1}, nil
}

func (s *cliSourceService) Pause(_ context.Context, id, _ int64) (sourceDomain.Source, error) {
	s.pausedID = id
	return sourceDomain.Source{}, nil
}
func (*cliSourceService) Resume(context.Context, int64, int64) (sourceDomain.Source, error) {
	return sourceDomain.Source{}, nil
}
func (s *cliSourceService) FetchManual(_ context.Context, request sourceApp.ManualFetch) (sourceApp.FetchOutcome, sourceDomain.FetchRun, error) {
	s.fetchedID, s.fetchedForce = request.SourceID, request.Force
	return sourceApp.FetchOutcome{}, sourceDomain.FetchRun{ID: "run-1", Status: sourceDomain.RunSucceeded}, nil
}

func TestSourceAddAndPause(t *testing.T) {
	service := &cliSourceService{}
	var stdout bytes.Buffer
	runner := New(Options{Sources: service, Stdout: &stdout, Stderr: &bytes.Buffer{}})
	if err := runner.Run(context.Background(), []string{"source", "add", "-url", "https://example.com/feed"}); err != nil {
		t.Fatal(err)
	}
	if service.addedURL != "https://example.com/feed" || !strings.Contains(stdout.String(), "source_id=9 created=true") {
		t.Fatalf("url=%q output=%q", service.addedURL, stdout.String())
	}
	if service.addedInterval != sourceDomain.DefaultFetchInterval {
		t.Fatalf("省略 -interval 时应使用默认周期，实际=%s", service.addedInterval)
	}
	if err := runner.Run(context.Background(), []string{"source", "pause", "9"}); err != nil {
		t.Fatal(err)
	}
	if service.pausedID != 9 {
		t.Fatalf("paused=%d", service.pausedID)
	}
}

func TestSourceCommandRejectsInvalidID(t *testing.T) {
	runner := New(Options{Sources: &cliSourceService{}, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}})
	if err := runner.Run(context.Background(), []string{"source", "fetch", "0"}); err == nil {
		t.Fatal("expected invalid id")
	}
}

func TestSourceFetchForceFlag(t *testing.T) {
	service := &cliSourceService{}
	var stdout bytes.Buffer
	runner := New(Options{Sources: service, Stdout: &stdout, Stderr: &bytes.Buffer{}})
	if err := runner.Run(context.Background(), []string{"source", "fetch", "2", "--force"}); err != nil {
		t.Fatal(err)
	}
	if service.fetchedID != 2 || !service.fetchedForce {
		t.Fatalf("fetched=%d force=%t", service.fetchedID, service.fetchedForce)
	}
	runner = New(Options{Sources: service, Stdout: &stdout, Stderr: &bytes.Buffer{}})
	if err := runner.Run(context.Background(), []string{"source", "fetch", "2"}); err != nil {
		t.Fatal(err)
	}
	if service.fetchedForce {
		t.Fatal("不带 --force 时不应强制")
	}
}

// TestSourceWritesShowIntervalVersionAndRun 覆盖 7.7 的展示要求：
// 新增/暂停/恢复展示周期与版本，抓取展示运行 ID 与统计。
func TestSourceWritesShowIntervalVersionAndRun(t *testing.T) {
	service := &cliSourceService{}
	var stdout bytes.Buffer
	runner := New(Options{Sources: service, Stdout: &stdout, Stderr: &bytes.Buffer{}})

	if err := runner.Run(context.Background(), []string{"source", "add", "-url", "https://example.com/feed", "-interval", "15m"}); err != nil {
		t.Fatal(err)
	}
	if service.addedInterval != 15*time.Minute {
		t.Fatalf("周期 = %s", service.addedInterval)
	}
	output := stdout.String()
	if !strings.Contains(output, "interval=15m0s") || !strings.Contains(output, "version=1") {
		t.Fatalf("新增输出缺少周期或版本: %q", output)
	}

	stdout.Reset()
	if err := runner.Run(context.Background(), []string{"source", "resume", "9"}); err != nil {
		t.Fatal(err)
	}
	if output := stdout.String(); !strings.Contains(output, "version=") || !strings.Contains(output, "next_fetch=") {
		t.Fatalf("恢复输出缺少版本或下次抓取: %q", output)
	}

	stdout.Reset()
	if err := runner.Run(context.Background(), []string{"source", "fetch", "9"}); err != nil {
		t.Fatal(err)
	}
	if output := stdout.String(); !strings.Contains(output, "run_id=run-1") || !strings.Contains(output, "status=succeeded") {
		t.Fatalf("抓取输出缺少运行结果: %q", output)
	}
}

// TestSourceListShowsIntervalAndVersion 固化列表新增的展示列。
func TestSourceListShowsIntervalAndVersion(t *testing.T) {
	service := &cliSourceService{sources: []sourceDomain.Source{{
		ID: 3, Status: sourceDomain.StatusActive, Title: "示例", FeedURL: "https://example.com/feed",
		FetchInterval: 45 * time.Minute, LockVersion: 4, NextFetchAt: time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC),
		ConsecutiveFailures: 2,
	}}}
	var stdout bytes.Buffer
	runner := New(Options{Sources: service, Stdout: &stdout, Stderr: &bytes.Buffer{}})
	if err := runner.Run(context.Background(), []string{"source", "list"}); err != nil {
		t.Fatal(err)
	}
	output := stdout.String()
	for _, want := range []string{"周期=45m0s", "版本=4", "连续失败=2", "2026-09-21T12:00:00Z"} {
		if !strings.Contains(output, want) {
			t.Fatalf("列表输出缺少 %s: %q", want, output)
		}
	}
}

// TestSourceRunsShowsHistory 覆盖新增的历史子命令。
func TestSourceRunsShowsHistory(t *testing.T) {
	errorCode := "SSRF_BLOCKED"
	service := &cliSourceService{runs: []sourceDomain.FetchRun{{
		ID: "run-9", Trigger: sourceDomain.TriggerScheduled, Status: sourceDomain.RunFailed,
		Inserted: 1, Updated: 2, Unchanged: 3, Skipped: 4, ErrorCode: &errorCode,
		StartedAt: time.Date(2026, 9, 21, 11, 0, 0, 0, time.UTC),
	}}}
	var stdout bytes.Buffer
	runner := New(Options{Sources: service, Stdout: &stdout, Stderr: &bytes.Buffer{}})
	if err := runner.Run(context.Background(), []string{"source", "runs", "5", "--limit", "5"}); err != nil {
		t.Fatal(err)
	}
	if service.historyLimit != 5 {
		t.Fatalf("分页 = %d", service.historyLimit)
	}
	output := stdout.String()
	for _, want := range []string{"run-9", "scheduled", "failed", "插入=1", "跳过=4", "错误=SSRF_BLOCKED"} {
		if !strings.Contains(output, want) {
			t.Fatalf("历史输出缺少 %s: %q", want, output)
		}
	}
}
