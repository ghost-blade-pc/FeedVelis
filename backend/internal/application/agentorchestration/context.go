package agentorchestration

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"unicode/utf8"

	agent "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentrun"
	conversation "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentconversation"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentevidence"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
)

type ContextBuilder struct {
	source      agent.ContextSource
	preferences agent.PreferenceSource
	projector   agent.VisibilityProjector
	maxBytes    int
}

func NewContextBuilder(source agent.ContextSource, preferences agent.PreferenceSource, projector agent.VisibilityProjector, maxBytes int) *ContextBuilder {
	return &ContextBuilder{source: source, preferences: preferences, projector: projector, maxBytes: maxBytes}
}

// Build 把最近成功回合按顺序放入上下文，超额时只剔除最旧完整回合。
// reservedBytes 由适配器提供系统策略、工具 schema 和协议包装的实际字节预留。
func (b *ContextBuilder) Build(ctx context.Context, userID, conversationID string, question conversation.Message, contextArticleID int64, constraints agentrun.Constraints, reservedBytes int) (agent.Context, error) {
	if question.ConversationID != conversationID || question.Role != conversation.RoleUser || !utf8.ValidString(question.Content) || strings.ContainsRune(question.Content, 0) || strings.TrimSpace(question.Content) == "" || contextArticleID < 0 || reservedBytes < 0 {
		return agent.Context{}, agentrun.ErrInvalidInput
	}
	value := agent.Context{Question: question.Content, History: []agent.CompletedTurn{}, Preferences: []string{}, ContextArticleID: contextArticleID, Constraints: constraints}
	fits := func() bool {
		raw, err := json.Marshal(value)
		return err == nil && reservedBytes <= b.maxBytes && len(raw) <= b.maxBytes-reservedBytes
	}
	if !fits() {
		return agent.Context{}, &agentrun.Error{Code: agentrun.CodeContextLimit}
	}
	if b.preferences != nil {
		prefs, err := b.preferences.Preferences(ctx, userID)
		if err != nil {
			return agent.Context{}, &agentrun.Error{Code: agentrun.CodeDependencyUnavailable}
		}
		value.Preferences = prefs
	}
	if b.source != nil {
		turns, err := b.source.RecentSuccessful(ctx, userID, conversationID, 6)
		if err != nil {
			return agent.Context{}, &agentrun.Error{Code: agentrun.CodeDependencyUnavailable}
		}
		for _, turn := range turns {
			if turn.Question.ConversationID != conversationID || turn.Question.Role != conversation.RoleUser || turn.RunID == "" {
				return agent.Context{}, &agentrun.Error{Code: agentrun.CodeDependencyUnavailable}
			}
		}
		sort.Slice(turns, func(i, j int) bool { return turns[i].Question.Sequence < turns[j].Question.Sequence })
		if len(turns) > 6 {
			turns = turns[len(turns)-6:]
		}
		for _, turn := range turns {
			if b.projector == nil {
				return agent.Context{}, &agentrun.Error{Code: agentrun.CodeDependencyUnavailable}
			}
			a, err := b.projector.Answer(ctx, turn.Answer)
			if err != nil {
				return agent.Context{}, err
			}
			turn.Answer = a
			value.History = append(value.History, turn)
		}
	}
	for !fits() && len(value.History) > 0 {
		value.History = value.History[1:]
	}
	if !fits() {
		return agent.Context{}, &agentrun.Error{Code: agentrun.CodeContextLimit}
	}
	return value, nil
}

// ResolveArticle 仅从上一成功回合的结构化引用确定文章；返回身份仍须重新 get_article。
// number=0 只在该回合恰好有一个仍有效来源时允许省略编号。
func ResolveArticle(history []agent.CompletedTurn, number int) (int64, ClarificationReason) {
	if len(history) == 0 || number < 0 || number > agentevidence.MaxSources {
		return 0, AmbiguousReference
	}
	a := history[len(history)-1].Answer
	if number == 0 {
		if len(a.Cards) != 1 {
			return 0, AmbiguousReference
		}
		number = a.Cards[0].Number
	}
	for _, card := range a.Cards {
		if card.Number == number {
			for _, ref := range a.References {
				if ref.Number == number && ref.ArticleID == card.ArticleID && ref.RevisionID == card.RevisionID {
					return card.ArticleID, ""
				}
			}
		}
	}
	return 0, AmbiguousReference
}

func ReadCurrentArticle(ctx context.Context, invoker *Invoker, execution agent.Execution, articleID int64, budget *agentrun.Budget) error {
	body, err := json.Marshal(map[string]int64{"article_id": articleID})
	if err != nil {
		return err
	}
	_, err = invoker.InvokeBatch(ctx, execution, []agent.ToolCall{{Name: "get_article", Arguments: body}}, budget)
	return err
}
