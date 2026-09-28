package opensearch

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	searchApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	projectionApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/searchprojection"
)

func TestKNNBodyContainsInternalFilters(t *testing.T) {
	r := searchApp.KNNRequest{IndexRequest: searchApp.IndexRequest{PITID: "pit", Size: 100, KeepAlive: time.Minute, Query: searchApp.Query{Q: "文本", Filters: searchApp.Filters{Keyword: "关键词", Topic: "主题", SourceID: 7}}}, Vector: []float64{1, 0}, Profile: "e-v2"}
	body, err := BuildKNNBody(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"knn", "filter", "visible", "keywords.keyword", "topics.keyword", "source_id", "embedding_profile_version", "generation_result_id", "revision_id", "embedding_result_id", "published_at", "article_id"} {
		if !strings.Contains(string(body), key) {
			t.Fatalf("缺少 %s", key)
		}
	}
	if strings.Contains(string(body), "multi_match") {
		t.Fatal("KNN 不应要求文本命中")
	}
	r.Size = 101
	if _, err := BuildKNNBody(r); err == nil {
		t.Fatal("接受超量候选")
	}
}

func TestSemanticSchemaDetection(t *testing.T) {
	for _, tc := range []struct {
		version, encoding int
		profile, identity string
		ok                bool
	}{
		{2, 2, "keyword", "mapping-v2|analyzer-cjk|dims-2|encoding-v2", true},
		{1, 1, "", "mapping-v1|analyzer-cjk|dims-2|encoding-v1", false},
		{2, 2, "keyword", "", false}, {2, 1, "keyword", "mapping-v2|analyzer-cjk|dims-2|encoding-v2", false},
	} {
		client := newStubClient(t, func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"physical": map[string]any{"mappings": map[string]any{"_meta": map[string]any{"velis_schema_version": tc.version, "velis_projection_encoding": tc.encoding, "velis_schema_identity": tc.identity, "velis_embedding_dimensions": 2}, "properties": map[string]any{"vector": map[string]any{"type": "knn_vector", "dimension": 2}, "embedding_profile_version": map[string]any{"type": tc.profile}}}}})
		})
		client.cfg.QueryTimeout = time.Second
		ok, err := client.SupportsSemantic(context.Background(), 2)
		if err != nil || ok != tc.ok {
			t.Fatalf("schema %d: %v %v", tc.version, ok, err)
		}
	}
}

func TestHybridRealKNNPITAndFilters(t *testing.T) {
	h := newHarness(t, 3)
	ctx := context.Background()
	spec := h.spec(2, 3)
	spec.SchemaIdentity = "mapping-v2|analyzer-cjk|dims-3|encoding-v2"
	if _, err := h.client.Ensure(ctx, spec, h.aliases(spec.PhysicalIndex)); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	docs := []projectionApp.Document{}
	for i := int64(1); i <= 4; i++ {
		d := document(i, 1, true)
		d.SchemaVersion = 2
		d.PublishedAt = &now
		d.Title = "语义文章"
		d.PlainText = "不同措辞"
		d.Vector = []float64{1, 0, 0}
		d.EmbeddingProfileVersion = "e-v2"
		d.GenerationResultID = "97000000-0000-0000-0000-000000000001"
		d.EmbeddingResultID = "97000000-0000-0000-0000-000000000003"
		docs = append(docs, d)
	}
	docs[0].Title = "独有文本 zebra"
	docs[0].Vector = nil
	docs[0].EmbeddingProfileVersion = ""
	docs[2].EmbeddingProfileVersion = "e-old"
	docs[3].Keywords = []string{"其它关键词"}
	items := make([]projectionApp.BulkItem, len(docs))
	for i, d := range docs {
		items[i] = projectionApp.BulkItem{PhysicalIndex: spec.PhysicalIndex, Document: d}
	}
	outcomes, err := h.client.Bulk(ctx, items)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range outcomes {
		if !o.Result.Applied() {
			t.Fatalf("bulk: %+v", o)
		}
	}
	if err = h.client.Refresh(ctx, spec.PhysicalIndex); err != nil {
		t.Fatal(err)
	}
	ok, err := h.client.SupportsSemantic(ctx, 3)
	if err != nil || !ok {
		t.Fatalf("v2 能力: %v %v", ok, err)
	}
	pit, err := h.client.CreatePIT(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer h.client.ClosePIT(ctx, pit)
	request := searchApp.IndexRequest{Query: searchApp.Query{Q: "zebra"}, PITID: pit, Size: 100, KeepAlive: time.Minute}
	bm, err := h.client.Search(ctx, request)
	if err != nil || len(bm.Candidates) != 1 || bm.Candidates[0].ArticleID != 1 {
		t.Fatalf("BM25 独有: %+v %v", bm, err)
	}
	request.Query.Filters = searchApp.Filters{Keyword: "关键词", Topic: "主题"}
	knn, err := h.client.SearchKNN(ctx, searchApp.KNNRequest{IndexRequest: request, Vector: []float64{1, 0, 0}, Profile: "e-v2"})
	if err != nil || len(knn.Candidates) != 1 || knn.Candidates[0].ArticleID != 2 {
		t.Fatalf("KNN 独有/profile/过滤: %+v %v", knn, err)
	}
	request.Query.Filters.SourceID = 999
	knn, err = h.client.SearchKNN(ctx, searchApp.KNNRequest{IndexRequest: request, Vector: []float64{1, 0, 0}, Profile: "e-v2"})
	if err != nil || len(knn.Candidates) != 0 {
		t.Fatalf("来源过滤: %+v %v", knn, err)
	}
}

func TestKNNRejectsInvalidResponses(t *testing.T) {
	base := `{"pit_id":"pit","_shards":{"total":1,"successful":1,"failed":0},"hits":{"hits":[{"_id":"1","sort":[1,1788220800000,1],"_source":{"revision_id":10,"generation_result_id":"97000000-0000-0000-0000-000000000001","embedding_result_id":"97000000-0000-0000-0000-000000000003","embedding_profile_version":"e-v2"}}]}}`
	for _, body := range []string{
		strings.Replace(base, `"revision_id":10`, `"revision_id":0`, 1), strings.Replace(base, "e-v2", "e-old", 1),
		strings.Replace(base, `"successful":1,"failed":0`, `"successful":0,"failed":1`, 1),
		strings.Replace(base, `"embedding_result_id":"97000000-0000-0000-0000-000000000003"`, `"embedding_result_id":"bad"`, 1),
		strings.Replace(base, `"pit_id":"pit"`, `"pit_id":"pit","timed_out":true`, 1),
	} {
		client := newStubClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) })
		client.cfg.QueryTimeout = time.Second
		_, err := client.SearchKNN(context.Background(), searchApp.KNNRequest{IndexRequest: searchApp.IndexRequest{PITID: "pit", Size: 1, KeepAlive: time.Minute}, Vector: []float64{1, 0}, Profile: "e-v2"})
		if err == nil {
			t.Fatal("非法 KNN 响应通过")
		}
	}
}
