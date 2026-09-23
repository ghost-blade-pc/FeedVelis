package articleevent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestContractFixtures(t *testing.T) {
	root := filepath.Join("..", "..", "..", "api", "events", "article", "v1", "fixtures")
	valid, err := filepath.Glob(filepath.Join(root, "valid", "*.json"))
	if err != nil || len(valid) != 4 {
		t.Fatalf("读取有效 fixture: count=%d err=%v", len(valid), err)
	}
	for _, path := range valid {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			event, decodeErr := Decode(data)
			if decodeErr != nil {
				t.Fatalf("有效 fixture 被拒绝: %v", decodeErr)
			}
			encoded, encodeErr := Encode(event)
			if encodeErr != nil {
				t.Fatal(encodeErr)
			}
			if strings.Contains(string(encoded), "markdown") || strings.Contains(string(encoded), "html") || strings.Contains(string(encoded), "plain_text") {
				t.Fatal("事件不得携带正文")
			}
		})
	}
	invalid, err := filepath.Glob(filepath.Join(root, "invalid", "*.json"))
	if err != nil || len(invalid) < 6 {
		t.Fatalf("读取无效 fixture: count=%d err=%v", len(invalid), err)
	}
	for _, path := range invalid {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if _, decodeErr := Decode(data); decodeErr == nil {
				t.Fatal("无效 fixture 未被拒绝")
			}
		})
	}
}

func TestNewUsesContextTraceOrCreatesRoot(t *testing.T) {
	ctx, err := WithTraceID(context.Background(), "0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	payload := PublicPayload{ArticleID: 42, OriginType: "user", RevisionID: 81, RevisionNo: 3, ContentHash: strings.Repeat("a", 64), Status: "published"}
	event, err := New(ctx, PublishedType, 42, 7, time.Date(2026, 9, 23, 10, 15, 30, 0, time.FixedZone("CST", 8*3600)), payload)
	if err != nil {
		t.Fatal(err)
	}
	if event.TraceID != "0123456789abcdef0123456789abcdef" || event.OccurredAt.Location() != time.UTC {
		t.Fatalf("事件上下文错误: %+v", event)
	}
	root, err := New(context.Background(), PublishedType, 42, 7, time.Now(), payload)
	if err != nil {
		t.Fatal(err)
	}
	if !hex32.MatchString(root.TraceID) {
		t.Fatalf("根 trace_id 无效: %q", root.TraceID)
	}
}

func TestFactoriesRejectInvalidFacts(t *testing.T) {
	valid := PublicFact{ArticleID: 42, OriginType: "user", RevisionID: 81, RevisionNo: 3, ContentHash: strings.Repeat("a", 64), LockVersion: 7}
	tests := []struct {
		name   string
		mutate func(*PublicFact)
	}{
		{"文章 ID", func(f *PublicFact) { f.ArticleID = 0 }},
		{"来源", func(f *PublicFact) { f.OriginType = "external" }},
		{"修订 ID", func(f *PublicFact) { f.RevisionID = 0 }},
		{"修订号", func(f *PublicFact) { f.RevisionNo = 0 }},
		{"内容哈希", func(f *PublicFact) { f.ContentHash = "body" }},
		{"聚合版本", func(f *PublicFact) { f.LockVersion = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fact := valid
			test.mutate(&fact)
			if _, err := Published(context.Background(), fact, time.Now()); err == nil {
				t.Fatal("非法事实未被拒绝")
			}
		})
	}
	if _, err := Offlined(context.Background(), 42, 81, 8, "system", time.Now()); err == nil {
		t.Fatal("非法下架原因未被拒绝")
	}
}
