package source

import (
	"context"
	"testing"
	"time"

	articleApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
	sourceDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/source"
)

type sourceRepositoryFake struct {
	source      sourceDomain.Source
	notModified bool
}

func (r *sourceRepositoryFake) Add(context.Context, string, string, string, time.Time) (sourceDomain.Source, bool, error) {
	return r.source, true, nil
}
func (r *sourceRepositoryFake) List(context.Context) ([]sourceDomain.Source, error) {
	return []sourceDomain.Source{r.source}, nil
}
func (r *sourceRepositoryFake) Get(context.Context, int64) (sourceDomain.Source, error) {
	return r.source, nil
}
func (r *sourceRepositoryFake) ClaimByID(context.Context, int64, string, time.Time, time.Time) (sourceDomain.Source, error) {
	if r.source.Status == sourceDomain.StatusPaused {
		return sourceDomain.Source{}, sourceDomain.ErrInvalidStatus
	}
	return r.source, nil
}
func (*sourceRepositoryFake) Pause(context.Context, int64, time.Time) error  { return nil }
func (*sourceRepositoryFake) Resume(context.Context, int64, time.Time) error { return nil }
func (*sourceRepositoryFake) ClaimDue(context.Context, string, time.Time, time.Time, int) ([]sourceDomain.Source, error) {
	return nil, nil
}
func (r *sourceRepositoryFake) MarkNotModified(context.Context, int64, *string, *string, time.Time, time.Time) error {
	r.notModified = true
	return nil
}
func (*sourceRepositoryFake) MarkSuccess(context.Context, int64, sourceDomain.Metadata, time.Time, time.Time) error {
	return nil
}
func (*sourceRepositoryFake) MarkFailure(context.Context, int64, sourceDomain.FailureUpdate) error {
	return nil
}

type fetcherFake struct{ response ports.FetchResponse }

func (f fetcherFake) Fetch(context.Context, ports.FetchRequest) (ports.FetchResponse, error) {
	return f.response, nil
}

type parserFake struct{ called bool }

func (p *parserFake) Parse(context.Context, []byte, string) (ports.ParsedFeed, error) {
	p.called = true
	return ports.ParsedFeed{}, nil
}

type articleRepositoryFake struct{}

func (articleRepositoryFake) Upsert(context.Context, articleDomain.Candidate, time.Time) (articleDomain.UpsertResult, int64, error) {
	return articleDomain.UpsertInserted, 1, nil
}
func (articleRepositoryFake) ListPublished(context.Context, *articleDomain.Cursor, int) ([]articleDomain.ListItem, error) {
	return nil, nil
}

type sanitizerFake struct{}

func (sanitizerFake) Sanitize(string) ports.SanitizedContent { return ports.SanitizedContent{} }

type clockFake struct{ now time.Time }

func (c clockFake) Now() time.Time { return c.now }

func TestNotModifiedDoesNotParseOrIngest(t *testing.T) {
	repository := &sourceRepositoryFake{source: sourceDomain.Source{ID: 1, FeedURL: "https://example.com/feed", Status: sourceDomain.StatusActive}}
	parser := &parserFake{}
	clock := clockFake{now: time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)}
	articles := articleApp.NewService(articleRepositoryFake{}, sanitizerFake{}, clock)
	service := NewService(repository, fetcherFake{response: ports.FetchResponse{NotModified: true}}, parser, articles, clock, nil)
	outcome, err := service.FetchByID(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.NotModified || parser.called || !repository.notModified {
		t.Fatalf("outcome=%+v parser=%t marked=%t", outcome, parser.called, repository.notModified)
	}
}

func TestPausedSourceCannotBeFetched(t *testing.T) {
	repository := &sourceRepositoryFake{source: sourceDomain.Source{ID: 1, Status: sourceDomain.StatusPaused}}
	clock := clockFake{now: time.Now()}
	articles := articleApp.NewService(articleRepositoryFake{}, sanitizerFake{}, clock)
	service := NewService(repository, fetcherFake{}, &parserFake{}, articles, clock, nil)
	if _, err := service.FetchByID(context.Background(), 1); err != sourceDomain.ErrInvalidStatus {
		t.Fatalf("err=%v", err)
	}
}
