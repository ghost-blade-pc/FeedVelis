package enrichment

import (
	"context"
	"fmt"
)

type BackfillRequest struct {
	Stage, Mode                         string
	Limit                               int
	DryRun                              bool
	GenerationProfile, EmbeddingProfile string
}
type BackfillReport struct {
	Created, Skipped, Failed int
	HasMore                  bool
}

type BackfillStore interface {
	Candidates(context.Context, BackfillRequest) ([]int64, bool, error)
	AdvanceCandidate(context.Context, int64, BackfillRequest) (bool, error)
}

type BackfillService struct{ store BackfillStore }

func NewAIBackfillService(store BackfillStore) *BackfillService {
	return &BackfillService{store: store}
}

func (s *BackfillService) Run(ctx context.Context, request BackfillRequest) (BackfillReport, error) {
	if request.Limit < 1 || request.Limit > 1000 {
		return BackfillReport{}, fmt.Errorf("limit 必须介于 1 和 1000")
	}
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
	ids, more, err := s.store.Candidates(ctx, request)
	if err != nil {
		return BackfillReport{}, err
	}
	report := BackfillReport{HasMore: more}
	if request.DryRun {
		report.Created = len(ids)
		return report, nil
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return report, ctx.Err()
		}
		changed, advanceErr := s.store.AdvanceCandidate(ctx, id, request)
		if advanceErr != nil {
			report.Failed++
			continue
		}
		if changed {
			report.Created++
		} else {
			report.Skipped++
		}
	}
	if report.Failed > 0 {
		return report, fmt.Errorf("AI 补录有 %d 篇失败", report.Failed)
	}
	return report, nil
}
