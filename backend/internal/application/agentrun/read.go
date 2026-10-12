package agentrun

import (
	"context"
	"time"

	conversationApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentconversation"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentevidence"
	run "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
)

type Summary struct {
	AcceptedSnapshot
	AssistantMessageID *string    `json:"assistant_message_id"`
	ErrorCode          *run.Code  `json:"error_code"`
	FinishedAt         *time.Time `json:"finished_at"`
	Partial            bool       `json:"partial"`
}
type Detail struct {
	Summary
	Answer *agentevidence.Answer `json:"answer"`
	Usage  run.Usage             `json:"usage"`
}
type Page struct {
	Items      []Summary `json:"items"`
	NextCursor *string   `json:"next_cursor"`
	HasMore    bool      `json:"has_more"`
}

func summarize(record Record) Summary {
	v := record.Run
	result := Summary{AcceptedSnapshot: AcceptedSnapshot{ID: v.ID, ConversationID: v.ConversationID, UserMessageID: v.Input.MessageID, Status: v.State, CreatedAt: v.CreatedAt}, Partial: record.Partial}
	if v.AssistantMessageID != "" {
		id := v.AssistantMessageID
		result.AssistantMessageID = &id
	}
	if v.ErrorCode != "" {
		code := v.ErrorCode
		if !code.Valid() {
			code = run.CodeInternal
		}
		result.ErrorCode = &code
	}
	if !v.FinishedAt.IsZero() {
		finished := v.FinishedAt
		result.FinishedAt = &finished
	}
	return result
}

func (s *Service) WithPaging(codec *conversationApp.CursorCodec, ttl time.Duration) *Service {
	s.cursor, s.cursorTTL = codec, ttl
	return s
}
func (s *Service) WithProjector(projector VisibilityProjector) *Service {
	s.projector = projector
	return s
}

func (s *Service) authorizeConversation(ctx context.Context, caller Caller, id string) error {
	if err := s.authorization.CheckSession(ctx, caller.UserID, caller.SessionID); err != nil {
		return err
	}
	_, err := s.conversations.Find(ctx, caller.UserID, id, false)
	return err
}

func (s *Service) Detail(ctx context.Context, caller Caller, conversationID, runID string) (Detail, error) {
	caller, err := normalizeCaller(caller)
	if err != nil {
		return Detail{}, err
	}
	conversationID, err = normalizedID(conversationID)
	if err != nil {
		return Detail{}, err
	}
	runID, err = normalizedID(runID)
	if err != nil {
		return Detail{}, err
	}
	var result Detail
	err = s.snapshot.WithinReadSnapshot(ctx, func(ctx context.Context, _ bool) error {
		if err := s.authorizeConversation(ctx, caller, conversationID); err != nil {
			return err
		}
		record, err := s.repository.Find(ctx, Scope{caller.UserID, conversationID, runID})
		if err != nil {
			return err
		}
		result = Detail{Summary: summarize(record), Usage: record.Usage}
		if record.Answer != nil {
			// 缺少统一可见性投影时禁止返回数据库原文。
			if s.projector == nil {
				return &run.Error{Code: run.CodeDependencyUnavailable}
			}
			answer, err := s.projector.Answer(ctx, *record.Answer)
			if err != nil {
				return err
			}
			result.Answer = &answer
		}
		return nil
	})
	return result, err
}

func (s *Service) List(ctx context.Context, caller Caller, conversationID, rawCursor string, limit int, now time.Time) (Page, error) {
	caller, err := normalizeCaller(caller)
	if err != nil {
		return Page{}, err
	}
	conversationID, err = normalizedID(conversationID)
	if err != nil {
		return Page{}, err
	}
	if limit == 0 {
		limit = 20
	}
	if limit < 1 || limit > 50 || now.IsZero() {
		return Page{}, run.ErrInvalidInput
	}
	if s.cursor == nil || s.cursorTTL < time.Minute || s.cursorTTL > 24*time.Hour {
		return Page{}, &run.Error{Code: run.CodeInternal}
	}
	var result Page
	err = s.snapshot.WithinReadSnapshot(ctx, func(ctx context.Context, _ bool) error {
		if err := s.authorizeConversation(ctx, caller, conversationID); err != nil {
			return err
		}
		position := conversationApp.Cursor{UserID: caller.UserID, Purpose: conversationApp.RunListPurpose, ConversationID: conversationID, StartedAt: now.UTC(), ExpiresAt: now.Add(s.cursorTTL).UTC()}
		var boundary *Boundary
		if rawCursor != "" {
			var err error
			position, err = s.cursor.Decode(rawCursor, caller.UserID, conversationApp.RunListPurpose, conversationID, now)
			if err != nil {
				return err
			}
			boundary = &Boundary{CreatedAt: position.ActivityAt, ID: position.ID}
		}
		records, err := s.repository.List(ctx, caller.UserID, conversationID, boundary, limit+1)
		if err != nil {
			return err
		}
		result = Page{Items: []Summary{}, HasMore: len(records) > limit}
		if result.HasMore {
			records = records[:limit]
		}
		for _, record := range records {
			result.Items = append(result.Items, summarize(record))
		}
		if result.HasMore {
			last := records[len(records)-1].Run
			position.ActivityAt, position.ID = last.CreatedAt, last.ID
			next, err := s.cursor.Encode(position)
			if err != nil {
				return err
			}
			result.NextCursor = &next
		}
		return nil
	})
	return result, err
}

// Cleanup 一次最多处理100个终态回合，不在调用内无界循环。
func (s *Service) Cleanup(ctx context.Context) (int, error) {
	count := 0
	err := s.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		var err error
		count, err = s.repository.Cleanup(ctx, 100)
		return err
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}
