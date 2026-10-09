package redis

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strconv"
	"time"

	cacheApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlecache"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/config"
	client "github.com/redis/go-redis/v9"
	"github.com/redis/go-redis/v9/maintnotifications"
)

const (
	cardVersion   = 1
	latestVersion = 1
	planVersion   = 1
	maxCardBytes  = 64 * 1024
	maxPlanBytes  = 128 * 1024
)

type Cache struct {
	client   *client.Client
	cfg      config.CacheConfig
	observer cacheApp.Observer
	now      func() time.Time
}

var _ cacheApp.Cache = (*Cache)(nil)

// New 不发起网络请求，Redis 不可达不影响启动或 readiness。
func New(cfg config.CacheConfig, observer cacheApp.Observer, now func() time.Time) (*Cache, error) {
	if !cfg.Enabled {
		return nil, errors.New("Redis 适配器要求启用缓存配置")
	}
	if err := config.ValidateCache(&cfg, ""); err != nil {
		return nil, err
	}
	if observer == nil {
		observer = cacheApp.NopObserver{}
	}
	if now == nil {
		now = time.Now
	}
	options := &client.Options{Addr: cfg.Redis.Address, DB: cfg.Redis.Database, Username: string(cfg.Redis.Username), Password: string(cfg.Redis.Password), Protocol: 2,
		MaxRetries: -1, DialerRetries: -1, ContextTimeoutEnabled: true, DialTimeout: cfg.OperationTimeout, ReadTimeout: cfg.OperationTimeout, WriteTimeout: cfg.OperationTimeout, PoolTimeout: cfg.OperationTimeout,
		PoolSize: cfg.PoolSize, MaxActiveConns: cfg.PoolSize, DisableIdentity: true, MaintNotificationsConfig: &maintnotifications.Config{Mode: maintnotifications.ModeDisabled}}
	// 明确使用一次 DialContext，关闭 SDK 的命令及连接重试。
	options.Dialer = func(ctx context.Context, network, address string) (net.Conn, error) {
		d := net.Dialer{Timeout: cfg.OperationTimeout}
		if cfg.Redis.TLS {
			return (&tls.Dialer{NetDialer: &d, Config: &tls.Config{MinVersion: tls.VersionTLS12}}).DialContext(ctx, network, address)
		}
		return d.DialContext(ctx, network, address)
	}
	return &Cache{client: client.NewClient(options), cfg: cfg, observer: observer, now: now}, nil
}
func (c *Cache) Close() error { return c.client.Close() }
func (c *Cache) NewRequest(ctx context.Context) context.Context {
	if cacheApp.BudgetFrom(ctx) != nil {
		return ctx
	}
	return cacheApp.WithBudget(ctx, cacheApp.NewBudget(c.cfg.OperationTimeout, c.cfg.RequestBudget, c.now))
}

type envelope struct {
	Version   int             `json:"version"`
	Identity  string          `json:"identity"`
	CreatedAt time.Time       `json:"created_at"`
	ExpiresAt time.Time       `json:"expires_at"`
	Payload   json.RawMessage `json:"payload"`
	Checksum  string          `json:"checksum"`
}

