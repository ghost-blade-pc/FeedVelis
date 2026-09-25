package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	projectionApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/searchprojection"
	projectionDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/searchprojection"
)

type searchStub struct {
	report     projectionApp.RetryReport
	state      projectionApp.RebuildState
	indexState projectionApp.IndexState
	status     projectionApp.IndexStatus
	err        error
	scopes     []projectionApp.RetryScope
	cleanups   []cleanupCall
}

func (s *searchStub) InitIndex(context.Context) (projectionApp.IndexState, error) {
	if s.err != nil {
		return projectionApp.IndexState{}, s.err
	}
	return s.indexState, nil
}

func (s *searchStub) Start(context.Context) (projectionApp.RebuildState, error) {
	return s.rebuild()
}

func (s *searchStub) Resume(_ context.Context, _ string) (projectionApp.RebuildState, error) {
	return s.rebuild()
}

func (s *searchStub) Status(context.Context, int) (projectionApp.IndexStatus, error) {
	if s.err != nil {
		return projectionApp.IndexStatus{}, s.err
	}
	return s.status, nil
}

func (s *searchStub) Cutover(_ context.Context, _ string) (projectionApp.RebuildState, error) {
	return s.rebuild()
}

func (s *searchStub) Rollback(_ context.Context, _ string) (projectionApp.RebuildState, error) {
	return s.rebuild()
}

func (s *searchStub) Abandon(_ context.Context, _ string) (projectionApp.RebuildState, error) {
	return s.rebuild()
}

func (s *searchStub) Cleanup(_ context.Context, index string, confirm bool) error {
	s.cleanups = append(s.cleanups, cleanupCall{index: index, confirm: confirm})
	return s.err
}

func (s *searchStub) rebuild() (projectionApp.RebuildState, error) {
	if s.err != nil {
		return projectionApp.RebuildState{}, s.err
	}
	return s.state, nil
}

type cleanupCall struct {
	index   string
	confirm bool
}

func (s *searchStub) Retry(_ context.Context, scope projectionApp.RetryScope, _ time.Time) (projectionApp.RetryReport, error) {
	s.scopes = append(s.scopes, scope)
	if s.err != nil {
		return projectionApp.RetryReport{}, s.err
	}
	report := s.report
	report.Scope = scope
	return report, nil
}

func runSearchCommand(t *testing.T, service SearchService, args ...string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	runner := New(Options{Search: service, Stdout: &stdout, Stderr: &stderr,
		Now: func() time.Time { return time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC) }})
	err := runner.Run(context.Background(), append([]string{"search"}, args...))
	return stdout.String(), stderr.String(), err
}

// TestSearchRetryRequiresBoundedScope 覆盖 5.4 的有界范围校验。
func TestSearchRetryRequiresBoundedScope(t *testing.T) {
	stub := &searchStub{}
	for _, args := range [][]string{
		{"retry"},
		{"retry", "-limit", "0"},
		{"retry", "-limit", "1001"},
		{"retry", "-limit", "10", "多余参数"},
	} {
		if _, _, err := runSearchCommand(t, stub, args...); err == nil {
			t.Fatalf("参数 %v 必须被拒绝", args)
		}
	}
	if len(stub.scopes) != 0 {
		t.Fatalf("非法范围不得触达存储: %+v", stub.scopes)
	}
}

func TestSearchRetryRefusesWhenOpenSearchIsUnconfigured(t *testing.T) {
	if _, _, err := runSearchCommand(t, nil, "retry", "-limit", "10"); err == nil ||
		!strings.Contains(err.Error(), "未配置 OpenSearch") {
		t.Fatalf("未配置时必须明确拒绝: %v", err)
	}
}

// TestSearchRetryReportsAuditableScope 覆盖 5.4 的报告：
// 输出必须包含精确范围、跳过数量与逐个目标身份。
func TestSearchRetryReportsAuditableScope(t *testing.T) {
	stub := &searchStub{report: projectionApp.RetryReport{
		FailedTotal: 3, Superseded: 1, Remaining: 4,
		Reactivated: []projectionApp.RetriedDelivery{
			{ArticleID: 7, Index: "velis-articles-v1-20260925t120000z-aaaaaa", Generation: 4},
			{ArticleID: 9, Index: "velis-articles-v1-20260925t120000z-aaaaaa", Generation: 2},
		},
	}}
	stdout, _, err := runSearchCommand(t, stub, "retry", "-article-id", "7", "-limit", "10")
	if err != nil {
		t.Fatal(err)
	}
	if len(stub.scopes) != 1 || stub.scopes[0].ArticleID != 7 || stub.scopes[0].Limit != 10 || stub.scopes[0].Index != "" {
		t.Fatalf("范围必须原样传递: %+v", stub.scopes)
	}
	for _, fragment := range []string{
		"运行时刻: 2026-09-25T12:00:00Z", "article_id=7", "limit=10",
		"范围内失败投递: 3（其中 generation 已推进、被跳过: 1）",
		"本次重新激活: 2", "范围内仍未完成: 4",
		"- article_id=7 index=velis-articles-v1-20260925t120000z-aaaaaa projection_generation=4",
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("报告缺少 %q:\n%s", fragment, stdout)
		}
	}
}

