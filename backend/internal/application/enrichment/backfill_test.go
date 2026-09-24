package enrichment

import (
	"context"
	"testing"
)

type backfillStoreFake struct {
	ids      []int64
	more     bool
	advanced int
	changed  bool
}

func (f *backfillStoreFake) Candidates(context.Context, BackfillRequest) ([]int64, bool, error) {
	return f.ids, f.more, nil
}
func (f *backfillStoreFake) AdvanceCandidate(context.Context, int64, BackfillRequest) (bool, error) {
	f.advanced++
	return f.changed, nil
}

func TestAIBackfillDryRunAndIdempotentStats(t *testing.T) {
	store := &backfillStoreFake{ids: []int64{1, 2}, more: true, changed: true}
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
