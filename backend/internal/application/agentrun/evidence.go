package agentrun

import (
	"context"
	"strings"

	conversation "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentconversation"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentevidence"
	run "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
)

func evidenceError(code run.Code) error { return &run.Error{Code: code} }

func exact(values []string, value string) bool {
	if value == "" {
		return true
	}
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func satisfies(f ArticleFact, c run.Constraints) bool {
	return (c.SourceID == 0 || c.SourceID == f.SourceID) && exact(f.Keywords, c.Keyword) && exact(f.Topics, c.Topic)
}

// currentFacts 只读取一次批量当前事实；不使用工具卡片的公开性或缓存身份替代。
func currentFacts(ctx context.Context, reader EvidenceFacts, refs []agentevidence.Reference) (map[int64]ArticleFact, error) {
	if len(refs) == 0 {
		return map[int64]ArticleFact{}, nil
	}
	if reader == nil {
		return nil, evidenceError(run.CodeDependencyUnavailable)
	}
	ids := []ArticleIdentity{}
	seen := map[int64]bool{}
	for _, r := range refs {
		if r.Kind != agentevidence.Article || r.ArticleID < 1 || r.RevisionID < 1 || r.Number < 1 || r.Number > agentevidence.MaxSources {
			return nil, agentevidence.ErrCitation
		}
		if !seen[r.ArticleID] {
			ids = append(ids, ArticleIdentity{r.ArticleID, r.RevisionID})
			seen[r.ArticleID] = true
		}
	}
	if len(ids) > agentevidence.MaxSources {
		return nil, agentevidence.ErrOutput
	}
	values, err := reader.Current(ctx, ids)
	if err != nil {
		return nil, evidenceError(run.CodeDependencyUnavailable)
	}
	facts := map[int64]ArticleFact{}
	for _, f := range values {
		if !seen[f.ArticleID] {
			return nil, evidenceError(run.CodeDependencyUnavailable)
		}
		if _, ok := facts[f.ArticleID]; ok {
			return nil, evidenceError(run.CodeDependencyUnavailable)
		}
		facts[f.ArticleID] = f
	}
	return facts, nil
}

// ValidateEvidence 在每个片段发送前与最终事务内复用；返回的卡片只来自可信读取。
func ValidateEvidence(ctx context.Context, reader EvidenceFacts, answer agentevidence.Answer, constraints run.Constraints, trusted map[string]agentevidence.Card) (agentevidence.Answer, error) {
	facts, err := currentFacts(ctx, reader, answer.References)
	if err != nil {
		return agentevidence.Answer{}, err
	}
	answer.Cards = []agentevidence.Card{}
	numbers := map[int]ArticleIdentity{}
	refs := map[string]bool{}
	for _, r := range answer.References {
		f, ok := facts[r.ArticleID]
		if !ok || !f.Public {
			return agentevidence.Answer{}, evidenceError(run.CodeEvidenceUnavailable)
		}
		if f.RevisionID != r.RevisionID {
			return agentevidence.Answer{}, evidenceError(run.CodeEvidenceStale)
		}
		if !satisfies(f, constraints) {
			return agentevidence.Answer{}, agentevidence.ErrCitation
		}
		card, ok := trusted[r.EvidenceID]
		if !ok || card.ArticleID != r.ArticleID || card.RevisionID != r.RevisionID || refs[r.EvidenceID] {
			return agentevidence.Answer{}, agentevidence.ErrCitation
		}
		refs[r.EvidenceID] = true
		id := ArticleIdentity{r.ArticleID, r.RevisionID}
		if prev, ok := numbers[r.Number]; ok {
			if prev != id {
				return agentevidence.Answer{}, agentevidence.ErrCitation
			}
			answer.Cards[r.Number-1].Truncated = answer.Cards[r.Number-1].Truncated || card.Truncated || r.Truncated
			continue
		}
		if r.Number != len(numbers)+1 {
			return agentevidence.Answer{}, agentevidence.ErrCitation
		}
		numbers[r.Number] = id
		// 当前身份元数据与工具实际保留的摘要、推荐结果分别保持可信来源。
		current := f.Card
		current.Number = r.Number
		current.ArticleID = r.ArticleID
		current.RevisionID = r.RevisionID
		current.Summary = card.Summary
		current.SummarySource = card.SummarySource
		current.MetadataTruncated = current.MetadataTruncated || card.MetadataTruncated
		current.Truncated = card.Truncated || r.Truncated
		current.Mode = card.Mode
		current.Degraded = card.Degraded
		current.DegradeReason = card.DegradeReason
		current.RecommendationReason = card.RecommendationReason
		answer.Cards = append(answer.Cards, current)
	}
	for _, b := range answer.Blocks {
		for _, id := range b.EvidenceIDs {
			if !refs[id] {
				return agentevidence.Answer{}, agentevidence.ErrCitation
			}
		}
	}
	return answer, nil
}

type Projector struct {
	facts   EvidenceFacts
	history interface {
		Find(context.Context, Scope) (Record, error)
	}
}

func NewProjector(facts EvidenceFacts) *Projector { return &Projector{facts: facts} }

func (p *Projector) WithHistorySource(source interface {
	Find(context.Context, Scope) (Record, error)
}) *Projector {
	p.history = source
	return p
}

func (p *Projector) Messages(ctx context.Context, userID, conversationID string, items []conversation.Message) ([]conversation.Message, error) {
	result := append([]conversation.Message{}, items...)
	for i, m := range result {
		if m.RunID == nil {
			continue
		}
		if m.Role != conversation.RoleAssistant || p.history == nil {
			return nil, evidenceError(run.CodeDependencyUnavailable)
		}
		r, err := p.history.Find(ctx, Scope{UserID: userID, ConversationID: conversationID, RunID: *m.RunID})
		if err != nil {
			return nil, err
		}
		if r.Run.State != run.Succeeded || r.Run.AssistantMessageID != m.ID || r.Answer == nil {
			return nil, evidenceError(run.CodeDependencyUnavailable)
		}
		a, err := p.Answer(ctx, *r.Answer)
		if err != nil {
			return nil, err
		}
		result[i].Content = AnswerText(a)
		result[i].ContentRedacted = &a.Redacted
	}
	return result, nil
}

func (p *Projector) Turns(ctx context.Context, turns []CompletedTurn) ([]CompletedTurn, error) {
	result := append([]CompletedTurn{}, turns...)
	for i, t := range result {
		a, err := p.Answer(ctx, t.Answer)
		if err != nil {
			return nil, err
		}
		result[i].Answer = a
	}
	return result, nil
}

// Answer 不改变传入的持久化值。任何失效引用会隐藏整个关联片段及该来源元数据。
func (p *Projector) Answer(ctx context.Context, answer agentevidence.Answer) (agentevidence.Answer, error) {
	if answer.Version != 1 || !answer.Kind.Valid() {
		return agentevidence.Answer{}, agentevidence.ErrOutput
	}
	facts, err := currentFacts(ctx, p.facts, answer.References)
	if err != nil {
		return agentevidence.Answer{}, err
	}
	valid := map[string]bool{}
	numbers := map[int]bool{}
	result := answer
	result.References = []agentevidence.Reference{}
	result.Cards = []agentevidence.Card{}
	result.Blocks = []agentevidence.Block{}
	for _, r := range answer.References {
		f, ok := facts[r.ArticleID]
		valid[r.EvidenceID] = ok && f.Public && f.RevisionID == r.RevisionID
		if valid[r.EvidenceID] {
			result.References = append(result.References, r)
			numbers[r.Number] = true
		} else {
			result.Redacted = true
		}
	}
	for _, c := range answer.Cards {
		f, ok := facts[c.ArticleID]
		if numbers[c.Number] && ok && f.Public && f.RevisionID == c.RevisionID {
			result.Cards = append(result.Cards, c)
		}
	}
	for _, b := range answer.Blocks {
		bad := b.Redacted
		for _, id := range b.EvidenceIDs {
			if !valid[id] {
				bad = true
			}
		}
		if answer.Kind == agentevidence.AnswerArticle && len(b.EvidenceIDs) == 0 {
			bad = true
		}
		if bad {
			b.Text = agentevidence.UnavailableText
			b.EvidenceIDs = []string{}
			b.Quotes = nil
			b.Redacted = true
			result.Redacted = true
		} else {
			b.EvidenceIDs = append([]string{}, b.EvidenceIDs...)
			b.Quotes = append([]agentevidence.Quote(nil), b.Quotes...)
		}
		result.Blocks = append(result.Blocks, b)
	}
	return result, nil
}

// Events 的 answer.block 必须携带自己的引用身份，不能依赖订阅之前的 sources 缓存。
// 日志持久化完整关系后，不同批次与不同实例都可独立投影。
func (p *Projector) Events(ctx context.Context, events []Event) ([]Event, error) {
	refs := []agentevidence.Reference{}
	seen := map[string]bool{}
	for _, e := range events {
		for _, r := range e.References {
			if !seen[r.EvidenceID] {
				refs = append(refs, r)
				seen[r.EvidenceID] = true
			}
		}
	}
	facts, err := currentFacts(ctx, p.facts, refs)
	if err != nil {
		return nil, err
	}
	// 此次投影使用同一个批量事实快照，避免逐事件读取时状态不一致。
	snapshot := NewProjector(fixedFacts(facts))
	result := make([]Event, 0, len(events))
	for _, e := range events {
		if e.Kind == AnswerSources || e.Kind == AnswerBlock {
			a := agentevidence.Answer{Version: 1, Kind: agentevidence.AnswerArticle, References: e.References, Cards: e.Cards, Blocks: []agentevidence.Block{}}
			if e.Block != nil {
				a.Blocks = append(a.Blocks, *e.Block)
			}
			a, err = snapshot.Answer(ctx, a)
			if err != nil {
				return nil, err
			}
			e.References = a.References
			e.Cards = a.Cards
			e.Redacted = a.Redacted
			if e.Block != nil {
				b := a.Blocks[0]
				e.Block = &b
			}
		}
		result = append(result, e)
	}
	return result, nil
}

type fixedFacts map[int64]ArticleFact

func (f fixedFacts) Current(_ context.Context, ids []ArticleIdentity) ([]ArticleFact, error) {
	result := []ArticleFact{}
	for _, id := range ids {
		if value, ok := f[id.ArticleID]; ok {
			result = append(result, value)
		}
	}
	return result, nil
}

func AnswerText(answer agentevidence.Answer) string {
	parts := make([]string, 0, len(answer.Blocks))
	for _, b := range answer.Blocks {
		parts = append(parts, b.Text)
	}
	return strings.Join(parts, "\n\n")
}
