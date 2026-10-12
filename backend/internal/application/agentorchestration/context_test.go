package agentorchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	agent "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentrun"
	conversation "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentconversation"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentevidence"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
)

type contextSourceStub struct {
	turns              []agent.CompletedTurn
	user, conversation string
	limit              int
}

func (s *contextSourceStub) RecentSuccessful(_ context.Context, user, cid string, limit int) ([]agent.CompletedTurn, error) {
	s.user = user
	s.conversation = cid
	s.limit = limit
	return append([]agent.CompletedTurn{}, s.turns...), nil
}
func contextQuestion() conversation.Message {
	return conversation.Message{ConversationID: testRun, Role: conversation.RoleUser, Sequence: 100, Content: "解释上一轮文章的实现细节"}
}
func contextTurns(n int) []agent.CompletedTurn {
	result := []agent.CompletedTurn{}
	for i := 1; i <= n; i++ {
		result = append(result, agent.CompletedTurn{RunID: fmt.Sprint(i), Question: conversation.Message{ConversationID: testRun, Role: conversation.RoleUser, Sequence: int64(i), Content: fmt.Sprint("历史问题", i)}, Answer: agentevidence.Answer{Version: 1, Kind: agentevidence.AnswerArticle, Blocks: []agentevidence.Block{{ID: "b", Text: "历史解释", EvidenceIDs: []string{"old-evidence"}}}, References: []agentevidence.Reference{{EvidenceID: "old-evidence", Number: 1, Kind: agentevidence.Article, ArticleID: 1, RevisionID: 2}}, Cards: []agentevidence.Card{{Number: 1, ArticleID: 1, RevisionID: 2}}}})
	}
	return result
}

func TestContextWholeTurnTrimAndNoQuestionTruncation(t *testing.T) {
	s := &contextSourceStub{turns: contextTurns(8)}
	p := agent.NewProjector(currentStub{7})
	builder := NewContextBuilder(s, agent.EmptyPreferences{}, p, 49152)
	v, err := builder.Build(t.Context(), testOther, testRun, contextQuestion(), 0, agentrun.Constraints{}, 500)
	if err != nil || len(v.History) != 6 || v.History[0].RunID != "3" || v.History[5].RunID != "8" || len(v.Preferences) != 0 || s.user != testOther || s.conversation != testRun || s.limit != 6 {
		t.Fatal(v, err)
	}
	raw, _ := json.Marshal(v)
	builder.maxBytes = len(raw) + 499
	v, err = builder.Build(t.Context(), testOther, testRun, contextQuestion(), 0, agentrun.Constraints{}, 500)
	if err != nil || len(v.History) != 5 || v.History[0].RunID != "4" || len(v.History[0].Answer.Blocks) != 1 {
		t.Fatal("未按完整回合裁剪", v, err)
	}
	q := contextQuestion()
	q.Content = strings.Repeat("🙂", 1000)
	builder.maxBytes = 1000
	if _, err := builder.Build(t.Context(), testOther, testRun, q, 0, agentrun.Constraints{}, 0); agent.SafeCode(err) != agentrun.CodeContextLimit {
		t.Fatal("当前问题被截断", err)
	}
	s.turns[0].Question.ConversationID = testOther
	builder.maxBytes = 49152
	if _, err := builder.Build(t.Context(), testOther, testRun, contextQuestion(), 0, agentrun.Constraints{}, 0); agent.SafeCode(err) != agentrun.CodeDependencyUnavailable {
		t.Fatal("混入他人会话", err)
	}
}

func TestStructuredReferenceReacquiresCurrentEvidence(t *testing.T) {
	history := contextTurns(1)
	id, reason := ResolveArticle(history, 1)
	if id != 1 || reason != "" {
		t.Fatal(id, reason)
	}
	i, r, _, budget := invokerFixture(t)
	if err := ReadCurrentArticle(t.Context(), i, agent.Execution{Scope: agent.Scope{UserID: testRun}}, id, budget); err != nil || r.calls != 1 || budget.Usage().ToolCalls != 1 || len(i.Collection.Evidence) != 1 || i.Collection.Evidence[0].ID == "old-evidence" || i.Collection.Evidence[0].ContentKind != agentevidence.Body {
		t.Fatal("旧证据直接复用", err)
	}
	for _, number := range []int{-1, 2, 11} {
		if _, reason := ResolveArticle(history, number); reason != AmbiguousReference {
			t.Fatal("无效指代")
		}
	}
	history[0].Answer.Cards = append(history[0].Answer.Cards, agentevidence.Card{Number: 2, ArticleID: 3, RevisionID: 4})
	if _, reason := ResolveArticle(history, 0); reason != AmbiguousReference {
		t.Fatal("歧义猜文章")
	}
	history[0].Answer.Cards = nil
	history[0].Answer.References = nil
	if _, reason := ResolveArticle(history, 1); reason != AmbiguousReference {
		t.Fatal("失效来源仍可用于上下文")
	}
}
