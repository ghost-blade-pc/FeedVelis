package articlesearch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"time"

	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
)

const QueryInputVersion = "search-query-v1"
const RRFConstant = 60
const HybridTTL = 2 * time.Minute

// QueryEmbedder 只生成一次查询向量，不持久化、不调用生成模型。
type QueryEmbedder interface {
	EmbedQuery(context.Context, string) ([]float64, error)
}

type SemanticIndex interface {
	SearchKNN(context.Context, KNNRequest) (CandidateBatch, error)
	SupportsSemantic(context.Context, int) (bool, error)
}

type CurrentArticleReader interface {
	ListPublishedWithIdentity(context.Context, []int64) ([]CurrentArticle, error)
}

type VectorIdentity struct {
	RevisionID   int64
	GenerationID string
	EmbeddingID  string
	Profile      string
}

func (v VectorIdentity) Matches(other VectorIdentity) bool {
	return v.RevisionID > 0 && v.GenerationID != "" && v.EmbeddingID != "" && v.Profile != "" && v == other
}

type CurrentArticle struct {
	Item     articleDomain.ListItem
	Identity VectorIdentity
}

type KNNRequest struct {
	IndexRequest
	Vector  []float64
	Profile string
}

type HybridConfig struct {
	Enabled          bool
	BM25Candidates   int
	KNNCandidates    int
	EmbeddingTimeout time.Duration
	KNNTimeout       time.Duration
	Provider         string
	Model            string
	Profile          string
	Dimensions       int
}

// Fingerprint 绑定会改变召回或语义身份的全部计划参数，不包含凭据。
func (h HybridConfig) Fingerprint() string {
	payload := struct {
		Version  string
		Config   HybridConfig
		Constant int
		TTL      time.Duration
	}{QueryInputVersion, h, RRFConstant, HybridTTL}
	encoded, _ := json.Marshal(payload)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func ValidQueryVector(vector []float64, dimensions int) bool {
	if dimensions < 1 || len(vector) != dimensions {
		return false
	}
	nonzero := false
	for _, value := range vector {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
		nonzero = nonzero || value != 0
	}
	return nonzero
}

// QueryEmbeddingFailure 仅携带受控错误分类；底层原因不会进入日志或指标。
type QueryEmbeddingFailure struct {
	Class string
	Cause error
}

func (e *QueryEmbeddingFailure) Error() string { return "查询 Embedding 不可用" }
func (e *QueryEmbeddingFailure) Unwrap() error { return e.Cause }

type requestIDContextKey struct{}

func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDContextKey{}, id)
}
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDContextKey{}).(string)
	return id
}
