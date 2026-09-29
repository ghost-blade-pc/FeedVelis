package opensearch

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/recommendation"
)

// Recall 复用公开读别名 PIT、严格响应解析和连接超时；PIT 仅在首查期间存在。
func (c *Client) Recall(ctx context.Context, terms recommendation.Terms, limit int, keepAlive time.Duration) ([]articlesearch.Candidate, error) {
	if !terms.Valid() || limit < 1 || limit > 100 || keepAlive <= 0 {
		return nil, errors.New("推荐候选请求无效")
	}
	pit, err := c.CreatePIT(ctx, keepAlive)
	if err != nil {
		return nil, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = c.ClosePIT(cleanup, pit)
	}()
	body, err := BuildRecommendBody(terms, pit, limit, keepAlive)
	if err != nil {
		return nil, err
	}
	queryCtx, cancel := c.queryContext(ctx)
	defer cancel()
	var response searchResponse
	if err := c.performJSON(queryCtx, http.MethodPost, "/_search", body, &response, true); err != nil {
		return nil, err
	}
	batch, err := parseSearchResponse(response, limit)
	if err != nil {
		return nil, err
	}
	pit = batch.PITID
	return batch.Candidates, nil
}

// RecallHybrid 在同一 PIT 内读取两路候选；KNN 单路失败仅返回固定原因。
func (c *Client) RecallHybrid(ctx context.Context, terms recommendation.Terms, bmLimit, knnLimit int, keepAlive time.Duration, vector []float64, profile string, knnTimeout time.Duration) (recommendation.HybridRecall, error) {
	if !terms.Valid() || bmLimit < 1 || bmLimit > 100 || knnLimit < 1 || knnLimit > 100 || keepAlive <= 0 {
		return recommendation.HybridRecall{}, errors.New("推荐候选请求无效")
	}
	pit, err := c.CreatePIT(ctx, keepAlive)
	if err != nil {
		return recommendation.HybridRecall{}, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = c.ClosePIT(cleanup, pit)
	}()
	body, err := BuildRecommendBody(terms, pit, bmLimit, keepAlive)
	if err != nil {
		return recommendation.HybridRecall{}, err
	}
	queryCtx, cancel := c.queryContext(ctx)
	defer cancel()
	var response searchResponse
	if err := c.performJSON(queryCtx, http.MethodPost, "/_search", body, &response, true); err != nil {
		return recommendation.HybridRecall{}, err
	}
	batch, err := parseSearchResponse(response, bmLimit)
	if err != nil {
		return recommendation.HybridRecall{}, err
	}
	pit = batch.PITID
	result := recommendation.HybridRecall{BM25: batch.Candidates}
	if len(vector) == 0 || profile == "" {
		result.SemanticReason = "semantic_unavailable"
		return result, nil
	}
	if knnTimeout <= 0 {
		result.SemanticReason = "knn_failed"
		return result, nil
	}
	supported, supportErr := c.SupportsSemantic(ctx, len(vector))
	if supportErr != nil || !supported {
		result.SemanticReason = "schema_incompatible"
		return result, nil
	}
	knnCtx, cancelKNN := context.WithTimeout(ctx, knnTimeout)
	defer cancelKNN()
	semantic, err := c.SearchKNN(knnCtx, articlesearch.KNNRequest{IndexRequest: articlesearch.IndexRequest{PITID: pit, Size: knnLimit, KeepAlive: keepAlive}, Vector: vector, Profile: profile})
	if err != nil || knnCtx.Err() != nil {
		result.SemanticReason = "knn_failed"
		return result, nil
	}
	result.KNN = semantic.Candidates
	pit = semantic.PITID
	return result, nil
}

func BuildRecommendBody(terms recommendation.Terms, pit string, limit int, keepAlive time.Duration) ([]byte, error) {
	if !terms.Valid() || pit == "" || limit < 1 || limit > 100 || keepAlive <= 0 {
		return nil, errors.New("推荐候选请求无效")
	}
	should := make([]any, 0, len(terms.Keywords)+len(terms.Topics))
	for _, keyword := range terms.Keywords {
		should = append(should, map[string]any{"match": map[string]any{"keywords": map[string]any{"query": keyword, "boost": 4}}})
	}
	for _, topic := range terms.Topics {
		should = append(should, map[string]any{"match": map[string]any{"topics": map[string]any{"query": topic, "boost": 3}}})
	}
	body := map[string]any{
		"size": limit, "_source": false, "track_scores": true, "track_total_hits": false,
		"pit": map[string]any{"id": pit, "keep_alive": formatOpenSearchDuration(keepAlive)},
		"query": map[string]any{"bool": map[string]any{
			"filter": []any{map[string]any{"term": map[string]any{"visible": true}}},
			"should": should, "minimum_should_match": 1,
		}},
		"sort": []any{
			map[string]any{"_score": map[string]any{"order": "desc"}},
			map[string]any{"published_at": map[string]any{"order": "desc"}},
			map[string]any{"article_id": map[string]any{"order": "desc"}},
		},
	}
	return json.Marshal(body)
}
