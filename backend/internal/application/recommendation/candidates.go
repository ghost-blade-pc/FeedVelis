package recommendation

import (
	"context"
	"strings"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
)

type Terms struct {
	Keywords []string
	Topics   []string
}

func (t Terms) Valid() bool {
	if len(t.Keywords) > 3 || len(t.Topics) > 3 || len(t.Keywords)+len(t.Topics) == 0 {
		return false
	}
	for _, list := range [][]string{t.Keywords, t.Topics} {
		seen := map[string]bool{}
		for _, value := range list {
			if strings.TrimSpace(value) != value || value == "" || len([]rune(value)) > 64 || seen[value] {
				return false
			}
			seen[value] = true
		}
	}
	return true
}

// CandidateIndex 是推荐专用的有界召回端口；调用方负责 PostgreSQL 当前事实复核。
type CandidateIndex interface {
	Recall(context.Context, Terms, int, time.Duration) ([]articlesearch.Candidate, error)
}

type HybridRecall struct {
	BM25           []articlesearch.Candidate
	KNN            []articlesearch.Candidate
	SemanticReason string
}

type HybridCandidateIndex interface {
	RecallHybrid(context.Context, Terms, int, int, time.Duration, []float64, string, time.Duration) (HybridRecall, error)
}
