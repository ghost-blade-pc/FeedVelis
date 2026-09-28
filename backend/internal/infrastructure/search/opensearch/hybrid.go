package opensearch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/config"
)

func BuildKNNBody(request articlesearch.KNNRequest) ([]byte, error) {
	if request.Size < 1 || request.Size > 100 || request.Profile == "" || !articlesearch.ValidQueryVector(request.Vector, len(request.Vector)) {
		return nil, articlesearch.ErrInvalidIndexResponse
	}
	raw, err := BuildQueryBody(request.IndexRequest)
	if err != nil {
		return nil, err
	}
	var body map[string]any
	if err = json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	filters := body["query"].(map[string]any)["bool"].(map[string]any)["filter"].([]any)
	filters = append(filters, map[string]any{"exists": map[string]any{"field": "vector"}}, map[string]any{"term": map[string]any{"embedding_profile_version": request.Profile}})
	body["query"] = map[string]any{"knn": map[string]any{"vector": map[string]any{"vector": request.Vector, "k": request.Size, "filter": map[string]any{"bool": map[string]any{"filter": filters}}}}}
	body["_source"] = []string{"revision_id", "generation_result_id", "embedding_result_id", "embedding_profile_version"}
	delete(body, "search_after")
	return json.Marshal(body)
}

func (c *Client) SearchKNN(ctx context.Context, request articlesearch.KNNRequest) (articlesearch.CandidateBatch, error) {
	body, err := BuildKNNBody(request)
	if err != nil {
		return articlesearch.CandidateBatch{}, err
	}
	ctx, cancel := c.queryContext(ctx)
	defer cancel()
	var response searchResponse
	if err = c.performJSON(ctx, http.MethodPost, "/_search", body, &response, true); err != nil {
		return articlesearch.CandidateBatch{}, err
	}
	batch, err := parseSearchResponse(response, request.Size)
	if err != nil {
		return articlesearch.CandidateBatch{}, err
	}
	for i, hit := range response.Hits.Hits {
		var source struct {
			RevisionID int64  `json:"revision_id"`
			Generation string `json:"generation_result_id"`
			Embedding  string `json:"embedding_result_id"`
			Profile    string `json:"embedding_profile_version"`
		}
		if err = json.Unmarshal(hit.Source, &source); err != nil {
			return articlesearch.CandidateBatch{}, articlesearch.ErrInvalidIndexResponse
		}
		identity := articlesearch.VectorIdentity{RevisionID: source.RevisionID, GenerationID: source.Generation, EmbeddingID: source.Embedding, Profile: source.Profile}
		if !identity.Matches(identity) || source.Profile != request.Profile || !validUUID(source.Generation) || !validUUID(source.Embedding) {
			return articlesearch.CandidateBatch{}, articlesearch.ErrInvalidIndexResponse
		}
		batch.Candidates[i].Identity = identity
	}
	return batch, nil
}

func validUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, c := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// SupportsSemantic 每次首查读取实际读别名映射，避免切换或回滚后沿用旧能力缓存。
func (c *Client) SupportsSemantic(ctx context.Context, dimensions int) (bool, error) {
	ctx, cancel := c.queryContext(ctx)
	defer cancel()
	var mappings map[string]struct {
		Mappings struct {
			Meta struct {
				Version    int    `json:"velis_schema_version"`
				Identity   string `json:"velis_schema_identity"`
				Dimensions int    `json:"velis_embedding_dimensions"`
				Encoding   int    `json:"velis_projection_encoding"`
			} `json:"_meta"`
			Properties map[string]struct {
				Type      string `json:"type"`
				Dimension int    `json:"dimension"`
			} `json:"properties"`
		} `json:"mappings"`
	}
	if err := c.performJSON(ctx, http.MethodGet, "/"+url.PathEscape(c.cfg.ReadAlias())+"/_mapping", nil, &mappings, false); err != nil {
		return false, err
	}
	if len(mappings) != 1 {
		return false, nil
	}
	expected := config.SearchConfig{SchemaVersion: 2, EmbeddingDimensions: dimensions}.SchemaIdentity()
	for _, entry := range mappings {
		m := entry.Mappings
		return m.Meta.Version == 2 && m.Meta.Identity == expected && m.Meta.Dimensions == dimensions && m.Meta.Encoding == 2 && m.Properties["embedding_profile_version"].Type == "keyword" && m.Properties["vector"].Type == "knn_vector" && m.Properties["vector"].Dimension == dimensions, nil
	}
	return false, fmt.Errorf("读索引映射为空")
}
