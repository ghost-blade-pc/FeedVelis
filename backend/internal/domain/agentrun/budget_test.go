package agentrun

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func token(v int64) *int64 { return &v }
func testBudget(t *testing.T, limits BudgetLimits) *Budget {
	t.Helper()
	b, err := NewBudget(limits)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBudgetFailedAttemptsAndUnknownUsage(t *testing.T) {
	b := testBudget(t, DefaultBudgetLimits())
	for i := 0; i < 3; i++ {
		r, err := b.ReserveModel(100, true)
		if err != nil {
			t.Fatal(err)
		}
		if err := b.SettleModel(r, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.ReserveModel(100, true); !errors.Is(err, ErrBudget) {
		t.Fatal("取证耗尽没有保留成文调用")
	}
	r, err := b.ReserveModel(100, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.SettleModel(r, &ModelUsage{Input: token(10), Output: token(20), Total: token(30)}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.ReserveModel(100, false); !errors.Is(err, ErrBudget) {
		t.Fatal("失败调用未计数")
	}
	u := b.Usage()
	if u.ModelCalls != 4 || u.Complete || u.InputTokens != nil || u.OutputTokens != nil || u.TotalTokens != nil {
		t.Fatalf("未知用量被伪造为实际值: %+v", u)
	}
}

func TestBudgetConservativelyRetainsOutput(t *testing.T) {
	limits := DefaultBudgetLimits()
	limits.TotalOutput = 2048
	b := testBudget(t, limits)
	r, _ := b.ReserveModel(100, true)
	_ = b.SettleModel(r, nil)
	if _, err := b.ReserveModel(100, false); !errors.Is(err, ErrBudget) {
		t.Fatal("缺 usage 退还了输出预算")
	}
	b = testBudget(t, limits)
	r, _ = b.ReserveModel(100, true)
	if err := b.SettleModel(r, &ModelUsage{Input: token(20), Output: token(20), Total: token(40)}); err != nil {
		t.Fatal(err)
	}
	// 已知输出只结算实际值，但下一调用仍需完整的最大输出预留。
	if _, err := b.ReserveModel(100, false); !errors.Is(err, ErrBudget) {
		t.Fatal("部分余额允许超额预留")
	}
	u := b.Usage()
	if !u.Complete || *u.InputTokens != 20 || *u.OutputTokens != 20 || *u.TotalTokens != 40 {
		t.Fatalf("实际结算: %+v", u)
	}
	if err := b.SettleModel(r, nil); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("预留被重复结算")
	}
}

func TestBatchToolsAndLastSlotConcurrency(t *testing.T) {
	b := testBudget(t, DefaultBudgetLimits())
	if err := b.ReserveTools(5); err != nil {
		t.Fatal(err)
	}
	if err := b.ReserveTools(2); !errors.Is(err, ErrBudget) || b.Usage().ToolCalls != 5 {
		t.Fatal("超额批量部分执行")
	}
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Go(func() {
			if b.ReserveTools(1) == nil {
				accepted.Add(1)
			}
		})
	}
	wg.Wait()
	if accepted.Load() != 1 || b.Usage().ToolCalls != 6 {
		t.Fatal("最后工具名额竞争突破上限")
	}
	if err := b.ReserveTools(1); !errors.Is(err, ErrBudget) {
		t.Fatal("失败尝试名额被复用")
	}
}

func TestBudgetOverrunInputAndAudit(t *testing.T) {
	for _, usage := range []*ModelUsage{
		{Input: token(10), Output: token(2049), Total: token(2059)},
		{Output: token(2049)}, {Input: token(-1)},
		{Input: token(1), Output: token(2), Total: token(9)},
		{Input: token(70000), Output: token(1), Total: token(70001)},
	} {
		b := testBudget(t, DefaultBudgetLimits())
		r, _ := b.ReserveModel(100, true)
		if err := b.SettleModel(r, usage); err == nil {
			t.Fatal("非法/超额 usage 被接受")
		}
		if _, err := b.ReserveModel(100, false); !errors.Is(err, ErrBudget) {
			t.Fatal("Provider 超额后继续调用")
		}
		if err := b.ReserveTools(1); !errors.Is(err, ErrBudget) {
			t.Fatal("Provider 超额后继续工具调用")
		}
	}
	b := testBudget(t, DefaultBudgetLimits())
	if _, err := b.ReserveModel(49153, true); err == nil || b.Usage().ModelCalls != 0 {
		t.Fatal("输入超过字节限额仍调用")
	}
	limits := DefaultBudgetLimits()
	limits.AuditTokens = 3000
	b = testBudget(t, limits)
	r, err := b.ReserveModel(600, true)
	if err != nil {
		t.Fatal(err)
	}
	_ = b.SettleModel(r, nil)
	if _, err := b.ReserveModel(1, false); !errors.Is(err, ErrBudget) {
		t.Fatal("未知 usage 绕过审计预算")
	}
}
