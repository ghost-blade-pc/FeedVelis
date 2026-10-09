package redis

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	cacheApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlecache"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/recommendation"
)

func TestEnvelopeVersionsIdentityAndLifetime(t *testing.T) {
	now := time.Now()
	f := testFragment(1)
	raw, expires, ok := encode(cardVersion, "identity", f, now, time.Minute, now, maxCardBytes)
	if !ok || !expires.Equal(now.Add(time.Minute)) {
		t.Fatal("编码失败")
	}
	var decoded cacheApp.CardFragment
	if !decode(raw, cardVersion, "identity", time.Minute, now, maxCardBytes, &decoded) || decoded.Identity != f.Identity {
		t.Fatal("解码失败")
	}
	for _, tc := range []struct {
		version  int
		identity string
		now      time.Time
		limit    int
	}{{2, "identity", now, maxCardBytes}, {1, "other", now, maxCardBytes}, {1, "identity", expires, maxCardBytes}, {1, "identity", now, 1}} {
		if decode(raw, tc.version, tc.identity, time.Minute, tc.now, tc.limit, &decoded) {
			t.Fatal("接受无效载荷")
		}
	}
	for _, created := range []time.Time{time.Time{}, now.Add(time.Second), now.Add(-time.Minute)} {
		if _, _, ok := encode(cardVersion, "identity", f, created, time.Minute, now, maxCardBytes); ok {
			t.Fatal("接受过期或未来回填")
		}
	}
	var env envelope
	_ = json.Unmarshal(raw, &env)
	env.Payload = []byte(`{"Title":"tampered"}`)
	bad, _ := json.Marshal(env)
	if decode(bad, 1, "identity", time.Minute, now, maxCardBytes, &decoded) {
		t.Fatal("摘要未保护载荷")
	}
}
func TestPayloadValidationBoundaries(t *testing.T) {
	f := testFragment(1)
	if !validFragment(f) {
		t.Fatal("合法卡片被拒绝")
	}
	f.Title = strings.Repeat("文", 501)
	if validFragment(f) {
		t.Fatal("标题超限")
	}
	f = testFragment(1)
	f.Identity.GenerationID = "not-uuid"
	if validFragment(f) {
		t.Fatal("非法身份")
	}
	q := cacheApp.LatestQuery{Limit: 50}
	p := cacheApp.LatestPage{Exhausted: true}
	now := time.Now()
	for i := int64(51); i > 0; i-- {
		p.Candidates = append(p.Candidates, cacheApp.Candidate{ArticleID: i, SortAt: now})
	}
	if !validPage(q, p) {
		t.Fatal("51 项边界被拒绝")
	}
	p.Candidates = append(p.Candidates, cacheApp.Candidate{ArticleID: 52, SortAt: now})
	if validPage(q, p) {
		t.Fatal("超限页")
	}
	p.Candidates = p.Candidates[:51]
	p.Candidates[1] = p.Candidates[0]
	if validPage(q, p) {
		t.Fatal("重复成员")
	}
	plan := cacheApp.RecommendationPlan{ExclusionHash: strings.Repeat("a", 64), RankingVersion: recommendation.RankingVersion}
	for i := int64(1); i <= 200; i++ {
		plan.OriginalIDs = append(plan.OriginalIDs, i)
		plan.Items = append(plan.Items, recommendation.FrozenItem{ID: i, Reason: "keyword_match"})
	}
	if !validPlan(plan) {
		t.Fatal("200 项边界被拒绝")
	}
	plan.Items[0].Reason = "recent"
	if !validPlan(plan) {
		t.Fatal("当前增强为空时的 recent 计划被拒绝")
	}
	plan.OriginalIDs = append(plan.OriginalIDs, 201)
	if validPlan(plan) {
		t.Fatal("超限原候选")
	}
	plan.OriginalIDs = plan.OriginalIDs[:200]
	plan.Items[0].ID = 201
	if validPlan(plan) {
		t.Fatal("排序项不属于原候选")
	}
}
