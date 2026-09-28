package articlesearch

import (
	"strings"
	"testing"
	"time"
)

func TestFrozenCursorRoundTripAndBoundaries(t *testing.T) {
	codec, _ := NewCursorCodec([]byte(strings.Repeat("k", 32)))
	now := time.Now().UTC()
	plan := HybridConfig{Enabled: true, Profile: "e-v1"}
	query := Query{Q: "私密查询", Limit: 1}
	state := FrozenPage{Mode: ModeHybrid, ExpiresAt: now.Add(HybridTTL), Offset: 1}
	identity := VectorIdentity{RevisionID: 9223372036854775807, GenerationID: "ffffffff-ffff-ffff-ffff-ffffffffffff", EmbeddingID: "ffffffff-ffff-ffff-ffff-ffffffffffff", Profile: plan.Profile}
	for i := int64(1); i <= 200; i++ {
		state.Candidates = append(state.Candidates, FrozenCandidate{ArticleID: i, Identity: identity, Semantic: true})
	}
	token, err := codec.EncodeFrozen(query, plan, state)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) >= 16*1024 || strings.Contains(token, query.Q) {
		t.Fatalf("载荷长度/隐私: %d", len(token))
	}
	decoded, err := codec.DecodeFrozen(query, plan, token, now)
	if err != nil || len(decoded.Candidates) != 200 || decoded.Offset != 1 || !decoded.Candidates[0].Identity.Matches(identity) {
		t.Fatalf("往返: %+v %v", decoded, err)
	}
	if !decoded.ExpiresAt.Equal(state.ExpiresAt) {
		t.Fatal("绝对 TTL 改变")
	}
	for _, bad := range []string{token[:len(token)-2] + "AA", "3." + token[2:], "2.bad", strings.Repeat("x", 16*1024+1)} {
		if _, err = codec.DecodeFrozen(query, plan, bad, now); err == nil {
			t.Fatal("接受非法游标")
		}
	}
	wrong, _ := NewCursorCodec([]byte(strings.Repeat("z", 32)))
	if _, err = wrong.DecodeFrozen(query, plan, token, now); err == nil {
		t.Fatal("接受错误密钥")
	}
	changed := plan
	changed.Profile = "e-v2"
	if _, err = codec.DecodeFrozen(query, changed, token, now); err == nil {
		t.Fatal("接受变更计划")
	}
	changed = plan
	changed.Enabled = false
	if _, err = codec.DecodeFrozen(query, changed, token, now); err == nil {
		t.Fatal("关闭计划仍有效")
	}
	if _, err = codec.DecodeFrozen(Query{Q: "其它查询"}, plan, token, now); err == nil {
		t.Fatal("接受错误查询")
	}
	if _, err = codec.DecodeFrozen(query, plan, token, state.ExpiresAt); err == nil {
		t.Fatal("接受过期游标")
	}
	for _, mutate := range []func(*FrozenPage){func(s *FrozenPage) { s.Offset = -1 }, func(s *FrozenPage) { s.Offset = 200 }, func(s *FrozenPage) { s.Candidates[1].ArticleID = s.Candidates[0].ArticleID }, func(s *FrozenPage) { s.Mode = 99 }} {
		copyState := state
		copyState.Candidates = append([]FrozenCandidate(nil), state.Candidates...)
		mutate(&copyState)
		if _, err = codec.EncodeFrozen(query, plan, copyState); err == nil {
			t.Fatal("接受非法快照")
		}
	}
}
