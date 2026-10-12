// Package agentevidence 定义独立于模型框架的本轮证据与版本化回答。
package agentevidence

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
)

const (
	Version         = 1
	MaxBlocks       = 32
	MaxSources      = 10
	UnavailableText = "相关内容暂不可用"
)

type SourceKind string
type ContentKind string
type AnswerKind string

const (
	Article       SourceKind  = "article"
	Summary       ContentKind = "summary"
	Body          ContentKind = "body"
	AnswerArticle AnswerKind  = "answer"
	NoResults     AnswerKind  = "no_results"
	Clarification AnswerKind  = "clarification"
)

func (k AnswerKind) Valid() bool { return k == AnswerArticle || k == NoResults || k == Clarification }

var (
	ErrCitation = &agentrun.Error{Code: agentrun.CodeCitationInvalid}
	ErrOutput   = &agentrun.Error{Code: agentrun.CodeOutputInvalid}
)

// Evidence.Content 只能是实际提供给成文模型的保留材料，不随引用序列化。
type Evidence struct {
	Version     int         `json:"version"`
	ID          string      `json:"evidence_id"`
	RunID       string      `json:"-"`
	Kind        SourceKind  `json:"kind"`
	ArticleID   int64       `json:"article_id"`
	RevisionID  int64       `json:"revision_id"`
	ObtainedAt  time.Time   `json:"obtained_at"`
	ContentKind ContentKind `json:"content_kind"`
	Truncated   bool        `json:"truncated"`
	Content     string      `json:"-"`
}

func validUUID(raw string) bool {
	id, err := account.ParseUUID(raw)
	return err == nil && !id.IsZero() && id.String() == raw
}
func validText(raw string) bool {
	return utf8.ValidString(raw) && !strings.ContainsRune(raw, 0) && strings.TrimSpace(raw) != ""
}

func (e Evidence) Valid() bool {
	return e.Version == Version && validUUID(e.ID) && validUUID(e.RunID) && e.Kind == Article && e.ArticleID > 0 && e.RevisionID > 0 &&
		!e.ObtainedAt.IsZero() && (e.ContentKind == Summary || e.ContentKind == Body) && validText(e.Content)
}

type Source struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}
type Author struct {
	ID       string `json:"id,omitempty"`
	Nickname string `json:"nickname,omitempty"`
	Name     string `json:"name,omitempty"`
}

// Card 由可信工具与当前事实装配；成文协议不接受这些字段。
type Card struct {
	Number               int     `json:"number"`
	ArticleID            int64   `json:"article_id"`
	RevisionID           int64   `json:"revision_id"`
	Title                string  `json:"title"`
	OriginType           string  `json:"origin_type"`
	Source               *Source `json:"source"`
	Author               *Author `json:"author"`
	Path                 string  `json:"path"`
	OriginalURL          *string `json:"original_url"`
	Summary              string  `json:"summary"`
	SummarySource        string  `json:"summary_source"`
	MetadataTruncated    bool    `json:"metadata_truncated"`
	Truncated            bool    `json:"truncated"`
	Mode                 string  `json:"mode,omitempty"`
	Degraded             bool    `json:"degraded"`
	DegradeReason        *string `json:"degrade_reason,omitempty"`
	RecommendationReason string  `json:"recommendation_reason,omitempty"`
}

type Quote struct {
	EvidenceID string `json:"evidence_id"`
	Text       string `json:"text"`
}
type Block struct {
	ID          string   `json:"id"`
	Text        string   `json:"text"`
	EvidenceIDs []string `json:"evidence_ids"`
	Quotes      []Quote  `json:"quotes,omitempty"`
	Redacted    bool     `json:"redacted"`
}
type Reference struct {
	EvidenceID  string      `json:"evidence_id"`
	Number      int         `json:"number"`
	Kind        SourceKind  `json:"kind"`
	ArticleID   int64       `json:"article_id"`
	RevisionID  int64       `json:"revision_id"`
	ContentKind ContentKind `json:"content_kind"`
	Truncated   bool        `json:"truncated"`
}
type Answer struct {
	Version    int         `json:"version"`
	Kind       AnswerKind  `json:"kind"`
	Blocks     []Block     `json:"blocks"`
	References []Reference `json:"references"`
	Cards      []Card      `json:"cards"`
	Redacted   bool        `json:"redacted"`
}

// Set 不允许模型登记证据；应用只能加入已提供给本轮模型的工具材料。
type Set struct {
	runID string
	items map[string]Evidence
}

func NewSet(runID string) (*Set, error) {
	if !validUUID(runID) {
		return nil, ErrCitation
	}
	return &Set{runID: runID, items: make(map[string]Evidence)}, nil
}
func (s *Set) Add(e Evidence) error {
	if !e.Valid() || e.RunID != s.runID {
		return ErrCitation
	}
	if _, ok := s.items[e.ID]; ok {
		return ErrCitation
	}
	s.items[e.ID] = e
	return nil
}
func (s *Set) Get(id string) (Evidence, bool) { e, ok := s.items[id]; return e, ok }

