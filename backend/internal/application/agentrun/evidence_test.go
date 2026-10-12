package agentrun

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentevidence"
	run "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
	"testing"
)

type factStub struct {
	values []ArticleFact
	err    error
	calls  int
}

func (f *factStub) Current(_ context.Context, _ []ArticleIdentity) ([]ArticleFact, error) {
	f.calls++
	return f.values, f.err
}
func answerFixture() agentevidence.Answer {
	return agentevidence.Answer{Version: 1, Kind: agentevidence.AnswerArticle, Blocks: []agentevidence.Block{{ID: "b", Text: "两篇文章的解释", EvidenceIDs: []string{"e1", "e2"}, Quotes: []agentevidence.Quote{{EvidenceID: "e2", Text: "引文"}}}, {ID: "b2", Text: "第一篇解释", EvidenceIDs: []string{"e1"}}}, References: []agentevidence.Reference{{EvidenceID: "e1", Number: 1, Kind: agentevidence.Article, ArticleID: 1, RevisionID: 2}, {EvidenceID: "e2", Number: 2, Kind: agentevidence.Article, ArticleID: 3, RevisionID: 4}}, Cards: []agentevidence.Card{{Number: 1, ArticleID: 1, RevisionID: 2, Title: "第一篇"}, {Number: 2, ArticleID: 3, RevisionID: 4, Title: "第二篇私密标题"}}}
}
func factsFixture() *factStub {
	return &factStub{values: []ArticleFact{{ArticleID: 1, RevisionID: 2, Public: true, SourceID: 7, Keywords: []string{"Go"}, Topics: []string{"技术"}, Card: agentevidence.Card{Title: "当前可信标题", Path: "/articles/1"}}, {ArticleID: 3, RevisionID: 4, Public: true, SourceID: 7, Keywords: []string{"Go"}, Topics: []string{"技术"}, Card: agentevidence.Card{Title: "第二篇当前标题", Path: "/articles/3"}}}}
}

func TestEvidenceBeforeBlockAndFinalCommit(t *testing.T) {
	f := factsFixture()
	a := answerFixture()
	cards := map[string]agentevidence.Card{"e1": a.Cards[0], "e2": a.Cards[1]}
	c := cards["e1"]
	c.Mode = "latest_fallback"
	c.Degraded = true
	c.Truncated = true
	c.Summary = "工具实际摘要"
	cards["e1"] = c
	valid, err := ValidateEvidence(t.Context(), f, a, run.Constraints{SourceID: 7, Keyword: "Go", Topic: "技术"}, cards)
	if err != nil || len(valid.Cards) != 2 || valid.Cards[0].Title != "当前可信标题" || valid.Cards[0].Path != "/articles/1" || valid.Cards[0].Summary != "工具实际摘要" || !valid.Cards[0].Degraded || !valid.Cards[0].Truncated || valid.Cards[0].Mode != "latest_fallback" {
		t.Fatal(valid, err)
	}
	f.values[1].Public = false
	if _, err := ValidateEvidence(t.Context(), f, a, run.Constraints{}, cards); SafeCode(err) != run.CodeEvidenceUnavailable {
		t.Fatal("提交前下架", err)
	}
	f.values[1].Public = true
	f.values[1].RevisionID = 5
	if _, err := ValidateEvidence(t.Context(), f, a, run.Constraints{}, cards); SafeCode(err) != run.CodeEvidenceStale {
		t.Fatal("提交前修订变化", err)
	}
	f.values[1].RevisionID = 4
	for _, c := range []run.Constraints{{SourceID: 9}, {Keyword: "go"}, {Topic: "不存在"}} {
		if _, err := ValidateEvidence(t.Context(), f, a, c, cards); SafeCode(err) != run.CodeCitationInvalid {
			t.Fatal("精确条件被忽略", err)
		}
	}
	f.err = errors.New("包含敏感数据的事实源错误")
	if _, err := ValidateEvidence(t.Context(), f, a, run.Constraints{}, cards); SafeCode(err) != run.CodeDependencyUnavailable {
		t.Fatal("事实源错误", err)
	}
}

func TestUnifiedProjectionDoesNotMutatePersistedAnswer(t *testing.T) {
	f := factsFixture()
	p := NewProjector(f)
	a := answerFixture()
	before, _ := json.Marshal(a)
	for _, stale := range []bool{false, true} {
		f.values[1].Public = !stale
		f.values[1].RevisionID = 5
		projected, err := p.Answer(t.Context(), a)
		if err != nil {
			t.Fatal(err)
		}
		if !projected.Redacted || projected.Blocks[0].Text != agentevidence.UnavailableText || len(projected.Blocks[0].EvidenceIDs) != 0 || len(projected.Blocks[0].Quotes) != 0 || projected.Blocks[1].Redacted || len(projected.Cards) != 1 || len(projected.References) != 1 {
			t.Fatal(projected)
		}
		events := []Event{{Version: 1, RunID: "r", Sequence: 1, Kind: AnswerSources, References: a.References, Cards: a.Cards}, {Version: 1, RunID: "r", Sequence: 2, Kind: AnswerBlock, Block: &a.Blocks[0], References: a.References}}
		stream, err := p.Events(t.Context(), events)
		if err != nil || stream[1].Sequence != 2 || stream[1].Block.Text != projected.Blocks[0].Text || !stream[0].Redacted || len(stream[0].Cards) != 1 {
			t.Fatal(stream, err)
		}
		turns, err := p.Turns(t.Context(), []CompletedTurn{{RunID: "r", Answer: a}})
		if err != nil || turns[0].Answer.Blocks[0].Text != projected.Blocks[0].Text {
			t.Fatal(turns, err)
		}
	}
	after, _ := json.Marshal(a)
	if string(before) != string(after) {
		t.Fatal("原文被投影改写")
	}
	f.err = errors.New("故障")
	if _, err := p.Answer(t.Context(), a); SafeCode(err) != run.CodeDependencyUnavailable {
		t.Fatal("失败开放")
	}
	if _, err := p.Events(t.Context(), []Event{{Kind: AnswerSources, References: a.References}}); err == nil {
		t.Fatal("事件故障开放")
	}
}
