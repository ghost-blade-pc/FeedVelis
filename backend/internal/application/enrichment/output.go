package enrichment

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

type OutputLimits struct{ SummaryChars, KeywordCount, TopicCount, LabelChars int }

const (
	ReasonJSONSyntax       = "json_syntax"
	ReasonExtraContent     = "extra_content"
	ReasonUnknownField     = "unknown_field"
	ReasonSummaryEmpty     = "summary_empty"
	ReasonSummaryTooLong   = "summary_too_long"
	ReasonControlCharacter = "control_character"
	ReasonLabelCount       = "label_count"
	ReasonLabelEmpty       = "label_empty"
	ReasonLabelTooLong     = "label_too_long"
	ReasonDuplicateLabel   = "duplicate_label"
	ReasonVectorDimensions = "vector_dimensions"
	ReasonVectorNonFinite  = "vector_non_finite"
)

// GeneratedContentJSONSchema 返回与本地严格校验边界一致的最终输出 Schema。
func GeneratedContentJSONSchema(limits OutputLimits) map[string]any {
	labelItems := map[string]any{"type": "string", "minLength": 1, "maxLength": limits.LabelChars}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"summary", "keywords", "topics"},
		"properties": map[string]any{
			"summary":  map[string]any{"type": "string", "minLength": 1, "maxLength": limits.SummaryChars},
			"keywords": map[string]any{"type": "array", "minItems": 1, "maxItems": limits.KeywordCount, "uniqueItems": true, "items": labelItems},
			"topics":   map[string]any{"type": "array", "minItems": 1, "maxItems": limits.TopicCount, "uniqueItems": true, "items": labelItems},
		},
	}
}

func ValidateMapSummary(value string, maxChars int) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", invalidOutput(ReasonSummaryEmpty, "分块摘要为空", nil)
	}
	if utf8.RuneCountInString(value) > maxChars {
		return "", invalidOutput(ReasonSummaryTooLong, "分块摘要过长", nil)
	}
	if containsDisallowedControl(value) {
		return "", invalidOutput(ReasonControlCharacter, "分块摘要包含控制字符", nil)
	}
	return value, nil
}

func ParseGeneratedContent(raw []byte, limits OutputLimits) (GeneratedContent, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var content GeneratedContent
	if err := decoder.Decode(&content); err != nil {
		reason := ReasonJSONSyntax
		if strings.HasPrefix(err.Error(), "json: unknown field ") {
			reason = ReasonUnknownField
		}
		return GeneratedContent{}, invalidOutput(reason, "生成结果不是严格 JSON 对象", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return GeneratedContent{}, invalidOutput(ReasonExtraContent, "生成结果包含额外内容", err)
	}
	content.Summary = strings.TrimSpace(content.Summary)
	if content.Summary == "" {
		return GeneratedContent{}, invalidOutput(ReasonSummaryEmpty, "摘要为空", nil)
	}
	if utf8.RuneCountInString(content.Summary) > limits.SummaryChars {
		return GeneratedContent{}, invalidOutput(ReasonSummaryTooLong, "摘要过长", nil)
	}
	if containsDisallowedControl(content.Summary) {
		return GeneratedContent{}, invalidOutput(ReasonControlCharacter, "摘要包含控制字符", nil)
	}
	var err error
	if content.Keywords, err = normalizeLabels(content.Keywords, 1, limits.KeywordCount, limits.LabelChars, "关键词"); err != nil {
		return GeneratedContent{}, err
	}
	if content.Topics, err = normalizeLabels(content.Topics, 1, limits.TopicCount, limits.LabelChars, "主题"); err != nil {
		return GeneratedContent{}, err
	}
	return content, nil
}

func normalizeLabels(values []string, min, max, maxChars int, label string) ([]string, error) {
	if len(values) < min || len(values) > max {
		return nil, invalidOutput(ReasonLabelCount, fmt.Sprintf("%s数量超出边界", label), nil)
	}
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = normalizeInline(value)
		key := strings.ToLower(value)
		if value == "" {
			return nil, invalidOutput(ReasonLabelEmpty, label+"为空", nil)
		}
		if utf8.RuneCountInString(value) > maxChars {
			return nil, invalidOutput(ReasonLabelTooLong, label+"过长", nil)
		}
		if containsDisallowedControl(value) {
			return nil, invalidOutput(ReasonControlCharacter, label+"包含控制字符", nil)
		}
		if _, exists := seen[key]; exists {
			return nil, invalidOutput(ReasonDuplicateLabel, label+"规范化后重复", nil)
		}
		seen[key] = struct{}{}
		normalized = append(normalized, value)
	}
	return normalized, nil
}

func invalidOutput(reason, message string, cause error) error {
	return NewErrorWithReason(ErrorInvalidOutput, true, reason, message, cause)
}
