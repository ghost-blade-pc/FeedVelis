package recommendation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"
)

// PlanIdentity 的 UserID 必须来自服务端认证；画像摘要不能替代用户身份。
type PlanIdentity struct{ UserID, ProfileHash, ConfigHash string }

// CachedPlan 只保存排序候选，不包含分页状态、画像或文章载荷。
type CachedPlan struct {
	Items          []FrozenItem
	OriginalIDs    []int64
	ExclusionHash  string
	RankingVersion int
	VectorProfile  string
	Degraded       bool
	DegradeReason  string
}
type PlanCache interface {
	NewRequest(context.Context) context.Context
	GetPlan(context.Context, PlanIdentity) (CachedPlan, bool, error)
	PutPlan(context.Context, PlanIdentity, CachedPlan, time.Time) error
}

// WithPlanCache 在首查入口复用读取缓存的预算，回源计数由装配层接到受控指标。
func (s *Service) WithPlanCache(cache PlanCache, fallback ...func(context.Context)) *Service {
	s.cache = cache
	if len(fallback) > 0 {
		s.planFallback = fallback[0]
	}
	return s
}
func hashValue(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func ProfileFingerprint(profile Profile) string {
	keywords, topics, sources := map[string]Evidence{}, map[string]Evidence{}, map[int64]Evidence{}
	for key, value := range profile.Keywords {
		keywords[key] = value
	}
	for key, value := range profile.Topics {
		topics[key] = value
	}
	for key, value := range profile.Sources {
		sources[key] = value
	}
	// JSON 对 map 键作确定性排序，nil 和空集合统一为空集合；排除集另外核对。
	return hashValue(struct {
		Keywords, Topics map[string]Evidence
		Sources          map[int64]Evidence
	}{keywords, topics, sources})
}
func (s *Service) ConfigFingerprint() string {
	_, hybrid := s.index.(HybridCandidateIndex)
	return hashValue(struct {
		Version           int
		Config            Config
		EffectiveSemantic bool
	}{RankingVersion, s.config, s.config.SemanticEnabled && s.embedder != nil && hybrid && s.config.EmbeddingProfile != ""})
}
func ExclusionFingerprint(ids []int64, excluded map[int64]string) string {
	type exclusion struct {
		ID     int64
		Reason string
	}
	values := []exclusion{}
	seen := map[int64]bool{}
	for _, id := range ids {
		if !seen[id] && excluded[id] != "" {
			values = append(values, exclusion{id, excluded[id]})
			seen[id] = true
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i].ID < values[j].ID })
	return hashValue(values)
}
func (s *Service) planCacheAllowed(ctx context.Context) bool {
	if s.cache == nil {
		return false
	}
	if reader, ok := s.reader.(interface{ CacheAllowed(context.Context) bool }); ok {
		return reader.CacheAllowed(ctx)
	}
	return true
}
func (s *Service) cachedPlan(ctx context.Context, userID string, terms Terms, profile Profile, started time.Time) (CachedPlan, error) {
	id := PlanIdentity{UserID: userID, ProfileHash: ProfileFingerprint(profile), ConfigHash: s.ConfigFingerprint()}
	allowed := s.planCacheAllowed(ctx)
	if allowed {
		cached, hit, err := s.cache.GetPlan(ctx, id)
		if ctx.Err() != nil {
			return CachedPlan{}, ErrDependencyUnavailable
		}
		if err == nil && hit && validCachedPlan(cached, s.config.EmbeddingProfile) {
			excluded, err := s.profile.Exclusions(ctx, userID, cached.OriginalIDs)
			if err != nil {
				return CachedPlan{}, ErrDependencyUnavailable
			}
			if ExclusionFingerprint(cached.OriginalIDs, excluded) == cached.ExclusionHash {
				return cached, nil
			}
		}
	}
	if s.planFallback != nil {
		s.planFallback(ctx)
	}
	generated, err := s.plan(ctx, userID, terms, profile, started)
	if err != nil {
		return CachedPlan{}, err
	}
	if allowed {
		_ = s.cache.PutPlan(ctx, id, generated, started)
	}
	if ctx.Err() != nil {
		return CachedPlan{}, ErrDependencyUnavailable
	}
	return generated, nil
}

// 适配器负责格式与摘要校验；用例仍拒绝不属于原集合的候选及不支持的计划。
func validCachedPlan(p CachedPlan, profile string) bool {
	if len(p.Items) > maxFrozenCandidates || len(p.OriginalIDs) > maxFrozenCandidates || p.RankingVersion != RankingVersion || p.VectorProfile != profile || len(p.ExclusionHash) != 64 || p.Degraded && p.DegradeReason != "semantic_unavailable" || !p.Degraded && p.DegradeReason != "" {
		return false
	}
	if _, err := hex.DecodeString(p.ExclusionHash); err != nil {
		return false
	}
	original := map[int64]bool{}
	for _, id := range p.OriginalIDs {
		if id <= 0 || original[id] {
			return false
		}
		original[id] = true
	}
	seen := map[int64]bool{}
	for _, item := range p.Items {
		if !original[item.ID] || seen[item.ID] || !validReason(item.Reason) || item.Reason == "latest_fallback" {
			return false
		}
		seen[item.ID] = true
		if item.Semantic && (profile == "" || item.Identity.RevisionID <= 0 || item.Identity.GenerationID == "" || item.Identity.EmbeddingID == "") || !item.Semantic && item.Identity != (FrozenIdentity{}) {
			return false
		}
	}
	return true
}
