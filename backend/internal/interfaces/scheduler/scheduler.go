// Package scheduler 将周期触发适配为 Source Application 调用。
package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"time"

	sourceApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/source"
)

type Scheduler struct {
	service  *sourceApp.Service
	logger   *slog.Logger
	owner    string
	interval time.Duration
}

func New(service *sourceApp.Service, logger *slog.Logger, owner string, interval time.Duration) *Scheduler {
	return &Scheduler{service: service, logger: logger, owner: owner, interval: interval}
}

func (s *Scheduler) Run(ctx context.Context) error {
	if err := s.runBatch(ctx); err != nil && ctx.Err() == nil {
		s.logger.Error("认领待抓取来源失败", "error", err)
	}
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := s.runBatch(ctx); err != nil && ctx.Err() == nil {
				s.logger.Error("认领待抓取来源失败", "error", err)
			}
		}
	}
}

func (s *Scheduler) runBatch(ctx context.Context) error {
	sources, err := s.service.ClaimDue(ctx, s.owner, 20, 2*time.Minute)
	if err != nil {
		return err
	}
	var group sync.WaitGroup
	for _, src := range sources {
		src := src
		group.Add(1)
		go func() {
			defer group.Done()
			startedAt := time.Now()
			outcome, err := s.service.FetchClaimed(ctx, src)
			if err != nil {
				s.logger.Warn("来源抓取失败", "source_id", src.ID, "error_code", sourceApp.ErrorCode(err), "duration_ms", time.Since(startedAt).Milliseconds())
				return
			}
			s.logger.Info("来源抓取完成", "source_id", src.ID, "not_modified", outcome.NotModified,
				"inserted", outcome.Report.Inserted, "updated", outcome.Report.Updated, "unchanged", outcome.Report.Unchanged,
				"skipped", skippedCount(outcome.Report.Skipped), "duration_ms", time.Since(startedAt).Milliseconds())
		}()
	}
	group.Wait()
	return ctx.Err()
}

func skippedCount(reasons map[string]int) int {
	count := 0
	for _, value := range reasons {
		count += value
	}
	return count
}
