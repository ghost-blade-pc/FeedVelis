package recommendation

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
)

type planEntry struct {
	plan    CachedPlan
	created time.Time
}
type memoryPlans struct {
	entries    map[PlanIdentity]planEntry
	now        func() time.Time
	gets, puts int
	err        error
}

func (c *memoryPlans) NewRequest(ctx context.Context) context.Context { return ctx }
func (c *memoryPlans) GetPlan(_ context.Context, id PlanIdentity) (CachedPlan, bool, error) {
	c.gets++
	entry, ok := c.entries[id]
	return entry.plan, ok && c.now().Before(entry.created.Add(30*time.Second)), c.err
}
func (c *memoryPlans) PutPlan(_ context.Context, id PlanIdentity, plan CachedPlan, created time.Time) error {
	c.puts++
	if c.err == nil && c.now().Before(created.Add(30*time.Second)) {
		c.entries[id] = planEntry{plan, created}
	}
	return c.err
}
func planFixture(t *testing.T) (*Service, *recommendProfileFake, *recommendIndexFake, *recommendReaderFake, *memoryPlans) {
	s, p, i, r := recommendFixture(t)
	c := &memoryPlans{entries: map[PlanIdentity]planEntry{}, now: s.now}
	s.WithPlanCache(c)
	return s, p, i, r, c
}
func TestProfileAndConfigurationFingerprint(t *testing.T) {
	a := Profile{Keywords: map[string]Evidence{"z": {Weight: 3, PositiveArticles: 1}, "a": {Weight: -2, NegativeWeight: 2}}, Topics: map[string]Evidence{"t": {Weight: 4}}, Sources: map[int64]Evidence{9: {Weight: 1}, 2: {Weight: 2}}}
	b := Profile{Keywords: map[string]Evidence{}, Topics: map[string]Evidence{}, Sources: map[int64]Evidence{}}
	for _, key := range []string{"a", "z"} {
		b.Keywords[key] = a.Keywords[key]
	}
	b.Topics["t"] = a.Topics["t"]
	b.Sources[2] = a.Sources[2]
	b.Sources[9] = a.Sources[9]
	if ProfileFingerprint(a) != ProfileFingerprint(b) || ProfileFingerprint(Profile{}) != ProfileFingerprint(Profile{Keywords: map[string]Evidence{}}) {
		t.Fatal("证据顺序或空集合影响指纹")
	}
	original := ProfileFingerprint(a)
	for _, e := range []Evidence{{Weight: 4, PositiveArticles: 1}, {Weight: 3, PositiveArticles: 2}, {Weight: 3, PositiveArticles: 1, NegativeArticles: 1}, {Weight: 3, PositiveArticles: 1, PositiveWeight: 4}, {Weight: 3, PositiveArticles: 1, NegativeWeight: 2}} {
		b.Keywords["z"] = e
		if ProfileFingerprint(b) == original {
			t.Fatal("排序证据未参与指纹", e)
		}
	}
	b.Keywords["z"] = a.Keywords["z"]
	b.Excluded = map[int64]string{1: NotInterestedArticle}
	if ProfileFingerprint(b) != original {
		t.Fatal("排除集应独立核对")
	}
	s, _, _, _ := recommendFixture(t)
	baseline := s.ConfigFingerprint()
	base := s.config
	variants := []Config{base, base, base, base, base, base, base, base, base, base}
	variants[0].BM25Candidates++
	variants[1].KNNCandidates++
	variants[2].FirstQueryTimeout++
	variants[3].EmbeddingTimeout++
	variants[4].KNNTimeout++
	variants[5].SemanticEnabled = !base.SemanticEnabled
	variants[6].EmbeddingProfile = "other"
	variants[7].Provider = "p"
	variants[8].Model = "m"
	variants[9].EmbeddingInputVersion = "v2"
	for _, cfg := range variants {
		s.config = cfg
		if s.ConfigFingerprint() == baseline {
			t.Fatal("配置变化未参与指纹", cfg)
		}
	}
	s.config = base
	s.config.SemanticEnabled = true
	s.config.EmbeddingProfile = "p"
	s.index = &recommendHybridFake{}
	without := s.ConfigFingerprint()
	s.embedder = &recommendEmbedFake{}
	if s.ConfigFingerprint() == without {
		t.Fatal("有效语义开关未参与指纹")
	}
}
func TestPlanIdentityIsolationAndFreshIndependentPages(t *testing.T) {
	s, _, i, _, c := planFixture(t)
	ctx := context.Background()
	first, err := s.Get(ctx, "user-a", 1, "")
	if err != nil || first.NextCursor == nil {
		t.Fatal(first, err)
	}
	oldState, err := s.codec.Decode(*first.NextCursor, "user-a", s.now())
	if err != nil {
		t.Fatal(err)
	}
	oldNow := s.now()
	s.now = func() time.Time { return oldNow.Add(time.Second) }
	c.now = s.now
	second, err := s.Get(ctx, "user-a", 1, "")
	if err != nil || second.NextCursor == nil || i.calls != 1 {
		t.Fatal("相同首查未复用计划", second, err, i.calls)
	}
	newState, err := s.codec.Decode(*second.NextCursor, "user-a", s.now())
	if err != nil || !newState.StartedAt.Equal(s.now()) || newState.Offset != oldState.Offset || !newState.ExpiresAt.After(oldState.ExpiresAt) {
		t.Fatal("首查复用消费状态或时间", newState, err)
	}
	if _, err := s.Get(ctx, "user-b", 1, ""); err != nil || i.calls != 2 || len(c.entries) != 2 {
		t.Fatal("相同画像跨用户命中", err, i.calls)
	}
	clear(c.entries)
	beforeGets := c.gets
	if _, err := s.Get(ctx, "user-a", 1, *first.NextCursor); err != nil || i.calls != 2 || c.gets != beforeGets {
		t.Fatal("清空后续页依赖计划或重新召回", err, i.calls)
	}
}
func TestPlanExclusionsWithdrawalExpiryAndProfileChange(t *testing.T) {
	s, p, i, r, c := planFixture(t)
	ctx := context.Background()
	p.excluded[3] = NotInterestedArticle
	r.excluded[3] = true
	if _, err := s.Get(ctx, "u", 1, ""); err != nil {
		t.Fatal(err)
	}
	hash := ProfileFingerprint(p.profile)
	for _, entry := range c.entries {
		for _, item := range entry.plan.Items {
			if item.ID == 3 {
				t.Fatal("硬排除未先于排序")
			}
		}
		if !reflect.DeepEqual(entry.plan.OriginalIDs, []int64{3, 1}) {
			t.Fatal("丢失硬排除前原集合", entry.plan)
		}
	}
	delete(p.excluded, 3)
	delete(r.excluded, 3)
	if _, err := s.Get(ctx, "u", 1, ""); err != nil || i.calls != 2 || ProfileFingerprint(p.profile) != hash {
		t.Fatal("无标签排除撤销未重算", err, i.calls)
	}
	p.excluded[3] = NotInterestedArticle
	r.excluded[3] = true
	s.Get(ctx, "u", 1, "")
	// Profile 端口只返回当前有效排除；自然过期与撤销都必须被原集合签名发现。
	delete(p.excluded, 3)
	delete(r.excluded, 3)
	s.Get(ctx, "u", 1, "")
	if i.calls != 4 {
		t.Fatal("排除自然过期未重算", i.calls)
	}
	p.profile.Keywords["go"] = Evidence{Weight: 4}
	s.Get(ctx, "u", 1, "")
	if i.calls != 5 {
		t.Fatal("新画像沿用旧计划")
	}
	old := s.now()
	s.now = func() time.Time { return old.Add(31 * time.Second) }
	c.now = s.now
	s.Get(ctx, "u", 1, "")
	if i.calls != 6 {
		t.Fatal("计划自然过期未回源")
	}
}
func TestPlanRejectsInvalidMembersAndPreservesFailureSemantics(t *testing.T) {
	variants := []func(*CachedPlan){func(p *CachedPlan) { p.Items[0].ID = 999 }, func(p *CachedPlan) { p.Items = append(p.Items, p.Items[0]) }, func(p *CachedPlan) { p.OriginalIDs = make([]int64, 201) }, func(p *CachedPlan) { p.DegradeReason = "search_unavailable"; p.Degraded = true }, func(p *CachedPlan) { p.VectorProfile = "other" }, func(p *CachedPlan) { p.Items[0].Reason = "latest_fallback" }}
	for _, mutate := range variants {
		s, _, i, _, c := planFixture(t)
		s.Get(context.Background(), "u", 1, "")
		for id, entry := range c.entries {
			entry.plan.Items = append([]FrozenItem{}, entry.plan.Items...)
			mutate(&entry.plan)
			c.entries[id] = entry
		}
		if _, err := s.Get(context.Background(), "u", 1, ""); err != nil || i.calls != 2 {
			t.Fatal("非法计划没有回源", err)
		}
	}
	s, p, i, r, c := planFixture(t)
	ctx := context.Background()
	c.err = errors.New("Redis 故障")
	page, err := s.Get(ctx, "u", 1, "")
	if err != nil || page.Mode != "personalized" || page.Degraded {
		t.Fatal("Redis 故障改变推荐降级", page, err)
	}
	c.err = nil
	clear(c.entries)
	i.err = errors.New("搜索故障")
	page, err = s.Get(ctx, "u", 1, "")
	if err != nil || page.Mode != "latest_fallback" || c.puts != 1 || len(c.entries) != 0 {
		t.Fatal("缓存了故障降级", page, err)
	}
	i.err = nil
	page, err = s.Get(ctx, "u", 1, "")
	if err != nil || page.Mode != "personalized" || len(c.entries) != 1 {
		t.Fatal("搜索恢复未重新召回", page, err)
	}
	p.err = errors.New("画像故障")
	if _, err := s.Get(ctx, "u", 1, ""); !errors.Is(err, ErrDependencyUnavailable) {
		t.Fatal("命中掩盖画像错误", err)
	}
	p.err = nil
	r.err = errors.New("事实故障")
	if _, err := s.Get(ctx, "u", 1, ""); !errors.Is(err, ErrDependencyUnavailable) {
		t.Fatal("命中掩盖当前事实错误", err)
	}
}
func TestPlanHitCurrentVisibilityAndSemanticIdentity(t *testing.T) {
	s, _, _, r, c := planFixture(t)
	ctx := context.Background()
	s.Get(ctx, "u", 1, "")
	id := PlanIdentity{}
	entry := planEntry{}
	for k, v := range c.entries {
		id = k
		entry = v
	}
	entry.plan.VectorProfile = "e-v1"
	entry.plan.Items[0].Semantic = true
	entry.plan.Items[0].Identity = FrozenIdentity{RevisionID: 1, GenerationID: "g", EmbeddingID: "e"}
	s.config.EmbeddingProfile = "e-v1"
	id.ConfigHash = s.ConfigFingerprint()
	c.entries[id] = entry
	// 计划语义名次冻结，当前身份变化必须过滤；不能转为旧 BM25 名次。
	current := r.items[entry.plan.Items[0].ID]
	current.Identity = articlesearch.VectorIdentity{RevisionID: 2, GenerationID: "new", EmbeddingID: "new", Profile: "e-v1"}
	r.items[current.Item.ID] = current
	page, err := s.Get(ctx, "u", 1, "")
	if err != nil || page.Items[0].Article.ID == entry.plan.Items[0].ID {
		t.Fatal("陈旧语义计划未过滤", page, err)
	}
	delete(r.items, entry.plan.Items[1].ID)
	page, err = s.Get(ctx, "u", 1, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Items {
		if item.Article.ID == entry.plan.Items[1].ID {
			t.Fatal("下架计划仍返回")
		}
	}
}
func TestPlanColdStartAndParentCancellation(t *testing.T) {
	s, p, i, _, c := planFixture(t)
	if _, err := s.Get(context.Background(), "", 1, ""); err != nil || c.gets != 0 || c.puts != 0 || i.calls != 0 {
		t.Fatal("匿名路径访问计划", err)
	}
	p.profile = Profile{}
	if _, err := s.Get(context.Background(), "u", 1, ""); err != nil || c.gets != 0 || i.calls != 0 {
		t.Fatal("无画像路径访问计划", err)
	}
	p.profile = Profile{Keywords: map[string]Evidence{"go": {Weight: 3}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Get(ctx, "u", 1, ""); !errors.Is(err, ErrDependencyUnavailable) {
		t.Fatal("父取消被缓存吞掉", err)
	}
}

type guardedPlanReader struct{ *recommendReaderFake }

func (guardedPlanReader) CacheAllowed(context.Context) bool { return false }
func TestPlanWriteTransactionBypassAndMaximumSize(t *testing.T) {
	s, _, i, r, c := planFixture(t)
	s.reader = guardedPlanReader{r}
	for n := 0; n < 2; n++ {
		if _, err := s.Get(context.Background(), "u", 1, ""); err != nil {
			t.Fatal(err)
		}
	}
	if c.gets != 0 || c.puts != 0 || i.calls != 2 {
		t.Fatal("已有写事务访问公开计划缓存", c.gets, c.puts, i.calls)
	}
	p := CachedPlan{RankingVersion: RankingVersion, ExclusionHash: strings.Repeat("a", 64)}
	for id := int64(1); id <= 200; id++ {
		p.OriginalIDs = append(p.OriginalIDs, id)
		p.Items = append(p.Items, FrozenItem{ID: id, Reason: "recent"})
	}
	if !validCachedPlan(p, "") {
		t.Fatal("拒绝合法200上界")
	}
	p.Items = append(p.Items, FrozenItem{ID: 201, Reason: "recent"})
	p.OriginalIDs = append(p.OriginalIDs, 201)
	if validCachedPlan(p, "") {
		t.Fatal("接受超上界计划")
	}
}
func TestCachedPlanKeepsActualSemanticDegradation(t *testing.T) {
	s, _, _, _, c := planFixture(t)
	s.config.SemanticEnabled = true
	s.config.EmbeddingProfile = "e-v1"
	first, err := s.Get(context.Background(), "u", 1, "")
	if err != nil || first.DegradeReason != "semantic_unavailable" {
		t.Fatal(first, err)
	}
	again, err := s.Get(context.Background(), "u", 1, "")
	if err != nil || !again.Degraded || again.DegradeReason != first.DegradeReason || c.puts != 1 {
		t.Fatal("计划丢失真实语义降级", again, err, c.puts)
	}
}
