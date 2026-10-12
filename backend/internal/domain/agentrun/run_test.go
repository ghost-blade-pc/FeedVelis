package agentrun

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

const testID = "81000000-0000-0000-0000-000000000001"
const otherID = "81000000-0000-0000-0000-000000000002"

var testNow = time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)

func newTestRun(t *testing.T) Run {
	t.Helper()
	r, err := New(testID, testID, testID, testID, Input{MessageID: testID}, testNow, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestEveryStateTransition(t *testing.T) {
	states := []State{Accepted, Running, Succeeded, Failed, Canceled, TimedOut, "unknown"}
	allowed := map[State]map[State]bool{
		Accepted: {Running: true, Failed: true, Canceled: true, TimedOut: true},
		Running:  {Succeeded: true, Failed: true, Canceled: true, TimedOut: true},
	}
	for _, from := range states {
		for _, to := range states {
			t.Run(string(from)+"/"+string(to), func(t *testing.T) {
				want := allowed[from][to]
				if from.CanTransition(to) != want {
					t.Fatalf("迁移判定错误: %s → %s", from, to)
				}
				r := newTestRun(t)
				r.State = from
				r.OwnerToken, r.LeaseUntil = otherID, testNow.Add(10*time.Second)
				before := r
				var err error
				switch to {
				case Running:
					err = r.Claim(otherID, testNow)
				case Succeeded:
					err = r.Complete(otherID, otherID, testNow)
				default:
					err = r.Finish(to, CodeCanceled, "", testNow)
				}
				if (err == nil) != want {
					t.Fatalf("迁移执行错误: %v", err)
				}
				if err != nil && !reflect.DeepEqual(before, r) {
					t.Fatal("拒绝的迁移修改了事实")
				}
				if want && to.Terminal() && (r.AssistantReserved || r.FinishedAt.IsZero() || r.OwnerToken != "") {
					t.Fatal("终态未释放执行权与预留")
				}
			})
		}
	}
}

func TestExecutionLeaseAndDeadline(t *testing.T) {
	r := newTestRun(t)
	if err := r.Claim(otherID, testNow); err != nil {
		t.Fatal(err)
	}
	deadline := r.Deadline
	if err := r.Renew(otherID, testNow.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if r.LeaseUntil != testNow.Add(12*time.Second) || r.Deadline != deadline {
		t.Fatal("续约错误地改变了总期限")
	}
	if err := r.CheckOwner(testID, testNow.Add(3*time.Second)); !errors.Is(err, ErrExecutionLost) {
		t.Fatal(err)
	}
	before := r
	if err := r.Renew(otherID, r.LeaseUntil); !errors.Is(err, ErrExecutionLost) || !reflect.DeepEqual(r, before) {
		t.Fatalf("过期租约可被复活: %v", err)
	}
	if err := r.Complete(otherID, otherID, r.LeaseUntil); err == nil {
		t.Fatal("迟到执行者提交成功")
	}
	r = newTestRun(t)
	if err := r.Claim(otherID, r.Deadline); !errors.Is(err, ErrDeadline) {
		t.Fatalf("接受排队到期: %v", err)
	}
	if err := r.Finish(TimedOut, CodeTimeout, "", r.Deadline); err != nil || r.Deadline != deadline {
		t.Fatalf("超时未收敛: %v", err)
	}
	r = newTestRun(t)
	if err := r.Claim(otherID, testNow.Add(59*time.Second)); err != nil || r.LeaseUntil != r.Deadline {
		t.Fatalf("租约超出总期限: %v", err)
	}
	if err := r.Finish(Succeeded, "", otherID, testNow); err == nil {
		t.Fatal("成功绕过执行权")
	}
}

func TestExplicitRetry(t *testing.T) {
	input := Input{MessageID: testID, ContextArticleID: 42, Constraints: Constraints{Keyword: "Go", SourceID: 8}}
	if err := CheckRetry(input, nil); err != nil {
		t.Fatal(err)
	}
	changed := input
	changed.RetryOf = otherID
	if err := CheckRetry(changed, nil); !errors.Is(err, ErrInputConflict) {
		t.Fatal(err)
	}
	for _, state := range []State{Failed, Canceled, TimedOut, Accepted, Running, Succeeded, "unknown"} {
		history := []Attempt{{ID: otherID, State: state, Input: input}}
		err := CheckRetry(changed, history)
		if (err == nil) != state.Retryable() {
			t.Fatalf("%s: %v", state, err)
		}
	}
	history := []Attempt{{ID: otherID, State: Failed, Input: input}}
	for _, alter := range []func(*Input){
		func(i *Input) { i.RetryOf = "" }, func(i *Input) { i.RetryOf = testID },
		func(i *Input) { i.MessageID = otherID }, func(i *Input) { i.ContextArticleID++ },
		func(i *Input) { i.Constraints.Topic = "改变约束" },
	} {
		invalid := changed
		alter(&invalid)
		if err := CheckRetry(invalid, history); !errors.Is(err, ErrInputConflict) {
			t.Fatalf("非法重试被接受: %#v %v", invalid, err)
		}
	}
	history = append(history, Attempt{ID: testID, State: Failed, Input: changed})
	third := changed
	third.RetryOf = testID
	if err := CheckRetry(third, history); err != nil {
		t.Fatal(err)
	}
	history = append(history, Attempt{ID: otherID, State: TimedOut, Input: third})
	if err := CheckRetry(changed, history); !errors.Is(err, ErrAttemptLimit) {
		t.Fatal(err)
	}
	// 成功事实即使出现在更早的历史中也永久阻止再次生成。
	history = history[:2]
	history[0].State = Succeeded
	if err := CheckRetry(third, history); !errors.Is(err, ErrInputConflict) {
		t.Fatal(err)
	}
}

func TestRunInputAndTerminalValidation(t *testing.T) {
	for _, timeout := range []time.Duration{0, 4 * time.Second, 121 * time.Second} {
		if _, err := New(testID, testID, testID, testID, Input{MessageID: testID}, testNow, timeout); err == nil {
			t.Fatal("非法期限被接受")
		}
	}
	r := newTestRun(t)
	before := r
	if err := r.Finish(Failed, Code("Provider私密原文"), "", testNow); err == nil || !reflect.DeepEqual(r, before) {
		t.Fatal("原始错误被保存")
	}
	if err := r.Finish(Canceled, CodeCanceled, otherID, testNow); err == nil {
		t.Fatal("取消保存助手消息")
	}
}

func TestQuotaReservations(t *testing.T) {
	tests := []struct {
		account, conversation, messages, reserved, max int
		want                                           error
	}{
		{0, 0, 199, 0, 200, nil}, {0, 0, 200, 0, 200, ErrMessageLimit},
		{0, 0, 199, 1, 200, ErrMessageLimit}, {1, 0, 0, 0, 200, nil},
		{2, 0, 0, 0, 200, ErrInProgress}, {0, 1, 0, 1, 200, ErrInProgress},
		{0, 0, 201, 0, 200, ErrMessageLimit}, {-1, 0, 0, 0, 200, ErrInvalidInput},
	}
	for _, tt := range tests {
		if err := CheckAdmission(tt.account, tt.conversation, tt.messages, tt.reserved, tt.max); !errors.Is(err, tt.want) {
			t.Fatalf("%+v: %v", tt, err)
		}
	}
	if err := CheckAssistantCommit(199, 1, 200); err != nil {
		t.Fatal(err)
	}
	if err := CheckAssistantCommit(199, 1, 199); !errors.Is(err, ErrMessageLimit) {
		t.Fatal("旧预留突破降低后的配额")
	}
	if err := CheckAssistantCommit(1, 0, 200); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("无预留提交")
	}
}
