package agentorchestration

import (
	"encoding/json"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentevidence"
	"os"
	"testing"
)

func TestDocumentedAnswerFixtures(t *testing.T) {
	for _, name := range []string{"answer", "clarification", "redacted"} {
		raw, err := os.ReadFile("testdata/" + name + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var a agentevidence.Answer
		if json.Unmarshal(raw, &a) != nil || a.Version != 1 || !a.Kind.Valid() || len(a.Blocks) != 1 || a.References == nil || a.Cards == nil {
			t.Fatal("协议夹具无效", name)
		}
		switch name {
		case "answer":
			if len(a.References) != 1 || a.References[0].EvidenceID != a.Blocks[0].EvidenceIDs[0] || a.Cards[0].Number != a.References[0].Number || a.Redacted {
				t.Fatal(a)
			}
		case "clarification":
			want, _ := Template(DecisionFacts{Clarification: UnsupportedFilter})
			b, _ := json.Marshal(want)
			if string(b) != string(raw[:len(raw)-1]) {
				t.Fatal("文档模板与实现不符")
			}
		case "redacted":
			if !a.Redacted || !a.Blocks[0].Redacted || a.Blocks[0].Text != agentevidence.UnavailableText || len(a.Cards) != 0 || len(a.References) != 0 {
				t.Fatal(a)
			}
		}
	}
}
