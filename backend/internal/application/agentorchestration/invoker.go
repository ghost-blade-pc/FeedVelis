package agentorchestration

import (
	"context"
	"encoding/json"
	"time"

	agent "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentrun"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agenttools"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/strictjson"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentevidence"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
)

type ActionGuard interface {
	Check(context.Context, agent.Execution) error
}
type ReadingTools interface {
	SearchJSON(context.Context, agenttools.Caller, []byte) (agenttools.SearchResult, error)
	RecommendJSON(context.Context, agenttools.Caller, []byte) (agenttools.RecommendResult, error)
	GetJSON(context.Context, agenttools.Caller, []byte) (agenttools.GetResult, error)
}
type Invoker struct {
	tools      ReadingTools
	guard      ActionGuard
	facts      agent.EvidenceFacts
	Policy     *ConstraintPolicy
	Collection *EvidenceCollection
	Decision   DecisionFacts
	progress   func(context.Context, string, string, agentrun.Code) error
	usage      func(context.Context, agentrun.Usage) error
}

func (i *Invoker) WithProgress(progress func(context.Context, string, string, agentrun.Code) error) *Invoker {
	i.progress = progress
	return i
}
func (i *Invoker) WithUsage(usage func(context.Context, agentrun.Usage) error) *Invoker {
	i.usage = usage
	return i
}

func NewInvoker(tools ReadingTools, guard ActionGuard, facts agent.EvidenceFacts, policy *ConstraintPolicy, collection *EvidenceCollection) *Invoker {
	return &Invoker{tools: tools, guard: guard, facts: facts, Policy: policy, Collection: collection}
}

func (*Invoker) Manifests() []agent.ToolManifest {
	return []agent.ToolManifest{
		{Name: "search_articles", Version: "v1", InputSchema: `{"type":"object","additionalProperties":false,"required":["q"],"properties":{"q":{"type":"string"},"keyword":{"type":"string"},"topic":{"type":"string"},"source_id":{"type":"integer","minimum":1},"limit":{"type":"integer","minimum":1,"maximum":10}}}`, OutputSchema: ToolOutputSchema("search_articles"), Permission: "article.public.read", Effect: "read", Timeout: 5 * time.Second, MaxOutputBytes: 128 * 1024},
		{Name: "recommend_articles", Version: "v1", InputSchema: `{"type":"object","additionalProperties":false,"properties":{"limit":{"type":"integer","minimum":1,"maximum":10}}}`, OutputSchema: ToolOutputSchema("recommend_articles"), Permission: "article.public.recommend.self", Effect: "read", Timeout: 5 * time.Second, MaxOutputBytes: 128 * 1024},
		{Name: "get_article", Version: "v1", InputSchema: `{"type":"object","additionalProperties":false,"required":["article_id"],"properties":{"article_id":{"type":"integer","minimum":1},"max_chars":{"type":"integer","minimum":1,"maximum":8000}}}`, OutputSchema: ToolOutputSchema("get_article"), Permission: "article.public.read", Effect: "read", Timeout: 5 * time.Second, MaxOutputBytes: 128 * 1024},
	}
}
func toolError(err error) error {
	code := agentrun.CodeToolFailed
	switch agenttools.CodeOf(err) {
	case agenttools.ValidationFailed:
		code = agentrun.CodeToolRejected
	case agenttools.ArticleNotFound:
		code = agentrun.CodeEvidenceUnavailable
	case agenttools.DependencyUnavailable:
		code = agentrun.CodeDependencyUnavailable
	case agenttools.ToolTimeout:
		code = agentrun.CodeTimeout
	case agenttools.ToolCanceled:
		code = agentrun.CodeCanceled
	}
	return &agentrun.Error{Code: code}
}

func (i *Invoker) InvokeBatch(ctx context.Context, execution agent.Execution, calls []agent.ToolCall, budget *agentrun.Budget) ([]agent.ToolResult, error) {
	if i.guard == nil || i.tools == nil || i.Policy == nil || i.Collection == nil || budget == nil {
		return nil, &agentrun.Error{Code: agentrun.CodeDependencyUnavailable}
	}
	if err := i.guard.Check(ctx, execution); err != nil {
		return nil, err
	}
	if err := budget.ReserveTools(len(calls)); err != nil {
		return nil, err
	}
	if i.usage != nil {
		if err := i.usage(ctx, budget.Usage()); err != nil {
			return nil, err
		}
	}
	results := make([]agent.ToolResult, 0, len(calls))
	for _, call := range calls {
		if err := i.guard.Check(ctx, execution); err != nil {
			return nil, err
		}
		if i.progress != nil {
			if err := i.progress(ctx, call.Name, "started", ""); err != nil {
				return nil, err
			}
		}
		result, err := i.invoke(ctx, execution, call)
		if err != nil {
			i.Decision.RequiredFailure = agent.SafeCode(err)
			if i.progress != nil {
				_ = i.progress(ctx, call.Name, "failed", i.Decision.RequiredFailure)
			}
			return nil, err
		}
		if err := i.guard.Check(ctx, execution); err != nil {
			return nil, err
		}
		if i.progress != nil {
			if err := i.progress(ctx, call.Name, "succeeded", ""); err != nil {
				return nil, err
			}
		}
		results = append(results, result)
	}
	return results, nil
}

