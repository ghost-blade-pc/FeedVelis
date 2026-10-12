package agentorchestration

import (
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentevidence"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
	"strings"
	"testing"
)

func TestTrustedAnswerDecision(t *testing.T) {
	for _, f := range []DecisionFacts{{}, {RequiredFailure: agentrun.CodeToolFailed, SuccessfulRetrievals: 1}, {RequiredFailure: agentrun.CodeDependencyUnavailable, Clarification: MissingTask}, {Clarification: "自定义模型说明"}} {
		if _, err := Template(f); err == nil {
			t.Fatal("无事实或故障触发成功模板", f)
		}
	}
	a, err := Template(DecisionFacts{SuccessfulRetrievals: 1})
	if err != nil || a.Kind != agentevidence.NoResults || len(a.Cards) != 0 || len(a.References) != 0 {
		t.Fatal(a, err)
	}
	for reason := range clarifications {
		a, err := Template(DecisionFacts{Clarification: reason})
		if err != nil || a.Kind != agentevidence.Clarification || len(a.Blocks[0].EvidenceIDs) != 0 {
			t.Fatal(a, err)
		}
		if strings.Contains(a.Blocks[0].Text, "已保存") || strings.Contains(a.Blocks[0].Text, "已记住") {
			t.Fatal("声称执行了未启用能力")
		}
	}
	if k, err := Decide(DecisionFacts{SuccessfulRetrievals: 1, UsableResults: 1}); err != nil || k != agentevidence.AnswerArticle {
		t.Fatal(k, err)
	}
	if _, err := Template(DecisionFacts{UsableResults: 1}); err == nil {
		t.Fatal("正常回答使用无证据模板")
	}
}
