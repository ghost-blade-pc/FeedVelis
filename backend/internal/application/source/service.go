// Package source 编排 Source 管理、认领与单来源抓取。
package source

import (
	"context"
	"errors"
	"time"

	articleApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	sourceDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/source"
)

const normalFetchInterval = 30 * time.Minute

type Service struct {
	repository sourceDomain.Repository
	fetcher    ports.FeedFetcher
	parser     ports.FeedParser
	articles   *articleApp.Service
	clock      ports.Clock
	jitter     func() float64
}

type FetchOutcome struct {
	NotModified bool
	Report      articleApp.IngestReport
}

type FailureError struct {
	code  string
	cause error
}

func (e *FailureError) Error() string { return e.code + ": " + e.cause.Error() }
func (e *FailureError) Unwrap() error { return e.cause }
func (e *FailureError) Code() string  { return e.code }

func NewService(repository sourceDomain.Repository, fetcher ports.FeedFetcher, parser ports.FeedParser, articles *articleApp.Service, clock ports.Clock, jitter func() float64) *Service {
	if jitter == nil {
		jitter = func() float64 { return 0 }
	}
	return &Service{repository: repository, fetcher: fetcher, parser: parser, articles: articles, clock: clock, jitter: jitter}
}

func (s *Service) Add(ctx context.Context, rawURL string) (sourceDomain.Source, bool, error) {
	normalized, err := sourceDomain.NormalizeFeedURL(rawURL)
	if err != nil {
		return sourceDomain.Source{}, false, err
	}
	now := s.clock.Now().UTC()
	return s.repository.Add(ctx, rawURL, normalized, sourceDomain.DefaultTitle(normalized), now)
}

func (s *Service) List(ctx context.Context) ([]sourceDomain.Source, error) {
	return s.repository.List(ctx)
}

func (s *Service) Pause(ctx context.Context, id int64) error {
	return s.repository.Pause(ctx, id, s.clock.Now().UTC())
}

func (s *Service) Resume(ctx context.Context, id int64) error {
	return s.repository.Resume(ctx, id, s.clock.Now().UTC())
}

func (s *Service) FetchByID(ctx context.Context, id int64) (FetchOutcome, error) {
	now := s.clock.Now().UTC()
	source, err := s.repository.ClaimByID(ctx, id, "manual", now, now.Add(2*time.Minute))
	if err != nil {
		return FetchOutcome{}, err
	}
	return s.fetch(ctx, source)
}

func (s *Service) FetchClaimed(ctx context.Context, source sourceDomain.Source) (FetchOutcome, error) {
	return s.fetch(ctx, source)
}

func (s *Service) ClaimDue(ctx context.Context, owner string, limit int, leaseDuration time.Duration) ([]sourceDomain.Source, error) {
	now := s.clock.Now().UTC()
	return s.repository.ClaimDue(ctx, owner, now, now.Add(leaseDuration), limit)
}

func (s *Service) fetch(ctx context.Context, src sourceDomain.Source) (FetchOutcome, error) {
	now := s.clock.Now().UTC()
	response, err := s.fetcher.Fetch(ctx, ports.FetchRequest{URL: src.FeedURL, ETag: src.ETag, LastModified: src.LastModified})
	if err != nil {
		return FetchOutcome{}, s.fail(ctx, src, classifyFetchError(err), err)
	}
	if response.NotModified {
		if err := s.repository.MarkNotModified(ctx, src.ID, response.ETag, response.LastModified, now, now.Add(normalFetchInterval)); err != nil {
			return FetchOutcome{}, err
		}
		return FetchOutcome{NotModified: true}, nil
	}
	feed, err := s.parser.Parse(ctx, response.Body, response.FinalURL)
	if err != nil {
		return FetchOutcome{}, s.fail(ctx, src, "PARSE_FAILED", err)
	}
	report, err := s.articles.Ingest(ctx, src.ID, feed.Items)
	if err != nil {
		return FetchOutcome{}, s.fail(ctx, src, "INGEST_FAILED", err)
	}
	siteURL := normalizeSiteURL(feed.SiteURL)
	metadata := sourceDomain.Metadata{Title: sourceDomain.TruncateRunes(feed.Title, sourceDomain.MaxTitleRunes), SiteURL: siteURL, ETag: response.ETag, LastModified: response.LastModified}
	if metadata.Title == "" {
		metadata.Title = sourceDomain.DefaultTitle(src.NormalizedFeedURL)
	}
	if err := s.repository.MarkSuccess(ctx, src.ID, metadata, now, now.Add(normalFetchInterval)); err != nil {
		return FetchOutcome{}, err
	}
	return FetchOutcome{Report: report}, nil
}

func normalizeSiteURL(value *string) *string {
	if value == nil {
		return nil
	}
	normalized, err := sourceDomain.NormalizeFeedURL(*value)
	if err != nil {
		return nil
	}
	return &normalized
}

func (s *Service) fail(ctx context.Context, src sourceDomain.Source, code string, cause error) error {
	update := sourceDomain.NextFailure(s.clock.Now().UTC(), src.ConsecutiveFailures, code, s.jitter())
	if err := s.repository.MarkFailure(ctx, src.ID, update); err != nil {
		return errors.Join(cause, err)
	}
	return &FailureError{code: code, cause: cause}
}

func ErrorCode(err error) string {
	type coded interface{ Code() string }
	var value coded
	if errors.As(err, &value) {
		return value.Code()
	}
	return "FETCH_FAILED"
}

func classifyFetchError(err error) string { return ErrorCode(err) }
