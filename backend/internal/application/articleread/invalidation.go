package articleread

import (
	"context"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlecache"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
)

// Invalidator 的动作由最外层事务同步执行；请求取消不撤销已经提交的业务结果。
type Invalidator struct {
	registrar ports.AfterCommitRegistrar
	cache     articlecache.Cache
	lifecycle context.Context
	timeout   time.Duration
}

func NewInvalidator(registrar ports.AfterCommitRegistrar, cache articlecache.Cache, lifecycle context.Context, timeout time.Duration) *Invalidator {
	if lifecycle == nil {
		lifecycle = context.Background()
	}
	if timeout <= 0 {
		timeout = 100 * time.Millisecond
	}
	return &Invalidator{registrar, cache, lifecycle, timeout}
}
func (i *Invalidator) ScheduleLatestInvalidation(ctx context.Context) error {
	return i.registrar.RegisterAfterCommit(ctx, "article.latest", func(committed context.Context) error {
		work, cancel := context.WithTimeout(articlecache.WithoutBudget(context.WithoutCancel(committed)), i.timeout)
		defer cancel()
		stop := context.AfterFunc(i.lifecycle, cancel)
		defer stop()
		if i.lifecycle.Err() != nil {
			cancel()
		}
		return i.cache.InvalidateLatest(i.cache.NewRequest(work))
	})
}