func digest(v []byte) string     { d := sha256.Sum256(v); return hex.EncodeToString(d[:]) }
func checksum(e envelope) string { e.Checksum = ""; v, _ := json.Marshal(e); return digest(v) }
func encode(version int, identity string, value any, created time.Time, ttl time.Duration, now time.Time, maxBytes int) ([]byte, time.Time, bool) {
	expires := created.UTC().Add(ttl)
	if created.IsZero() || created.After(now) || !now.Before(expires) {
		return nil, time.Time{}, false
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > maxBytes {
		return nil, time.Time{}, false
	}
	e := envelope{Version: version, Identity: identity, CreatedAt: created.UTC(), ExpiresAt: expires, Payload: raw}
	e.Checksum = checksum(e)
	v, err := json.Marshal(e)
	return v, expires, err == nil && len(v) <= maxBytes
}
func strictDecode(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	if err := d.Decode(&struct{}{}); err != io.EOF {
		return errors.New("缓存包含多余数据")
	}
	return nil
}
func decode(raw []byte, version int, identity string, ttl time.Duration, now time.Time, maxBytes int, out any) bool {
	if len(raw) > maxBytes {
		return false
	}
	var e envelope
	if strictDecode(raw, &e) != nil || e.Version != version || e.Identity != identity || e.CreatedAt.IsZero() || e.CreatedAt.After(now) || !now.Before(e.ExpiresAt) || !e.ExpiresAt.Equal(e.CreatedAt.Add(ttl)) || e.Checksum != checksum(e) {
		return false
	}
	return strictDecode(e.Payload, out) == nil
}
func (c *Cache) cardKey(id cacheApp.CardIdentity) string {
	generation := id.GenerationID
	if generation == "" {
		generation = "none"
	}
	return c.cfg.Namespace + ":card:v" + strconv.Itoa(cardVersion) + ":" + strconv.FormatInt(id.ArticleID, 10) + ":" + strconv.FormatInt(id.RevisionID, 10) + ":" + generation
}
func latestIdentity(q cacheApp.LatestQuery) string {
	position := "first"
	if q.Position != nil {
		position = strconv.FormatInt(q.Position.SortAt.UTC().UnixMicro(), 10) + ":" + strconv.FormatInt(q.Position.ArticleID, 10)
	}
	return strconv.Itoa(q.Limit) + ":" + digest([]byte(position))
}
func (c *Cache) latestKey(q cacheApp.LatestQuery) string {
	return c.cfg.Namespace + ":latest:v" + strconv.Itoa(latestVersion) + ":" + latestIdentity(q)
}
func planIdentity(id cacheApp.PlanIdentity) string {
	return id.UserID + ":" + id.ProfileHash + ":" + id.ConfigHash
}
func (c *Cache) planKey(id cacheApp.PlanIdentity) string {
	return c.cfg.Namespace + ":recommend:v" + strconv.Itoa(planVersion) + ":" + planIdentity(id)
}

// 原子限制错误类型和单值大小后再 MGET，避免损坏超大值进入客户端内存。
// 每个返回项是字符串、nil（缺失）或 -1（损坏），保持输入位置。
const boundedMGet = `local out, keys, positions = {}, {}, {}
for i,k in ipairs(KEYS) do
  local n = redis.pcall('STRLEN', k)
  if type(n) == 'table' or n > tonumber(ARGV[1]) then out[i] = -1
  else out[i] = false; keys[#keys+1] = k; positions[#positions+1] = i end
end
if #keys > 0 then
  local values = redis.call('MGET', unpack(keys))
  for i,v in ipairs(values) do out[positions[i]] = v end
end
return out`

func classify(err error) cacheApp.Reason {
	if errors.Is(err, cacheApp.ErrBypass) {
		return cacheApp.Exhausted
	}
	if errors.Is(err, context.Canceled) {
		return cacheApp.Canceled
	}
	var n net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &n) && n.Timeout() {
		return cacheApp.Timeout
	}
	return cacheApp.Transport
}
func (c *Cache) call(ctx context.Context, object cacheApp.Object, op cacheApp.Operation, count int, fn func(context.Context) error) (bool, error) {
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	b := cacheApp.BudgetFrom(ctx)
	start := c.now()
	err := cacheApp.ErrBypass
	if b != nil {
		err = b.Execute(ctx, fn)
	}
	reason := cacheApp.None
	if err != nil {
		reason = classify(err)
	}
	c.observer.ObserveDuration(ctx, object, op, reason, max(time.Duration(0), c.now().Sub(start)))
	if err == nil {
		return true, nil
	}
	result := cacheApp.Bypass
	if op != cacheApp.Get {
		result = cacheApp.Failure
	}
	c.observer.Count(ctx, object, op, result, reason, count)
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	return false, nil
}
func (c *Cache) get(ctx context.Context, object cacheApp.Object, keys []string, maxBytes int) ([]any, error) {
	var values []any
	ok, err := c.call(ctx, object, cacheApp.Get, len(keys), func(op context.Context) error {
		v, e := c.client.Eval(op, boundedMGet, keys, maxBytes).Slice()
		values = v
		return e
	})
	if err != nil {
		return nil, err
	}
	if !ok {
		return make([]any, len(keys)), nil
	}
	if len(values) != len(keys) {
		c.observer.Count(ctx, object, cacheApp.Get, cacheApp.Corrupt, cacheApp.Invalid, len(keys))
		return make([]any, len(keys)), nil
	}
	return values, nil
}

