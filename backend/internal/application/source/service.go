// Package source 编排 Source 管理、认领与单来源抓取。
package source

import (
	"context"
	"errors"
	"time"

	articleApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/article"
	"github.com/google/uuid"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	sourceDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/source"
)

// manualLeaseTTL 是手动抓取的租约时长：同步 HTTP 操作有界，租约只需覆盖一次请求。
const manualLeaseTTL = 2 * time.Minute

type Service struct {
	repository sourceDomain.Repository
	runs       sourceDomain.FetchRunRepository
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

// NewFailureError 构造带稳定分类的抓取失败；分类会进入响应但不携带上游细节。
func NewFailureError(code string, cause error) *FailureError {
	return &FailureError{code: code, cause: cause}
}

func (e *FailureError) Error() string { return e.code + ": " + e.cause.Error() }
func (e *FailureError) Unwrap() error { return e.cause }
func (e *FailureError) Code() string  { return e.code }

func NewService(repository sourceDomain.Repository, runs sourceDomain.FetchRunRepository, fetcher ports.FeedFetcher,
	parser ports.FeedParser, articles *articleApp.Service, clock ports.Clock, txManager ports.TxManager, jitter func() float64) *Service {
	if jitter == nil {
		jitter = func() float64 { return 0 }
	}
	return &Service{repository: repository, runs: runs, fetcher: fetcher, parser: parser,
		articles: articles, clock: clock, txManager: txManager, jitter: jitter}
}

// Add 新增来源；未指定周期时使用领域默认值。
func (s *Service) Add(ctx context.Context, rawURL string) (sourceDomain.Source, bool, error) {
	return s.AddWithInterval(ctx, rawURL, sourceDomain.DefaultFetchInterval)
}

func (s *Service) AddWithInterval(ctx context.Context, rawURL string, fetchInterval time.Duration) (sourceDomain.Source, bool, error) {
	normalized, err := sourceDomain.NormalizeFeedURL(rawURL)
	if err != nil {
		return sourceDomain.Source{}, false, err
	}
	if err := sourceDomain.ValidateFetchInterval(fetchInterval); err != nil {
		return sourceDomain.Source{}, false, err
	}
	now := s.clock.Now().UTC()
	return s.repository.Add(ctx, rawURL, normalized, sourceDomain.DefaultTitle(normalized), fetchInterval, now)
}

func (s *Service) List(ctx context.Context) ([]sourceDomain.Source, error) {
	return s.repository.List(ctx)
}

// Get 读取单个来源当前状态，供调用方取得 lock_version。
func (s *Service) Get(ctx context.Context, id int64) (sourceDomain.Source, error) {
	return s.repository.Get(ctx, id)
}

// Pause 暂停来源；expectedVersion 必须与当前 lock_version 一致。
func (s *Service) Pause(ctx context.Context, id, expectedVersion int64) (sourceDomain.Source, error) {
	return s.repository.Pause(ctx, id, expectedVersion, s.clock.Now().UTC())
}

// Resume 恢复来源；暂停会清除租约，因此旧抓取无法再写入完成状态。
func (s *Service) Resume(ctx context.Context, id, expectedVersion int64) (sourceDomain.Source, error) {
	return s.repository.Resume(ctx, id, expectedVersion, s.clock.Now().UTC())
}

// SetFetchInterval 修改抓取周期；Feed URL 不可修改，更换地址必须新增来源。
func (s *Service) SetFetchInterval(ctx context.Context, id, expectedVersion int64, interval time.Duration) (sourceDomain.Source, error) {
	if err := sourceDomain.ValidateFetchInterval(interval); err != nil {
		return sourceDomain.Source{}, err
	}
	return s.repository.SetFetchInterval(ctx, id, expectedVersion, interval, s.clock.Now().UTC())
}

// ManualFetch 描述一次手动抓取。OnComplete 在完成事务内被调用，
// 让调用方把成功结果写回自己的幂等记录，与文章修订、Source 完成同事务提交。
type ManualFetch struct {
	SourceID   int64
	Force      bool
	Actor      *string
	OnComplete func(context.Context, sourceDomain.FetchRun) error
}

// FetchByID 手动抓取单个源；force 为 true 时忽略条件请求头，强制重新拉取并重洗全部条目。
// actor 为空表示本地运维触发：历史里记为定时来源，直到调用方提供操作者身份。
func (s *Service) FetchByID(ctx context.Context, id int64, force bool, actor *string) (FetchOutcome, error) {
	outcome, _, err := s.FetchManual(ctx, ManualFetch{SourceID: id, Force: force, Actor: actor})
	return outcome, err
}

// FetchManual 手动抓取：租约认领与运行记录在同一短事务里提交，
// 随后在事务外做网络调用，最后在完成事务里写入文章、Source 与运行结果。
func (s *Service) FetchManual(ctx context.Context, request ManualFetch) (FetchOutcome, sourceDomain.FetchRun, error) {
	now := s.clock.Now().UTC()
	var claimed sourceDomain.Source
	var run sourceDomain.FetchRun
	err := s.txManager.WithinTransaction(ctx, func(txContext context.Context) error {
		source, claimErr := s.repository.ClaimByID(txContext, request.SourceID, "manual", now, now.Add(manualLeaseTTL))
		if claimErr != nil {
			return claimErr
		}
		claimed = source
		recorded, runErr := s.startRun(txContext, source, triggerFor(request.Actor), request.Actor, now)
		run = recorded
		return runErr
	})
	if err != nil {
		return FetchOutcome{}, sourceDomain.FetchRun{}, err
	}
	return s.execute(ctx, claimed, run, request.Force, request.OnComplete)
}

// FetchClaimed 处理调度器已经认领的来源；运行记录单独短事务提交。
func (s *Service) FetchClaimed(ctx context.Context, source sourceDomain.Source) (FetchOutcome, error) {
	now := s.clock.Now().UTC()
	var run sourceDomain.FetchRun
	err := s.txManager.WithinTransaction(ctx, func(txContext context.Context) error {
		recorded, runErr := s.startRun(txContext, source, sourceDomain.TriggerScheduled, nil, now)
		run = recorded
		return runErr
	})
	if err != nil {
		return FetchOutcome{}, err
	}
	outcome, _, err := s.execute(ctx, source, run, false, nil)
	return outcome, err
}

// startRun 先把该来源上租约已失效的遗留运行收敛为中止，再登记本次运行。
// 两个动作与租约同事务，避免崩溃后留下永久停留在运行中的记录。
func (s *Service) startRun(ctx context.Context, src sourceDomain.Source, trigger sourceDomain.FetchTrigger,
	actor *string, now time.Time) (sourceDomain.FetchRun, error) {
	if _, err := s.runs.AbortStale(ctx, src.ID, src.LeaseGeneration, now); err != nil {
		return sourceDomain.FetchRun{}, err
	}
	run, err := sourceDomain.NewFetchRun(uuid.NewString(), src.ID, trigger, actor, src.LeaseGeneration, now)
	if err != nil {
		return sourceDomain.FetchRun{}, err
	}
	if err := s.runs.Start(ctx, run); err != nil {
		return sourceDomain.FetchRun{}, err
	}
	return run, nil
}

func triggerFor(actor *string) sourceDomain.FetchTrigger {
	if actor != nil {
		return sourceDomain.TriggerManual
	}
	return sourceDomain.TriggerScheduled
}

func (s *Service) ClaimDue(ctx context.Context, owner string, limit int, leaseDuration time.Duration) ([]sourceDomain.Source, error) {
	now := s.clock.Now().UTC()
	return s.repository.ClaimDue(ctx, owner, now, now.Add(leaseDuration), limit)
}

// execute 执行已登记的运行：网络与解析在事务外，完成写入在同一个带 fencing 的事务内。
// execute 执行已登记的运行并返回它的最终状态。
func (s *Service) execute(ctx context.Context, src sourceDomain.Source, run sourceDomain.FetchRun, force bool,
	onComplete func(context.Context, sourceDomain.FetchRun) error) (FetchOutcome, sourceDomain.FetchRun, error) {
	lease, err := src.CurrentLease()
	if err != nil {
		return FetchOutcome{}, run, err
	}
	request := ports.FetchRequest{URL: src.FeedURL}
	if !force {
		request.ETag = src.ETag
		request.LastModified = src.LastModified
	}
	response, err := s.fetcher.Fetch(ctx, request)
	if err != nil {
		return FetchOutcome{}, run, s.fail(ctx, src, lease, run, classifyFetchError(err), err)
	}
	if response.NotModified {
		completedAt := s.clock.Now().UTC()
		err := s.txManager.WithinTransaction(ctx, func(txContext context.Context) error {
			if err := s.repository.MarkNotModified(txContext, src.ID, lease, response.ETag, response.LastModified,
				completedAt, completedAt.Add(src.FetchIntervalOr())); err != nil {
				return err
			}
			return s.completeRun(txContext, &run, sourceDomain.FetchRunStats{NotModified: true}, completedAt, onComplete)
		})
		if err != nil {
			return FetchOutcome{}, run, err
		}
		return FetchOutcome{NotModified: true}, run, nil
	}
	feed, err := s.parser.Parse(ctx, response.Body, response.FinalURL)
	if err != nil {
		return FetchOutcome{}, run, s.fail(ctx, src, lease, run, "PARSE_FAILED", err)
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
		if err := s.repository.MarkSuccess(txContext, src.ID, lease, metadata, completedAt,
			completedAt.Add(src.FetchIntervalOr())); err != nil {
			return err
		}
		return s.completeRun(txContext, &run, ingestStats(report), completedAt, onComplete)
	})
	if err != nil {
		if errors.Is(err, sourceDomain.ErrLeaseLost) {
			return FetchOutcome{}, run, err
		}
		return FetchOutcome{}, run, s.fail(ctx, src, lease, run, "INGEST_FAILED", err)
	}
	return FetchOutcome{Report: report}, run, nil
}

