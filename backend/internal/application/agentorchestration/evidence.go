// Package agentorchestration 组织可信工具材料、受控回答和有限上下文。
package agentorchestration

import (
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agenttools"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentevidence"
)

// Material 是实际交给成文模型的材料；集合登记与序列化使用同一份保留文本。
type Material struct {
	ID   string                    `json:"evidence_id"`
	Kind agentevidence.ContentKind `json:"content_kind"`
	Text string                    `json:"text"`
}

type EvidenceCollection struct {
	Set       *agentevidence.Set
	RunID     string
	Materials []Material
	Evidence  []agentevidence.Evidence
	Cards     map[string]agentevidence.Card
	maxBytes  int
	used      int
}

func NewEvidenceCollection(runID string, maxBytes int) (*EvidenceCollection, error) {
	set, err := agentevidence.NewSet(runID)
	if err != nil {
		return nil, err
	}
	if maxBytes < 1 {
		return nil, agentevidence.ErrOutput
	}
	return &EvidenceCollection{Set: set, RunID: runID, Cards: map[string]agentevidence.Card{}, maxBytes: maxBytes, used: 2, Materials: []Material{}, Evidence: []agentevidence.Evidence{}}, nil
}

func toolCard(ref agenttools.ArticleRef) agentevidence.Card {
	c := agentevidence.Card{ArticleID: ref.ArticleID, RevisionID: ref.RevisionID, Title: ref.Title, OriginType: string(ref.OriginType), Path: ref.Path, OriginalURL: ref.OriginalURL, Summary: ref.Summary, SummarySource: ref.SummarySource, MetadataTruncated: ref.MetadataTruncated}
	if ref.Source != nil {
		c.Source = &agentevidence.Source{ID: ref.Source.ID, Title: ref.Source.Title}
	}
	if ref.Author != nil {
		c.Author = &agentevidence.Author{ID: ref.Author.ID, Nickname: ref.Author.Nickname, Name: ref.Author.Name}
	}
	return c
}

// retain 按实际 JSON 字节限额裁剪，分配的 ID 只对应保留下来的前缀。
// 未保留的候选或空正文不登记，因此模型不能引用其尾部或身份。
func (c *EvidenceCollection) retain(ref agenttools.ArticleRef, kind agentevidence.ContentKind, text string, truncated bool, card agentevidence.Card, now time.Time) error {
	if !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return agentevidence.ErrOutput
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}
	id, err := account.NewUUID()
	if err != nil {
		return err
	}
	m := Material{ID: id.String(), Kind: kind, Text: text}
	remaining := c.maxBytes - c.used
	separator := 0
	if len(c.Materials) > 0 {
		separator = 1
	}
	remaining -= separator
	encoded, err := json.Marshal(m)
	if err != nil {
		return agentevidence.ErrOutput
	}
	if len(encoded) > remaining {
		chars := []rune(text)
		low, high := 0, len(chars)
		for low < high {
			mid := (low + high + 1) / 2
			m.Text = string(chars[:mid])
			b, _ := json.Marshal(m)
			if len(b) <= remaining {
				low = mid
			} else {
				high = mid - 1
			}
		}
		m.Text = string(chars[:low])
		truncated = true
		encoded, _ = json.Marshal(m)
	}
	if strings.TrimSpace(m.Text) == "" || len(encoded) > remaining {
		return nil
	}
	e := agentevidence.Evidence{Version: 1, ID: m.ID, RunID: c.RunID, Kind: agentevidence.Article, ArticleID: ref.ArticleID, RevisionID: ref.RevisionID, ObtainedAt: now, ContentKind: kind, Truncated: truncated, Content: m.Text}
	if err := c.Set.Add(e); err != nil {
		return err
	}
	card.Truncated = card.Truncated || truncated
	c.Materials = append(c.Materials, m)
	c.Evidence = append(c.Evidence, e)
	c.Cards[e.ID] = card
	c.used += len(encoded) + separator
	return nil
}

func (c *EvidenceCollection) Search(result agenttools.SearchResult, now time.Time) error {
	for _, ref := range result.Items {
		if err := c.retain(ref, agentevidence.Summary, ref.Summary, result.Truncated, toolCard(ref), now); err != nil {
			return err
		}
	}
	return nil
}
func (c *EvidenceCollection) Recommend(result agenttools.RecommendResult, now time.Time) error {
	for _, item := range result.Items {
		card := toolCard(item.ArticleRef)
		card.Mode = result.Mode
		card.Degraded = result.Degraded
		card.DegradeReason = result.DegradeReason
		card.RecommendationReason = item.Reason
		if err := c.retain(item.ArticleRef, agentevidence.Summary, item.Summary, result.Truncated, card, now); err != nil {
			return err
		}
	}
	return nil
}
func (c *EvidenceCollection) Get(result agenttools.GetResult, now time.Time) error {
	return c.retain(result.ArticleRef, agentevidence.Body, result.Content, result.Truncated, toolCard(result.ArticleRef), now)
}
