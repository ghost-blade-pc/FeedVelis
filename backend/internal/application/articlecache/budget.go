package articlecache

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrBypass = errors.New("缓存请求已绕过")

// RequestBudget 串行分配同一请求所有缓存操作的剩余预算，不限制业务依赖耗时。
type RequestBudget interface {
	Execute(context.Context, func(context.Context) error) error
	Remaining() time.Duration
}
type budgetKey struct{}

func WithBudget(ctx context.Context, b RequestBudget) context.Context {
	return context.WithValue(ctx, budgetKey{}, b)
}

// WithoutBudget 仅供提交后独立动作使用，保留关联信息并屏蔽原请求额度。
func WithoutBudget(ctx context.Context) context.Context {
	return context.WithValue(ctx, budgetKey{}, struct{}{})
}
func BudgetFrom(ctx context.Context) RequestBudget {
	b, _ := ctx.Value(budgetKey{}).(RequestBudget)
	return b
}

type Budget struct {
	mu                   sync.Mutex
	operation, remaining time.Duration
	bypass               bool
	now                  func() time.Time
}

func NewBudget(operation, total time.Duration, now func() time.Time) *Budget {
	if now == nil {
		now = time.Now
	}
	return &Budget{operation: operation, remaining: total, now: now}
}
func (b *Budget) Remaining() time.Duration { b.mu.Lock(); defer b.mu.Unlock(); return b.remaining }

// 调用方仅把传输错误交给 Execute；数据损坏在操作成功后作为未命中处理。
func (b *Budget) Execute(parent context.Context, call func(context.Context) error) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := parent.Err(); err != nil {
		return err
	}
	if b.bypass || b.remaining <= 0 || b.operation <= 0 {
		return ErrBypass
	}
	limit := min(b.operation, b.remaining)
	ctx, cancel := context.WithTimeout(parent, limit)
	start := b.now()
	err := call(ctx)
	elapsed := max(time.Duration(0), b.now().Sub(start))
	b.remaining = max(time.Duration(0), b.remaining-elapsed)
	if err == nil {
		err = ctx.Err()
	}
	cancel()
	if err != nil || b.remaining == 0 {
		b.bypass = true
	}
	if parent.Err() != nil {
		return parent.Err()
	}
	return err
}
