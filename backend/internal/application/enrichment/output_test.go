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
	tests := []string{
		"```json\n{\"summary\":\"摘要\",\"keywords\":[\"k\"],\"topics\":[\"t\"]}\n```",
		`{"summary":"摘要","keywords":["k"],"topics":["t"],"extra":true}`,
		`{"summary":"","keywords":["k"],"topics":["t"]}`,
		`{"summary":"摘要","keywords":[],"topics":["t"]}`,
		`{"summary":"摘要","keywords":["Go"," go "],"topics":["t"]}`,
		`{"summary":"摘要","keywords":["012345678"],"topics":["t"]}`,
	}
	for _, raw := range tests {
		if _, err := ParseGeneratedContent([]byte(raw), testLimits); err == nil {
			t.Fatalf("应拒绝: %s", raw)
		} else if code, _ := ErrorClassification(err); code != ErrorInvalidOutput {
			t.Fatalf("错误分类=%s", code)
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
