package recommendation

import (
	"bytes"
	"compress/flate"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
)

const maxRecommendCursorBytes = 16 * 1024
const maxFrozenCandidates = 200
const maxRecommendPlainBytes = 128 * 1024

var ErrInvalidCursor = errors.New("推荐游标无效")

type CursorState struct {
	Version       int                   `json:"version"`
	Identity      string                `json:"identity"`
	Mode          string                `json:"mode"`
	Degraded      bool                  `json:"degraded"`
	DegradeReason string                `json:"degrade_reason,omitempty"`
	ExpiresAt     time.Time             `json:"expires_at"`
	StartedAt     time.Time             `json:"started_at"`
	VectorProfile string                `json:"vector_profile,omitempty"`
	Items         []FrozenItem          `json:"items"`
	Offset        int                   `json:"offset"`
	Latest        *articleDomain.Cursor `json:"latest,omitempty"`
	LatestOn      bool                  `json:"latest_on"`
	LatestEnd     bool                  `json:"latest_end"`
}

// FrozenItem 只保留续页需要的文章身份和原因，不保存排序分数或偏好证据。
type FrozenItem struct {
	ID       int64          `json:"i"`
	Reason   string         `json:"r"`
	Identity FrozenIdentity `json:"v,omitempty"`
	Semantic bool           `json:"s,omitempty"`
}

type FrozenIdentity struct {
	RevisionID   int64  `json:"r"`
	GenerationID string `json:"g"`
	EmbeddingID  string `json:"e"`
}

func (v FrozenIdentity) Matches(current articlesearch.VectorIdentity, profile string) bool {
	return v.RevisionID > 0 && v.GenerationID != "" && v.EmbeddingID != "" && profile != "" &&
		v.RevisionID == current.RevisionID && v.GenerationID == current.GenerationID &&
		v.EmbeddingID == current.EmbeddingID && profile == current.Profile
}

func Freeze(items []RankedItem) []FrozenItem {
	result := make([]FrozenItem, 0, len(items))
	for _, item := range items {
		frozen := FrozenItem{ID: item.ID, Reason: item.Reason, Semantic: item.Semantic}
		if item.Semantic {
			frozen.Identity = FrozenIdentity{RevisionID: item.Identity.RevisionID, GenerationID: item.Identity.GenerationID, EmbeddingID: item.Identity.EmbeddingID}
		}
		result = append(result, frozen)
	}
	return result
}

type CursorCodec struct{ aead cipher.AEAD }

func NewCursorCodec(key []byte) (*CursorCodec, error) {
	if len(key) < 32 {
		return nil, errors.New("推荐游标密钥至少 32 字节")
	}
	derived, err := hkdf.Key(sha256.New, key, nil, "velis/recommendation/cursor-v1", 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(derived)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &CursorCodec{aead: aead}, nil
}

func IdentityBinding(userID string) string {
	if userID == "" {
		return "anonymous"
	}
	digest := sha256.Sum256([]byte("velis/recommendation/user-v1:" + userID))
	return hex.EncodeToString(digest[:])
}

func (c *CursorCodec) Encode(state CursorState) (string, error) {
	if c == nil || !validCursorState(state, time.Time{}) {
		return "", ErrInvalidCursor
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return "", ErrInvalidCursor
	}
	if len(raw) > maxRecommendPlainBytes {
		return "", ErrInvalidCursor
	}
	var compressed bytes.Buffer
	writer, err := flate.NewWriter(&compressed, flate.BestSpeed)
	if err != nil {
		return "", err
	}
	if _, err := writer.Write(raw); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nonce, nonce, compressed.Bytes(), []byte("recommendation-v1"))
	token := "r1." + base64.RawURLEncoding.EncodeToString(sealed)
	if len(token) > maxRecommendCursorBytes {
		return "", ErrInvalidCursor
	}
	return token, nil
}

func (c *CursorCodec) Decode(token, userID string, now time.Time) (CursorState, error) {
	if c == nil || !strings.HasPrefix(token, "r1.") || len(token) > maxRecommendCursorBytes {
		return CursorState{}, ErrInvalidCursor
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimPrefix(token, "r1."))
	if err != nil || len(data) < c.aead.NonceSize()+c.aead.Overhead() {
		return CursorState{}, ErrInvalidCursor
	}
	compressed, err := c.aead.Open(nil, data[:c.aead.NonceSize()], data[c.aead.NonceSize():], []byte("recommendation-v1"))
	if err != nil {
		return CursorState{}, ErrInvalidCursor
	}
	reader := flate.NewReader(bytes.NewReader(compressed))
	defer reader.Close()
	raw, err := io.ReadAll(io.LimitReader(reader, maxRecommendPlainBytes+1))
	if err != nil || len(raw) > maxRecommendPlainBytes {
		return CursorState{}, ErrInvalidCursor
	}
	var state CursorState
	if err := json.Unmarshal(raw, &state); err != nil || state.Identity != IdentityBinding(userID) || !validCursorState(state, now) {
		return CursorState{}, ErrInvalidCursor
	}
	return state, nil
}

func validCursorState(state CursorState, now time.Time) bool {
	if state.Version != RankingVersion || state.Identity == "" || state.ExpiresAt.IsZero() || state.Offset < 0 || state.Offset > len(state.Items) || len(state.Items) > maxFrozenCandidates ||
		state.Mode != "personalized" && state.Mode != "cold_start" && state.Mode != "latest_fallback" ||
		state.DegradeReason != "" && state.DegradeReason != "search_unavailable" && state.DegradeReason != "semantic_unavailable" && state.DegradeReason != "candidate_shortage" ||
		!now.IsZero() && (!now.Before(state.ExpiresAt) || state.ExpiresAt.After(now.Add(10*time.Minute))) {
		return false
	}
	seen := make(map[int64]bool, len(state.Items))
	for _, item := range state.Items {
		if item.ID <= 0 || seen[item.ID] || !validReason(item.Reason) {
			return false
		}
		seen[item.ID] = true
		if item.Semantic && (state.VectorProfile == "" || item.Identity.RevisionID <= 0 || item.Identity.GenerationID == "" || item.Identity.EmbeddingID == "") || !item.Semantic && item.Identity != (FrozenIdentity{}) {
			return false
		}
	}
	return state.Latest == nil || state.Latest.ArticleID > 0 && !state.Latest.SortAt.IsZero()
}

func validReason(reason string) bool {
	switch reason {
	case "keyword_match", "topic_match", "similar_content", "recent", "latest_fallback":
		return true
	default:
		return false
	}
}
