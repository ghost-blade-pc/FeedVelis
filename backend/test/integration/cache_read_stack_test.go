package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlecache"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articleread"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/recommendation"
	redisAdapter "github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/cache/redis"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/config"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
	"github.com/ghost-blade-pc/Velis_Feed/backend/test/testkit/redistest"
)

// 只登记适配器本次读写的精确键用于清理；不扫描 Redis，也不删除其他运行的键。
type ownedReadCache struct {
	articlecache.Cache
	mu                        sync.Mutex
	h                         *redistest.Harness
	gets, puts, invalidations int
	invalidateErr             error
}

func (c *ownedReadCache) cardKey(id articlecache.CardIdentity) string {
	generation := id.GenerationID
	if generation == "" {
		generation = "none"
	}
	return c.h.Key(fmt.Sprintf("card:v1:%d:%d:%s", id.ArticleID, id.RevisionID, generation))
}
func (c *ownedReadCache) latestKey(q articlecache.LatestQuery) string {
	position := "first"
	if q.Position != nil {
		position = fmt.Sprintf("%d:%d", q.Position.SortAt.UTC().UnixMicro(), q.Position.ArticleID)
	}
	sum := sha256.Sum256([]byte(position))
	return c.h.Key(fmt.Sprintf("latest:v1:%d:%s", q.Limit, hex.EncodeToString(sum[:])))
}
func (c *ownedReadCache) GetCards(ctx context.Context, ids []articlecache.CardIdentity) (map[articlecache.CardIdentity]articlecache.CardFragment, error) {
	c.mu.Lock()
	c.gets++
	c.mu.Unlock()
	for _, id := range ids {
		c.cardKey(id)
	}
	return c.Cache.GetCards(ctx, ids)
}
func (c *ownedReadCache) PutCards(ctx context.Context, writes []articlecache.CardWrite) error {
	c.mu.Lock()
	c.puts++
	c.mu.Unlock()
	for _, w := range writes {
		c.cardKey(w.Fragment.Identity)
	}
	return c.Cache.PutCards(ctx, writes)
}
func (c *ownedReadCache) GetLatest(ctx context.Context, q articlecache.LatestQuery) (articlecache.LatestPage, bool, error) {
	c.latestKey(q)
	return c.Cache.GetLatest(ctx, q)
}
func (c *ownedReadCache) PutLatest(ctx context.Context, q articlecache.LatestQuery, p articlecache.LatestPage, created time.Time) error {
	c.latestKey(q)
	return c.Cache.PutLatest(ctx, q, p, created)
}
func (c *ownedReadCache) InvalidateLatest(ctx context.Context) error {
	c.mu.Lock()
	c.invalidations++
	c.mu.Unlock()
	if c.invalidateErr != nil {
		return c.invalidateErr
	}
	for i := 1; i <= 50; i++ {
		c.latestKey(articlecache.LatestQuery{Limit: i})
	}
	return c.Cache.InvalidateLatest(ctx)
}
func (c *ownedReadCache) planKey(id articlecache.PlanIdentity) string {
	return c.h.Key(fmt.Sprintf("recommend:v1:%s:%s:%s", id.UserID, id.ProfileHash, id.ConfigHash))
}
func (c *ownedReadCache) GetPlan(ctx context.Context, id articlecache.PlanIdentity) (articlecache.RecommendationPlan, bool, error) {
	c.planKey(id)
	return c.Cache.GetPlan(ctx, id)
}
func (c *ownedReadCache) PutPlan(ctx context.Context, id articlecache.PlanIdentity, p articlecache.RecommendationPlan, created time.Time) error {
	c.planKey(id)
	return c.Cache.PutPlan(ctx, id, p, created)
}

func newOwnedReadCache(t *testing.T, address string, observers ...articlecache.Observer) (*ownedReadCache, *redistest.Harness) {
	t.Helper()
	h := redistest.New(t)
	cfg := config.Default().Cache
	cfg.Enabled = true
	cfg.Namespace = h.Namespace
	cfg.Redis.Address = h.Address
	if address != "" {
		cfg.Redis.Address = address
	}
	cfg.Redis.Username = config.SecretBytes(os.Getenv("VELIS_TEST_REDIS_USERNAME"))
	cfg.Redis.Password = config.SecretBytes(os.Getenv("VELIS_TEST_REDIS_PASSWORD"))
	if err := config.ValidateCache(&cfg, "test"); err != nil {
		t.Fatal(err)
	}
	var observer articlecache.Observer
	if len(observers) > 0 {
		observer = observers[0]
	}
	adapter, err := redisAdapter.New(cfg, observer, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { adapter.Close() })
	return &ownedReadCache{Cache: adapter, h: h}, h
}
func sharedIntegrationReader(t *testing.T, env *testEnv, faults ...*func(bool)) *articleread.Service {
	t.Helper()
	var cache articlecache.Cache = articlecache.Disabled{}
	if os.Getenv("VELIS_TEST_REDIS_ADDRESS") != "" {
		address := ""
		if len(faults) > 0 {
			h := redistest.New(t)
			proxy := redistest.NewProxy(t, h.Address)
			address = proxy.Address()
			*faults[0] = proxy.Disconnect
		}
		cache, _ = newOwnedReadCache(t, address)
		t.Log("已接入真实 Redis 共享卡片读取")
	}
	if len(faults) > 0 && *faults[0] == nil {
		*faults[0] = func(bool) {}
	}
	return articleread.NewService(postgres.NewArticleRepository(env.pool), postgres.NewTxManager(env.pool), cache, nil, 100, nil)
}

var _ articlesearch.PublicArticleReader = (*articleread.Service)(nil)
var _ recommendation.Reader = (*articleread.Service)(nil)
