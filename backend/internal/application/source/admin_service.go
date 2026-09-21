package source

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	idempotencyApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/idempotency"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	sourceDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/source"
)

// ErrSourceExists 表示规范化后等价的 Feed URL 已存在。
var ErrSourceExists = errors.New("来源已存在")

// AdminService 是管理员 Source 管理用例：全部写操作要求幂等键，修改既有来源还要求当前版本。
type AdminService struct {
	repository  sourceDomain.Repository
	runs        sourceDomain.FetchRunRepository
	fetch       *Service
	idempotency *idempotencyApp.Service
	clock       ports.Clock
}

func NewAdminService(repository sourceDomain.Repository, runs sourceDomain.FetchRunRepository,
	fetch *Service, idempotency *idempotencyApp.Service, clock ports.Clock) *AdminService {
	return &AdminService{repository: repository, runs: runs, fetch: fetch, idempotency: idempotency, clock: clock}
}

type CreateCommand struct {
	ActorUserID    string
	IdempotencyKey string
	FeedURL        string
	// FetchInterval 为零时使用领域默认值。
	FetchInterval time.Duration
}

type CreateResult struct {
	Source  sourceDomain.Source
	Created bool
}

// Create 新增来源：规范化 URL 已存在时返回既有来源并标记未创建，不产生第二个调度身份。
func (s *AdminService) Create(ctx context.Context, command CreateCommand) (CreateResult, bool, error) {
	normalized, err := sourceDomain.NormalizeFeedURL(command.FeedURL)
	if err != nil {
		return CreateResult{}, false, err
	}
	interval := command.FetchInterval
	if interval == 0 {
		interval = sourceDomain.DefaultFetchInterval
	}
	if err := sourceDomain.ValidateFetchInterval(interval); err != nil {
		return CreateResult{}, false, err
	}
	payload := struct {
		URL      string `json:"feed_url"`
		Interval int64  `json:"fetch_interval_seconds"`
	}{normalized, int64(interval / time.Second)}
	now := s.clock.Now().UTC()
	outcome, err := s.idempotency.Execute(ctx, idempotencyApp.Command{
		Identity: idempotencyApp.Identity{ActorUserID: command.ActorUserID, Operation: "source.create", Key: command.IdempotencyKey},
		Payload:  payload, Now: now,
	}, func(txContext context.Context) (any, string, string, error) {
		created, inserted, addErr := s.repository.Add(txContext, command.FeedURL, normalized,
			sourceDomain.DefaultTitle(normalized), interval, now)
		if addErr != nil {
			return nil, "", "", addErr
		}
		return CreateResult{Source: created, Created: inserted}, "source", jsonID(created.ID), nil
	})
	if err != nil {
		return CreateResult{}, false, err
	}
	var result CreateResult
	if err := json.Unmarshal(outcome.Result, &result); err != nil {
		return CreateResult{}, false, err
	}
	return result, outcome.Replayed, nil
}

func (s *AdminService) List(ctx context.Context) ([]sourceDomain.Source, error) {
	return s.repository.List(ctx)
}

func (s *AdminService) Get(ctx context.Context, sourceID int64) (sourceDomain.Source, error) {
	return s.repository.Get(ctx, sourceID)
}

// SourceCommand 是暂停、恢复与周期修改共用的命令；三者都要求当前版本。
type SourceCommand struct {
	ActorUserID     string
	IdempotencyKey  string
	SourceID        int64
	ExpectedVersion int64
	// Interval 仅周期修改使用。
	Interval time.Duration
}

func (s *AdminService) Pause(ctx context.Context, command SourceCommand) (sourceDomain.Source, bool, error) {
	return s.mutate(ctx, "source.pause", command, func(txContext context.Context, current sourceDomain.Source, now time.Time) (sourceDomain.Source, error) {
		if current.Status == sourceDomain.StatusPaused {
			// 已经暂停：保持幂等，不递增版本。
			return current, nil
		}
		return s.repository.Pause(txContext, current.ID, command.ExpectedVersion, now)
	})
}

func (s *AdminService) Resume(ctx context.Context, command SourceCommand) (sourceDomain.Source, bool, error) {
	return s.mutate(ctx, "source.resume", command, func(txContext context.Context, current sourceDomain.Source, now time.Time) (sourceDomain.Source, error) {
		if current.Status != sourceDomain.StatusPaused {
			return current, nil
		}
		return s.repository.Resume(txContext, current.ID, command.ExpectedVersion, now)
	})
}

func (s *AdminService) SetInterval(ctx context.Context, command SourceCommand) (sourceDomain.Source, bool, error) {
	if err := sourceDomain.ValidateFetchInterval(command.Interval); err != nil {
		return sourceDomain.Source{}, false, err
	}
	return s.mutate(ctx, "source.interval", command, func(txContext context.Context, current sourceDomain.Source, now time.Time) (sourceDomain.Source, error) {
		return s.repository.SetFetchInterval(txContext, current.ID, command.ExpectedVersion, command.Interval, now)
	})
}

