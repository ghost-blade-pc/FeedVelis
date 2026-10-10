package agentconversation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/idempotency"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
	conversation "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentconversation"
)

type Limits struct {
	MaxConversations int
	MaxMessages      int
	MaxMessageChars  int
}

func DefaultLimits() Limits {
	return Limits{MaxConversations: 50, MaxMessages: 200, MaxMessageChars: 4000}
}

type Service struct {
	repository  Repository
	tx          ports.TxManager
	snapshot    ReadSnapshot
	idempotency *idempotency.Service
	limits      Limits
}

// NewService 固定使用独立的24小时窗口，不继承文章模块的保留期。
func NewService(repository Repository, tx ports.TxManager, snapshot ReadSnapshot, dedup idempotency.Repository, limits Limits) *Service {
	return &Service{repository: repository, tx: tx, snapshot: snapshot,
		idempotency: idempotency.NewService(dedup, tx, 24*time.Hour), limits: limits}
}

type Result struct {
	Status   int
	Snapshot json.RawMessage
	Replayed bool
}

// Envelope 是内部成功快照；删除后只保留版本、会话引用和终态。
type Envelope struct {
	Version        int             `json:"version"`
	ConversationID string          `json:"conversation_id"`
	Deleted        bool            `json:"deleted,omitempty"`
	Status         int             `json:"status,omitempty"`
	Snapshot       json.RawMessage `json:"snapshot,omitempty"`
}

func normalizeID(raw string) (string, error) {
	id, err := account.ParseUUID(raw)
	if err != nil || id.IsZero() {
		return "", conversation.ErrInvalidInput
	}
	return id.String(), nil
}

func commandIdentity(userID, operation, key string) (idempotency.Identity, error) {
	user, err := normalizeID(userID)
	if err != nil {
		return idempotency.Identity{}, err
	}
	identifier, err := normalizeID(key)
	if err != nil {
		return idempotency.Identity{}, err
	}
	return idempotency.Identity{ActorUserID: user, Operation: operation, Key: identifier}, nil
}

func success(id string, status int, value any) (any, string, string, error) {
	snapshot, err := json.Marshal(value)
	if err != nil {
		return nil, "", "", err
	}
	return Envelope{Version: 1, ConversationID: id, Status: status, Snapshot: snapshot}, ResourceType, id, nil
}

func decodeOutcome(outcome idempotency.Outcome) (Result, error) {
	var envelope Envelope
	if err := json.Unmarshal(outcome.Result, &envelope); err != nil {
		return Result{}, errors.New("会话幂等结果无法解析")
	}
	if envelope.Version != 1 || envelope.ConversationID == "" {
		return Result{}, errors.New("会话幂等结果版本无效")
	}
	if envelope.Deleted {
		return Result{}, conversation.ErrNotFound
	}
	if (envelope.Status != 200 && envelope.Status != 201) || len(envelope.Snapshot) == 0 {
		return Result{}, errors.New("会话幂等结果不完整")
	}
	// JSONB 会重排键并加入空白；统一编码使首次响应与重放具有相同正文。
	decoder := json.NewDecoder(bytes.NewReader(envelope.Snapshot))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return Result{}, errors.New("会话快照无法解析")
	}
	snapshot, err := json.Marshal(value)
	if err != nil {
		return Result{}, errors.New("会话快照无法编码")
	}
	return Result{Status: envelope.Status, Snapshot: snapshot, Replayed: outcome.Replayed}, nil
}

func (s *Service) Create(ctx context.Context, userID, key string, title *string) (Result, error) {
	identity, err := commandIdentity(userID, CreateOperation, key)
	if err != nil {
		return Result{}, err
	}
	normalized, err := conversation.NormalizeTitle(title)
	if err != nil {
		return Result{}, err
	}
	var result Result
	err = s.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		state, err := s.repository.LockUser(ctx, identity.ActorUserID)
		if err != nil {
			return err
		}
		resource, err := s.repository.CreateResource(ctx, identity, state.Now)
		if err != nil {
			return err
		}
		if resource != "" {
			if _, err := s.repository.Find(ctx, identity.ActorUserID, resource, false); err != nil {
				return err
			}
		}
		outcome, err := s.idempotency.Execute(ctx, idempotency.Command{Identity: identity, Payload: struct{ Title string }{normalized}, Now: state.Now}, func(ctx context.Context) (any, string, string, error) {
			if state.ConversationCount >= s.limits.MaxConversations {
				return nil, "", "", conversation.ErrConversationLimit
			}
			id, err := account.NewUUID()
			if err != nil {
				return nil, "", "", err
			}
			c, err := conversation.New(id.String(), identity.ActorUserID, &normalized, state.Now)
			if err != nil {
				return nil, "", "", err
			}
			if err := s.repository.InsertConversation(ctx, c); err != nil {
				return nil, "", "", err
			}
			return success(c.ID, 201, c)
		})
		if err != nil {
			return err
		}
		result, err = decodeOutcome(outcome)
		return err
	})
	return result, err
}

