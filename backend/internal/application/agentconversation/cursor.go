package agentconversation

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"
)

var ErrInvalidCursor = errors.New("会话游标无效")

const ListPurpose = "list"
const HistoryPurpose = "history"

// Cursor 只保存身份、排序边界及绝对期限，不保存标题或正文。
type Cursor struct {
	UserID         string    `json:"u"`
	Purpose        string    `json:"p"`
	ConversationID string    `json:"c,omitempty"`
	ActivityAt     time.Time `json:"a,omitempty"`
	ID             string    `json:"i,omitempty"`
	BeforeSequence int64     `json:"b,omitempty"`
	StartedAt      time.Time `json:"s"`
	ExpiresAt      time.Time `json:"e"`
}

type CursorCodec struct{ aead cipher.AEAD }

func NewCursorCodec(key []byte) (*CursorCodec, error) {
	if len(key) != 32 {
		return nil, ErrInvalidCursor
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrInvalidCursor
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrInvalidCursor
	}
	return &CursorCodec{aead: aead}, nil
}

func (c *CursorCodec) Encode(value Cursor) (string, error) {
	if !validCursor(value) {
		return "", ErrInvalidCursor
	}
	plain, err := json.Marshal(value)
	if err != nil {
		return "", ErrInvalidCursor
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	header := []byte("agent-conversation/v1/" + value.Purpose)
	sealed := c.aead.Seal(nil, nonce, plain, header)
	data := append([]byte{1}, nonce...)
	data = append(data, sealed...)
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func (c *CursorCodec) Decode(raw, userID, purpose, conversationID string, now time.Time) (Cursor, error) {
	if len(raw) == 0 || len(raw) > 4096 {
		return Cursor{}, ErrInvalidCursor
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil || len(data) < 1+c.aead.NonceSize()+c.aead.Overhead() || data[0] != 1 {
		return Cursor{}, ErrInvalidCursor
	}
	plain, err := c.aead.Open(nil, data[1:1+c.aead.NonceSize()], data[1+c.aead.NonceSize():], []byte("agent-conversation/v1/"+purpose))
	if err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	var value Cursor
	if json.Unmarshal(plain, &value) != nil || !validCursor(value) || value.UserID != userID || value.Purpose != purpose || value.ConversationID != conversationID || !now.Before(value.ExpiresAt) {
		return Cursor{}, ErrInvalidCursor
	}
	return value, nil
}

func validCursor(c Cursor) bool {
	if _, err := normalizeID(c.UserID); err != nil || c.StartedAt.IsZero() || !c.ExpiresAt.After(c.StartedAt) || c.ExpiresAt.Sub(c.StartedAt) > 24*time.Hour {
		return false
	}
	switch c.Purpose {
	case ListPurpose:
		_, err := normalizeID(c.ID)
		return err == nil && !c.ActivityAt.IsZero() && c.ConversationID == "" && c.BeforeSequence == 0
	case HistoryPurpose:
		_, err := normalizeID(c.ConversationID)
		return err == nil && c.BeforeSequence > 0 && c.ID == "" && c.ActivityAt.IsZero()
	default:
		return false
	}
}
