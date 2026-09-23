package article

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articleevent"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
)

type fakeArticleRepository struct {
	candidates []articleDomain.Candidate
	list       []articleDomain.ListItem
	detail     articleDomain.Detail
}

type mutationRepository struct {
	fakeArticleRepository
	mutations []articleDomain.MutationResult
}

func (r *mutationRepository) UpsertMutation(_ context.Context, candidate articleDomain.Candidate, _ time.Time) (articleDomain.MutationResult, error) {
	r.candidates = append(r.candidates, candidate)
	result := r.mutations[0]
	r.mutations = r.mutations[1:]
	return result, nil
}

type outboxFake struct{ events []articleevent.Envelope }

func (o *outboxFake) Append(_ context.Context, event articleevent.Envelope) error {
	o.events = append(o.events, event)
	return nil
}
func (*outboxFake) Get(context.Context, string) (articleevent.Envelope, error) {
	return articleevent.Envelope{}, errors.New("unused")
}
func (*outboxFake) ExistsAggregateEvent(context.Context, string, int64, string) (bool, error) {
	return false, nil
}

type immediateTransaction struct{}

func (immediateTransaction) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

func (r *fakeArticleRepository) Upsert(_ context.Context, candidate articleDomain.Candidate, _ time.Time) (articleDomain.UpsertResult, int64, error) {
	r.candidates = append(r.candidates, candidate)
	return articleDomain.UpsertInserted, int64(len(r.candidates)), nil
}

func (r *fakeArticleRepository) ListPublished(context.Context, *articleDomain.Cursor, int) ([]articleDomain.ListItem, error) {
	return r.list, nil
}

func (r *fakeArticleRepository) GetPublished(context.Context, int64) (articleDomain.Detail, error) {
	return r.detail, nil
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

func TestIngestAppendsPublishedAndRevisedButNotUnchanged(t *testing.T) {
	now := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	repository := &mutationRepository{mutations: []articleDomain.MutationResult{
		{Result: articleDomain.UpsertInserted, ArticleID: 1, Origin: articleDomain.OriginRSS, RevisionID: 11, RevisionNo: 1, ContentHash: strings.Repeat("a", 64), Status: articleDomain.StatusPublished, LockVersion: 1},
		{Result: articleDomain.UpsertUpdated, ArticleID: 1, Origin: articleDomain.OriginRSS, RevisionID: 12, RevisionNo: 2, ContentHash: strings.Repeat("b", 64), Status: articleDomain.StatusPublished, LockVersion: 2},
		{Result: articleDomain.UpsertUnchanged, ArticleID: 1, Origin: articleDomain.OriginRSS, RevisionID: 12, RevisionNo: 2, ContentHash: strings.Repeat("b", 64), Status: articleDomain.StatusPublished, LockVersion: 2},
	}}
	outbox := &outboxFake{}
	service := NewServiceWithOutbox(repository, fakeSanitizer{}, fixedClock{value: now}, immediateTransaction{}, outbox)
	id := "rss-1"
	content := "正文"
	for index := 0; index < 3; index++ {
		if _, err := service.Ingest(context.Background(), 7, []ports.ParsedItem{{ID: &id, URL: "https://example.com/a", Content: &content}}); err != nil {
			t.Fatal(err)
		}
	}
	if len(outbox.events) != 2 || outbox.events[0].EventType != articleevent.PublishedType || outbox.events[1].EventType != articleevent.RevisedType {
		t.Fatalf("事件选择错误: %+v", outbox.events)
	}
}

func TestArticleTransitionEventSelection(t *testing.T) {
	now := time.Date(2026, 9, 23, 1, 0, 0, 0, time.UTC)
	base := articleDomain.StoredArticle{ID: 7, Origin: articleDomain.OriginUser, Status: articleDomain.StatusDraft, RevisionID: 9, RevisionNumber: 1, LockVersion: 1, Revision: articleDomain.RevisionData{ContentHash: strings.Repeat("a", 64)}}
	reasonAuthor, reasonAdmin := articleDomain.OfflineByAuthor, articleDomain.OfflineByAdmin
	tests := []struct {
		name    string
		before  *articleDomain.StoredArticle
		after   articleDomain.StoredArticle
		changed bool
		want    string
	}{
		{"直接发布", nil, func() articleDomain.StoredArticle { v := base; v.Status = articleDomain.StatusPublished; return v }(), true, articleevent.PublishedType},
		{"公开修订", func() *articleDomain.StoredArticle { v := base; v.Status = articleDomain.StatusPublished; return &v }(), func() articleDomain.StoredArticle {
			v := base
			v.Status = articleDomain.StatusPublished
			v.LockVersion = 2
			return v
		}(), true, articleevent.RevisedType},
		{"草稿编辑", &base, func() articleDomain.StoredArticle { v := base; v.LockVersion = 2; return v }(), true, ""},
		{"作者下架", func() *articleDomain.StoredArticle { v := base; v.Status = articleDomain.StatusPublished; return &v }(), func() articleDomain.StoredArticle {
			v := base
			v.Status = articleDomain.StatusOffline
			v.OfflineReason = &reasonAuthor
			v.LockVersion = 2
			return v
		}(), false, articleevent.OfflinedType},
		{"管理员下架", func() *articleDomain.StoredArticle { v := base; v.Status = articleDomain.StatusPublished; return &v }(), func() articleDomain.StoredArticle {
			v := base
			v.Status = articleDomain.StatusOffline
			v.OfflineReason = &reasonAdmin
			v.LockVersion = 2
			return v
		}(), false, articleevent.OfflinedType},
		{"恢复公开", func() *articleDomain.StoredArticle {
			v := base
			v.Status = articleDomain.StatusOffline
			v.OfflineReason = &reasonAuthor
			return &v
		}(), func() articleDomain.StoredArticle {
			v := base
			v.Status = articleDomain.StatusPublished
			v.LockVersion = 2
			return v
		}(), false, articleevent.PublishedType},
		{"任意状态删除", &base, func() articleDomain.StoredArticle {
			v := base
			v.Status = articleDomain.StatusDeleted
			v.LockVersion = 2
			return v
		}(), false, articleevent.DeletedType},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			outbox := &outboxFake{}
			if err := appendArticleTransition(context.Background(), outbox, test.before, test.after, test.changed, now); err != nil {
				t.Fatal(err)
			}
			if test.want == "" {
				if len(outbox.events) != 0 {
					t.Fatalf("不应发事件: %+v", outbox.events)
				}
				return
			}
			if len(outbox.events) != 1 || outbox.events[0].EventType != test.want {
				t.Fatalf("events=%+v", outbox.events)
			}
		})
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
	decoded, err := decodeCursor(*page.NextCursor)
	if err != nil || decoded.ArticleID != 2 || !decoded.SortAt.Equal(now) {
		t.Fatal(err)
	}
}

