package articleread

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlecache"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
)

type latestFixture struct {
	*readFixture
	latestCalls    int
	queryPositions []*articleDomain.Cursor
}

func (f *latestFixture) ListLatestCandidates(_ context.Context, q articlecache.LatestQuery) (articlecache.LatestPage, error) {
	f.latestCalls++
	f.queryPositions = append(f.queryPositions, q.Position)
	if f.fail != nil {
		return articlecache.LatestPage{}, f.fail
	}
	candidates := []articlecache.Candidate{}
	for _, fact := range f.facts {
		c := articlecache.Candidate{ArticleID: fact.Item.ID, SortAt: fact.Item.SortAt}
		if after(c, q.Position) {
			candidates = append(candidates, c)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].SortAt.After(candidates[j].SortAt) || candidates[i].SortAt.Equal(candidates[j].SortAt) && candidates[i].ArticleID > candidates[j].ArticleID
	})
	return articlecache.LatestPage{Candidates: candidates[:min(q.Limit+1, len(candidates))], Exhausted: len(candidates) < q.Limit+1}, nil
}
func after(c articlecache.Candidate, p *articleDomain.Cursor) bool {
	return p == nil || c.SortAt.Before(p.SortAt) || c.SortAt.Equal(p.SortAt) && c.ArticleID < p.ArticleID
}
func queryKey(q articlecache.LatestQuery) string { return fmt.Sprintf("%d:%v", q.Limit, q.Position) }

type latestCache struct {
	*memoryCache
	pages      map[string]articlecache.LatestPage
	latestPuts int
	stalePool  []articlecache.Candidate
}

