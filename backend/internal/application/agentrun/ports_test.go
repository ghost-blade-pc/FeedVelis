package agentrun

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentevidence"
	run "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
)

const testID = "81000000-0000-0000-0000-000000000001"
const otherID = "81000000-0000-0000-0000-000000000002"

type guardFixture struct {
	inSnapshot           bool
	order                []string
	current              run.Run
	now                  time.Time
	authError, readError error
}

func (f *guardFixture) WithinReadSnapshot(ctx context.Context, callback func(context.Context, bool) error) error {
	f.inSnapshot = true
	defer func() { f.inSnapshot = false }()
	return callback(ctx, true)
}
func (f *guardFixture) CheckSession(_ context.Context, userID, sessionID string) error {
	if !f.inSnapshot {
		panic("授权不在当前快照中")
	}
	f.order = append(f.order, "auth")
	if userID != testID || sessionID != testID {
		return &run.Error{Code: run.CodeAuthInvalid}
	}
	return f.authError
}
func (f *guardFixture) ReadExecution(_ context.Context, scope Scope) (run.Run, time.Time, error) {
	if !f.inSnapshot {
		panic("执行权不在当前快照中")
	}
	f.order = append(f.order, "read")
	if scope.UserID != testID || scope.ConversationID != testID || scope.RunID != testID {
		return run.Run{}, f.now, run.ErrNotFound
	}
	return f.current, f.now, f.readError
}
func fixture(t *testing.T) (*guardFixture, Execution) {
	t.Helper()
	now := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	r, err := run.New(testID, testID, testID, testID, run.Input{MessageID: testID}, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Claim(otherID, now); err != nil {
		t.Fatal(err)
	}
	return &guardFixture{current: r, now: now}, Execution{Scope: Scope{testID, testID, testID}, SessionID: testID, OwnerToken: otherID}
}

func TestGuardPortAuthorizationAndOwnership(t *testing.T) {
	tests := []struct {
		name   string
		change func(*guardFixture, *Execution)
		code   run.Code
		reads  int
	}{
		{"有效", func(*guardFixture, *Execution) {}, "", 1},
		{"注销", func(f *guardFixture, _ *Execution) { f.authError = &run.Error{Code: run.CodeAuthInvalid} }, run.CodeAuthInvalid, 0},
		{"授权库故障", func(f *guardFixture, _ *Execution) { f.authError = errors.New("敏感底层错误") }, run.CodeDependencyUnavailable, 0},
		{"跨账户", func(_ *guardFixture, e *Execution) { e.UserID = otherID }, run.CodeAuthInvalid, 0},
		{"会话删除", func(f *guardFixture, _ *Execution) { f.readError = run.ErrNotFound }, run.CodeNotFound, 1},
		{"取消", func(f *guardFixture, _ *Execution) { f.current.State = run.Canceled }, run.CodeExecutorLost, 1},
		{"过期租约", func(f *guardFixture, _ *Execution) { f.now = f.current.LeaseUntil }, run.CodeExecutorLost, 1},
		{"错误执行者", func(_ *guardFixture, e *Execution) { e.OwnerToken = testID }, run.CodeExecutorLost, 1},
		{"不同登录会话", func(f *guardFixture, _ *Execution) { f.current.SessionID = otherID }, run.CodeExecutorLost, 1},
		{"不同所有者", func(f *guardFixture, _ *Execution) { f.current.UserID = otherID }, run.CodeExecutorLost, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, e := fixture(t)
			tt.change(f, &e)
			err := NewGuard(f, f, f).Check(context.Background(), e)
			if SafeCode(err) != tt.code {
				t.Fatalf("错误分类: %v", err)
			}
			if len(f.order) != 1+tt.reads || f.order[0] != "auth" {
				t.Fatalf("认证未先行: %v", f.order)
			}
		})
	}
}

func TestGuardCanceledContextAndMissingPorts(t *testing.T) {
	f, e := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := NewGuard(f, f, f).Check(ctx, e); !errors.Is(err, context.Canceled) || len(f.order) > 0 {
		t.Fatal("取消后仍访问依赖")
	}
	if err := NewGuard(nil, f, f).Check(context.Background(), e); SafeCode(err) != run.CodeDependencyUnavailable {
		t.Fatal("缺端口没有关闭执行")
	}
}

