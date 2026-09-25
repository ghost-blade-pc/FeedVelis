package observability

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	projectionApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/searchprojection"
	projectionDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/searchprojection"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// TestSearchMetricLabelsAreBounded 覆盖 5.3：
// 指标标签必须来自受控词表，正文、HTML、向量与凭据都不得成为标签。
func TestSearchMetricLabelsAreBounded(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewSearchMetrics(registry)
	observer := NewSearchObserver(metrics, slog.New(slog.NewTextHandler(io.Discard, nil)))
	job := projectionApp.ClaimedJob{ArticleID: 7, Generation: 3, Target: mustTarget(t)}
	observer.Claimed(job, 1)
	observer.Delivered(job, projectionApp.DeliveryOutcome{Result: projectionDomain.ResultCreated})
	observer.Delivered(job, projectionApp.DeliveryOutcome{Result: projectionDomain.ResultPermanent,
		Code: "raw_es_error_with_正文", Message: "failed to parse field [vector] with <p>html</p>"})
	observer.Delivered(job, projectionApp.DeliveryOutcome{Result: projectionDomain.ResultRetryable,
		Code: projectionApp.ErrorThrottled, Message: "429"})
	observer.Staled(job, 2)
	observer.Inconsistent(job)
	observer.Observe(projectionApp.Backlog{Pending: 3, Failing: 1, OldestSeconds: 12.5, LaggingDeliveries: 2})
	observer.Rebuilt(projectionApp.RebuildState{ID: "rebuild-1", Phase: projectionDomain.PhaseCatchUp,
		CandidateIndex: "velis-articles-v1-20260925t120000z-aaaaaa"}, "catchup")

	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]map[string]bool{
		"status":         setOf(SearchStatuses...),
		"operation":      setOf(SearchOperations...),
		"result":         setOf(append(SearchBulkResults, "success", "failure", "stale")...),
		"code":           setOf(errorCodeStrings()...),
		"phase":          setOf(SearchRebuildPhases...),
		"role":           setOf(SearchIndexRoles...),
		"schema_version": setOf("unknown", "1", "2", "3"),
	}
	seen := map[string]bool{}
	for _, family := range families {
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				name, value := label.GetName(), label.GetValue()
				seen[name] = true
				if !allowed[name][value] {
					t.Fatalf("指标 %s 的标签 %s=%q 不在受控词表内", family.GetName(), name, value)
				}
				for _, forbidden := range []string{"正文", "html", "<p>", "vector", "0.1,0.2", "password", "secret", "velis-articles-v1-"} {
					if strings.Contains(value, forbidden) {
						t.Fatalf("指标 %s 的标签泄露 %q: %s=%s", family.GetName(), forbidden, name, value)
					}
				}
			}
		}
	}
	for _, name := range []string{"status", "operation", "result", "code", "phase", "role"} {
		if !seen[name] {
			t.Fatalf("期望的标签 %s 未被覆盖", name)
		}
	}
	// 未知错误码必须折叠为受控分类，而不是原样进入标签。
	if got := counterValue(t, families, "velis_search_errors_total", map[string]string{"code": string(projectionApp.ErrorInternal)}); got != 1 {
		t.Fatalf("未受控错误码必须折叠为 internal: %v", got)
	}
	if got := counterValue(t, families, "velis_search_projection_operations_total", map[string]string{"operation": "stale", "result": "stale"}); got != 1 {
		t.Fatalf("陈旧执行必须被单独计数: %v", got)
	}
	if got := counterValue(t, families, "velis_search_projection_operations_total", map[string]string{"operation": "inconsistent", "result": "success"}); got != 1 {
		t.Fatalf("不一致选择必须被计数: %v", got)
	}
	if got := counterValue(t, families, "velis_search_rebuild_phase", map[string]string{"phase": "catchup"}); got != 1 {
		t.Fatalf("重建阶段必须反映当前阶段: %v", got)
	}
	if got := counterValue(t, families, "velis_search_bulk_items_total", map[string]string{"result": "permanent"}); got != 1 {
		t.Fatalf("逐项分类必须被计数: %v", got)
	}
}

// counterValue 按指标名与标签读取当前值；不存在的组合返回 0。
func counterValue(t *testing.T, families []*dto.MetricFamily, name string, labels map[string]string) float64 {
	t.Helper()
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			matched := true
			for key, value := range labels {
				found := false
				for _, label := range metric.GetLabel() {
					if label.GetName() == key && label.GetValue() == value {
						found = true
						break
					}
				}
				if !found {
					matched = false
					break
				}
			}
			if matched {
				return metric.GetCounter().GetValue() + metric.GetGauge().GetValue()
			}
		}
	}
	return 0
}

func TestSearchMetricsPreinitializesControlledLabels(t *testing.T) {
	registry := prometheus.NewRegistry()
	NewSearchMetrics(registry)
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, family := range families {
		counts[family.GetName()] = len(family.GetMetric())
	}
	want := map[string]int{
		"velis_search_projection_jobs":             len(SearchStatuses),
		"velis_search_bulk_items_total":            len(SearchBulkResults),
		"velis_search_errors_total":                len(projectionApp.ErrorCodes),
		"velis_search_rebuild_phase":               len(SearchRebuildPhases),
		"velis_search_projection_operations_total": len(SearchOperations) * 3,
	}
	for name, expected := range want {
		if counts[name] != expected {
			t.Fatalf("%s 预初始化数量 = %d，期望 %d", name, counts[name], expected)
		}
	}
}

func mustTarget(t *testing.T) projectionDomain.Target {
	t.Helper()
	target, err := projectionDomain.NewTarget(projectionDomain.ActionUpsert, 1, 10, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func setOf(values ...string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func errorCodeStrings() []string {
	codes := make([]string, 0, len(projectionApp.ErrorCodes))
	for _, code := range projectionApp.ErrorCodes {
		codes = append(codes, string(code))
	}
	return codes
}
