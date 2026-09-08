package article

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
)

type fakeArticleRepository struct {
	candidates []articleDomain.Candidate
	list       []articleDomain.ListItem
}

func (r *fakeArticleRepository) Upsert(_ context.Context, candidate articleDomain.Candidate, _ time.Time) (articleDomain.UpsertResult, int64, error) {
	r.candidates = append(r.candidates, candidate)
	return articleDomain.UpsertInserted, int64(len(r.candidates)), nil
}

func (r *fakeArticleRepository) ListPublished(context.Context, *articleDomain.Cursor, int) ([]articleDomain.ListItem, error) {
	return r.list, nil
}

type fakeSanitizer struct{}

func (fakeSanitizer) Sanitize(value string) ports.SanitizedContent {
	return ports.SanitizedContent{HTML: "<p>clean</p>", PlainText: strings.TrimSpace(value)}
}

type fixedClock struct{ value time.Time }

func (c fixedClock) Now() time.Time { return c.value }

func TestIngestSkipsInvalidURLAndTruncatesContent(t *testing.T) {
	repository := &fakeArticleRepository{}
	service := NewService(repository, fakeSanitizer{}, fixedClock{value: time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)})
	id := "id-1"
	content := strings.Repeat("你", articleDomain.MaxRawContentBytes)
	report, err := service.Ingest(context.Background(), 7, []ports.ParsedItem{
		{ID: &id, URL: "javascript:x"},
		{ID: &id, URL: "https://example.com/a", Title: "A", Content: &content},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Inserted != 1 || report.Skipped["INVALID_CANONICAL_URL"] != 1 {
		t.Fatalf("report=%+v", report)
	}
	if !repository.candidates[0].Content.RawContentTruncated || len(*repository.candidates[0].Content.RawContent) > articleDomain.MaxRawContentBytes {
		t.Fatal("content was not safely truncated")
	}
}

func TestListCreatesOpaqueCursor(t *testing.T) {
	now := time.Now().UTC()
	repository := &fakeArticleRepository{list: []articleDomain.ListItem{{ID: 3, SortAt: now}, {ID: 2, SortAt: now}, {ID: 1, SortAt: now}}}
	service := NewService(repository, fakeSanitizer{}, fixedClock{value: now})
	page, err := service.List(context.Background(), "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if !page.HasMore || page.NextCursor == nil || len(page.Items) != 2 {
		t.Fatalf("page=%+v", page)
	}
	if _, err := decodeCursor(*page.NextCursor); err != nil {
		t.Fatal(err)
	}
}
