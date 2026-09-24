package enrichment

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPrepareGenerationInputDeterministicAndUnicodeSafe(t *testing.T) {
	input := RevisionInput{ArticleID: 1, RevisionID: 2, Title: "  标题  ", Language: "zh-CN", PlainText: "第一段\r\n\r\n  第二   段🙂🙂🙂  "}
	first := PrepareGenerationInput(input, 5, 8)
	second := PrepareGenerationInput(input, 5, 8)
	if first.Hash != second.Hash || strings.Join(first.Chunks, "|") != strings.Join(second.Chunks, "|") {
		t.Fatal("重复运行必须一致")
	}
	if first.Normalized.Title != "标题" || first.Normalized.PlainText != "第一段\n\n第二 段🙂🙂🙂" {
		t.Fatalf("规范化结果错误: %+v", first.Normalized)
	}
	for _, chunk := range first.Chunks {
		if !utf8.ValidString(chunk) || utf8.RuneCountInString(chunk) > 5 {
			t.Fatalf("分块破坏 Unicode 或越界: %q", chunk)
		}
	}
}

func TestPrepareGenerationInputSelectsBoundedChunks(t *testing.T) {
	prepared := PrepareGenerationInput(RevisionInput{PlainText: strings.Repeat("甲", 50)}, 5, 3)
	if !prepared.Truncated || len(prepared.Chunks) != 3 {
		t.Fatalf("truncated=%t chunks=%d", prepared.Truncated, len(prepared.Chunks))
	}
	if prepared.Chunks[0] != strings.Repeat("甲", 5) || prepared.Chunks[2] != strings.Repeat("甲", 5) {
		t.Fatal("确定性首尾选段错误")
	}
}

func TestGenerationInputHashTracksVersionedFields(t *testing.T) {
	base := RevisionInput{ArticleID: 1, RevisionID: 2, Title: "标题", Language: "zh", PlainText: "正文"}
	want := PrepareGenerationInput(base, 10, 2).Hash
	base.Title = "新标题"
	if got := PrepareGenerationInput(base, 10, 2).Hash; got == want {
		t.Fatal("标题变化必须改变哈希")
	}
}
