package opensearch

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/recommendation"
)

func TestRecommendQueryIsBoundedAndPublic(t *testing.T) {
	terms := recommendation.Terms{Keywords: []string{"Go"}, Topics: []string{"后端"}}
	body, err := BuildRecommendBody(terms, "pit", 100, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`"visible":true`, `"keywords"`, `"topics"`, `"minimum_should_match":1`, `"size":100`, `"_source":false`} {
		if !strings.Contains(string(body), value) {
			t.Fatalf("推荐查询缺少 %s: %s", value, body)
		}
	}
	for _, input := range []struct {
		terms recommendation.Terms
		limit int
	}{{recommendation.Terms{}, 1}, {terms, 101}, {terms, 0}, {recommendation.Terms{Keywords: []string{""}}, 1}, {recommendation.Terms{Keywords: []string{"a", "a"}}, 1}} {
		if _, err := BuildRecommendBody(input.terms, "pit", input.limit, time.Minute); err == nil {
			t.Fatalf("接受非法候选请求: %+v", input)
		}
	}
}

func TestRecommendRecallRejectsPartialResponseAndHonorsTimeout(t *testing.T) {
	terms := recommendation.Terms{Keywords: []string{"Go"}}
	for _, reply := range []string{
		`{"pit_id":"pit","_shards":{"total":2,"successful":1,"failed":1},"hits":{"hits":[]}}`,
		`{"pit_id":"pit","timed_out":true,"_shards":{"total":1,"successful":1,"failed":0},"hits":{"hits":[]}}`,
	} {
		client := queryTestClient(t, func(request *http.Request) (*http.Response, error) {
			if strings.Contains(request.URL.Path, "point_in_time") {
				return jsonResponse(request, 200, `{"pit_id":"pit","_shards":{"total":1,"successful":1,"failed":0}}`), nil
			}
			if request.Method == http.MethodDelete {
				return jsonResponse(request, 200, `{"pits":[{"pit_id":"pit","successful":true}]}`), nil
			}
			return jsonResponse(request, 200, reply), nil
		})
		if _, err := client.Recall(context.Background(), terms, 1, time.Minute); !errors.Is(err, articlesearch.ErrInvalidIndexResponse) {
			t.Fatalf("非法/部分响应未拒绝: %v", err)
		}
	}
	client := queryTestClient(t, func(request *http.Request) (*http.Response, error) {
		if request.Context().Err() != nil {
			return nil, request.Context().Err()
		}
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, err := client.Recall(ctx, terms, 1, time.Minute); err == nil {
		t.Fatal("首查超时未停止")
	}
}
