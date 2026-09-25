package articlesearch

import (
	"context"
	"errors"
	"time"

	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
)

type Searcher interface {
	Search(context.Context, Request) (Page, error)
}

type Service struct {
	index    QueryIndex
	reader   PublicArticleReader
	codec    *CursorCodec
	observer Observer
	config   Config
	now      func() time.Time
}

func NewService(index QueryIndex, reader PublicArticleReader, codec *CursorCodec, observer Observer, config Config) *Service {
	if observer == nil {
		observer = NopObserver{}
	}
	return &Service{index: index, reader: reader, codec: codec, observer: observer, config: config, now: time.Now}
}

func (s *Service) Search(ctx context.Context, request Request) (page Page, returnedErr error) {
	started := s.now()
	result := ResultSuccess
	defer func() { s.observer.ObserveRequest(ctx, result, s.now().Sub(started)) }()

	query, err := Normalize(request)
	if err != nil {
		result = ResultValidation
		return Page{}, err
	}
	if s.index == nil || s.reader == nil || s.codec == nil {
		result = ResultSearchUnavailable
		return Page{}, controlled(CodeSearchUnavailable, ErrIndexUnavailable)
	}

	pitID := ""
	var after *SortPosition
	if query.Cursor != "" {
		decodedPIT, decodedAfter, decodeErr := s.codec.Decode(query, query.Cursor, s.now())
		if decodeErr != nil {
			result = ResultInvalidCursor
			return Page{}, decodeErr
		}
		pitID, after = decodedPIT, &decodedAfter
	} else {
		stageStarted := s.now()
		pitID, err = s.index.CreatePIT(ctx, s.config.PITKeepAlive)
		s.observer.ObserveStage(ctx, StageOpenSearch, s.now().Sub(stageStarted))
		if err != nil || pitID == "" {
			result = ResultSearchUnavailable
			s.observer.ObservePIT(ctx, PITFailure)
			return Page{}, controlled(CodeSearchUnavailable, errOr(err, ErrInvalidIndexResponse))
		}
		s.observer.ObservePIT(ctx, PITCreate)
	}

	closeOnReturn := true
	defer func() {
		if closeOnReturn {
			s.closePIT(ctx, pitID)
		}
	}()

	type visibleHit struct {
		item     interfaceItem
		position SortPosition
	}
	visible := make([]visibleHit, 0, query.Limit+1)
	scanned := 0
	var lastChecked *SortPosition
	exhausted := false

	for len(visible) < query.Limit+1 && scanned < s.config.MaxCandidatesPerRequest && !exhausted {
		size := min(s.config.CandidateBatchSize, s.config.MaxCandidatesPerRequest-scanned)
		stageStarted := s.now()
		batch, searchErr := s.index.Search(ctx, IndexRequest{Query: query, PITID: pitID, After: after, Size: size, KeepAlive: s.config.PITKeepAlive})
		s.observer.ObserveStage(ctx, StageOpenSearch, s.now().Sub(stageStarted))
		if searchErr != nil {
			if errors.Is(searchErr, ErrPITNotFound) {
				result = ResultInvalidCursor
				return Page{}, controlled(CodeInvalidCursor, searchErr)
			}
			result = ResultSearchUnavailable
			return Page{}, controlled(CodeSearchUnavailable, searchErr)
		}
		if batch.PITID == "" || len(batch.Candidates) > size || (len(batch.Candidates) == 0 && !batch.Exhausted) {
			result = ResultSearchUnavailable
			return Page{}, controlled(CodeSearchUnavailable, ErrInvalidIndexResponse)
		}
		pitID = batch.PITID
		exhausted = batch.Exhausted
		if len(batch.Candidates) == 0 {
			continue
		}
		scanned += len(batch.Candidates)
		s.observer.AddCandidates(ctx, len(batch.Candidates))
		ids := make([]int64, len(batch.Candidates))
		for index, candidate := range batch.Candidates {
			ids[index] = candidate.ArticleID
		}
		stageStarted = s.now()
		items, readErr := s.reader.ListPublishedByIDs(ctx, ids)
		s.observer.ObserveStage(ctx, StagePostgres, s.now().Sub(stageStarted))
		if readErr != nil {
			result = ResultDependencyUnavailable
			return Page{}, controlled(CodeDependencyUnavailable, readErr)
		}
		byID := make(map[int64]interfaceItem, len(items))
		for _, item := range items {
			byID[item.ID] = interfaceItem{value: item}
		}
		for _, candidate := range batch.Candidates {
			position := candidate.Position
			lastChecked = &position
			item, ok := byID[candidate.ArticleID]
			if !ok {
				s.observer.AddFiltered(ctx, 1)
				continue
			}
			visible = append(visible, visibleHit{item: item, position: position})
			if len(visible) == query.Limit+1 {
				break
			}
		}
		if len(batch.Candidates) > 0 {
			position := batch.Candidates[len(batch.Candidates)-1].Position
			after = &position
		}
	}

	page.Items = make([]articleItem, 0, min(len(visible), query.Limit))
	for index := 0; index < len(visible) && index < query.Limit; index++ {
		page.Items = append(page.Items, visible[index].item.value)
	}
	if len(visible) > query.Limit {
		page.HasMore = true
		last := visible[query.Limit-1].position
		page.NextCursor, err = s.nextCursor(query, pitID, last)
	} else if !exhausted && scanned >= s.config.MaxCandidatesPerRequest {
		page.HasMore = true
		s.observer.ObserveScanLimit(ctx)
		if lastChecked == nil {
			result = ResultInternal
			return Page{}, controlled(CodeInternal, errors.New("扫描上限缺少推进位置"))
		}
		page.NextCursor, err = s.nextCursor(query, pitID, *lastChecked)
	}
	if err != nil {
		result = ResultInternal
		return Page{}, controlled(CodeInternal, err)
	}
	if page.HasMore {
		closeOnReturn = false
	}
	return page, nil
}

// 下面两个别名只用于让编排代码保持清晰，公共 Page 仍直接暴露领域 ArticleItem。
type articleItem = articleDomain.ListItem
type interfaceItem struct{ value articleItem }

func (s *Service) nextCursor(query Query, pitID string, position SortPosition) (*string, error) {
	encoded, err := s.codec.Encode(query, pitID, position, s.now().Add(s.config.PITKeepAlive))
	if err != nil {
		return nil, err
	}
	return &encoded, nil
}

func (s *Service) closePIT(ctx context.Context, pitID string) {
	if pitID == "" {
		return
	}
	if err := s.index.ClosePIT(ctx, pitID); err != nil {
		s.observer.ObservePIT(ctx, PITFailure)
		return
	}
	s.observer.ObservePIT(ctx, PITDelete)
}

func errOr(err, fallback error) error {
	if err != nil {
		return err
	}
	return fallback
}

type UnavailableService struct{}

func (UnavailableService) Search(context.Context, Request) (Page, error) {
	return Page{}, controlled(CodeSearchUnavailable, ErrIndexUnavailable)
}
