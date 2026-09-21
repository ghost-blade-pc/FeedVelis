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

func (r *sourceRepositoryFake) Add(context.Context, string, string, string, time.Duration, time.Time) (sourceDomain.Source, bool, error) {
	return r.source, true, nil
}
func (r *sourceRepositoryFake) List(context.Context) ([]sourceDomain.Source, error) {
	return []sourceDomain.Source{r.source}, nil
}
func (r *sourceRepositoryFake) Get(context.Context, int64) (sourceDomain.Source, error) {
	return r.source, nil
}
func (r *sourceRepositoryFake) ClaimByID(_ context.Context, _ int64, owner string, _ time.Time, leaseUntil time.Time) (sourceDomain.Source, error) {
	if r.source.Status == sourceDomain.StatusPaused {
		return sourceDomain.Source{}, sourceDomain.ErrInvalidStatus
	}
	r.source.LeaseOwner = &owner
	r.source.LeaseExpiresAt = &leaseUntil
	return r.source, nil
}
func (*sourceRepositoryFake) Pause(context.Context, int64, int64, time.Time) (sourceDomain.Source, error) {
	return sourceDomain.Source{}, nil
}
func (*sourceRepositoryFake) SetFetchInterval(context.Context, int64, int64, time.Duration, time.Time) (sourceDomain.Source, error) {
	return sourceDomain.Source{}, nil
}

func (*sourceRepositoryFake) Resume(context.Context, int64, int64, time.Time) (sourceDomain.Source, error) {
	return sourceDomain.Source{}, nil
}
func (*sourceRepositoryFake) ClaimDue(context.Context, string, time.Time, time.Time, int) ([]sourceDomain.Source, error) {
	return nil, nil
}
func (r *sourceRepositoryFake) MarkNotModified(context.Context, int64, sourceDomain.Lease, *string, *string, time.Time, time.Time) error {
	r.notModified = true
	return nil
}
func (*sourceRepositoryFake) MarkSuccess(context.Context, int64, sourceDomain.Lease, sourceDomain.Metadata, time.Time, time.Time) error {
	return nil
}
func (*sourceRepositoryFake) MarkFailure(context.Context, int64, sourceDomain.Lease, sourceDomain.FailureUpdate) error {
	return nil
}

// fetchRunRepositoryFake 记录运行的生命周期调用。
type fetchRunRepositoryFake struct {
	started     []sourceDomain.FetchRun
	completed   []sourceDomain.FetchRun
	abortStale  int64
	aborted     int64
	startErr    error
	completeErr error
}

func (f *fetchRunRepositoryFake) Start(_ context.Context, run sourceDomain.FetchRun) error {
	if f.startErr != nil {
		return f.startErr
	}
	f.started = append(f.started, run)
	return nil
}

func (f *fetchRunRepositoryFake) Complete(_ context.Context, run sourceDomain.FetchRun) error {
	if f.completeErr != nil {
		return f.completeErr
	}
	f.completed = append(f.completed, run)
	return nil
}

func (f *fetchRunRepositoryFake) AbortStale(context.Context, int64, int64, time.Time) (int64, error) {
	return f.aborted, nil
}

func (f *fetchRunRepositoryFake) ListBySource(context.Context, int64, *sourceDomain.FetchRunCursor, int) ([]sourceDomain.FetchRun, error) {
	return nil, nil
}

func (f *fetchRunRepositoryFake) CurrentRunning(context.Context, int64) (sourceDomain.FetchRun, error) {
	return sourceDomain.FetchRun{}, sourceDomain.ErrNotFound
}

type fetcherFake struct{ response ports.FetchResponse }

func (f fetcherFake) Fetch(context.Context, ports.FetchRequest) (ports.FetchResponse, error) {
	return f.response, nil
}

// requestCapturingFetcher 记录收到的抓取请求，用于断言条件请求头。
type requestCapturingFetcher struct {
	captured ports.FetchRequest
	response ports.FetchResponse
}

func (f *requestCapturingFetcher) Fetch(_ context.Context, request ports.FetchRequest) (ports.FetchResponse, error) {
	f.captured = request
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
func (articleRepositoryFake) GetPublished(context.Context, int64) (articleDomain.Detail, error) {
	return articleDomain.Detail{}, nil
}

type sanitizerFake struct{}

func (sanitizerFake) Sanitize(string) ports.SanitizedContent { return ports.SanitizedContent{} }

// activeSource 构造一个已认领的来源：运行记录要求租约与正的 generation。
func activeSource(t *testing.T, id int64, feedURL string) sourceDomain.Source {
	t.Helper()
	leaseUntil := time.Date(2026, 9, 8, 0, 2, 0, 0, time.UTC)
	owner := "worker-1"
	return sourceDomain.Source{ID: id, FeedURL: feedURL, Status: sourceDomain.StatusActive,
		LeaseOwner: &owner, LeaseExpiresAt: &leaseUntil, LeaseGeneration: 1}
}

type clockFake struct{ now time.Time }

func (c clockFake) Now() time.Time { return c.now }

type transactionManagerFake struct{}

func (transactionManagerFake) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

func TestNotModifiedDoesNotParseOrIngest(t *testing.T) {
	repository := &sourceRepositoryFake{source: activeSource(t, 1, "https://example.com/feed")}
	parser := &parserFake{}
	clock := clockFake{now: time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)}
	articles := articleApp.NewService(articleRepositoryFake{}, sanitizerFake{}, clock)
	service := NewService(repository, &fetchRunRepositoryFake{}, fetcherFake{response: ports.FetchResponse{NotModified: true}}, parser, articles, clock, transactionManagerFake{}, nil)
	outcome, err := service.FetchByID(context.Background(), 1, false, nil)
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
	service := NewService(repository, &fetchRunRepositoryFake{}, fetcherFake{}, &parserFake{}, articles, clock, transactionManagerFake{}, nil)
	if _, err := service.FetchByID(context.Background(), 1, false, nil); err != sourceDomain.ErrInvalidStatus {
		t.Fatalf("err=%v", err)
	}
}

func TestForceFetchSkipsConditionalHeaders(t *testing.T) {
	etag := `"etag-1"`
	lastModified := "Mon, 07 Sep 2026 00:00:00 GMT"
	claimed := activeSource(t, 1, "https://example.com/feed")
	claimed.ETag, claimed.LastModified = &etag, &lastModified
	repository := &sourceRepositoryFake{source: claimed}
	clock := clockFake{now: time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)}
	articles := articleApp.NewService(articleRepositoryFake{}, sanitizerFake{}, clock)
	fetcher := &requestCapturingFetcher{response: ports.FetchResponse{}}
	service := NewService(repository, &fetchRunRepositoryFake{}, fetcher, &parserFake{}, articles, clock, transactionManagerFake{}, nil)
	if _, err := service.FetchByID(context.Background(), 1, true, nil); err != nil {
		t.Fatal(err)
	}
	if fetcher.captured.ETag != nil || fetcher.captured.LastModified != nil {
		t.Fatalf("强制抓取不应携带条件请求头: %+v", fetcher.captured)
	}
	if _, err := service.FetchByID(context.Background(), 1, false, nil); err != nil {
		t.Fatal(err)
	}
	if fetcher.captured.ETag == nil || fetcher.captured.LastModified == nil {
		t.Fatalf("常规抓取应携带条件请求头: %+v", fetcher.captured)
	}
}
