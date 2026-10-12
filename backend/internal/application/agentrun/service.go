package agentrun

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"time"

	conversationApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentconversation"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/idempotency"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
	conversation "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentconversation"
	run "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
)

type Caller struct{ UserID, SessionID string }
type Config struct {
	Accepting   bool
	Timeout     time.Duration
	MaxMessages int
}
type Service struct {
	repository    AcceptanceRepository
	conversations conversationApp.Repository
	tx            ports.TxManager
	snapshot      conversationApp.ReadSnapshot
	authorization CurrentAuthorization
	idempotency   *idempotency.Service
	config        Config
	projector     VisibilityProjector
	cursor        *conversationApp.CursorCodec
	cursorTTL     time.Duration
	accepting     atomic.Bool
}

func NewService(repo AcceptanceRepository, conversations conversationApp.Repository, tx ports.TxManager, snapshot conversationApp.ReadSnapshot, auth CurrentAuthorization, dedup idempotency.Repository, config Config) *Service {
	s := &Service{repository: repo, conversations: conversations, tx: tx, snapshot: snapshot, authorization: auth,
		idempotency: idempotency.NewService(dedup, tx, 24*time.Hour), config: config}
	s.accepting.Store(config.Accepting)
	return s
}

func (s *Service) StopAccepting() { s.accepting.Store(false) }

type AcceptedSnapshot struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversation_id"`
	UserMessageID  string    `json:"user_message_id"`
	Status         run.State `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
}
type Acceptance struct {
	Snapshot AcceptedSnapshot
	Replayed bool
}

func normalizeCaller(c Caller) (Caller, error) {
	user, err := normalizedID(c.UserID)
	if err != nil {
		return Caller{}, &run.Error{Code: run.CodeAuthInvalid}
	}
	session, err := normalizedID(c.SessionID)
	if err != nil {
		return Caller{}, &run.Error{Code: run.CodeAuthInvalid}
	}
	return Caller{user, session}, nil
}

func (s *Service) Accept(ctx context.Context, caller Caller, conversationID, key string, command Command) (Acceptance, error) {
	caller, err := normalizeCaller(caller)
	if err != nil {
		return Acceptance{}, err
	}
	conversationID, err = normalizedID(conversationID)
	if err != nil {
		return Acceptance{}, err
	}
	key, err = normalizedID(key)
	if err != nil {
		return Acceptance{}, err
	}
	input, err := NormalizeCommand(command)
	if err != nil {
		return Acceptance{}, err
	}
	var result Acceptance
	err = s.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		state, err := s.repository.LockUser(ctx, caller.UserID)
		if err != nil {
			return err
		}
		if err := s.authorization.CheckSession(ctx, caller.UserID, caller.SessionID); err != nil {
			return err
		}
		// 归属先于幂等重放与键冲突，已删除会话不能返回旧私密快照。
		if _, err := s.conversations.Find(ctx, caller.UserID, conversationID, false); err != nil {
			return err
		}
		outcome, err := s.idempotency.Execute(ctx, idempotency.Command{Identity: idempotency.Identity{ActorUserID: caller.UserID, Operation: AcceptOperation, Key: key},
			Payload: struct {
				ConversationID string
				Input          run.Input
			}{conversationID, input}, Now: state.Now}, func(ctx context.Context) (any, string, string, error) {
			c, err := s.repository.LockConversation(ctx, caller.UserID, conversationID)
			if err != nil {
				return nil, "", "", err
			}
			message, err := s.repository.Message(ctx, conversationID, input.MessageID)
			if err != nil {
				return nil, "", "", err
			}
			if message.Role != conversation.RoleUser || message.Sequence != c.NextSequence-1 {
				return nil, "", "", run.ErrInputConflict
			}
			history, err := s.repository.Attempts(ctx, conversationID, input.MessageID)
			if err != nil {
				return nil, "", "", err
			}
			if err := run.CheckRetry(input, history); err != nil {
				return nil, "", "", err
			}
			active, err := s.repository.AccountActive(ctx, caller.UserID)
			if err != nil {
				return nil, "", "", err
			}
			conversationActive, err := s.repository.ConversationActive(ctx, conversationID)
			if err != nil {
				return nil, "", "", err
			}
			if err := run.CheckAdmission(active, conversationActive, c.MessageCount, conversationActive, s.config.MaxMessages); err != nil {
				return nil, "", "", err
			}
			if !s.accepting.Load() {
				return nil, "", "", &run.Error{Code: run.CodeGenerationUnavailable}
			}
			id, err := account.NewUUID()
			if err != nil {
				return nil, "", "", err
			}
			r, err := run.New(id.String(), caller.UserID, caller.SessionID, conversationID, input, state.Now, s.config.Timeout)
			if err != nil {
				return nil, "", "", err
			}
			if err := s.repository.InsertAccepted(ctx, r); err != nil {
				return nil, "", "", err
			}
			accepted := AcceptedSnapshot{ID: r.ID, ConversationID: r.ConversationID, UserMessageID: r.Input.MessageID, Status: run.Accepted, CreatedAt: r.CreatedAt}
			snapshot, err := json.Marshal(accepted)
			if err != nil {
				return nil, "", "", err
			}
			return conversationApp.Envelope{Version: 1, ConversationID: conversationID, Status: 202, Snapshot: snapshot}, conversationApp.ResourceType, conversationID, nil
		})
		if err != nil {
			return err
		}
		var envelope conversationApp.Envelope
		if json.Unmarshal(outcome.Result, &envelope) != nil || envelope.Version != 1 || envelope.ConversationID != conversationID {
			return &run.Error{Code: run.CodeInternal}
		}
		if envelope.Deleted {
			return conversation.ErrNotFound
		}
		if envelope.Status != 202 || json.Unmarshal(envelope.Snapshot, &result.Snapshot) != nil || result.Snapshot.Status != run.Accepted {
			return &run.Error{Code: run.CodeInternal}
		}
		result.Replayed = outcome.Replayed
		return nil
	})
	return result, err
}
