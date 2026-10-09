package articlecache

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBudgetSharedAcrossOperations(t *testing.T) {
	now := time.Now()
	b := NewBudget(50*time.Millisecond, 100*time.Millisecond, func() time.Time { return now })
	ctx := WithBudget(context.Background(), b)
	if BudgetFrom(ctx) != b {
		t.Fatal("预算没有共享")
	}
	for i, want := range []time.Duration{50 * time.Millisecond, 50 * time.Millisecond, 20 * time.Millisecond} {
		err := b.Execute(ctx, func(c context.Context) error {
			deadline, _ := c.Deadline()
			left := time.Until(deadline)
			if left > want || left < want-5*time.Millisecond {
				t.Errorf("第 %d 次额度错误: %v", i, left)
			}
			now = now.Add(min(40*time.Millisecond, want))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := b.Execute(ctx, func(context.Context) error { t.Fatal("耗尽后仍调用"); return nil }); !errors.Is(err, ErrBypass) {
		t.Fatal(err)
	}
}
func TestBudgetFailureAndParentCancellation(t *testing.T) {
	b := NewBudget(50*time.Millisecond, 100*time.Millisecond, nil)
	failed := errors.New("传输失败")
	if err := b.Execute(context.Background(), func(context.Context) error { return failed }); err != failed {
		t.Fatal(err)
	}
	if err := b.Execute(context.Background(), func(context.Context) error { t.Fatal("故障后仍调用"); return nil }); err != ErrBypass {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.Execute(ctx, func(context.Context) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	b = NewBudget(50*time.Millisecond, 100*time.Millisecond, nil)
	if err := b.Execute(ctx, func(c context.Context) error { <-c.Done(); return c.Err() }); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
func TestDisabledCache(t *testing.T) {
	d := Disabled{}
	ctx := d.NewRequest(context.Background())
	cards, err := d.GetCards(ctx, []CardIdentity{{ArticleID: 1}})
	if err != nil || len(cards) != 0 {
		t.Fatal(cards, err)
	}
	if _, hit, err := d.GetLatest(ctx, LatestQuery{}); hit || err != nil {
		t.Fatal(hit, err)
	}
	if _, hit, err := d.GetPlan(ctx, PlanIdentity{}); hit || err != nil {
		t.Fatal(hit, err)
	}
	if err := d.PutCards(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := d.PutLatest(ctx, LatestQuery{}, LatestPage{}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := d.PutPlan(ctx, PlanIdentity{}, RecommendationPlan{}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := d.InvalidateLatest(ctx); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := d.GetCards(canceled, nil); err != context.Canceled {
		t.Fatal(err)
	}
}