func TestLatestCursorVersionTwoRoundTripAndRejectsVersionOne(t *testing.T) {
	now := time.Date(2026, 9, 22, 9, 30, 0, 0, time.FixedZone("CST", 8*60*60))
	want := articleDomain.Cursor{SortAt: now, ArticleID: 42}
	encoded := encodeCursor(want)
	decoded, err := decodeCursor(encoded)
	if err != nil || decoded.ArticleID != want.ArticleID || !decoded.SortAt.Equal(now) {
		t.Fatalf("v2 游标往返 = %+v err=%v", decoded, err)
	}

	legacyJSON, err := json.Marshal(cursorPayload{Version: 1, SortAt: now.UTC(), ArticleID: 42})
	if err != nil {
		t.Fatal(err)
	}
	legacy := base64.RawURLEncoding.EncodeToString(legacyJSON)
	if _, err := decodeCursor(legacy); !errors.Is(err, articleDomain.ErrInvalidCursor) {
		t.Fatalf("v1 游标应失效，实际错误: %v", err)
	}
}

func TestGetRejectsInvalidIDAndReturnsDetail(t *testing.T) {
	htmlValue := "<p>正文</p>"
	repository := &fakeArticleRepository{detail: articleDomain.Detail{Item: articleDomain.ListItem{ID: 3}, SanitizedHTML: &htmlValue}}
	service := NewService(repository, fakeSanitizer{}, fixedClock{value: time.Now().UTC()})
	if _, err := service.Get(context.Background(), 0); err == nil {
		t.Fatal("非法 ID 应被拒绝")
	}
	detail, err := service.Get(context.Background(), 3)
	if err != nil || detail.Item.ID != 3 || detail.SanitizedHTML == nil || *detail.SanitizedHTML != htmlValue {
		t.Fatalf("detail=%+v err=%v", detail, err)
	}
}
