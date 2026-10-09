package articleread

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlecache"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
)

type readFixture struct {
	facts                    map[int64]CurrentFact
	fragments                map[articlecache.CardIdentity]articlecache.CardFragment
	factCalls, fragmentCalls int
	loads                    [][]articlecache.CardIdentity
	active                   bool
	allowed                  bool
	fail                     error
	commitError              error
}

func newFixture(n int) *readFixture {
	f := &readFixture{facts: map[int64]CurrentFact{}, fragments: map[articlecache.CardIdentity]articlecache.CardFragment{}, allowed: true}
	for id := int64(1); id <= int64(n); id++ {
		identity := articlecache.CardIdentity{ArticleID: id, RevisionID: id}
		f.facts[id] = CurrentFact{Identity: identity, Item: articleDomain.ListItem{ID: id, Origin: articleDomain.OriginUser, SortAt: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC), Author: &articleDomain.AuthorSummary{ID: "author", Nickname: "昵称"}}, VectorIdentity: articlesearch.VectorIdentity{RevisionID: id}}
		f.fragments[identity] = articlecache.CardFragment{Identity: identity, Origin: articleDomain.OriginUser, Title: "标题", Excerpt: "摘录"}
	}
	return f
}
func (f *readFixture) WithinReadSnapshot(ctx context.Context, fn func(context.Context, bool) error) error {
	f.active = true
	err := fn(ctx, f.allowed)
	f.active = false
	if err != nil {
		return err
	}
	return f.commitError
}
func (f *readFixture) ListPublicFacts(_ context.Context, ids []int64) ([]CurrentFact, error) {
	f.factCalls++
	if f.fail != nil {
		return nil, f.fail
	}
	var result []CurrentFact
	for i := len(ids) - 1; i >= 0; i-- {
		if fact, ok := f.facts[ids[i]]; ok {
			result = append(result, fact)
		}
	}
	return result, nil
}
func (f *readFixture) ListPublicFragments(_ context.Context, ids []articlecache.CardIdentity) ([]articlecache.CardFragment, error) {
	f.fragmentCalls++
	f.loads = append(f.loads, append([]articlecache.CardIdentity{}, ids...))
	var result []articlecache.CardFragment
	for _, id := range ids {
		if fragment, ok := f.fragments[id]; ok {
			result = append(result, fragment)
		}
	}
	return result, nil
}
func (*readFixture) ListLatestCandidates(context.Context, articlecache.LatestQuery) (articlecache.LatestPage, error) {
	return articlecache.LatestPage{}, nil
}

type memoryCache struct {
	articlecache.Disabled
	fixture            *readFixture
	cards              map[articlecache.CardIdentity]articlecache.CardFragment
	getCalls, putCalls int
	err                error
}

func newMemoryCache(f *readFixture) *memoryCache {
	return &memoryCache{fixture: f, cards: map[articlecache.CardIdentity]articlecache.CardFragment{}}
}
func (c *memoryCache) GetCards(_ context.Context, ids []articlecache.CardIdentity) (map[articlecache.CardIdentity]articlecache.CardFragment, error) {
	if !c.fixture.active {
		panic("缓存读取离开快照")
	}
	c.getCalls++
	result := map[articlecache.CardIdentity]articlecache.CardFragment{}
	for _, id := range ids {
		if value, ok := c.cards[id]; ok {
			result[id] = value
		}
	}
	return result, c.err
}
func (c *memoryCache) PutCards(_ context.Context, items []articlecache.CardWrite) error {
	if c.fixture.active {
		panic("快照结束前回填")
	}
	c.putCalls++
	for _, item := range items {
		c.cards[item.Fragment.Identity] = item.Fragment
	}
	return c.err
}

