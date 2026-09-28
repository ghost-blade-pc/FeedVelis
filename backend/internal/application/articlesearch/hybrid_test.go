package articlesearch

import (
	"math"
	"testing"
	"time"
)

func TestHybridPlanFingerprint(t *testing.T) {
	h := HybridConfig{Enabled: true, BM25Candidates: 100, KNNCandidates: 100, EmbeddingTimeout: time.Second, KNNTimeout: time.Second, Provider: "p", Model: "m", Profile: "v1", Dimensions: 3}
	if h.Fingerprint() != h.Fingerprint() {
		t.Fatal("计划指纹不稳定")
	}
	for _, mutate := range []func(*HybridConfig){
		func(h *HybridConfig) { h.Enabled = false }, func(h *HybridConfig) { h.BM25Candidates-- }, func(h *HybridConfig) { h.KNNCandidates-- },
		func(h *HybridConfig) { h.EmbeddingTimeout++ }, func(h *HybridConfig) { h.KNNTimeout++ }, func(h *HybridConfig) { h.Provider = "q" },
		func(h *HybridConfig) { h.Model = "n" }, func(h *HybridConfig) { h.Profile = "v2" }, func(h *HybridConfig) { h.Dimensions++ },
	} {
		other := h
		mutate(&other)
		if other.Fingerprint() == h.Fingerprint() {
			t.Fatal("计划变更未改变指纹")
		}
	}
}

func TestQueryVectorValidation(t *testing.T) {
	for _, tc := range []struct {
		vector []float64
		valid  bool
	}{
		{nil, false}, {[]float64{0, 0}, false}, {[]float64{1}, false}, {[]float64{math.NaN(), 1}, false},
		{[]float64{math.Inf(1), 1}, false}, {[]float64{1, 0}, true}, {[]float64{math.SmallestNonzeroFloat64, 0}, true},
	} {
		if ValidQueryVector(tc.vector, 2) != tc.valid {
			t.Fatalf("向量验证失败: %v", tc.vector)
		}
	}
}
