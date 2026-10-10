package recommendation

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
)

var ErrInvalidRequest = errors.New("推荐请求参数无效")
var ErrDependencyUnavailable = errors.New("推荐依赖不可用")

type Reader interface {
	ListPublishedWithIdentity(context.Context, []int64) ([]articlesearch.CurrentArticle, error)
	ListRecommendationLatest(context.Context, *articleDomain.Cursor, int, []int64, string, time.Time, time.Time) ([]articleDomain.ListItem, error)
}

type QueryEmbedder interface {
	EmbedQuery(context.Context, string) ([]float64, error)
}

type Config struct {
	FirstQueryTimeout     time.Duration
	BM25Candidates        int
	KNNCandidates         int
	CursorTTL             time.Duration
	EmbeddingTimeout      time.Duration
	KNNTimeout            time.Duration
	EmbeddingProfile      string
	Dimensions            int
	SemanticEnabled       bool
	Provider              string
	Model                 string
	EmbeddingInputVersion string
}

type Item struct {
	Article articleDomain.ListItem
	Reason  string
}

type Page struct {
	Items         []Item
	NextCursor    *string
	HasMore       bool
	Mode          string
	Degraded      bool
	DegradeReason string
}

type Service struct {
	profile      ProfileReader
	index        CandidateIndex
	reader       Reader
	codec        *CursorCodec
	embedder     QueryEmbedder
	config       Config
	now          func() time.Time
	observer     Observer
	cache        PlanCache
	planFallback func(context.Context)
}

type onceContextKey struct{}

// GetOnce 复用本人画像、排除和最终事实装配，不生成续页令牌。
func (s *Service) GetOnce(ctx context.Context, userID string, limit int) (Page, error) {
	return s.Get(context.WithValue(ctx, onceContextKey{}, true), userID, limit, "")
}

func NewService(profile ProfileReader, index CandidateIndex, reader Reader, codec *CursorCodec, embedder QueryEmbedder, config Config, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{profile: profile, index: index, reader: reader, codec: codec, embedder: embedder, config: config, now: now, observer: NopObserver{}}
}

func (s *Service) WithObserver(observer Observer) *Service {
	if observer != nil {
		s.observer = observer
	}
	return s
}

func (s *Service) Get(ctx context.Context, userID string, limit int, token string) (page Page, err error) {
	if s.cache != nil {
		ctx = s.cache.NewRequest(ctx)
	}
	if scope, ok := s.reader.(interface {
		NewRequest(context.Context) context.Context
	}); ok {
		ctx = scope.NewRequest(ctx)
	}
	started := time.Now()
	defer func() {
		result := "success"
		if err != nil {
			result = "error"
		}
		mode := page.Mode
		if mode == "" {
			mode = "unknown"
		}
		s.observer.ObserveRequest(ctx, mode, page.DegradeReason, result, time.Since(started))
	}()
	return s.get(ctx, userID, limit, token)
}

func (s *Service) get(ctx context.Context, userID string, limit int, token string) (Page, error) {
	if limit < 1 || limit > 50 || len(token) > maxRecommendCursorBytes {
		return Page{}, ErrInvalidRequest
	}
	if s.reader == nil || s.codec == nil {
		return Page{}, ErrDependencyUnavailable
	}
	if token != "" {
		state, err := s.codec.Decode(token, userID, s.now().UTC())
		if err != nil {
			return Page{}, err
		}
		return s.page(ctx, userID, limit, state)
	}
	budget := s.config.FirstQueryTimeout
	if budget <= 0 {
		budget = 5 * time.Second
	}
	firstCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	now := s.now().UTC()
	state := CursorState{Version: RankingVersion, Identity: IdentityBinding(userID), Mode: "cold_start", ExpiresAt: now.Add(s.config.CursorTTL), StartedAt: now, VectorProfile: s.config.EmbeddingProfile, LatestOn: true}
	if userID != "" {
		if s.profile == nil {
			return Page{}, ErrDependencyUnavailable
		}
		profile, err := s.profile.Profile(firstCtx, userID, nil)
		if err != nil {
			return Page{}, ErrDependencyUnavailable
		}
		terms := topTerms(profile)
		if terms.Valid() {
			state.Mode = "personalized"
			state.LatestOn = false
			if s.index == nil {
				state.Mode, state.Degraded, state.LatestOn, state.DegradeReason = "latest_fallback", true, true, "search_unavailable"
			} else {
				plan, err := s.cachedPlan(firstCtx, userID, terms, profile, now)
				if err != nil {
					if errors.Is(err, ErrDependencyUnavailable) {
						return Page{}, err
					}
					state.Mode, state.Degraded, state.LatestOn, state.DegradeReason = "latest_fallback", true, true, "search_unavailable"
				} else {
					state.Items, state.Degraded, state.DegradeReason = append([]FrozenItem{}, plan.Items...), plan.Degraded, plan.DegradeReason
					if plan.Degraded {
						state.DegradeReason = "semantic_unavailable"
					}
				}
			}
		}
	}
	return s.page(firstCtx, userID, limit, state)
}