func (i *Invoker) invoke(ctx context.Context, execution agent.Execution, call agent.ToolCall) (agent.ToolResult, error) {
	caller := agenttools.Caller{UserID: execution.UserID}
	started := len(i.Collection.Evidence)
	materialStart := len(i.Collection.Materials)
	var result any
	var refs []agenttools.ArticleRef
	var err error
	retrieval := false
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	switch call.Name {
	case "search_articles":
		body, e := i.Policy.Search(call.Arguments)
		if e != nil {
			return agent.ToolResult{}, e
		}
		value, e := i.tools.SearchJSON(ctx, caller, body)
		if e != nil {
			return agent.ToolResult{}, toolError(e)
		}
		result = value
		refs = value.Items
		retrieval = true
	case "recommend_articles":
		var input agenttools.RecommendInput
		if strictjson.Decode(call.Arguments, 1024, &input, "limit") != nil || (input.Limit != nil && (*input.Limit < 1 || *input.Limit > 10)) {
			return agent.ToolResult{}, &agentrun.Error{Code: agentrun.CodeToolRejected}
		}
		if i.Policy.Hard() {
			return agent.ToolResult{}, &agentrun.Error{Code: agentrun.CodeToolRejected}
		}
		value, e := i.tools.RecommendJSON(ctx, caller, call.Arguments)
		if e != nil {
			return agent.ToolResult{}, toolError(e)
		}
		result = value
		retrieval = true
		for _, item := range value.Items {
			refs = append(refs, item.ArticleRef)
		}
	case "get_article":
		var input agenttools.GetInput
		if strictjson.Decode(call.Arguments, 1024, &input, "article_id", "max_chars") != nil || input.ArticleID == nil || *input.ArticleID < 1 || (input.MaxChars != nil && (*input.MaxChars < 1 || *input.MaxChars > 8000)) {
			return agent.ToolResult{}, &agentrun.Error{Code: agentrun.CodeToolRejected}
		}
		value, e := i.tools.GetJSON(ctx, caller, call.Arguments)
		if e != nil {
			return agent.ToolResult{}, toolError(e)
		}
		result = value
		refs = []agenttools.ArticleRef{value.ArticleRef}
	default:
		return agent.ToolResult{}, &agentrun.Error{Code: agentrun.CodeToolRejected}
	}
	if err := i.guard.Check(ctx, execution); err != nil {
		return agent.ToolResult{}, err
	}
	// 包括没有摘要的候选，先复核身份与硬条件，不能把不可用结果伪装为成功空检索。
	check := agentevidence.Answer{Version: 1, Kind: agentevidence.AnswerArticle}
	trusted := map[string]agentevidence.Card{}
	for n, ref := range refs {
		id := stringID(n)
		check.References = append(check.References, agentevidence.Reference{EvidenceID: id, Number: n + 1, Kind: agentevidence.Article, ArticleID: ref.ArticleID, RevisionID: ref.RevisionID})
		trusted[id] = toolCard(ref)
	}
	if _, err := agent.ValidateEvidence(ctx, i.facts, check, i.Policy.Value(), trusted); err != nil {
		return agent.ToolResult{}, err
	}
	switch value := result.(type) {
	case agenttools.SearchResult:
		err = i.Collection.Search(value, time.Now())
	case agenttools.RecommendResult:
		err = i.Collection.Recommend(value, time.Now())
	case agenttools.GetResult:
		err = i.Collection.Get(value, time.Now())
	}
	if err != nil {
		return agent.ToolResult{}, err
	}
	if retrieval {
		i.Decision.SuccessfulRetrievals++
		i.Decision.UsableResults += len(refs)
	}
	// 材料预算不足是明确失败，不能把有结果的检索转换成 no_results。
	if len(refs) > 0 && len(i.Collection.Evidence) == started {
		return agent.ToolResult{}, &agentrun.Error{Code: agentrun.CodeContextLimit}
	}
	encoded, err := json.Marshal(struct {
		Result   any        `json:"result"`
		Evidence []Material `json:"evidence"`
	}{result, i.Collection.Materials[materialStart:]})
	if err != nil || len(encoded) > 128*1024 {
		return agent.ToolResult{}, &agentrun.Error{Code: agentrun.CodeContextLimit}
	}
	cards := []agentevidence.Card{}
	for _, e := range i.Collection.Evidence[started:] {
		cards = append(cards, i.Collection.Cards[e.ID])
	}
	return agent.ToolResult{Name: call.Name, Content: encoded, Evidence: append([]agentevidence.Evidence{}, i.Collection.Evidence[started:]...), Cards: cards, Empty: len(refs) == 0}, nil
}
func stringID(n int) string { return string(rune('a' + n)) }