type write struct {
	key     string
	value   []byte
	expires time.Time
}

func (c *Cache) set(ctx context.Context, object cacheApp.Object, writes []write) error {
	if len(writes) == 0 {
		return ctx.Err()
	}
	ok, err := c.call(ctx, object, cacheApp.Set, len(writes), func(op context.Context) error {
		_, e := c.client.Pipelined(op, func(p client.Pipeliner) error {
			for _, w := range writes {
				if c.now().Before(w.expires) {
					p.Do(op, "SET", w.key, w.value, "PXAT", w.expires.UnixMilli())
				}
			}
			return nil
		})
		return e
	})
	if ok {
		c.observer.Count(ctx, object, cacheApp.Set, cacheApp.Success, cacheApp.None, len(writes))
	}
	return err
}
func (c *Cache) countRead(ctx context.Context, object cacheApp.Object, hit, corrupt bool) {
	result := cacheApp.Miss
	if hit {
		result = cacheApp.Hit
	}
	c.observer.Count(ctx, object, cacheApp.Get, result, cacheApp.None, 1)
	if corrupt {
		c.observer.Count(ctx, object, cacheApp.Get, cacheApp.Corrupt, cacheApp.Invalid, 1)
	}
}

func (c *Cache) GetCards(ctx context.Context, ids []cacheApp.CardIdentity) (map[cacheApp.CardIdentity]cacheApp.CardFragment, error) {
	result := map[cacheApp.CardIdentity]cacheApp.CardFragment{}
	if len(ids) > 500 {
		return result, ctx.Err()
	}
	validIDs := make([]cacheApp.CardIdentity, 0, len(ids))
	for _, id := range ids {
		if validCardIdentity(id) {
			validIDs = append(validIDs, id)
		} else {
			c.countRead(ctx, cacheApp.Card, false, true)
		}
	}
	ids = validIDs
	for start := 0; start < len(ids); start += c.cfg.BatchSize {
		batch := ids[start:min(len(ids), start+c.cfg.BatchSize)]
		keys := make([]string, len(batch))
		for i, id := range batch {
			keys[i] = c.cardKey(id)
		}
		values, err := c.get(ctx, cacheApp.Card, keys, maxCardBytes)
		if err != nil {
			return result, err
		}
		for i, id := range batch {
			var fragment cacheApp.CardFragment
			raw, isString := values[i].(string)
			hit := validCardIdentity(id) && isString && decode([]byte(raw), cardVersion, keys[i], c.cfg.CardTTL, c.now(), maxCardBytes, &fragment) && fragment.Identity == id && validFragment(fragment)
			c.countRead(ctx, cacheApp.Card, hit, values[i] != nil && !hit)
			if hit {
				result[id] = fragment
			}
		}
	}
	return result, ctx.Err()
}
func (c *Cache) PutCards(ctx context.Context, items []cacheApp.CardWrite) error {
	if len(items) > 500 {
		return ctx.Err()
	}
	for start := 0; start < len(items); start += c.cfg.BatchSize {
		var writes []write
		for _, item := range items[start:min(len(items), start+c.cfg.BatchSize)] {
			if !validFragment(item.Fragment) {
				continue
			}
			key := c.cardKey(item.Fragment.Identity)
			if raw, expires, ok := encode(cardVersion, key, item.Fragment, item.CreatedAt, c.cfg.CardTTL, c.now(), maxCardBytes); ok {
				writes = append(writes, write{key, raw, expires})
			}
		}
		if err := c.set(ctx, cacheApp.Card, writes); err != nil {
			return err
		}
	}
	return ctx.Err()
}
func (c *Cache) GetLatest(ctx context.Context, q cacheApp.LatestQuery) (cacheApp.LatestPage, bool, error) {
	var page cacheApp.LatestPage
	if !validQuery(q) {
		return page, false, ctx.Err()
	}
	key := c.latestKey(q)
	values, err := c.get(ctx, cacheApp.Latest, []string{key}, maxCardBytes)
	if err != nil {
		return page, false, err
	}
	raw, isString := values[0].(string)
	hit := isString && decode([]byte(raw), latestVersion, key, c.cfg.LatestTTL, c.now(), maxCardBytes, &page) && validPage(q, page)
	c.countRead(ctx, cacheApp.Latest, hit, values[0] != nil && !hit)
	if !hit {
		page = cacheApp.LatestPage{}
	}
	return page, hit, nil
}
func (c *Cache) PutLatest(ctx context.Context, q cacheApp.LatestQuery, page cacheApp.LatestPage, created time.Time) error {
	if !validQuery(q) || !validPage(q, page) {
		return ctx.Err()
	}
	key := c.latestKey(q)
	if raw, expires, ok := encode(latestVersion, key, page, created, c.cfg.LatestTTL, c.now(), maxCardBytes); ok {
		return c.set(ctx, cacheApp.Latest, []write{{key, raw, expires}})
	}
	return ctx.Err()
}
func (c *Cache) InvalidateLatest(ctx context.Context) error {
	keys := make([]string, 50)
	for i := range keys {
		keys[i] = c.latestKey(cacheApp.LatestQuery{Limit: i + 1})
	}
	ok, err := c.call(ctx, cacheApp.Latest, cacheApp.Invalidate, 1, func(op context.Context) error {
		_, e := c.client.Pipelined(op, func(p client.Pipeliner) error {
			for start := 0; start < len(keys); start += c.cfg.BatchSize {
				p.Del(op, keys[start:min(len(keys), start+c.cfg.BatchSize)]...)
			}
			return nil
		})
		return e
	})
	if ok {
		c.observer.Count(ctx, cacheApp.Latest, cacheApp.Invalidate, cacheApp.Success, cacheApp.None, 1)
	}
	return err
}
func (c *Cache) GetPlan(ctx context.Context, id cacheApp.PlanIdentity) (cacheApp.RecommendationPlan, bool, error) {
	var plan cacheApp.RecommendationPlan
	if !validPlanIdentity(id) {
		return plan, false, ctx.Err()
	}
	key := c.planKey(id)
	values, err := c.get(ctx, cacheApp.Recommend, []string{key}, maxPlanBytes)
	if err != nil {
		return plan, false, err
	}
	raw, isString := values[0].(string)
	hit := isString && decode([]byte(raw), planVersion, key, c.cfg.RecommendTTL, c.now(), maxPlanBytes, &plan) && validPlan(plan)
	c.countRead(ctx, cacheApp.Recommend, hit, values[0] != nil && !hit)
	if !hit {
		plan = cacheApp.RecommendationPlan{}
	}
	return plan, hit, nil
}
func (c *Cache) PutPlan(ctx context.Context, id cacheApp.PlanIdentity, plan cacheApp.RecommendationPlan, created time.Time) error {
	if !validPlanIdentity(id) || !validPlan(plan) {
		return ctx.Err()
	}
	key := c.planKey(id)
	if raw, expires, ok := encode(planVersion, key, plan, created, c.cfg.RecommendTTL, c.now(), maxPlanBytes); ok {
		return c.set(ctx, cacheApp.Recommend, []write{{key, raw, expires}})
	}
	return ctx.Err()
}
