// Package agentrun 定义独立执行回合、执行权、显式重试及资源准入规则。
package agentrun

import (
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
)

type State string

const (
	Accepted              State = "accepted"
	Running               State = "running"
	Succeeded             State = "succeeded"
	Failed                State = "failed"
	Canceled              State = "canceled"
	TimedOut              State = "timed_out"
	MaxAttempts                 = 3
	MaxAccountActive            = 2
	MaxConversationActive       = 1
	LeaseDuration               = 10 * time.Second
)

func (s State) Active() bool { return s == Accepted || s == Running }
func (s State) Terminal() bool {
	return s == Succeeded || s == Failed || s == Canceled || s == TimedOut
}
func (s State) Retryable() bool { return s == Failed || s == Canceled || s == TimedOut }

func (s State) CanTransition(next State) bool {
	return (s == Accepted && (next == Running || next.Retryable())) || (s == Running && next.Terminal())
}

// Constraints 使用已规范化的精确条件；零值表示未指定，不保存模型自由文本。
type Constraints struct {
	Keyword  string `json:"keyword,omitempty"`
	Topic    string `json:"topic,omitempty"`
	SourceID int64  `json:"source_id,omitempty"`
}

func (c Constraints) Empty() bool { return c == (Constraints{}) }

type Input struct {
	MessageID        string      `json:"message_id"`
	ContextArticleID int64       `json:"context_article_id,omitempty"`
	Constraints      Constraints `json:"constraints"`
	RetryOf          string      `json:"retry_of,omitempty"`
}

// Attempt 是同一消息按接受次序排列的历史摘要，包含已锁定的原输入。
type Attempt struct {
	ID    string
	State State
	Input Input
}

func CheckRetry(input Input, history []Attempt) error {
	if len(history) >= MaxAttempts {
		return ErrAttemptLimit
	}
	if len(history) == 0 {
		if input.RetryOf != "" {
			return ErrInputConflict
		}
		return nil
	}
	for _, previous := range history {
		if previous.Input.MessageID != input.MessageID || !previous.State.Retryable() {
			return ErrInputConflict
		}
	}
	latest := history[len(history)-1]
	if input.RetryOf != latest.ID || input.ContextArticleID != latest.Input.ContextArticleID || input.Constraints != latest.Input.Constraints {
		return ErrInputConflict
	}
	return nil
}

type Run struct {
	ID                 string
	UserID             string
	SessionID          string
	ConversationID     string
	Input              Input
	State              State
	CreatedAt          time.Time
	Deadline           time.Time
	FinishedAt         time.Time
	OwnerToken         string
	LeaseUntil         time.Time
	AssistantReserved  bool
	AssistantMessageID string
	ErrorCode          Code
}

func validID(raw string) bool {
	id, err := account.ParseUUID(raw)
	return err == nil && !id.IsZero() && id.String() == raw
}

func New(id, userID, sessionID, conversationID string, input Input, now time.Time, timeout time.Duration) (Run, error) {
	if !validID(id) || !validID(userID) || !validID(sessionID) || !validID(conversationID) || !validID(input.MessageID) ||
		(input.RetryOf != "" && !validID(input.RetryOf)) || input.ContextArticleID < 0 || input.Constraints.SourceID < 0 ||
		now.IsZero() || timeout < 5*time.Second || timeout > 120*time.Second {
		return Run{}, ErrInvalidInput
	}
	return Run{ID: id, UserID: userID, SessionID: sessionID, ConversationID: conversationID, Input: input,
		State: Accepted, CreatedAt: now.UTC(), Deadline: now.Add(timeout).UTC(), AssistantReserved: true}, nil
}

func (r Run) Expired(now time.Time) bool { return !now.Before(r.Deadline) }

func (r Run) CheckOwner(token string, now time.Time) error {
	if r.State != Running || token == "" || token != r.OwnerToken || now.IsZero() || !now.Before(r.LeaseUntil) {
		return ErrExecutionLost
	}
	if r.Expired(now) {
		return ErrDeadline
	}
	return nil
}

func (r *Run) Claim(token string, now time.Time) error {
	if r.State != Accepted || !validID(token) || now.IsZero() || now.Before(r.CreatedAt) {
		return ErrExecutionLost
	}
	if r.Expired(now) {
		return ErrDeadline
	}
	r.State, r.OwnerToken = Running, token
	r.LeaseUntil = minTime(now.Add(LeaseDuration), r.Deadline).UTC()
	return nil
}

func (r *Run) Renew(token string, now time.Time) error {
	if err := r.CheckOwner(token, now); err != nil {
		return err
	}
	next := minTime(now.Add(LeaseDuration), r.Deadline).UTC()
	if next.After(r.LeaseUntil) {
		r.LeaseUntil = next
	}
	return nil
}

// Finish 提交非成功终态；成功只能通过 Complete 检查执行权。
func (r *Run) Finish(next State, code Code, assistantID string, now time.Time) error {
	if next == Succeeded {
		return ErrStateConflict
	}
	return r.finish(next, code, assistantID, now)
}

// Complete 仍需由应用在同一事务中校验完整回答、授权、配额并保存助手消息。
func (r *Run) Complete(token, assistantID string, now time.Time) error {
	if err := r.CheckOwner(token, now); err != nil {
		return err
	}
	return r.finish(Succeeded, "", assistantID, now)
}

func (r *Run) finish(next State, code Code, assistantID string, now time.Time) error {
	if !r.State.CanTransition(next) || !next.Terminal() || now.IsZero() || now.Before(r.CreatedAt) {
		return ErrStateConflict
	}
	if next == Succeeded {
		if code != "" || !validID(assistantID) || !r.AssistantReserved {
			return ErrInvalidInput
		}
		if r.Expired(now) {
			return ErrDeadline
		}
	} else if assistantID != "" || !code.Valid() {
		return ErrInvalidInput
	}
	r.State, r.ErrorCode, r.AssistantMessageID = next, code, assistantID
	r.FinishedAt, r.AssistantReserved = now.UTC(), false
	r.OwnerToken, r.LeaseUntil = "", time.Time{}
	return nil
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// CheckAdmission 在账户锁下检查，预留不占用消息序号或公开计数。
func CheckAdmission(accountActive, conversationActive, messageCount, reservations, maxMessages int) error {
	if accountActive < 0 || conversationActive < 0 || messageCount < 0 || reservations < 0 || maxMessages < 1 {
		return ErrInvalidInput
	}
	if accountActive >= MaxAccountActive || conversationActive >= MaxConversationActive {
		return ErrInProgress
	}
	if messageCount >= maxMessages || reservations >= maxMessages-messageCount {
		return ErrMessageLimit
	}
	return nil
}

// CheckAssistantCommit 将一个预留兑换为消息，降低配额后仍按当前上限拒绝。
func CheckAssistantCommit(messageCount, reservations, maxMessages int) error {
	if messageCount < 0 || reservations < 1 || maxMessages < 1 {
		return ErrInvalidInput
	}
	if messageCount >= maxMessages || reservations > maxMessages-messageCount {
		return ErrMessageLimit
	}
	return nil
}
