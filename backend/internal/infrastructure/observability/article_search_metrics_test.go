package observability

import (
	"context"
	"strings"
	"testing"
	"time"

	searchApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/expfmt"
)

func TestArticleSearchMetricsUseOnlyFixedLabels(t *testing.T) {
	registry := prometheus.NewRegistry()
	observer := NewArticleSearchObserver(NewArticleSearchMetrics(registry))
	ctx := context.Background()
	for _, result := range []searchApp.Result{searchApp.ResultSuccess, searchApp.ResultValidation, searchApp.ResultInvalidCursor, searchApp.ResultSearchUnavailable, searchApp.ResultDependencyUnavailable, searchApp.ResultInternal} {
		observer.ObserveRequest(ctx, result, time.Millisecond)
	}
	for _, stage := range []searchApp.Stage{searchApp.StageOpenSearch, searchApp.StagePostgres} {
		observer.ObserveStage(ctx, stage, time.Millisecond)
	}
	for _, operation := range []searchApp.PITOperation{searchApp.PITCreate, searchApp.PITDelete, searchApp.PITFailure} {
		observer.ObservePIT(ctx, operation)
	}
	observer.AddCandidates(ctx, 4)
	observer.AddFiltered(ctx, 2)
	observer.ObserveScanLimit(ctx)
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	encoder := expfmt.NewEncoder(&output, expfmt.NewFormat(expfmt.TypeTextPlain))
	for _, family := range families {
		if err := encoder.Encode(family); err != nil {
			t.Fatal(err)
		}
	}
	text := output.String()
	for _, required := range []string{"velis_article_search_requests_total", "velis_article_search_duration_seconds", "velis_article_search_candidates_total", "velis_article_search_filtered_total", "velis_article_search_scan_limit_total", "velis_article_search_pit_total"} {
		if !strings.Contains(text, required) {
			t.Fatalf("缺少指标 %s: %s", required, text)
		}
	}
	for _, forbidden := range []string{"private query", "keyword-value", "article-42", "cursor-value", "pit-secret", "physical-index", "password"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("指标泄露敏感值 %q: %s", forbidden, text)
		}
	}
	for _, family := range families {
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				if label.GetName() != "result" && label.GetName() != "stage" && label.GetName() != "operation" {
					t.Fatalf("出现动态标签 %s=%s", label.GetName(), label.GetValue())
				}
			}
		}
	}
}