func (c *latestCache) GetLatest(_ context.Context, q articlecache.LatestQuery) (articlecache.LatestPage, bool, error) {
	if c.err != nil {
		return articlecache.LatestPage{}, false, c.err
	}
	if c.stalePool != nil {
		pool := []articlecache.Candidate{}
		for _, candidate := range c.stalePool {
			if after(candidate, q.Position) {
				pool = append(pool, candidate)
			}
		}
		return articlecache.LatestPage{Candidates: pool[:min(q.Limit+1, len(pool))], Exhausted: len(pool) < q.Limit+1}, true, nil
	}
	page, ok := c.pages[queryKey(q)]
	return page, ok, nil
}
func (c *latestCache) PutLatest(_ context.Context, q articlecache.LatestQuery, p articlecache.LatestPage, _ time.Time) error {
	if c.fixture.active {
		panic("候选快照结束前回填")
	}
	c.latestPuts++
	c.pages[queryKey(q)] = p
	return c.err
}
func newLatest(n int) (*Service, *latestFixture, *latestCache) {
	f := &latestFixture{readFixture: newFixture(n)}
	c := &latestCache{memoryCache: newMemoryCache(f.readFixture), pages: map[string]articlecache.LatestPage{}}
	return NewService(f, f, c, nil, 100, nil), f, c
}
func TestLatestTiesOfflineLookaheadAndCacheFailure(t *testing.T) {
	s, f, c := newLatest(10)
	ctx := context.Background()
	items, next, more, err := s.ListLatest(ctx, nil, 2)
	if err != nil || !more || next.ArticleID != 9 || items[0].ID != 10 || items[1].ID != 9 {
		t.Fatal(items, next, more, err)
	}
	before := f.latestCalls
	items, _, _, err = s.ListLatest(ctx, nil, 2)
	if err != nil || f.latestCalls != before || len(items) != 2 {
		t.Fatal("热点候选未命中", err)
	}
	delete(f.facts, 10)
	delete(f.facts, 9)
	delete(f.facts, 8)
	items, next, more, err = s.ListLatest(ctx, nil, 2)
	if err != nil || len(items) != 2 || items[0].ID != 7 || items[1].ID != 6 || next.ArticleID != 6 || !more {
		t.Fatal("下架补页水位错误", items, next, more, err)
	}
	items, _, _, err = s.ListLatest(ctx, next, 2)
	if err != nil || items[0].ID != 5 || items[1].ID != 4 {
		t.Fatal("lookahead 被跳过", items, err)
	}
	clear(c.pages)
	for _, failure := range []error{nil, errors.New("损坏或不可达")} {
		c.err = failure
		items, _, _, err = s.ListLatest(ctx, nil, 2)
		if err != nil || len(items) != 2 || items[0].ID != 7 {
			t.Fatal("缓存故障未安全回源", items, err)
		}
	}
}
func TestLatestPositionMismatchReloadsWholeBatchAndEmptyPool(t *testing.T) {
	s, f, c := newLatest(4)
	ctx := context.Background()
	if _, _, _, err := s.ListLatest(ctx, nil, 2); err != nil {
		t.Fatal(err)
	}
	fact := f.facts[4]
	fact.Item.SortAt = fact.Item.SortAt.Add(-time.Hour)
	f.facts[4] = fact
	calls := f.latestCalls
	items, next, more, err := s.ListLatest(ctx, nil, 2)
	if err != nil || items[0].ID != 3 || items[1].ID != 2 || next.ArticleID != 2 || !more || f.latestCalls != calls+1 || f.queryPositions[len(f.queryPositions)-1] != nil {
		t.Fatal("位置变化未按原位置整批重查", items, next, err)
	}
	s, f, c = newLatest(0)
	for i := 0; i < 2; i++ {
		items, next, more, err = s.ListLatest(ctx, nil, 20)
		if err != nil || len(items) != 0 || next != nil || more {
			t.Fatal(items, next, more, err)
		}
	}
	if f.latestCalls != 1 || c.latestPuts != 1 {
		t.Fatal("空候选池未缓存")
	}
}
func TestLatestScanCapShortEmptyAndStableProgress(t *testing.T) {
	for _, visible := range []int64{0, 750} {
		t.Run(fmt.Sprint(visible), func(t *testing.T) {
			s, f, c := newLatest(1000)
			for id := int64(1000); id > 0; id-- {
				c.stalePool = append(c.stalePool, articlecache.Candidate{ArticleID: id, SortAt: f.facts[id].Item.SortAt})
				if id != visible && id != 100 {
					delete(f.facts, id)
				}
			}
			items, next, more, err := s.ListLatest(context.Background(), nil, 2)
			wanted := 0
			if visible != 0 {
				wanted = 1
			}
			if err != nil || len(items) != wanted || !more || next == nil || next.ArticleID != 501 {
				t.Fatal("500 候选边界错误", items, next, more, err)
			}
			items, next, more, err = s.ListLatest(context.Background(), next, 2)
			if err != nil || len(items) != 1 || items[0].ID != 100 || more || next != nil {
				t.Fatal("短空页未稳定推进", items, next, more, err)
			}
		})
	}
}
func TestLatestDatabaseFailureAndExistingWriteBypass(t *testing.T) {
	s, f, c := newLatest(2)
	ctx := context.Background()
	s.ListLatest(ctx, nil, 1)
	f.fail = errors.New("PostgreSQL 失败")
	if _, _, _, err := s.ListLatest(ctx, nil, 1); err != f.fail {
		t.Fatal("候选命中掩盖数据库故障", err)
	}
	f.fail = nil
	f.allowed = false
	c.getCalls = 0
	c.putCalls = 0
	c.latestPuts = 0
	clear(c.pages)
	if _, _, _, err := s.ListLatest(ctx, nil, 1); err != nil || c.getCalls != 0 || c.putCalls != 0 || c.latestPuts != 0 {
		t.Fatal("写事务访问公开缓存", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, _, err := s.ListLatest(ctx, nil, 1); !errors.Is(err, context.Canceled) {
		t.Fatal("父取消被吞掉", err)
	}
}