func (s *Service) Append(ctx context.Context, userID, id, key, content string) (Result, error) {
	identity, err := commandIdentity(userID, AppendOperation, key)
	if err != nil {
		return Result{}, err
	}
	identifier, err := normalizeID(id)
	if err != nil {
		return Result{}, err
	}
	normalized, err := conversation.NormalizeContent(content, s.limits.MaxMessageChars)
	if err != nil {
		return Result{}, err
	}
	return s.write(ctx, identity, identifier, struct{ ConversationID, Content string }{identifier, normalized}, func(ctx context.Context, c conversation.Conversation, now time.Time) (any, string, string, error) {
		if c.MessageCount >= s.limits.MaxMessages {
			return nil, "", "", conversation.ErrMessageLimit
		}
		messageID, err := account.NewUUID()
		if err != nil {
			return nil, "", "", err
		}
		m, err := conversation.NewMessage(messageID.String(), c.ID, c.NextSequence, conversation.RoleUser, normalized, s.limits.MaxMessageChars, now)
		if err != nil {
			return nil, "", "", err
		}
		if err := s.repository.AppendMessage(ctx, c.UserID, m); err != nil {
			return nil, "", "", err
		}
		return success(c.ID, 201, m)
	})
}

func (s *Service) Rename(ctx context.Context, userID, id, key, title string, expected int64) (Result, error) {
	identity, err := commandIdentity(userID, RenameOperation, key)
	if err != nil {
		return Result{}, err
	}
	identifier, err := normalizeID(id)
	if err != nil || expected < 1 {
		return Result{}, conversation.ErrInvalidInput
	}
	normalized, err := conversation.NormalizeTitle(&title)
	if err != nil {
		return Result{}, err
	}
	return s.write(ctx, identity, identifier, struct {
		ConversationID, Title string
		Version               int64
	}{identifier, normalized, expected}, func(ctx context.Context, c conversation.Conversation, now time.Time) (any, string, string, error) {
		previous := c.TitleVersion
		if err := c.Rename(normalized, expected, now); err != nil {
			return nil, "", "", err
		}
		if c.TitleVersion != previous {
			if err := s.repository.SaveTitle(ctx, c); err != nil {
				return nil, "", "", err
			}
		}
		return success(c.ID, 200, c)
	})
}

func (s *Service) write(ctx context.Context, identity idempotency.Identity, id string, payload any, execute func(context.Context, conversation.Conversation, time.Time) (any, string, string, error)) (Result, error) {
	var result Result
	err := s.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		state, err := s.repository.LockUser(ctx, identity.ActorUserID)
		if err != nil {
			return err
		}
		if _, err := s.repository.Find(ctx, identity.ActorUserID, id, false); err != nil {
			return err
		}
		outcome, err := s.idempotency.Execute(ctx, idempotency.Command{Identity: identity, Payload: payload, Now: state.Now}, func(ctx context.Context) (any, string, string, error) {
			c, err := s.repository.Find(ctx, identity.ActorUserID, id, true)
			if err != nil {
				return nil, "", "", err
			}
			return execute(ctx, c, state.Now)
		})
		if err != nil {
			return err
		}
		result, err = decodeOutcome(outcome)
		return err
	})
	return result, err
}

func (s *Service) Delete(ctx context.Context, userID, id string) error {
	user, err := normalizeID(userID)
	if err != nil {
		return err
	}
	identifier, err := normalizeID(id)
	if err != nil {
		return err
	}
	return s.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		state, err := s.repository.LockUser(ctx, user)
		if err != nil {
			return err
		}
		c, err := s.repository.Find(ctx, user, identifier, true)
		if errors.Is(err, conversation.ErrNotFound) {
			deleted, checkErr := s.repository.WasDeleted(ctx, user, identifier)
			if checkErr != nil {
				return checkErr
			}
			if deleted {
				return nil
			}
		}
		if err != nil {
			return err
		}
		return s.repository.DeleteConversation(ctx, c, state.Now)
	})
}

func (s *Service) Detail(ctx context.Context, userID, id string) (conversation.Conversation, error) {
	user, err := normalizeID(userID)
	if err != nil {
		return conversation.Conversation{}, err
	}
	identifier, err := normalizeID(id)
	if err != nil {
		return conversation.Conversation{}, err
	}
	var result conversation.Conversation
	err = s.snapshot.WithinReadSnapshot(ctx, func(ctx context.Context, _ bool) error {
		var err error
		result, err = s.repository.Find(ctx, user, identifier, false)
		return err
	})
	return result, err
}

func (s *Service) List(ctx context.Context, userID string, boundary *ListBoundary, limit int) ([]conversation.Conversation, bool, error) {
	user, err := normalizeID(userID)
	if err != nil || limit < 1 || limit > 50 {
		return nil, false, conversation.ErrInvalidInput
	}
	items, err := s.repository.List(ctx, user, boundary, limit+1)
	if err != nil {
		return nil, false, err
	}
	more := len(items) > limit
	if more {
		items = items[:limit]
	}
	return items, more, nil
}

func (s *Service) History(ctx context.Context, userID, id string, before int64, limit int) ([]conversation.Message, bool, error) {
	user, err := normalizeID(userID)
	if err != nil {
		return nil, false, err
	}
	identifier, err := normalizeID(id)
	if err != nil || before < 0 || limit < 1 || limit > 50 {
		return nil, false, conversation.ErrInvalidInput
	}
	var items []conversation.Message
	var more bool
	err = s.snapshot.WithinReadSnapshot(ctx, func(ctx context.Context, _ bool) error {
		if _, err := s.repository.Find(ctx, user, identifier, false); err != nil {
			return err
		}
		var err error
		items, err = s.repository.Messages(ctx, identifier, before, limit+1)
		if err != nil {
			return err
		}
		more = len(items) > limit
		if more {
			items = items[:limit]
		}
		for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
			items[i], items[j] = items[j], items[i]
		}
		return nil
	})
	return items, more, err
}