// mutate 是暂停/恢复/改周期的共同骨架：先按当前版本校验，再在幂等事务内写入。
func (s *AdminService) mutate(ctx context.Context, operation string, command SourceCommand,
	change func(context.Context, sourceDomain.Source, time.Time) (sourceDomain.Source, error)) (sourceDomain.Source, bool, error) {
	payload := struct {
		SourceID int64 `json:"source_id"`
		Expected int64 `json:"expected_version"`
		Interval int64 `json:"fetch_interval_seconds,omitempty"`
	}{command.SourceID, command.ExpectedVersion, int64(command.Interval / time.Second)}
	now := s.clock.Now().UTC()
	outcome, err := s.idempotency.Execute(ctx, idempotencyApp.Command{
		Identity: idempotencyApp.Identity{ActorUserID: command.ActorUserID, Operation: operation, Key: command.IdempotencyKey},
		Payload:  payload, Now: now,
	}, func(txContext context.Context) (any, string, string, error) {
		current, getErr := s.repository.Get(txContext, command.SourceID)
		if getErr != nil {
			return nil, "", "", getErr
		}
		if err := sourceDomain.CheckVersion(current.LockVersion, command.ExpectedVersion); err != nil {
			return nil, "", "", err
		}
		updated, changeErr := change(txContext, current, now)
		if changeErr != nil {
			return nil, "", "", changeErr
		}
		return updated, "source", jsonID(updated.ID), nil
	})
	if err != nil {
		return sourceDomain.Source{}, false, err
	}
	var updated sourceDomain.Source
	if err := json.Unmarshal(outcome.Result, &updated); err != nil {
		return sourceDomain.Source{}, false, err
	}
	return updated, outcome.Replayed, nil
}

type FetchCommand struct {
	ActorUserID    string
	IdempotencyKey string
	SourceID       int64
	Force          bool
}

// FetchRunResult 是手动抓取的结果：运行记录加上是否来自重放或仍在执行。
type FetchRunResult struct {
	Run      sourceDomain.FetchRun
	Outcome  FetchOutcome
	Replayed bool
	// Running 表示同键的首次执行仍在进行中，本次返回的是同一个运行 ID。
	Running bool
}

// fetchResultPayload 是写入幂等记录的结果载荷：历史记录本身就是这次操作的业务结果。
type fetchResultPayload struct {
	Run sourceDomain.FetchRun `json:"run"`
}

// FetchNow 同步执行一次手动抓取。
// 幂等占位与租约认领分开提交，网络调用不在任何事务内；
// 同键重试若首次仍在执行，返回同一运行 ID 而不是发起第二次抓取。
func (s *AdminService) FetchNow(ctx context.Context, command FetchCommand) (FetchRunResult, error) {
	payload := struct {
		SourceID int64 `json:"source_id"`
		Force    bool  `json:"force"`
	}{command.SourceID, command.Force}
	identity := idempotencyApp.Identity{ActorUserID: command.ActorUserID, Operation: "source.fetch", Key: command.IdempotencyKey}
	now := s.clock.Now().UTC()
	reservation, err := s.idempotency.Reserve(ctx, idempotencyApp.Command{Identity: identity, Payload: payload, Now: now})
	if err != nil {
		return FetchRunResult{}, err
	}
	if reservation.Replay != nil {
		var recorded fetchResultPayload
		if err := json.Unmarshal(reservation.Replay, &recorded); err != nil {
			return FetchRunResult{}, err
		}
		return FetchRunResult{Run: recorded.Run, Replayed: true}, nil
	}
	if reservation.Pending {
		running, runErr := s.runs.CurrentRunning(ctx, command.SourceID)
		if runErr != nil {
			return FetchRunResult{}, runErr
		}
		return FetchRunResult{Run: running, Running: true}, nil
	}

	actor := command.ActorUserID
	outcome, run, err := s.fetch.FetchManual(ctx, ManualFetch{
		SourceID: command.SourceID, Force: command.Force, Actor: &actor,
		OnComplete: func(txContext context.Context, completed sourceDomain.FetchRun) error {
			// 成功结果只在完成事务里写回：失败与回滚都不会固化为可重放的成功。
			return s.idempotency.Settle(txContext, identity, fetchResultPayload{Run: completed}, "fetch_run", completed.ID, s.clock.Now().UTC())
		},
	})
	if err != nil {
		return FetchRunResult{}, err
	}
	return FetchRunResult{Run: run, Outcome: outcome}, nil
}

type HistoryCommand struct {
	SourceID int64
	Cursor   string
	Limit    int
}

type HistoryPage struct {
	Items      []sourceDomain.FetchRun
	NextCursor *string
	HasMore    bool
}

// History 按开始时间倒序翻页；游标是不透明字符串，版本化后仍可演进。
func (s *AdminService) History(ctx context.Context, command HistoryCommand) (HistoryPage, error) {
	limit, err := sourceDomain.NormalizeFetchRunLimit(command.Limit)
	if err != nil {
		return HistoryPage{}, err
	}
	var cursor *sourceDomain.FetchRunCursor
	if command.Cursor != "" {
		decoded, err := decodeRunCursor(command.Cursor)
		if err != nil {
			return HistoryPage{}, err
		}
		cursor = &decoded
	}
	items, err := s.runs.ListBySource(ctx, command.SourceID, cursor, limit+1)
	if err != nil {
		return HistoryPage{}, err
	}
	page := HistoryPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.HasMore = true
		encoded := encodeRunCursor(page.Items[len(page.Items)-1])
		page.NextCursor = &encoded
	}
	return page, nil
}

func jsonID(value int64) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
