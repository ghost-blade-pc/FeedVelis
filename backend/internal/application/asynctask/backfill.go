package asynctask

import (
	"context"
	"fmt"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articleevent"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
)

type BackfillRepository interface {
	BackfillCandidates(context.Context, int) ([]int64, bool, error)
	LockBackfillCandidate(context.Context, int64) (ArticleFact, bool, error)
}

type BackfillReport struct {
	Created int
	Skipped int
	Failed  int
	HasMore bool
}
type BackfillService struct {
	repository BackfillRepository
	outbox     ports.Outbox
	tx         ports.TxManager
	now        func() time.Time
}

func NewBackfillService(repository BackfillRepository, outbox ports.Outbox, tx ports.TxManager, now func() time.Time) *BackfillService {
	if now == nil {
		now = time.Now
	}
	return &BackfillService{repository: repository, outbox: outbox, tx: tx, now: now}
}

func (s *BackfillService) Run(ctx context.Context, limit int) (BackfillReport, error) {
	if limit < 1 || limit > 1000 {
		return BackfillReport{}, fmt.Errorf("limit 必须介于 1 和 1000")
	}
	ids, more, err := s.repository.BackfillCandidates(ctx, limit)
	if err != nil {
		return BackfillReport{}, err
	}
	report := BackfillReport{HasMore: more}
	for _, id := range ids {
		if ctx.Err() != nil {
			return report, ctx.Err()
		}
		created := false
		err = s.tx.WithinTransaction(ctx, func(txContext context.Context) error {
			fact, eligible, lockErr := s.repository.LockBackfillCandidate(txContext, id)
			if lockErr != nil {
				return lockErr
			}
			if !eligible {
				report.Skipped++
				return nil
			}
			event, eventErr := articleevent.Published(txContext, articleevent.PublicFact{ArticleID: fact.ArticleID, OriginType: fact.Origin, RevisionID: fact.RevisionID, RevisionNo: fact.RevisionNo, ContentHash: fact.ContentHash, LockVersion: fact.LockVersion}, s.now().UTC())
			if eventErr != nil {
				return eventErr
			}
			if eventErr = s.outbox.Append(txContext, event); eventErr == nil {
				created = true
			}
			return eventErr
		})
		if err != nil {
			report.Failed++
			continue
		}
		if created {
			report.Created++
		}
	}
	if report.Failed > 0 {
		return report, fmt.Errorf("补录有 %d 篇失败", report.Failed)
	}
	return report, nil
}
