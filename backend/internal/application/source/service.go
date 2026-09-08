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
	txManager  ports.TxManager
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

func NewService(repository sourceDomain.Repository, fetcher ports.FeedFetcher, parser ports.FeedParser, articles *articleApp.Service, clock ports.Clock, txManager ports.TxManager, jitter func() float64) *Service {
	if jitter == nil {
		jitter = func() float64 { return 0 }
	}
	return &Service{repository: repository, fetcher: fetcher, parser: parser, articles: articles, clock: clock, txManager: txManager, jitter: jitter}
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

// FetchByID 手动抓取单个源；force 为 true 时忽略条件请求头，强制重新拉取并重洗全部条目。
func (s *Service) FetchByID(ctx context.Context, id int64, force bool) (FetchOutcome, error) {
	now := s.clock.Now().UTC()
	source, err := s.repository.ClaimByID(ctx, id, "manual", now, now.Add(2*time.Minute))
	if err != nil {
		return FetchOutcome{}, err
	}
	return s.fetch(ctx, source, force)
}

func (s *Service) FetchClaimed(ctx context.Context, source sourceDomain.Source) (FetchOutcome, error) {
	return s.fetch(ctx, source, false)
}

func (s *Service) ClaimDue(ctx context.Context, owner string, limit int, leaseDuration time.Duration) ([]sourceDomain.Source, error) {
	now := s.clock.Now().UTC()
	return s.repository.ClaimDue(ctx, owner, now, now.Add(leaseDuration), limit)
}

func (s *Service) fetch(ctx context.Context, src sourceDomain.Source, force bool) (FetchOutcome, error) {
	lease, err := src.CurrentLease()
	if err != nil {
		return FetchOutcome{}, err
	}
	request := ports.FetchRequest{URL: src.FeedURL}
	if !force {
		request.ETag = src.ETag
		request.LastModified = src.LastModified
	}
	response, err := s.fetcher.Fetch(ctx, request)
	if err != nil {
		return FetchOutcome{}, s.fail(ctx, src, lease, classifyFetchError(err), err)
	}
	if response.NotModified {
		completedAt := s.clock.Now().UTC()
		if err := s.repository.MarkNotModified(ctx, src.ID, lease, response.ETag, response.LastModified, completedAt, completedAt.Add(normalFetchInterval)); err != nil {
			return FetchOutcome{}, err
		}
		return FetchOutcome{NotModified: true}, nil
	}
	feed, err := s.parser.Parse(ctx, response.Body, response.FinalURL)
	if err != nil {
		return FetchOutcome{}, s.fail(ctx, src, lease, "PARSE_FAILED", err)
	}
	siteURL := normalizeSiteURL(feed.SiteURL)
	metadata := sourceDomain.Metadata{Title: sourceDomain.TruncateRunes(feed.Title, sourceDomain.MaxTitleRunes), SiteURL: siteURL, ETag: response.ETag, LastModified: response.LastModified}
	if metadata.Title == "" {
		metadata.Title = sourceDomain.DefaultTitle(src.NormalizedFeedURL)
	}
	var report articleApp.IngestReport
	err = s.txManager.WithinTransaction(ctx, func(txContext context.Context) error {
		var ingestErr error
		report, ingestErr = s.articles.Ingest(txContext, src.ID, feed.Items)
		if ingestErr != nil {
			return ingestErr
		}
		completedAt := s.clock.Now().UTC()
		return s.repository.MarkSuccess(txContext, src.ID, lease, metadata, completedAt, completedAt.Add(normalFetchInterval))
	})
	if err != nil {
		if errors.Is(err, sourceDomain.ErrLeaseLost) {
			return FetchOutcome{}, err
		}
		return FetchOutcome{}, s.fail(ctx, src, lease, "INGEST_FAILED", err)
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

func (s *Service) fail(ctx context.Context, src sourceDomain.Source, lease sourceDomain.Lease, code string, cause error) error {
	update := sourceDomain.NextFailure(s.clock.Now().UTC(), src.ConsecutiveFailures, code, s.jitter())
	if err := s.repository.MarkFailure(ctx, src.ID, lease, update); err != nil {
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
