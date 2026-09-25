package opensearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
)

const maxQueryResponseBytes = 1024 * 1024

func (c *Client) CreatePIT(ctx context.Context, keepAlive time.Duration) (string, error) {
	ctx, cancel := c.queryContext(ctx)
	defer cancel()
	path := "/" + url.PathEscape(c.cfg.ReadAlias()) + "/_search/point_in_time?keep_alive=" +
		url.QueryEscape(formatOpenSearchDuration(keepAlive)) + "&allow_partial_pit_creation=false"
	var response struct {
		PITID  string        `json:"pit_id"`
		Shards shardResponse `json:"_shards"`
	}
	if err := c.performJSON(ctx, http.MethodPost, path, nil, &response, false); err != nil {
		return "", err
	}
	if strings.TrimSpace(response.PITID) == "" || response.Shards.Failed != 0 || response.Shards.Successful != response.Shards.Total {
		return "", articlesearch.ErrInvalidIndexResponse
	}
	return response.PITID, nil
}

func (c *Client) Search(ctx context.Context, request articlesearch.IndexRequest) (articlesearch.CandidateBatch, error) {
	body, err := BuildQueryBody(request)
	if err != nil {
		return articlesearch.CandidateBatch{}, articlesearch.ErrInvalidIndexResponse
	}
	ctx, cancel := c.queryContext(ctx)
	defer cancel()
	var response searchResponse
	if err := c.performJSON(ctx, http.MethodPost, "/_search", body, &response, true); err != nil {
		return articlesearch.CandidateBatch{}, err
	}
	return parseSearchResponse(response, request.Size)
}

func (c *Client) ClosePIT(ctx context.Context, pitID string) error {
	body, err := json.Marshal(map[string]any{"pit_id": []string{pitID}})
	if err != nil {
		return articlesearch.ErrInvalidIndexResponse
	}
	ctx, cancel := c.queryContext(ctx)
	defer cancel()
	var response struct {
		PITs []struct {
			PITID      string `json:"pit_id"`
			Successful bool   `json:"successful"`
		} `json:"pits"`
	}
	if err := c.performJSON(ctx, http.MethodDelete, "/_search/point_in_time", body, &response, false); err != nil {
		return err
	}
	if len(response.PITs) != 1 || response.PITs[0].PITID != pitID || !response.PITs[0].Successful {
		return articlesearch.ErrInvalidIndexResponse
	}
	return nil
}

func BuildQueryBody(request articlesearch.IndexRequest) ([]byte, error) {
	if request.PITID == "" || request.Size < 1 || request.KeepAlive <= 0 {
		return nil, errors.New("查询请求无效")
	}
	filters := []any{map[string]any{"term": map[string]any{"visible": true}}}
	if request.Query.Filters.Keyword != "" {
		filters = append(filters, map[string]any{"term": map[string]any{"keywords.keyword": request.Query.Filters.Keyword}})
	}
	if request.Query.Filters.Topic != "" {
		filters = append(filters, map[string]any{"term": map[string]any{"topics.keyword": request.Query.Filters.Topic}})
	}
	if request.Query.Filters.SourceID > 0 {
		filters = append(filters, map[string]any{"term": map[string]any{"source_id": request.Query.Filters.SourceID}})
	}
	body := map[string]any{
		"size": request.Size, "_source": false, "track_scores": true, "track_total_hits": false,
		"pit": map[string]any{"id": request.PITID, "keep_alive": formatOpenSearchDuration(request.KeepAlive)},
		"query": map[string]any{"bool": map[string]any{
			"filter": filters,
			"must": []any{map[string]any{"multi_match": map[string]any{
				"query": request.Query.Q, "type": "best_fields",
				"fields": []string{"title^5", "keywords^4", "topics^3", "summary^2", "plain_text^1"},
			}}},
		}},
		"sort": []any{
			map[string]any{"_score": map[string]any{"order": "desc"}},
			map[string]any{"published_at": map[string]any{"order": "desc"}},
			map[string]any{"article_id": map[string]any{"order": "desc"}},
		},
	}
	if request.After != nil {
		if !request.After.Valid() {
			return nil, errors.New("search_after 无效")
		}
		body["search_after"] = []any{request.After.Score, request.After.PublishedAt.UTC().UnixMilli(), request.After.ArticleID}
	}
	return json.Marshal(body)
}

type shardResponse struct {
	Total      int               `json:"total"`
	Successful int               `json:"successful"`
	Skipped    int               `json:"skipped"`
	Failed     int               `json:"failed"`
	Failures   []json.RawMessage `json:"failures"`
}

