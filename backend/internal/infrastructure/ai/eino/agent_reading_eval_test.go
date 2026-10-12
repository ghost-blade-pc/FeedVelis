package eino

import (
	"context"
	"encoding/json"
	agent "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentrun"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentevidence"
	run "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/config"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

type evalFacts struct{}

func (evalFacts) Current(ctx context.Context, ids []agent.ArticleIdentity) ([]agent.ArticleFact, error) {
	facts, err := (readingFacts{}).Current(ctx, ids)
	for i := range facts {
		facts[i].Keywords = []string{"Go"}
	}
	return facts, err
}
func TestReadingFixedEvaluationSubset(t *testing.T) {
	raw, err := os.ReadFile("testdata/reading-eval.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		ID, Mode, Kind, Semantic string
		Code                     run.Code
		Models, Tools            int
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.ID, func(t *testing.T) {
			runner, req, tools, model, sink := readingFixture(t, tc.Mode)
			tools.mode = tc.Mode
			req.Context.Preferences = []string{}
			if tc.Mode == "hard_constraints" {
				req.Context.Constraints.Keyword = "Go"
				runner.facts = evalFacts{}
			}
			if tc.Mode == "followup" {
				req.Context.History = []agent.CompletedTurn{{RunID: "old-run", Answer: agentevidence.Answer{Version: 1, Kind: agentevidence.AnswerArticle, References: []agentevidence.Reference{{EvidenceID: "old-evidence", Number: 1, ArticleID: 1, RevisionID: 2}}, Cards: []agentevidence.Card{{Number: 1, ArticleID: 1, RevisionID: 2}}}}}
			}
			result, err := runner.Execute(t.Context(), req, sink)
			if agent.SafeCode(err) != tc.Code || model.count() != tc.Models || req.Budget.Usage().ToolCalls != tc.Tools {
				t.Fatal(result, err, model.count(), req.Budget.Usage())
			}
			identity := "not_applicable"
			if err != nil {
				identity = "rejected"
			}
			semantic := "not_applicable"
			if err == nil {
				if string(result.Answer.Kind) != tc.Kind {
					t.Fatal(result.Answer.Kind)
				}
				if result.Answer.Kind == agentevidence.AnswerArticle {
					identity = "valid"
					for _, ref := range result.Answer.References {
						if ref.ArticleID != 1 || ref.RevisionID != 2 || ref.EvidenceID == "old-evidence" {
							t.Fatal("未重新取证", ref)
						}
					}
					semantic = "supported"
					if strings.Contains(agent.AnswerText(result.Answer), "租约到期后写入仍被允许") {
						semantic = "unsupported"
					}
					if strings.Contains(agent.AnswerText(result.Answer), "API Key") {
						t.Fatal("注入文本进入回答")
					}
				}
			}
			if semantic != tc.Semantic {
				t.Fatal("固定标注语义不匹配", semantic, tc.Semantic)
			}
			if tc.Mode == "hard_constraints" && !strings.Contains(string(tools.searchArgs), `"keyword":"Go"`) {
				t.Fatal("显式条件未注入", string(tools.searchArgs))
			}
			if tc.Mode == "fallback" || tc.Mode == "feedback_isolation" {
				card := result.Answer.Cards[0]
				if !card.Degraded || card.Mode != "latest_fallback" || card.RecommendationReason != "最新公开文章" {
					t.Fatal(card)
				}
				if tools.user != req.UserID || len(req.Context.Preferences) != 0 {
					t.Fatal("身份或偏好旁路")
				}
			}
			t.Logf("固定阅读评测 id=%s 引用身份=%s 语义支持=%s（固定材料标注，非通用语义证明）模型=%d 工具=%d", tc.ID, identity, semantic, model.count(), req.Budget.Usage().ToolCalls)
		})
	}
}

func BenchmarkReadingLocalRun(b *testing.B) {
	stub := &readingStubModel{mode: "success"}
	server := httptest.NewServer(stub)
	defer server.Close()
	cfg := config.Default()
	if err := cfg.Validate(); err != nil {
		b.Fatal(err)
	}
	cfg.Agent.Generation.BaseURL = server.URL + "/v1"
	cfg.Agent.Generation.APIKey = config.SecretBytes("local-test-only")
	cfg.Agent.Generation.Model = "local-reading-stub"
	tools := &readingToolsStub{}
	runner := NewReadingRunner(cfg.Agent, tools, readingGuard{}, readingFacts{})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		stub.mu.Lock()
		stub.calls = 0
		stub.mu.Unlock()
		tools.calls = nil
		budget, _ := run.NewBudget(run.DefaultBudgetLimits())
		req := agent.ExecutionRequest{Execution: agent.Execution{Scope: agent.Scope{RunID: readingTestRun, UserID: readingTestRun}}, Context: agent.Context{Question: "解释执行租约"}, Budget: budget, Deadline: time.Now().Add(5 * time.Second)}
		if _, err := runner.Execute(context.Background(), req, &readingSink{}); err != nil {
			b.Fatal(err)
		}
	}
}
