// Package agentconversation 定义私有对话、不可变消息及独立文本规则。
package agentconversation

import (
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
)

const DefaultTitle = "新对话"
const MaxTitleChars = 100

var (
	ErrInvalidInput      = errors.New("会话参数无效")
	ErrNotFound          = errors.New("会话不存在")
	ErrTitleVersion      = errors.New("会话标题版本冲突")
	ErrConversationLimit = errors.New("会话数量已达上限")
	ErrMessageLimit      = errors.New("会话消息数量已达上限")
)

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

func (r Role) Valid() bool { return r == RoleUser || r == RoleAssistant }

// NormalizeTitle 区分省略与显式空标题；检查控制字符后才去除首尾空白。
func NormalizeTitle(title *string) (string, error) {
	if title == nil {
		return DefaultTitle, nil
	}
	if !utf8.ValidString(*title) {
		return "", ErrInvalidInput
	}
	for _, ch := range *title {
		if unicode.IsControl(ch) || ch == '\u2028' || ch == '\u2029' {
			return "", ErrInvalidInput
		}
	}
	value := strings.TrimSpace(*title)
	if value == "" || utf8.RuneCountInString(value) > MaxTitleChars {
		return "", ErrInvalidInput
	}
	return value, nil
}

// NormalizeContent 仅统一换行，不修复编码、不修剪正文、不渲染 Markdown。
func NormalizeContent(content string, maxChars int) (string, error) {
	if !utf8.ValidString(content) || strings.ContainsRune(content, 0) || maxChars < 1 {
		return "", ErrInvalidInput
	}
	value := strings.ReplaceAll(strings.ReplaceAll(content, "\r\n", "\n"), "\r", "\n")
	if strings.TrimSpace(value) == "" || utf8.RuneCountInString(value) > maxChars {
		return "", ErrInvalidInput
	}
	return value, nil
}

type Conversation struct {
	ID             string    `json:"id"`
	UserID         string    `json:"-"`
	Title          string    `json:"title"`
	TitleVersion   int64     `json:"title_version"`
	MessageCount   int       `json:"message_count"`
	NextSequence   int64     `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
	LastActivityAt time.Time `json:"last_activity_at"`
}

func New(id, userID string, title *string, now time.Time) (Conversation, error) {
	identifier, err := account.ParseUUID(id)
	if err != nil || identifier.IsZero() {
		return Conversation{}, ErrInvalidInput
	}
	owner, err := account.ParseUUID(userID)
	if err != nil || owner.IsZero() || now.IsZero() {
		return Conversation{}, ErrInvalidInput
	}
	normalized, err := NormalizeTitle(title)
	if err != nil {
		return Conversation{}, err
	}
	return Conversation{ID: identifier.String(), UserID: owner.String(), Title: normalized, TitleVersion: 1, NextSequence: 1, CreatedAt: now.UTC(), LastActivityAt: now.UTC()}, nil
}

func (c *Conversation) Rename(title string, expected int64, now time.Time) error {
	normalized, err := NormalizeTitle(&title)
	if err != nil || now.IsZero() {
		return ErrInvalidInput
	}
	if expected != c.TitleVersion {
		return ErrTitleVersion
	}
	if normalized != c.Title {
		c.Title = normalized
		c.TitleVersion++
		c.Activate(now)
	}
	return nil
}

func (c *Conversation) Activate(now time.Time) {
	if now.After(c.LastActivityAt) {
		c.LastActivityAt = now.UTC()
	}
}

// Message 只在追加时构造；仓储不提供更新或单条删除入口。
type Message struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversation_id"`
	Sequence       int64     `json:"sequence"`
	Role           Role      `json:"role"`
	Content        string    `json:"content"`
	CreatedAt      time.Time `json:"created_at"`
}

func NewMessage(id, conversationID string, sequence int64, role Role, content string, maxChars int, now time.Time) (Message, error) {
	identifier, err := account.ParseUUID(id)
	if err != nil || identifier.IsZero() {
		return Message{}, ErrInvalidInput
	}
	parent, err := account.ParseUUID(conversationID)
	if err != nil || parent.IsZero() || sequence < 1 || !role.Valid() || now.IsZero() {
		return Message{}, ErrInvalidInput
	}
	value, err := NormalizeContent(content, maxChars)
	if err != nil {
		return Message{}, err
	}
	return Message{ID: identifier.String(), ConversationID: parent.String(), Sequence: sequence, Role: role, Content: value, CreatedAt: now.UTC()}, nil
}