func TestSharedReadHitsMissingOrderAndDynamicFacts(t *testing.T) {
	f := newFixture(3)
	c := newMemoryCache(f)
	s := NewService(f, f, c, nil, 100, nil)
	ctx := context.Background()
	items, err := s.ListPublishedWithIdentity(ctx, []int64{3, 1, 2, 3, 99})
	if err != nil || len(items) != 3 || items[0].Item.ID != 3 || items[1].Item.ID != 1 || items[2].Item.ID != 2 {
		t.Fatal(items, err)
	}
	if f.factCalls != 1 || f.fragmentCalls != 1 || len(f.loads[0]) != 3 || c.putCalls != 1 {
		t.Fatal("全缺失出现 N+1")
	}
	if _, err := s.ListPublishedWithIdentity(ctx, []int64{3, 1, 2}); err != nil {
		t.Fatal(err)
	}
	if f.fragmentCalls != 1 || f.factCalls != 2 {
		t.Fatal("全命中没有仅复核事实")
	}
	delete(c.cards, f.facts[2].Identity)
	bad := c.cards[f.facts[3].Identity]
	bad.Identity.RevisionID = 100
	c.cards[f.facts[3].Identity] = bad
	if _, err := s.ListPublishedWithIdentity(ctx, []int64{3, 1, 2}); err != nil {
		t.Fatal(err)
	}
	if f.fragmentCalls != 2 || len(f.loads[1]) != 2 {
		t.Fatal("部分命中未批量回源缺失项", f.loads)
	}
	fact := f.facts[1]
	fact.Item.Author = &articleDomain.AuthorSummary{ID: "author", Nickname: "新昵称"}
	f.facts[1] = fact
	delete(f.facts, 2)
	items, err = s.ListPublishedWithIdentity(ctx, []int64{2, 1})
	if err != nil || len(items) != 1 || items[0].Item.Author.Nickname != "新昵称" {
		t.Fatal("动态事实或下架错误", items, err)
	}
}
func TestSharedReadBatchesFailuresAndNoUncommittedFill(t *testing.T) {
	f := newFixture(205)
	c := newMemoryCache(f)
	s := NewService(f, f, c, nil, 100, nil)
	var ids []int64
	for i := int64(205); i > 0; i-- {
		ids = append(ids, i)
	}
	items, err := s.ListPublishedWithIdentity(context.Background(), ids)
	if err != nil || len(items) != 205 || f.factCalls != 3 || f.fragmentCalls != 3 || len(f.loads[2]) != 5 {
		t.Fatal("批次边界或 N+1", err, f.factCalls, f.fragmentCalls)
	}
	if _, err := s.ListPublishedWithIdentity(context.Background(), make([]int64, 501)); err == nil {
		t.Fatal("总量未限制")
	}
	f.fail = errors.New("数据库失败")
	if _, err := s.ListPublishedWithIdentity(context.Background(), ids); err != f.fail {
		t.Fatal("缓存吞掉数据库错误", err)
	}
	f.fail = nil
	c.err = errors.New("缓存失败")
	if _, err := s.ListPublishedWithIdentity(context.Background(), []int64{1}); err != nil {
		t.Fatal("缓存失败阻止回源", err)
	}
	c.err = nil
	f.allowed = false
	beforeGet, beforePut := c.getCalls, c.putCalls
	if _, err := s.ListPublishedWithIdentity(context.Background(), []int64{1}); err != nil {
		t.Fatal(err)
	}
	if c.getCalls != beforeGet || c.putCalls != beforePut {
		t.Fatal("写事务调用公开缓存")
	}
	f.allowed = true
	f.commitError = errors.New("快照提交失败")
	delete(c.cards, f.facts[1].Identity)
	if _, err := s.ListPublishedWithIdentity(context.Background(), []int64{1}); err != f.commitError {
		t.Fatal(err)
	}
	if c.putCalls != beforePut {
		t.Fatal("失败快照回填")
	}
}
func TestSharedReadRejectsMismatchedDatabaseFragment(t *testing.T) {
	f := newFixture(1)
	c := newMemoryCache(f)
	s := NewService(f, f, c, nil, 100, nil)
	fragment := f.fragments[f.facts[1].Identity]
	fragment.Identity.RevisionID = 2
	f.fragments[f.facts[1].Identity] = fragment
	if _, err := s.ListPublishedWithIdentity(context.Background(), []int64{1}); !errors.Is(err, ErrInconsistentFragment) {
		t.Fatal(err)
	}
	if c.putCalls != 0 {
		t.Fatal("混合身份被回填")
	}
}