func topTerms(profile Profile) Terms {
	return Terms{Keywords: topPositive(profile.Keywords), Topics: topPositive(profile.Topics)}
}

func topPositive(evidence map[string]Evidence) []string {
	values := make([]string, 0, len(evidence))
	for value, item := range evidence {
		if item.Weight > 0 && value != "" && len([]rune(value)) <= 64 {
			values = append(values, value)
		}
	}
	sort.Slice(values, func(i, j int) bool {
		if evidence[values[i]].Weight != evidence[values[j]].Weight {
			return evidence[values[i]].Weight > evidence[values[j]].Weight
		}
		return values[i] < values[j]
	})
	if len(values) > 3 {
		values = values[:3]
	}
	return values
}

func (s *Service) plan(ctx context.Context, userID string, terms Terms, profile Profile, now time.Time) (CachedPlan, error) {
	var bm, knn []articlesearch.Candidate
	degraded := false
	calledHybrid := false
	var err error
	if s.config.SemanticEnabled && s.embedder != nil && s.config.EmbeddingProfile != "" {
		text := strings.Join(append(append([]string{}, terms.Keywords...), terms.Topics...), " ")
		embedCtx, cancel := context.WithTimeout(ctx, s.config.EmbeddingTimeout)
		vector, embedErr := s.embedder.EmbedQuery(embedCtx, text)
		if embedErr == nil && embedCtx.Err() != nil {
			embedErr = embedCtx.Err()
		}
		cancel()
		if embedErr == nil && articlesearch.ValidQueryVector(vector, s.config.Dimensions) {
			if hybrid, ok := s.index.(HybridCandidateIndex); ok {
				calledHybrid = true
				var result HybridRecall
				result, err = hybrid.RecallHybrid(ctx, terms, s.config.BM25Candidates, s.config.KNNCandidates, time.Minute, vector, s.config.EmbeddingProfile, s.config.KNNTimeout)
				bm, knn = result.BM25, result.KNN
				degraded = result.SemanticReason != ""
			} else {
				degraded = true
			}
		} else {
			degraded = true
		}
	} else if s.config.SemanticEnabled {
		degraded = true
	}
	if !calledHybrid && err == nil {
		bm, err = s.index.Recall(ctx, terms, s.config.BM25Candidates, time.Minute)
	}
	if err != nil {
		return CachedPlan{}, err
	}
	s.observer.AddRecall(ctx, "bm25", len(bm))
	s.observer.AddRecall(ctx, "knn", len(knn))
	ids := make([]int64, 0, len(bm)+len(knn))
	seen := map[int64]bool{}
	for _, list := range [][]articlesearch.Candidate{bm, knn} {
		for _, candidate := range list {
			if !seen[candidate.ArticleID] {
				seen[candidate.ArticleID] = true
				ids = append(ids, candidate.ArticleID)
			}
		}
	}
	if len(ids) > maxFrozenCandidates {
		return CachedPlan{}, ErrDependencyUnavailable
	}
	currentItems, err := s.reader.ListPublishedWithIdentity(ctx, ids)
	if err != nil {
		return CachedPlan{}, ErrDependencyUnavailable
	}
	current := make(map[int64]articlesearch.CurrentArticle, len(currentItems))
	for _, item := range currentItems {
		current[item.Item.ID] = item
	}
	excluded, err := s.profile.Exclusions(ctx, userID, ids)
	if err != nil {
		return CachedPlan{}, ErrDependencyUnavailable
	}
	profile.Excluded = excluded
	ordered := RankV1(FuseCurrent(bm, knn, current, s.config.EmbeddingProfile), current, profile, now)
	s.observer.AddRecall(ctx, "union", len(ordered))
	s.observer.AddFiltered(ctx, len(ids)-len(ordered))
	reason := ""
	if degraded {
		reason = "semantic_unavailable"
	}
	return CachedPlan{Items: Freeze(ordered), OriginalIDs: ids, ExclusionHash: ExclusionFingerprint(ids, excluded), RankingVersion: RankingVersion, VectorProfile: s.config.EmbeddingProfile, Degraded: degraded, DegradeReason: reason}, nil
}

