// Package agentconversation 编排账户私有会话的事务、幂等和分页。
package agentconversation

import (
	"context"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/idempotency"
	conversation "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentconversation"
)

const (
	CreateOperation = "agent.conversation.create"
	AppendOperation = "agent.message.append"
	RenameOperation = "agent.conversation.rename"
	ResourceType    = "agent_conversation"
)

// UserState 必须在写事务最先取得；Now 为数据库服务端时间。
type UserState struct {
	ConversationCount int
	Now               time.Time
}

type ListBoundary struct {
	ActivityAt time.Time
	ID         string
}

type Repository interface {
	LockUser(context.Context, string) (UserState, error)
	Find(context.Context, string, string, bool) (conversation.Conversation, error)
	WasDeleted(context.Context, string, string) (bool, error)
	CreateResource(context.Context, idempotency.Identity, time.Time) (string, error)
	InsertConversation(context.Context, conversation.Conversation) error
	SaveTitle(context.Context, conversation.Conversation) error
	AppendMessage(context.Context, string, conversation.Message) error
	DeleteConversation(context.Context, conversation.Conversation, time.Time) error
	List(context.Context, string, *ListBoundary, int) ([]conversation.Conversation, error)
	Messages(context.Context, string, int64, int) ([]conversation.Message, error)
}

type ReadSnapshot interface {
	WithinReadSnapshot(context.Context, func(context.Context, bool) error) error
}
