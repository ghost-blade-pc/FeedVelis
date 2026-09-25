package articlesearch

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
)

const maxCursorBytes = 16 * 1024

type CursorCodec struct {
	key []byte
}

type cursorPayload struct {
	Version          int          `json:"v"`
	QueryPlanVersion int          `json:"query_plan_version"`
	Fingerprint      string       `json:"fingerprint"`
	PITID            string       `json:"pit_id"`
	After            SortPosition `json:"after"`
	ExpiresAt        time.Time    `json:"expires_at"`
}

func NewCursorCodec(key []byte) (*CursorCodec, error) {
	if len(key) < 32 {
		return nil, errors.New("游标签名键至少 32 字节")
	}
	return &CursorCodec{key: append([]byte(nil), key...)}, nil
}

func QueryFingerprint(query Query) string {
	payload := struct {
		Plan    int     `json:"plan"`
		Q       string  `json:"q"`
		Filters Filters `json:"filters"`
	}{QueryPlanVersion, query.Q, query.Filters}
	encoded, _ := json.Marshal(payload)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func (c *CursorCodec) Encode(query Query, pitID string, after SortPosition, expiresAt time.Time) (string, error) {
	if strings.TrimSpace(pitID) == "" || !after.Valid() || expiresAt.IsZero() {
		return "", controlled(CodeInvalidCursor, errors.New("游标状态无效"))
	}
	payload, err := json.Marshal(cursorPayload{
		Version: 1, QueryPlanVersion: QueryPlanVersion, Fingerprint: QueryFingerprint(query),
		PITID: pitID, After: after, ExpiresAt: expiresAt.UTC(),
	})
	if err != nil {
		return "", controlled(CodeInvalidCursor, err)
	}
	signature := c.sign(payload)
	token := base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(signature)
	if len(token) > maxCursorBytes {
		return "", controlled(CodeInvalidCursor, errors.New("游标过长"))
	}
	return token, nil
}

func (c *CursorCodec) Decode(query Query, token string, now time.Time) (string, SortPosition, error) {
	if token == "" || len(token) > maxCursorBytes {
		return "", SortPosition{}, controlled(CodeInvalidCursor, errors.New("游标为空或过长"))
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return "", SortPosition{}, controlled(CodeInvalidCursor, errors.New("游标格式无效"))
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(parts[0])
	if err != nil || len(payload) > maxCursorBytes {
		return "", SortPosition{}, controlled(CodeInvalidCursor, errors.New("游标编码无效"))
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if err != nil || !hmac.Equal(signature, c.sign(payload)) {
		return "", SortPosition{}, controlled(CodeInvalidCursor, errors.New("游标签名无效"))
	}
	var decoded cursorPayload
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return "", SortPosition{}, controlled(CodeInvalidCursor, errors.New("游标载荷无效"))
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return "", SortPosition{}, controlled(CodeInvalidCursor, errors.New("游标包含额外内容"))
	}
	if decoded.Version != 1 || decoded.QueryPlanVersion != QueryPlanVersion ||
		decoded.Fingerprint != QueryFingerprint(query) || strings.TrimSpace(decoded.PITID) == "" ||
		!decoded.After.Valid() || decoded.ExpiresAt.IsZero() || !now.Before(decoded.ExpiresAt) {
		return "", SortPosition{}, controlled(CodeInvalidCursor, errors.New("游标版本、查询或状态无效"))
	}
	return decoded.PITID, decoded.After, nil
}

func (c *CursorCodec) sign(payload []byte) []byte {
	mac := hmac.New(sha256.New, c.key)
	_, _ = mac.Write(payload)
	return mac.Sum(nil)
}
