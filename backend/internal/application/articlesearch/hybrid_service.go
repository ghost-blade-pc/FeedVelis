package articlesearch

import (
	"context"
	"errors"
	"time"
)

// WithHybrid 由 bootstrap 注入独立在线模型和当前身份批量读取端口。
func (s *Service) WithHybrid(embedder QueryEmbedder, reader CurrentArticleReader) *Service {
	s.embedder = embedder
	s.currentReader = reader
	return s
}

type HybridObserver interface {
	ObserveMode(context.Context, RetrievalMode, string)
	AddRecall(context.Context, string, int)
	AddStale(context.Context, int)
	ObserveDependencyFailure(context.Context, string, string)
}

func (s *Service) mode(ctx context.Context, mode RetrievalMode, reason string) {
	if o, ok := s.observer.(HybridObserver); ok {
		o.ObserveMode(ctx, mode, reason)
	}
}
func (s *Service) recall(ctx context.Context, path string, n int) {
	if o, ok := s.observer.(HybridObserver); ok {
		o.AddRecall(ctx, path, n)
	}
}
func (s *Service) stale(ctx context.Context, n int) {
	if o, ok := s.observer.(HybridObserver); ok {
		o.AddStale(ctx, n)
	}
}

type semanticResult struct {
	batch  CandidateBatch
	reason string
}

func (s *Service) semanticRecall(ctx context.Context, query Query, pit string) semanticResult {
	h := s.config.Hybrid
	if s.embedder == nil {
		return semanticResult{reason: "embedding_unconfigured"}
	}
	index, ok := s.index.(SemanticIndex)
	if !ok {
		return semanticResult{reason: "schema_incompatible"}
	}
	supported, err := index.SupportsSemantic(ctx, h.Dimensions)
	if err != nil || !supported {
		return semanticResult{reason: "schema_incompatible"}
	}
	embeddingCtx, cancel := context.WithTimeout(ctx, h.EmbeddingTimeout)
	started := s.now()
	vector, err := s.embedder.EmbedQuery(embeddingCtx, query.Q)
	embeddingErr := embeddingCtx.Err()
	if err == nil && embeddingErr != nil {
		err = embeddingErr
	}
	cancel()
	s.observer.ObserveStage(ctx, StageEmbedding, s.now().Sub(started))
	if err != nil {
		class := "internal"
		var failure *QueryEmbeddingFailure
		if errors.As(err, &failure) {
			class = failure.Class
		}
		if errors.Is(err, context.DeadlineExceeded) {
			class = "timeout"
		}
		if errors.Is(err, context.Canceled) {
			class = "canceled"
		}
		if o, ok := s.observer.(HybridObserver); ok {
			o.ObserveDependencyFailure(ctx, "embedding", class)
		}
		reason := "embedding_failed"
		if class == "invalid_output" {
			reason = "invalid_vector"
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(embeddingErr, context.DeadlineExceeded) {
			reason = "embedding_timeout"
		}
		return semanticResult{reason: reason}
	}
	if !ValidQueryVector(vector, h.Dimensions) {
		return semanticResult{reason: "invalid_vector"}
	}
	knnCtx, cancel := context.WithTimeout(ctx, h.KNNTimeout)
	defer cancel()
	started = s.now()
	batch, err := index.SearchKNN(knnCtx, KNNRequest{IndexRequest: IndexRequest{Query: query, PITID: pit, Size: h.KNNCandidates, KeepAlive: s.config.PITKeepAlive}, Vector: vector, Profile: h.Profile})
	s.observer.ObserveStage(ctx, StageKNN, s.now().Sub(started))
	if err == nil && knnCtx.Err() != nil {
		err = knnCtx.Err()
	}
	if err != nil {
		reason := "knn_failed"
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(knnCtx.Err(), context.DeadlineExceeded) {
			reason = "knn_timeout"
		}
		return semanticResult{reason: reason}
	}
	if !validBatch(batch, h.KNNCandidates, true) {
		return semanticResult{reason: "knn_failed"}
	}
	return semanticResult{batch: batch}
}

func validBatch(batch CandidateBatch, max int, semantic bool) bool {
	if batch.PITID == "" || len(batch.Candidates) > max {
		return false
	}
	seen := map[int64]bool{}
	for _, c := range batch.Candidates {
		if c.ArticleID <= 0 || seen[c.ArticleID] || !c.Position.Valid() || c.Position.ArticleID != c.ArticleID {
			return false
		}
		seen[c.ArticleID] = true
		if semantic && !c.Identity.Matches(c.Identity) {
			return false
		}
	}
	return true
}

func (s *Service) searchHybrid(ctx context.Context, query Query) (Page, error) {
	if s.currentReader == nil {
		return Page{}, controlled(CodeDependencyUnavailable, errors.New("当前身份读取未配置"))
	}
	if query.Cursor != "" {
		state, err := s.codec.DecodeFrozen(query, s.config.Hybrid, query.Cursor, s.now())
		if err != nil {
			return Page{}, err
		}
		page, err := s.frozenPage(ctx, query, state)
		if err == nil {
			s.mode(ctx, state.Mode, "")
		}
		return page, err
	}
	started := s.now()
	pit, err := s.index.CreatePIT(ctx, s.config.PITKeepAlive)
	s.observer.ObserveStage(ctx, StageOpenSearch, s.now().Sub(started))
	if err != nil || pit == "" {
		return Page{}, controlled(CodeSearchUnavailable, errOr(err, ErrInvalidIndexResponse))
	}
	s.observer.ObservePIT(ctx, PITCreate)
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		s.closePIT(cleanup, pit)
	}()
	recallCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	semanticCh := make(chan semanticResult, 1)
	go func() { semanticCh <- s.semanticRecall(recallCtx, query, pit) }()
	started = s.now()
	bm25, err := s.index.Search(recallCtx, IndexRequest{Query: query, PITID: pit, Size: s.config.Hybrid.BM25Candidates, KeepAlive: s.config.PITKeepAlive})
	s.observer.ObserveStage(ctx, StageOpenSearch, s.now().Sub(started))
	if err != nil || !validBatch(bm25, s.config.Hybrid.BM25Candidates, false) {
		cancel()
		return Page{}, controlled(CodeSearchUnavailable, errOr(err, ErrInvalidIndexResponse))
	}
	var semantic semanticResult
	select {
	case semantic = <-semanticCh:
	case <-ctx.Done():
		return Page{}, controlled(CodeSearchUnavailable, ctx.Err())
	}
	if err = ctx.Err(); err != nil {
		return Page{}, controlled(CodeSearchUnavailable, err)
	}
	s.recall(ctx, "bm25", len(bm25.Candidates))
	s.recall(ctx, "knn", len(semantic.batch.Candidates))
	ids := []int64{}
	seen := map[int64]bool{}
	for _, list := range [][]Candidate{bm25.Candidates, semantic.batch.Candidates} {
		for _, c := range list {
			if !seen[c.ArticleID] {
				ids = append(ids, c.ArticleID)
				seen[c.ArticleID] = true
			}
		}
	}
	current, err := s.readCurrent(ctx, ids)
	if err != nil {
		return Page{}, err
	}
	validKNN := []Candidate{}
	for _, c := range semantic.batch.Candidates {
		item, ok := current[c.ArticleID]
		if ok && c.Identity.Profile == s.config.Hybrid.Profile && c.Identity.Matches(item.Identity) {
			validKNN = append(validKNN, c)
		} else {
			s.stale(ctx, 1)
		}
	}
	// 不可见项先从两路移除，再重新编号。
	validBM := []Candidate{}
	for _, c := range bm25.Candidates {
		if _, ok := current[c.ArticleID]; ok {
			validBM = append(validBM, c)
		} else {
			s.observer.AddFiltered(ctx, 1)
		}
	}
	mode := ModeHybrid
	var candidates []FrozenCandidate
	if len(validKNN) == 0 {
		mode = ModeBM25
		if semantic.reason == "" {
			semantic.reason = "no_valid_vector"
		}
		for _, c := range validBM {
			candidates = append(candidates, FrozenCandidate{ArticleID: c.ArticleID})
		}
	} else {
		started = s.now()
		for _, c := range FuseRRF(validBM, validKNN) {
			identity := VectorIdentity{}
			if c.Semantic {
				identity = c.Identity
			}
			candidates = append(candidates, FrozenCandidate{ArticleID: c.ArticleID, Semantic: c.Semantic, Identity: identity})
		}
		s.observer.ObserveStage(ctx, StageRRF, s.now().Sub(started))
	}
	s.recall(ctx, "union", len(candidates))
	s.mode(ctx, mode, semantic.reason)
	state := FrozenPage{Mode: mode, ExpiresAt: s.now().Add(HybridTTL), Candidates: candidates}
	return s.pageFromCurrent(query, state, current)
}