// completeRun 在同一事务里写入运行的终态与统计，并让调用方写回自己的幂等结果。
func (s *Service) completeRun(ctx context.Context, run *sourceDomain.FetchRun, stats sourceDomain.FetchRunStats,
	completedAt time.Time, onComplete func(context.Context, sourceDomain.FetchRun) error) error {
	if err := run.Succeed(stats, completedAt); err != nil {
		return err
	}
	if err := s.runs.Complete(ctx, *run); err != nil {
		return err
	}
	if onComplete == nil {
		return nil
	}
	return onComplete(ctx, *run)
}

func ingestStats(report articleApp.IngestReport) sourceDomain.FetchRunStats {
	skipped := 0
	for _, count := range report.Skipped {
		skipped += count
	}
	return sourceDomain.FetchRunStats{
		Inserted: report.Inserted, Updated: report.Updated, Unchanged: report.Unchanged, Skipped: skipped,
	}
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

// fail 在独立短事务里写入退避与失败记录。
// 若该写入本身因租约失效被拒，说明失败已无法归因，只保留可识别的租约丢失结果。
func (s *Service) fail(ctx context.Context, src sourceDomain.Source, lease sourceDomain.Lease,
	run sourceDomain.FetchRun, code string, cause error) error {
	now := s.clock.Now().UTC()
	update := sourceDomain.NextFailure(now, src.ConsecutiveFailures, code, s.jitter())
	err := s.txManager.WithinTransaction(ctx, func(txContext context.Context) error {
		if err := s.repository.MarkFailure(txContext, src.ID, lease, update); err != nil {
			return err
		}
		if err := run.Fail(code, now); err != nil {
			return err
		}
		return s.runs.Complete(txContext, run)
	})
	if errors.Is(err, sourceDomain.ErrLeaseLost) {
		return sourceDomain.ErrLeaseLost
	}
	if err != nil {
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