func (s *Service) page(ctx context.Context, userID string, limit int, state CursorState) (Page, error) {
	page := Page{Items: make([]Item, 0, limit), Mode: state.Mode, Degraded: state.Degraded, DegradeReason: state.DegradeReason}
	current := map[int64]articlesearch.CurrentArticle{}
	excluded := map[int64]string{}
	if state.Offset < len(state.Items) {
		ids := make([]int64, 0, len(state.Items)-state.Offset)
		for _, candidate := range state.Items[state.Offset:] {
			ids = append(ids, candidate.ID)
		}
		items, err := s.reader.ListPublishedWithIdentity(ctx, ids)
		if err != nil {
			return Page{}, ErrDependencyUnavailable
		}
		for _, item := range items {
			current[item.Item.ID] = item
		}
		if userID != "" {
			if s.profile == nil {
				return Page{}, ErrDependencyUnavailable
			}
			excluded, err = s.profile.Exclusions(ctx, userID, ids)
			if err != nil {
				return Page{}, ErrDependencyUnavailable
			}
		}
	}
	for state.Offset < len(state.Items) && len(page.Items) < limit {
		candidate := state.Items[state.Offset]
		state.Offset++
		item, ok := current[candidate.ID]
		if !ok || excluded[candidate.ID] != "" || candidate.Semantic && !candidate.Identity.Matches(item.Identity, state.VectorProfile) {
			s.observer.AddFiltered(ctx, 1)
			continue
		}
		value := item.Item
		value.RevisionID = item.Identity.RevisionID
		page.Items = append(page.Items, Item{Article: value, Reason: candidate.Reason})
	}
	for i := state.Offset; i < len(state.Items); i++ {
		candidate := state.Items[i]
		item, ok := current[candidate.ID]
		if ok && excluded[candidate.ID] == "" && (!candidate.Semantic || candidate.Identity.Matches(item.Identity, state.VectorProfile)) {
			page.HasMore = true
			break
		}
	}
	if !page.HasMore && !state.LatestEnd {
		skip := make([]int64, 0, len(state.Items))
		for _, candidate := range state.Items {
			skip = append(skip, candidate.ID)
		}
		need := limit - len(page.Items)
		latest, err := s.reader.ListRecommendationLatest(ctx, state.Latest, need+1, skip, userID, s.now().UTC(), state.StartedAt)
		if err != nil {
			return Page{}, ErrDependencyUnavailable
		}
		count := len(latest)
		if count > need {
			count = need
			page.HasMore = true
		} else {
			state.LatestEnd = true
		}
		if count > 0 {
			state.LatestOn = true
			s.observer.AddLatest(ctx, count)
			if state.Mode == "personalized" {
				page.Degraded = true
				state.Degraded = true
				if state.DegradeReason == "" {
					state.DegradeReason = "candidate_shortage"
					page.DegradeReason = state.DegradeReason
				}
			}
		}
		for _, item := range latest[:count] {
			reason := "latest_fallback"
			if state.Mode == "cold_start" {
				reason = "recent"
			}
			page.Items = append(page.Items, Item{Article: item, Reason: reason})
			state.Latest = &articleDomain.Cursor{SortAt: item.SortAt, ArticleID: item.ID}
		}
	}
	for _, item := range page.Items {
		origin := "user"
		if item.Article.Origin == articleDomain.OriginRSS {
			origin = "rss"
		}
		s.observer.AddSource(ctx, origin, 1)
	}
	if once, _ := ctx.Value(onceContextKey{}).(bool); page.HasMore && !once {
		token, err := s.codec.Encode(state)
		if err != nil {
			return Page{}, ErrInvalidCursor
		}
		page.NextCursor = &token
	}
	return page, nil
}
