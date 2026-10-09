package enrichment

import (
	"encoding/json"
	"strings"
	"unicode"
)

// BuildExtractiveFallback 只复制当前修订的有界原文片段，不调用模型或推断正文未表达的事实。
func BuildExtractiveFallback(revision RevisionInput, limits OutputLimits) (GeneratedContent, bool) {
	if limits.SummaryChars < 1 || limits.LabelChars < 1 || limits.KeywordCount < 1 || limits.TopicCount < 1 {
		return GeneratedContent{}, false
	}
	text := normalizeParagraphs(revision.PlainText)
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return ' '
		}
		return r
	}, text)
	text = strings.TrimSpace(text)
	if text == "" {
		return GeneratedContent{}, false
	}
	summaryLimit := limits.SummaryChars
	if summaryLimit > 400 {
		summaryLimit = 400
	}
	summary := truncateRunes(text, summaryLimit)
	label := normalizeInline(revision.Title)
	if label == "" {
		label = "文章"
	}
	label = truncateRunes(label, limits.LabelChars)
	content := GeneratedContent{Summary: summary, Keywords: []string{label}, Topics: []string{label}}
	encoded, err := json.Marshal(content)
	if err != nil {
		return GeneratedContent{}, false
	}
	validated, err := ParseGeneratedContent(encoded, limits)
	return validated, err == nil
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) > limit {
		runes = runes[:limit]
	}
	return strings.TrimSpace(string(runes))
}
