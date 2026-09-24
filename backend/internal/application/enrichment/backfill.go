package enrichment

import (
	"context"
	"fmt"
	"time"
)

type BackfillRequest struct {
	Stage, Mode                         string
	Limit                               int
	ArticleID                           int64
	Order                               string
	All, ConfirmAll, DryRun             bool
	GenerationProfile, EmbeddingProfile string
}

type BackfillCursor struct {
	EffectivePublishedAt time.Time
	ArticleID            int64
}

type BackfillCandidate struct {
	ArticleID            int64
	EffectivePublishedAt time.Time
}

type BackfillReport struct {
	Created, Skipped, Failed int
	HasMore                  bool
}

type BackfillStore interface {
	MaxArticleID(context.Context) (int64, error)
	Candidates(context.Context, BackfillRequest, *BackfillCursor, int, int64) ([]BackfillCandidate, bool, error)
	AdvanceCandidate(context.Context, int64, BackfillRequest) (bool, error)
}

type BackfillService struct{ store BackfillStore }

func NewAIBackfillService(store BackfillStore) *BackfillService {
	return &BackfillService{store: store}
}

func (s *BackfillService) Run(ctx context.Context, request BackfillRequest) (BackfillReport, error) {
	if request.Stage != "generation" && request.Stage != "embedding" && request.Stage != "all" {
		return BackfillReport{}, fmt.Errorf("stage 必须是 generation、embedding 或 all")
	}
	if request.Mode != "missing-only" && request.Mode != "outdated-only" {
		return BackfillReport{}, fmt.Errorf("mode 必须是 missing-only 或 outdated-only")
	}
	if (request.Stage == "generation" || request.Stage == "all") && request.GenerationProfile == "" {
		return BackfillReport{}, fmt.Errorf("generation profile 未配置")
	}
	if (request.Stage == "embedding" || request.Stage == "all") && request.EmbeddingProfile == "" {
		return BackfillReport{}, fmt.Errorf("embedding profile 未配置")
	}
	selectors := 0
	if request.ArticleID > 0 {
		selectors++
	}
	if request.Limit > 0 {
		selectors++
	}
	if request.All {
		selectors++
	}
	if selectors != 1 || request.ArticleID < 0 || request.Limit < 0 {
		return BackfillReport{}, fmt.Errorf("必须且只能选择 article-id、limit 或 all 之一")
	}
	if request.Limit > 1000 {
		return BackfillReport{}, fmt.Errorf("limit 必须介于 1 和 1000")
	}
	if request.Order != "" && request.Limit == 0 {
		return BackfillReport{}, fmt.Errorf("order 只能与 limit 一起使用")
	}
	if request.Order == "" {
		request.Order = "oldest"
	}
	if request.Order != "oldest" && request.Order != "newest" {
		return BackfillReport{}, fmt.Errorf("order 必须是 oldest 或 newest")
	}
	if request.All && !request.DryRun && !request.ConfirmAll {
		return BackfillReport{}, fmt.Errorf("全量补录可能产生大量模型费用，必须显式提供 confirm-all")
	}

	maxArticleID := int64(0)
	if request.All {
		var err error
		maxArticleID, err = s.store.MaxArticleID(ctx)
		if err != nil {
			return BackfillReport{}, err
		}
	}
	pageSize := request.Limit
	if request.ArticleID > 0 {
		pageSize = 1
	}
	if request.All {
		pageSize = 100
	}
	return s.runPages(ctx, request, pageSize, maxArticleID)
}

func (s *BackfillService) runPages(ctx context.Context, request BackfillRequest, pageSize int, maxArticleID int64) (BackfillReport, error) {
	var report BackfillReport
	var cursor *BackfillCursor
	var firstFailure error
	var firstFailureArticleID int64
	for {
		candidates, more, err := s.store.Candidates(ctx, request, cursor, pageSize, maxArticleID)
		if err != nil {
			return report, err
		}
		if request.ArticleID > 0 && len(candidates) == 0 {
			report.Skipped = 1
			return report, nil
		}
		if request.DryRun {
			report.Created += len(candidates)
		} else {
			for _, candidate := range candidates {
				if ctx.Err() != nil {
					report.HasMore = true
					return report, ctx.Err()
				}
				changed, advanceErr := s.store.AdvanceCandidate(ctx, candidate.ArticleID, request)
				if advanceErr != nil {
					report.Failed++
					if firstFailure == nil {
						firstFailure = advanceErr
						firstFailureArticleID = candidate.ArticleID
					}
					continue
				}
				if changed {
					report.Created++
				} else {
					report.Skipped++
				}
			}
		}
		if !request.All {
			report.HasMore = more
			break
		}
		if len(candidates) == 0 || !more {
			break
		}
		last := candidates[len(candidates)-1]
		cursor = &BackfillCursor{EffectivePublishedAt: last.EffectivePublishedAt, ArticleID: last.ArticleID}
	}
	if report.Failed > 0 {
		report.HasMore = true
		return report, fmt.Errorf("AI 补录有 %d 篇失败，首个失败 article_id=%d: %w", report.Failed, firstFailureArticleID, firstFailure)
	}
	return report, nil
}
