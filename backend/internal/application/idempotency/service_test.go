package idempotency

import "testing"

func TestDigestUsesSemanticJSON(t *testing.T) {
	a, err := Digest(map[string]any{"title": "文章", "published": true})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Digest(map[string]any{"published": true, "title": "文章"})
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("字段顺序不应影响规范化命令摘要")
	}
	c, _ := Digest(map[string]any{"title": "另一篇", "published": true})
	if a == c {
		t.Fatal("语义字段变化必须改变摘要")
	}
}
