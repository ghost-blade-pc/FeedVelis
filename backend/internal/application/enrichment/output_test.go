package enrichment

import (
	"math"
	"strings"
	"testing"
)

var testLimits = OutputLimits{SummaryChars: 20, KeywordCount: 12, TopicCount: 5, LabelChars: 8}

func TestParseGeneratedContentStrictValidation(t *testing.T) {
	valid, err := ParseGeneratedContent([]byte(`{"summary":"摘要","keywords":[" Go ","AI"],"topics":["工程"]}`), testLimits)
	if err != nil || valid.Keywords[0] != "Go" {
		t.Fatalf("合法结果: %+v err=%v", valid, err)
	}
	tests := []struct {
		raw, reason string
	}{
		{"```json\n{\"summary\":\"摘要\",\"keywords\":[\"k\"],\"topics\":[\"t\"]}\n```", ReasonJSONSyntax},
		{`{"summary":"摘要","keywords":["k"],"topics":["t"]} {}`, ReasonExtraContent},
		{`{"summary":"摘要","keywords":["k"],"topics":["t"],"extra":true}`, ReasonUnknownField},
		{`{"summary":"","keywords":["k"],"topics":["t"]}`, ReasonSummaryEmpty},
		{`{"summary":"这是一个超过二十个字符的摘要文本而且仍然继续增长","keywords":["k"],"topics":["t"]}`, ReasonSummaryTooLong},
		{`{"summary":"摘\u0001要","keywords":["k"],"topics":["t"]}`, ReasonControlCharacter},
		{`{"summary":"摘要","keywords":[],"topics":["t"]}`, ReasonLabelCount},
		{`{"summary":"摘要","keywords":["Go"," go "],"topics":["t"]}`, ReasonDuplicateLabel},
		{`{"summary":"摘要","keywords":["012345678"],"topics":["t"]}`, ReasonLabelTooLong},
	}
	for _, tc := range tests {
		if _, err := ParseGeneratedContent([]byte(tc.raw), testLimits); err == nil {
			t.Fatalf("应拒绝: %s", tc.raw)
		} else if code, _ := ErrorClassification(err); code != ErrorInvalidOutput {
			t.Fatalf("错误分类=%s", code)
		} else if reason := ErrorReason(err); reason != tc.reason {
			t.Fatalf("raw=%s reason=%s want=%s", tc.raw, reason, tc.reason)
		}
	}
}

func TestValidateMapSummaryUsesStableReasons(t *testing.T) {
	if value, err := ValidateMapSummary("  合法摘要  ", 8); err != nil || value != "合法摘要" {
		t.Fatalf("合法 Map 摘要=%q err=%v", value, err)
	}
	for _, tc := range []struct {
		value, reason string
	}{
		{"", ReasonSummaryEmpty},
		{"超过限制", ReasonSummaryTooLong},
		{"控\u0001制", ReasonControlCharacter},
	} {
		_, err := ValidateMapSummary(tc.value, 3)
		if err == nil || ErrorReason(err) != tc.reason {
			t.Fatalf("value=%q reason=%q err=%v", tc.value, ErrorReason(err), err)
		}
	}
}

func TestRetrievalDocumentHashSemanticsAndVectorValidation(t *testing.T) {
	revision := RevisionInput{Title: "标题", PlainText: strings.Repeat("正文", 20)}
	content := GeneratedContent{Summary: "摘要", Keywords: []string{"词"}, Topics: []string{"主题"}}
	first := BuildRetrievalDocument("v1", revision, content, 8)
	if first.Document != "title: 标题\nsummary: 摘要\nkeywords: 词\ntopics: 主题\nbody: 正文正文正文正文" {
		t.Fatalf("文档字段顺序或选段错误: %q", first.Document)
	}
	if BuildRetrievalDocument("v2", revision, content, 8).Hash == first.Hash {
		t.Fatal("输入版本应进入哈希")
	}
	content.Summary = "新摘要"
	if BuildRetrievalDocument("v1", revision, content, 8).Hash == first.Hash {
		t.Fatal("增强结果应进入哈希")
	}

	if err := ValidateVector([]float64{1, 2}, 2); err != nil {
		t.Fatal(err)
	}
	for _, vector := range [][]float64{nil, {1}, {math.NaN(), 1}, {math.Inf(1), 1}} {
		if err := ValidateVector(vector, 2); err == nil {
			t.Fatalf("应拒绝向量: %v", vector)
		}
	}
}