func (s *Service) readCurrent(ctx context.Context, ids []int64) (map[int64]CurrentArticle, error) {
	started := s.now()
	items, err := s.currentReader.ListPublishedWithIdentity(ctx, ids)
	s.observer.ObserveStage(ctx, StagePostgres, s.now().Sub(started))
	if err != nil {
		return nil, controlled(CodeDependencyUnavailable, err)
	}
	result := make(map[int64]CurrentArticle, len(items))
	for _, item := range items {
		result[item.Item.ID] = item
	}
	return result, nil
}

func (s *Service) frozenPage(ctx context.Context, query Query, state FrozenPage) (Page, error) {
	ids := make([]int64, 0, len(state.Candidates)-state.Offset)
	for _, c := range state.Candidates[state.Offset:] {
		ids = append(ids, c.ArticleID)
	}
	current, err := s.readCurrent(ctx, ids)
	if err != nil {
		return Page{}, err
	}
	for _, c := range state.Candidates[state.Offset:] {
		item, ok := current[c.ArticleID]
		if !ok {
			s.observer.AddFiltered(ctx, 1)
		} else if c.Semantic && !c.Identity.Matches(item.Identity) {
			s.stale(ctx, 1)
		}
	}
	return s.pageFromCurrent(query, state, current)
}

func (s *Service) pageFromCurrent(query Query, state FrozenPage, current map[int64]CurrentArticle) (Page, error) {
	page := Page{Items: make([]articleItem, 0, query.Limit)}
	for offset := state.Offset; offset < len(state.Candidates); offset++ {
		c := state.Candidates[offset]
		item, ok := current[c.ArticleID]
		if !ok || c.Semantic && !c.Identity.Matches(item.Identity) {
			continue
		}
		if len(page.Items) == query.Limit {
			state.Offset = offset
			token, err := s.codec.EncodeFrozen(query, s.config.Hybrid, state)
			if err != nil {
				return Page{}, err
			}
			page.NextCursor = &token
			page.HasMore = true
			break
		}
		page.Items = append(page.Items, item.Item)
	}
	return page, nil
}