type searchResponse struct {
	PITID    string        `json:"pit_id"`
	TimedOut bool          `json:"timed_out"`
	Shards   shardResponse `json:"_shards"`
	Hits     struct {
		Hits []struct {
			ID   string            `json:"_id"`
			Sort []json.RawMessage `json:"sort"`
		} `json:"hits"`
	} `json:"hits"`
}

func parseSearchResponse(response searchResponse, requested int) (articlesearch.CandidateBatch, error) {
	if response.TimedOut || strings.TrimSpace(response.PITID) == "" || response.Shards.Failed != 0 ||
		response.Shards.Successful != response.Shards.Total || len(response.Hits.Hits) > requested {
		return articlesearch.CandidateBatch{}, articlesearch.ErrInvalidIndexResponse
	}
	seen := make(map[int64]struct{}, len(response.Hits.Hits))
	candidates := make([]articlesearch.Candidate, 0, len(response.Hits.Hits))
	for _, hit := range response.Hits.Hits {
		articleID, err := strconv.ParseInt(hit.ID, 10, 64)
		if err != nil || articleID <= 0 || len(hit.Sort) != 3 {
			return articlesearch.CandidateBatch{}, articlesearch.ErrInvalidIndexResponse
		}
		if _, duplicate := seen[articleID]; duplicate {
			return articlesearch.CandidateBatch{}, articlesearch.ErrInvalidIndexResponse
		}
		seen[articleID] = struct{}{}
		var score float64
		var sortID int64
		if err := json.Unmarshal(hit.Sort[0], &score); err != nil || math.IsNaN(score) || math.IsInf(score, 0) {
			return articlesearch.CandidateBatch{}, articlesearch.ErrInvalidIndexResponse
		}
		publishedAt, err := parsePublishedSort(hit.Sort[1])
		if err != nil || json.Unmarshal(hit.Sort[2], &sortID) != nil || sortID != articleID {
			return articlesearch.CandidateBatch{}, articlesearch.ErrInvalidIndexResponse
		}
		position := articlesearch.SortPosition{Score: score, PublishedAt: publishedAt.UTC(), ArticleID: articleID}
		if !position.Valid() {
			return articlesearch.CandidateBatch{}, articlesearch.ErrInvalidIndexResponse
		}
		candidates = append(candidates, articlesearch.Candidate{ArticleID: articleID, Position: position})
	}
	return articlesearch.CandidateBatch{PITID: response.PITID, Candidates: candidates, Exhausted: len(candidates) < requested}, nil
}

func parsePublishedSort(raw json.RawMessage) (time.Time, error) {
	var milliseconds int64
	if err := json.Unmarshal(raw, &milliseconds); err == nil {
		if milliseconds <= 0 {
			return time.Time{}, articlesearch.ErrInvalidIndexResponse
		}
		return time.UnixMilli(milliseconds).UTC(), nil
	}
	var formatted string
	if err := json.Unmarshal(raw, &formatted); err != nil {
		return time.Time{}, articlesearch.ErrInvalidIndexResponse
	}
	value, err := time.Parse(time.RFC3339Nano, formatted)
	if err != nil {
		return time.Time{}, articlesearch.ErrInvalidIndexResponse
	}
	return value.UTC(), nil
}

func (c *Client) performJSON(ctx context.Context, method, path string, body []byte, target any, pitSearch bool) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, path, reader)
	if err != nil {
		return articlesearch.ErrInvalidIndexResponse
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.api.Client.Perform(request)
	if err != nil {
		return redactError(err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if pitSearch && response.StatusCode == http.StatusNotFound {
			return articlesearch.ErrPITNotFound
		}
		return redactError(fmt.Errorf("OpenSearch 返回状态 %d", response.StatusCode))
	}
	limited := io.LimitReader(response.Body, maxQueryResponseBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil || len(data) > maxQueryResponseBytes {
		return articlesearch.ErrInvalidIndexResponse
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(target); err != nil {
		return articlesearch.ErrInvalidIndexResponse
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return articlesearch.ErrInvalidIndexResponse
	}
	return nil
}

func (c *Client) queryContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := c.cfg.QueryTimeout
	if timeout <= 0 {
		timeout = c.cfg.RequestTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

func formatOpenSearchDuration(value time.Duration) string {
	return strconv.FormatInt(value.Milliseconds(), 10) + "ms"
}
