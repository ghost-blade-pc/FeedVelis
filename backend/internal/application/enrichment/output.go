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

func ParseGeneratedContent(raw []byte, limits OutputLimits) (GeneratedContent, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var content GeneratedContent
	if err := decoder.Decode(&content); err != nil {
		return GeneratedContent{}, invalidOutput("生成结果不是严格 JSON 对象", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return GeneratedContent{}, invalidOutput("生成结果包含额外内容", err)
	}
	content.Summary = strings.TrimSpace(content.Summary)
	if content.Summary == "" || utf8.RuneCountInString(content.Summary) > limits.SummaryChars || containsDisallowedControl(content.Summary) {
		return GeneratedContent{}, invalidOutput("摘要为空、过长或包含控制字符", nil)
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
		return nil, invalidOutput(fmt.Sprintf("%s数量超出边界", label), nil)
	}
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = normalizeInline(value)
		key := strings.ToLower(value)
		if value == "" || utf8.RuneCountInString(value) > maxChars || containsDisallowedControl(value) {
			return nil, invalidOutput(label+"为空、过长或包含控制字符", nil)
		}
		if _, exists := seen[key]; exists {
			return nil, invalidOutput(label+"规范化后重复", nil)
		}
		seen[key] = struct{}{}
		normalized = append(normalized, value)
	}
	return normalized, nil
}

func invalidOutput(message string, cause error) error {
	return NewError(ErrorInvalidOutput, true, message, cause)
}
