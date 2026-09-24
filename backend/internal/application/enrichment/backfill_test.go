package enrichment

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type backfillStoreFake struct {
	candidates []BackfillCandidate
	more       bool
	advanced   int
	changed    bool
	queries    int
	advanceErr error
}

func (f *backfillStoreFake) MaxArticleID(context.Context) (int64, error) { return 2, nil }
func (f *backfillStoreFake) Candidates(context.Context, BackfillRequest, *BackfillCursor, int, int64) ([]BackfillCandidate, bool, error) {
	f.queries++
	return f.candidates, f.more, nil
}

func TestAIBackfillValidatesSelectorsAndAllConfirmationBeforeStore(t *testing.T) {
	cases := []BackfillRequest{
		{Stage: "generation", Mode: "missing-only", GenerationProfile: "g"},
		{Stage: "generation", Mode: "missing-only", ArticleID: 1, Limit: 1, GenerationProfile: "g"},
		{Stage: "generation", Mode: "missing-only", ArticleID: 1, Order: "newest", GenerationProfile: "g"},
		{Stage: "generation", Mode: "missing-only", Limit: 1, Order: "random", GenerationProfile: "g"},
		{Stage: "generation", Mode: "missing-only", All: true, GenerationProfile: "g"},
	}
	for _, request := range cases {
		store := &backfillStoreFake{}
		if _, err := NewAIBackfillService(store).Run(context.Background(), request); err == nil || store.queries != 0 {
			t.Fatalf("request=%+v queries=%d err=%v", request, store.queries, err)
		}
	}
}

func TestAIBackfillExactMissingCandidateIsReportedSkipped(t *testing.T) {
	store := &backfillStoreFake{}
	report, err := NewAIBackfillService(store).Run(context.Background(), BackfillRequest{Stage: "generation", Mode: "missing-only", ArticleID: 99, GenerationProfile: "g"})
	if err != nil || report.Skipped != 1 || store.advanced != 0 {
		t.Fatalf("report=%+v advanced=%d err=%v", report, store.advanced, err)
	}
}
func (f *backfillStoreFake) AdvanceCandidate(context.Context, int64, BackfillRequest) (bool, error) {
	f.advanced++
	return f.changed, f.advanceErr
}

func TestAIBackfillFailureReportsFirstArticleAndCause(t *testing.T) {
	store := &backfillStoreFake{
		candidates: []BackfillCandidate{{ArticleID: 17}, {ArticleID: 18}},
		advanceErr: errors.New("数据库参数编码失败"),
	}
	report, err := NewAIBackfillService(store).Run(context.Background(), BackfillRequest{
		Stage:             "generation",
		Mode:              "missing-only",
		Limit:             2,
		GenerationProfile: "g1",
	})
	if err == nil || report.Failed != 2 || !report.HasMore || !strings.Contains(err.Error(), "article_id=17") || !strings.Contains(err.Error(), "数据库参数编码失败") {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

func TestAIBackfillDryRunAndIdempotentStats(t *testing.T) {
	store := &backfillStoreFake{candidates: []BackfillCandidate{{ArticleID: 1}, {ArticleID: 2}}, more: true, changed: true}
	service := NewAIBackfillService(store)
	request := BackfillRequest{Stage: "all", Mode: "missing-only", Limit: 2, DryRun: true, GenerationProfile: "g1", EmbeddingProfile: "e1"}
	report, err := service.Run(context.Background(), request)
	if err != nil || report.Created != 2 || !report.HasMore || store.advanced != 0 {
		t.Fatalf("report=%+v writes=%d err=%v", report, store.advanced, err)
	}
	request.DryRun = false
	store.changed = false
	report, err = service.Run(context.Background(), request)
	if err != nil || report.Skipped != 2 || store.advanced != 2 {
		t.Fatalf("report=%+v writes=%d err=%v", report, store.advanced, err)
	}
}

type pagedBackfillStoreFake struct {
	maxArticleID int64
	queries      int
	advanced     []int64
}

func (f *pagedBackfillStoreFake) MaxArticleID(context.Context) (int64, error) {
	return f.maxArticleID, nil
}

func (f *pagedBackfillStoreFake) Candidates(_ context.Context, _ BackfillRequest, cursor *BackfillCursor, limit int, maxArticleID int64) ([]BackfillCandidate, bool, error) {
	f.queries++
	if limit != 100 || maxArticleID != f.maxArticleID {
		return nil, false, nil
	}
	if cursor == nil {
		return []BackfillCandidate{{ArticleID: 1, EffectivePublishedAt: time.Unix(100, 0)}}, true, nil
	}
	if cursor.ArticleID != 1 || !cursor.EffectivePublishedAt.Equal(time.Unix(100, 0)) {
		return nil, false, nil
	}
	return []BackfillCandidate{{ArticleID: 2, EffectivePublishedAt: time.Unix(200, 0)}}, false, nil
}

func (f *pagedBackfillStoreFake) AdvanceCandidate(_ context.Context, articleID int64, _ BackfillRequest) (bool, error) {
	f.advanced = append(f.advanced, articleID)
	return true, nil
}

func TestAIBackfillAllUsesSnapshotAndInternalCursorPages(t *testing.T) {
	store := &pagedBackfillStoreFake{maxArticleID: 42}
	report, err := NewAIBackfillService(store).Run(context.Background(), BackfillRequest{
		Stage:             "all",
		Mode:              "missing-only",
		All:               true,
		ConfirmAll:        true,
		GenerationProfile: "g2",
		EmbeddingProfile:  "e2",
	})
	if err != nil || report.Created != 2 || report.HasMore || store.queries != 2 {
		t.Fatalf("report=%+v queries=%d err=%v", report, store.queries, err)
	}
	if len(store.advanced) != 2 || store.advanced[0] != 1 || store.advanced[1] != 2 {
		t.Fatalf("advanced=%v", store.advanced)
	}
}
