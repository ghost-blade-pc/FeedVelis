package opensearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	searchapp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	osearch "github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

func TestBuildQueryBodyUsesFixedPlanWithoutDangerousFeatures(t *testing.T) {
	body, err := BuildQueryBody(searchapp.IndexRequest{
		Query: searchapp.Query{Q: `go +url:https://example.com`, Filters: searchapp.Filters{Keyword: "Go", Topic: "后端", SourceID: 7}},
		PITID: "pit-id", Size: 100, KeepAlive: 2 * time.Minute,
		After: &searchapp.SortPosition{Score: 2.5, PublishedAt: time.Date(2026, 9, 25, 1, 2, 3, 0, time.UTC), ArticleID: 9},
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{`"visible":true`, `"keywords.keyword":"Go"`, `"topics.keyword":"后端"`, `"source_id":7`, `"type":"best_fields"`, `"title^5"`, `"keywords^4"`, `"topics^3"`, `"summary^2"`, `"plain_text^1"`, `"track_scores":true`, `"track_total_hits":false`, `"_source":false`, `"search_after"`} {
		if !strings.Contains(text, required) {
			t.Fatalf("查询 DSL 缺少 %s: %s", required, text)
		}
	}
	for _, forbidden := range []string{"vector", "highlight", "query_string", "simple_query_string", "fuzziness"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("查询 DSL 包含禁用能力 %q: %s", forbidden, text)
		}
	}
}

func TestQueryTransportPITLifecycleAndLatestPIT(t *testing.T) {
	requests := make([]capturedRequest, 0, 3)
	client := queryTestClient(t, func(request *http.Request) (*http.Response, error) {
		var body []byte
		if request.Body != nil {
			body, _ = io.ReadAll(request.Body)
		}
		requests = append(requests, capturedRequest{method: request.Method, path: request.URL.Path, query: request.URL.RawQuery, body: string(body), context: request.Context()})
		var response string
		switch len(requests) {
		case 1:
			response = `{"pit_id":"pit-1","_shards":{"total":1,"successful":1,"failed":0}}`
		case 2:
			response = `{"pit_id":"pit-2","timed_out":false,"_shards":{"total":1,"successful":1,"failed":0},"hits":{"hits":[{"_id":"7","sort":[2.5,"2026-09-25T01:02:03Z",7]}]}}`
		case 3:
			response = `{"pits":[{"pit_id":"pit-2","successful":true}]}`
		}
		return jsonResponse(request, http.StatusOK, response), nil
	})
	pitID, err := client.CreatePIT(context.Background(), 2*time.Minute)
	if err != nil || pitID != "pit-1" {
		t.Fatalf("CreatePIT = %q, %v", pitID, err)
	}
	batch, err := client.Search(context.Background(), searchapp.IndexRequest{Query: searchapp.Query{Q: "go"}, PITID: pitID, Size: 2, KeepAlive: 2 * time.Minute})
	if err != nil || batch.PITID != "pit-2" || len(batch.Candidates) != 1 || !batch.Exhausted {
		t.Fatalf("Search = %+v, %v", batch, err)
	}
	if err := client.ClosePIT(context.Background(), batch.PITID); err != nil {
		t.Fatal(err)
	}
	if requests[0].method != http.MethodPost || requests[0].path != "/velis-articles-read/_search/point_in_time" || !strings.Contains(requests[0].query, "allow_partial_pit_creation=false") || !strings.Contains(requests[0].query, "keep_alive=120000ms") {
		t.Fatalf("PIT create 请求错误: %+v", requests[0])
	}
	if requests[1].path != "/_search" || !strings.Contains(requests[1].body, `"pit":{"id":"pit-1","keep_alive":"120000ms"}`) {
		t.Fatalf("PIT search 请求错误: %+v", requests[1])
	}
	if requests[2].method != http.MethodDelete || requests[2].body != `{"pit_id":["pit-2"]}` {
		t.Fatalf("PIT close 请求错误: %+v", requests[2])
	}
}

func TestParseSearchResponseRejectsUnsafeShapes(t *testing.T) {
	valid := searchResponse{PITID: "pit"}
	valid.Shards.Total, valid.Shards.Successful = 1, 1
	valid.Hits.Hits = append(valid.Hits.Hits, struct {
		ID   string            `json:"_id"`
		Sort []json.RawMessage `json:"sort"`
	}{ID: "1", Sort: []json.RawMessage{json.RawMessage(`1.2`), json.RawMessage(`"2026-09-25T01:02:03Z"`), json.RawMessage(`1`)}})
	mutations := []func(*searchResponse){
		func(r *searchResponse) { r.TimedOut = true },
		func(r *searchResponse) { r.Shards.Failed = 1 },
		func(r *searchResponse) { r.PITID = "" },
		func(r *searchResponse) { r.Hits.Hits[0].ID = "0" },
		func(r *searchResponse) { r.Hits.Hits[0].Sort = []json.RawMessage{json.RawMessage(`1`)} },
		func(r *searchResponse) { r.Hits.Hits[0].Sort[1] = json.RawMessage(`"not-time"`) },
		func(r *searchResponse) { r.Hits.Hits = append(r.Hits.Hits, r.Hits.Hits[0]) },
	}
	for index, mutate := range mutations {
		copyValue := valid
		copyValue.Hits.Hits = append([]struct {
			ID   string            `json:"_id"`
			Sort []json.RawMessage `json:"sort"`
		}{}, valid.Hits.Hits...)
		copyValue.Hits.Hits[0].Sort = append([]json.RawMessage(nil), valid.Hits.Hits[0].Sort...)
		mutate(&copyValue)
		if _, err := parseSearchResponse(copyValue, 2); !errors.Is(err, searchapp.ErrInvalidIndexResponse) {
			t.Fatalf("case %d 未拒绝: %v", index, err)
		}
	}
}

func TestSearchClassifiesPITMissingAndRedactsTransportFailure(t *testing.T) {
	missing := queryTestClient(t, func(request *http.Request) (*http.Response, error) {
		return jsonResponse(request, http.StatusNotFound, `{"error":{"reason":"pit secret leaked"}}`), nil
	})
	_, err := missing.Search(context.Background(), searchapp.IndexRequest{Query: searchapp.Query{Q: "go"}, PITID: "expired", Size: 1, KeepAlive: time.Minute})
	if !errors.Is(err, searchapp.ErrPITNotFound) {
		t.Fatalf("PIT 404 分类错误: %v", err)
	}
	failing := queryTestClient(t, func(*http.Request) (*http.Response, error) {
		return nil, fmt.Errorf(`Post "https://reader:password@search.internal/_search?token=secret": timeout`)
	})
	_, err = failing.Search(context.Background(), searchapp.IndexRequest{Query: searchapp.Query{Q: "go"}, PITID: "pit", Size: 1, KeepAlive: time.Minute})
	if err == nil || strings.Contains(err.Error(), "password") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("传输错误未脱敏: %v", err)
	}
}

type capturedRequest struct {
	method, path, query, body string
	context                   context.Context
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func queryTestClient(t *testing.T, transport roundTripFunc) *Client {
	t.Helper()
	root, err := osearch.NewClient(osearch.Config{Addresses: []string{"http://search.test"}, Transport: transport, DisableRetry: true})
	if err != nil {
		t.Fatal(err)
	}
	return &Client{api: opensearchapi.NewFromClient(root), cfg: Config{IndexPrefix: "velis-articles", QueryTimeout: time.Second, RequestTimeout: time.Second}}
}

func jsonResponse(request *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Status: fmt.Sprintf("%d", status), Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}
}