// ValidateBlock 校验身份和显式逐字引文；requireBody 由当前任务策略决定。
// 身份和字面匹配不能证明自由文本的语义正确性。
func (s *Set) ValidateBlock(b Block, requireBody bool) error {
	if !validText(b.ID) || utf8.RuneCountInString(b.ID) > 100 || !validText(b.Text) || b.Redacted {
		return ErrOutput
	}
	if len(b.EvidenceIDs) == 0 || len(b.EvidenceIDs) > MaxSources*2 {
		return ErrCitation
	}
	seen := make(map[string]bool)
	hasBody := false
	for _, id := range b.EvidenceIDs {
		e, ok := s.items[id]
		if !ok || seen[id] {
			return ErrCitation
		}
		seen[id] = true
		hasBody = hasBody || e.ContentKind == Body
	}
	if requireBody && !hasBody {
		return ErrCitation
	}
	for _, q := range b.Quotes {
		e, ok := s.items[q.EvidenceID]
		if !ok || !seen[q.EvidenceID] || !validText(q.Text) || !strings.Contains(e.Content, q.Text) {
			return ErrCitation
		}
	}
	return nil
}

type sourceIdentity struct{ article, revision int64 }
type Assembler struct {
	set             *Set
	answer          Answer
	maxChars, chars int
	blockIDs        map[string]bool
	numbers         map[sourceIdentity]int
	references      map[string]bool
	finished        bool
	failed          bool
}

func NewAssembler(set *Set, maxChars int) (*Assembler, error) {
	if set == nil || maxChars < 1 {
		return nil, ErrOutput
	}
	return &Assembler{set: set, maxChars: maxChars, answer: Answer{Version: Version, Kind: AnswerArticle,
		Blocks: []Block{}, References: []Reference{}, Cards: []Card{}}, blockIDs: make(map[string]bool),
		numbers: make(map[sourceIdentity]int), references: make(map[string]bool)}, nil
}

func (a *Assembler) Add(b Block, requireBody bool) (err error) {
	defer func() {
		if err != nil {
			a.failed = true
		}
	}()
	if a.failed || a.finished || a.blockIDs[b.ID] || len(a.answer.Blocks) >= MaxBlocks || utf8.RuneCountInString(b.Text) > a.maxChars-a.chars {
		return ErrOutput
	}
	if err := a.set.ValidateBlock(b, requireBody); err != nil {
		return err
	}
	newSources := make(map[sourceIdentity]bool)
	for _, id := range b.EvidenceIDs {
		e := a.set.items[id]
		key := sourceIdentity{e.ArticleID, e.RevisionID}
		if a.numbers[key] == 0 {
			newSources[key] = true
		}
	}
	if len(a.numbers)+len(newSources) > MaxSources {
		return ErrOutput
	}
	// 所有检查完成后才改变组装状态。
	for _, id := range b.EvidenceIDs {
		e := a.set.items[id]
		key := sourceIdentity{e.ArticleID, e.RevisionID}
		if a.numbers[key] == 0 {
			a.numbers[key] = len(a.numbers) + 1
		}
		if !a.references[id] {
			a.answer.References = append(a.answer.References, Reference{EvidenceID: id, Number: a.numbers[key], Kind: e.Kind,
				ArticleID: e.ArticleID, RevisionID: e.RevisionID, ContentKind: e.ContentKind, Truncated: e.Truncated})
			a.references[id] = true
		}
	}
	b.EvidenceIDs = append([]string(nil), b.EvidenceIDs...)
	b.Quotes = append([]Quote(nil), b.Quotes...)
	a.answer.Blocks = append(a.answer.Blocks, b)
	a.blockIDs[b.ID] = true
	a.chars += utf8.RuneCountInString(b.Text)
	return nil
}

func (a *Assembler) Finish() error {
	if a.failed || a.finished || len(a.answer.Blocks) == 0 {
		return ErrOutput
	}
	a.finished = true
	return nil
}

// Abort 使任何协议或持久化错误永久阻止该组装器完成，保留此前完整片段。
func (a *Assembler) Abort() { a.failed = true }

// Snapshot 拷贝集合，partial 不能作为成功回答使用。
func (a *Assembler) Snapshot() (Answer, bool) {
	return cloneAnswer(a.answer), (a.failed || !a.finished) && len(a.answer.Blocks) > 0
}
func (a *Assembler) Completed() (Answer, error) {
	if a.failed || !a.finished {
		return Answer{}, ErrOutput
	}
	return cloneAnswer(a.answer), nil
}

func cloneAnswer(value Answer) Answer {
	value.Blocks = append([]Block{}, value.Blocks...)
	for i := range value.Blocks {
		value.Blocks[i].EvidenceIDs = append([]string{}, value.Blocks[i].EvidenceIDs...)
		value.Blocks[i].Quotes = append([]Quote(nil), value.Blocks[i].Quotes...)
	}
	value.References = append([]Reference{}, value.References...)
	value.Cards = append([]Card{}, value.Cards...)
	return value
}