// TestSearchRetryReportsServiceFailure 确认存储层错误原样上报，不被报告输出掩盖。
func TestSearchRetryReportsServiceFailure(t *testing.T) {
	stub := &searchStub{err: projectionApp.ErrInvalidRetryScope}
	stdout, _, err := runSearchCommand(t, stub, "retry", "-limit", "10")
	if !errors.Is(err, projectionApp.ErrInvalidRetryScope) {
		t.Fatalf("存储层错误必须原样上报: %v", err)
	}
	if strings.Contains(stdout, "失败投影重试报告") {
		t.Fatalf("失败时不得输出成功报告:\n%s", stdout)
	}
}

// TestSearchIndexInitReportsSchemaIdentity 覆盖 6.1：
// 初始化输出必须给出实际物理索引与 schema 身份，便于核对命令可复现。
func TestSearchIndexInitReportsSchemaIdentity(t *testing.T) {
	stub := &searchStub{indexState: projectionApp.IndexState{
		PhysicalIndex: "velis-articles-v1-20260925t120000z-aaaaaa", SchemaVersion: 1,
		SchemaIdentity: "mapping-v1|analyzer-cjk|dims-3|encoding-v1", HasReadAlias: true, HasWriteAlias: true,
		VisibleDocuments: 12,
	}}
	stdout, _, err := runSearchCommand(t, stub, "index", "init")
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"物理索引: velis-articles-v1-20260925t120000z-aaaaaa",
		"schema 身份: mapping-v1|analyzer-cjk|dims-3|encoding-v1",
		"读别名: true 写别名: true",
		"可检索文档: 12",
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("输出缺少 %q:\n%s", fragment, stdout)
		}
	}
}

// TestSearchRebuildStatusesAndCutover 覆盖 6.1 与 6.5 的命令面。
func TestSearchRebuildStatusesAndCutover(t *testing.T) {
	deadline := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	stub := &searchStub{state: projectionApp.RebuildState{
		ID: "rebuild-1", CandidateIndex: "velis-articles-v1-20260925t130000z-bbbbbb",
		Phase: projectionDomain.PhaseServing, StartChangeSeq: 10, CatchUpWatermark: 24,
		RollbackDeadline: &deadline,
		Validation: &projectionDomain.ValidationReport{PublicDocuments: 12, CandidateDocuments: 12,
			LaggingDeliveries: 0, Mismatches: []string{}, ReportedAt: deadline},
	}}
	stdout, _, err := runSearchCommand(t, stub, "rebuild", "start")
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"阶段: serving", "候选索引: velis-articles-v1-20260925t130000z-bbbbbb",
		"增量水位: change_seq=24（起点 10）", "校验: 公开 12 候选 12 落后投递 0 抽样不一致 0 通过=true",
		"回滚窗口截止: 2026-09-26T12:00:00Z",
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("输出缺少 %q:\n%s", fragment, stdout)
		}
	}
	if _, _, err := runSearchCommand(t, stub, "rebuild", "cutover", "rebuild-1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runSearchCommand(t, stub, "rebuild", "rollback"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runSearchCommand(t, stub, "rebuild", "abandon", "rebuild-1"); err != nil {
		t.Fatal(err)
	}
	// 未知子命令必须被拒绝。
	if _, _, err := runSearchCommand(t, stub, "rebuild", "重来"); err == nil {
		t.Fatal("未知子命令必须被拒绝")
	}
}

// TestSearchRebuildCleanupRequiresExplicitTarget 覆盖 6.6：
// cleanup 必须显式指定精确索引并显式确认，参数缺失时不得触达存储。
func TestSearchRebuildCleanupRequiresExplicitTarget(t *testing.T) {
	stub := &searchStub{}
	if _, _, err := runSearchCommand(t, stub, "rebuild", "cleanup"); err == nil {
		t.Fatal("缺少 -index 必须被拒绝")
	}
	if len(stub.cleanups) != 0 {
		t.Fatalf("非法参数不得触达存储: %+v", stub.cleanups)
	}
	if _, _, err := runSearchCommand(t, stub, "rebuild", "cleanup", "-index", "velis-articles-v1-20260925t120000z-aaaaaa"); err != nil {
		t.Fatal(err)
	}
	if len(stub.cleanups) != 1 || stub.cleanups[0].index != "velis-articles-v1-20260925t120000z-aaaaaa" || stub.cleanups[0].confirm {
		t.Fatalf("未确认的 cleanup 必须原样传递确认状态: %+v", stub.cleanups)
	}
}
