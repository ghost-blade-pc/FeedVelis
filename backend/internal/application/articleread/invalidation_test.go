package articleread

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlecache"
)

type actionRegistrar struct {
	action func(context.Context) error
	id     string
}

func (r *actionRegistrar) RegisterAfterCommit(_ context.Context, id string, action func(context.Context) error) error {
	r.id = id
	r.action = action
	return nil
}

type actionCache struct {
	articlecache.Disabled
	calls    int
	original articlecache.RequestBudget
	t        *testing.T
}

func (c *actionCache) NewRequest(ctx context.Context) context.Context {
	if articlecache.BudgetFrom(ctx) != nil {
		c.t.Fatal("提交后动作复用了请求预算")
	}
	return articlecache.WithBudget(ctx, articlecache.NewBudget(50*time.Millisecond, 100*time.Millisecond, nil))
}
func (c *actionCache) InvalidateLatest(ctx context.Context) error {
	c.calls++
	if articlecache.BudgetFrom(ctx) == nil || articlecache.BudgetFrom(ctx) == c.original {
		c.t.Fatal("没有独立额度")
	}
	if _, ok := ctx.Deadline(); !ok {
		c.t.Fatal("提交后动作没有截止时间")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.New("失效失败")
}
func TestInvalidationDetachedBoundedBudgetAndLifecycle(t *testing.T) {
	r := &actionRegistrar{}
	old := articlecache.NewBudget(0, 0, nil)
	cache := &actionCache{t: t, original: old}
	life, stop := context.WithCancel(context.Background())
	defer stop()
	invalidator := NewInvalidator(r, cache, life, 100*time.Millisecond)
	request, cancel := context.WithCancel(articlecache.WithBudget(context.Background(), old))
	if err := invalidator.ScheduleLatestInvalidation(request); err != nil {
		t.Fatal(err)
	}
	if r.id != "article.latest" || cache.calls != 0 {
		t.Fatal("登记期间执行失效")
	}
	cancel()
	if err := r.action(request); err == nil || errors.Is(err, context.Canceled) {
		t.Fatal("父请求取消撤销已提交失效", err)
	}
	stop()
	if err := r.action(request); !errors.Is(err, context.Canceled) {
		t.Fatal("进程退出未约束失效", err)
	}
}