type memoryStore struct {
	// 本契约只测试接受写入原子性；未使用的读方法由后续仓储契约补齐。
	Repository
	runs          []run.Run
	reserved      int
	inTransaction bool
}

var _ Store = (*memoryStore)(nil)

func (s *memoryStore) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	old, reserved := append([]run.Run(nil), s.runs...), s.reserved
	s.inTransaction = true
	defer func() { s.inTransaction = false }()
	if err := fn(ctx); err != nil {
		s.runs, s.reserved = old, reserved
		return err
	}
	return nil
}
func (s *memoryStore) WithinReadSnapshot(ctx context.Context, fn func(context.Context, bool) error) error {
	return fn(ctx, true)
}
func (s *memoryStore) InsertAccepted(_ context.Context, r run.Run) error {
	if !s.inTransaction {
		return errors.New("写入不在事务中")
	}
	s.runs = append(s.runs, r)
	if r.AssistantReserved {
		s.reserved++
	}
	return nil
}

func TestTransactionStoreContract(t *testing.T) {
	f, _ := fixture(t)
	s := &memoryStore{}
	var store Store = s
	failure := errors.New("注入幂等保存失败")
	err := store.WithinTransaction(context.Background(), func(ctx context.Context) error {
		if err := store.InsertAccepted(ctx, f.current); err != nil {
			return err
		}
		return failure
	})
	if !errors.Is(err, failure) || len(s.runs) != 0 || s.reserved != 0 {
		t.Fatal("失败事务泄露回合或预留")
	}
	err = store.WithinTransaction(context.Background(), func(ctx context.Context) error { return store.InsertAccepted(ctx, f.current) })
	if err != nil || len(s.runs) != 1 || s.reserved != 1 {
		t.Fatal("成功接受未原子保存预留")
	}
}

type testRunner struct{}

func (testRunner) Execute(ctx context.Context, request ExecutionRequest, sink OutputSink) (ExecutionResult, error) {
	if err := ctx.Err(); err != nil {
		return ExecutionResult{}, err
	}
	if err := sink.SubmitBlock(ctx, BlockCommit{Block: agentevidence.Block{ID: "b1", Text: request.Context.Question}}); err != nil {
		return ExecutionResult{}, err
	}
	return ExecutionResult{Answer: agentevidence.Answer{Version: 1, Kind: agentevidence.Clarification}}, nil
}

type testSink struct {
	failure error
	calls   int
}

func (s *testSink) SubmitBlock(context.Context, BlockCommit) error { s.calls++; return s.failure }

var _ Runner = testRunner{}
var _ OutputSink = (*testSink)(nil)

func TestExecutionPortPropagatesRejectedSubmission(t *testing.T) {
	failure := &run.Error{Code: run.CodeEvidenceStale}
	sink := &testSink{failure: failure}
	var runner Runner = testRunner{}
	_, err := runner.Execute(context.Background(), ExecutionRequest{Context: Context{Question: "当前问题"}}, sink)
	if !errors.Is(err, failure) || sink.calls != 1 {
		t.Fatal("编排器绕过提交拒绝")
	}
	preferences, err := (EmptyPreferences{}).Preferences(context.Background(), testID)
	if err != nil || preferences == nil || len(preferences) != 0 {
		t.Fatal("首版偏好源不为空")
	}
}

func TestEventAndSafeObservationContracts(t *testing.T) {
	for _, kind := range []EventKind{RunStatus, ToolStatus, AnswerSources, AnswerBlock, RunTerminal} {
		if !kind.Valid() {
			t.Fatal(kind)
		}
	}
	if EventKind("stream.error").Valid() || EventKind("model.delta").Valid() {
		t.Fatal("订阅控制或模型事件进入持久日志")
	}
	if SafeCode(errors.New("api_key=不应出现的值")) != run.CodeInternal {
		t.Fatal("Provider 文本透传")
	}
	if SafeCode(&run.Error{Code: "原始私密错误"}) != run.CodeInternal {
		t.Fatal("未知分类透传")
	}
	if SafeCode(fmt.Errorf("包装: %w", run.ErrBudget)) != run.CodeBudgetExceeded {
		t.Fatal("稳定分类丢失")
	}
}
