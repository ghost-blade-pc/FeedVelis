// Package articleevent 定义与消息中间件无关的文章集成事件契约。
package articleevent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"time"

	"github.com/google/uuid"
)

const (
	PublishedType = "article.published.v1"
	RevisedType   = "article.revised.v1"
	OfflinedType  = "article.offlined.v1"
	DeletedType   = "article.deleted.v1"
	Producer      = "velis.content"
)

var (
	ErrInvalidEvent = errors.New("文章事件契约无效")
	hex32           = regexp.MustCompile(`^[0-9a-f]{32}$`)
	hex64           = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type Aggregate struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Version int64  `json:"version"`
}

type Envelope struct {
	EventID    string          `json:"event_id"`
	EventType  string          `json:"event_type"`
	OccurredAt time.Time       `json:"occurred_at"`
	Producer   string          `json:"producer"`
	TraceID    string          `json:"trace_id"`
	Aggregate  Aggregate       `json:"aggregate"`
	Payload    json.RawMessage `json:"payload"`
}

type PublicPayload struct {
	ArticleID   int64  `json:"article_id"`
	OriginType  string `json:"origin_type"`
	RevisionID  int64  `json:"revision_id"`
	RevisionNo  int    `json:"revision_no"`
	ContentHash string `json:"content_hash"`
	Status      string `json:"status"`
}

type OfflinedPayload struct {
	ArticleID  int64  `json:"article_id"`
	RevisionID int64  `json:"revision_id"`
	Status     string `json:"status"`
	Reason     string `json:"reason"`
}

type DeletedPayload struct {
	ArticleID  int64  `json:"article_id"`
	RevisionID int64  `json:"revision_id"`
	Status     string `json:"status"`
}

type PublicFact struct {
	ArticleID   int64
	OriginType  string
	RevisionID  int64
	RevisionNo  int
	ContentHash string
	LockVersion int64
}

func Published(ctx context.Context, fact PublicFact, at time.Time) (Envelope, error) {
	return publicEvent(ctx, PublishedType, fact, at)
}

func Revised(ctx context.Context, fact PublicFact, at time.Time) (Envelope, error) {
	return publicEvent(ctx, RevisedType, fact, at)
}

func publicEvent(ctx context.Context, eventType string, fact PublicFact, at time.Time) (Envelope, error) {
	return New(ctx, eventType, fact.ArticleID, fact.LockVersion, at, PublicPayload{
		ArticleID: fact.ArticleID, OriginType: fact.OriginType, RevisionID: fact.RevisionID,
		RevisionNo: fact.RevisionNo, ContentHash: fact.ContentHash, Status: "published",
	})
}

func Offlined(ctx context.Context, articleID, revisionID, lockVersion int64, reason string, at time.Time) (Envelope, error) {
	return New(ctx, OfflinedType, articleID, lockVersion, at, OfflinedPayload{ArticleID: articleID, RevisionID: revisionID, Status: "offline", Reason: reason})
}

func Deleted(ctx context.Context, articleID, revisionID, lockVersion int64, at time.Time) (Envelope, error) {
	return New(ctx, DeletedType, articleID, lockVersion, at, DeletedPayload{ArticleID: articleID, RevisionID: revisionID, Status: "deleted"})
}

type TraceKey struct{}

func WithTraceID(ctx context.Context, traceID string) (context.Context, error) {
	if !hex32.MatchString(traceID) {
		return nil, fmt.Errorf("%w: trace_id", ErrInvalidEvent)
	}
	return context.WithValue(ctx, TraceKey{}, traceID), nil
}

func TraceID(ctx context.Context) (string, error) {
	if value, ok := ctx.Value(TraceKey{}).(string); ok && hex32.MatchString(value) {
		return value, nil
	}
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("生成 trace_id: %w", err)
	}
	return hex.EncodeToString(data), nil
}

func New(ctx context.Context, eventType string, articleID, aggregateVersion int64, occurredAt time.Time, payload any) (Envelope, error) {
	traceID, err := TraceID(ctx)
	if err != nil {
		return Envelope{}, err
	}
	eventID, err := uuid.NewV7()
	if err != nil {
		return Envelope{}, fmt.Errorf("生成事件 ID: %w", err)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, fmt.Errorf("编码事件载荷: %w", err)
	}
	envelope := Envelope{
		EventID: eventID.String(), EventType: eventType, OccurredAt: occurredAt.UTC(), Producer: Producer,
		TraceID: traceID, Aggregate: Aggregate{Type: "article", ID: strconv.FormatInt(articleID, 10), Version: aggregateVersion},
		Payload: data,
	}
	if err := envelope.Validate(); err != nil {
		return Envelope{}, err
	}
	return envelope, nil
}

func (e Envelope) Validate() error {
	id, err := uuid.Parse(e.EventID)
	if err != nil || id.Version() != 7 {
		return fmt.Errorf("%w: event_id 必须是 UUIDv7", ErrInvalidEvent)
	}
	if e.Producer != Producer || !hex32.MatchString(e.TraceID) || e.Aggregate.Type != "article" || e.Aggregate.Version <= 0 {
		return ErrInvalidEvent
	}
	articleID, err := strconv.ParseInt(e.Aggregate.ID, 10, 64)
	if err != nil || articleID <= 0 {
		return fmt.Errorf("%w: aggregate.id", ErrInvalidEvent)
	}
	if e.OccurredAt.IsZero() || e.OccurredAt.Location() != time.UTC {
		return fmt.Errorf("%w: occurred_at 必须是 UTC", ErrInvalidEvent)
	}
	switch e.EventType {
	case PublishedType, RevisedType:
		var payload PublicPayload
		if err := decodeStrict(e.Payload, &payload); err != nil || payload.ArticleID != articleID || payload.RevisionID <= 0 || payload.RevisionNo <= 0 || payload.Status != "published" || (payload.OriginType != "rss" && payload.OriginType != "user") || !hex64.MatchString(payload.ContentHash) {
			return fmt.Errorf("%w: 公开事件 payload", ErrInvalidEvent)
		}
	case OfflinedType:
		var payload OfflinedPayload
		if err := decodeStrict(e.Payload, &payload); err != nil || payload.ArticleID != articleID || payload.RevisionID <= 0 || payload.Status != "offline" || (payload.Reason != "author" && payload.Reason != "admin") {
			return fmt.Errorf("%w: 下架事件 payload", ErrInvalidEvent)
		}
	case DeletedType:
		var payload DeletedPayload
		if err := decodeStrict(e.Payload, &payload); err != nil || payload.ArticleID != articleID || payload.RevisionID <= 0 || payload.Status != "deleted" {
			return fmt.Errorf("%w: 删除事件 payload", ErrInvalidEvent)
		}
	default:
		return fmt.Errorf("%w: 未支持的 event_type", ErrInvalidEvent)
	}
	return nil
}

func Encode(e Envelope) ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(e)
}

func Decode(data []byte) (Envelope, error) {
	var envelope Envelope
	if err := decodeStrict(data, &envelope); err != nil {
		return Envelope{}, fmt.Errorf("%w: %v", ErrInvalidEvent, err)
	}
	if err := envelope.Validate(); err != nil {
		return Envelope{}, err
	}
	return envelope, nil
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("JSON 后存在额外内容")
	}
	return nil
}
