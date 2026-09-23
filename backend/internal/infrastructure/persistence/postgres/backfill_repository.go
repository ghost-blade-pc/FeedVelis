package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/asynctask"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
)

type BackfillRepository struct{ pool *pgxpool.Pool }

func NewBackfillRepository(pool *pgxpool.Pool) *BackfillRepository {
	return &BackfillRepository{pool: pool}
}

func (r *BackfillRepository) BackfillCandidates(ctx context.Context, limit int) ([]int64, bool, error) {
	rows, err := r.pool.Query(ctx, `SELECT a.id FROM velis.articles a WHERE a.status='published'
AND NOT EXISTS(SELECT 1 FROM velis.outbox_events o WHERE o.aggregate_type='article' AND o.aggregate_id=a.id::text)
AND NOT EXISTS(SELECT 1 FROM velis.async_tasks t WHERE t.aggregate_type='article' AND t.aggregate_id=a.id::text)
ORDER BY a.id LIMIT $1`, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	ids := make([]int64, 0, limit+1)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, false, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(ids) > limit
	if more {
		ids = ids[:limit]
	}
	return ids, more, nil
}

func (r *BackfillRepository) LockBackfillCandidate(ctx context.Context, articleID int64) (asynctask.ArticleFact, bool, error) {
	tx, ok := transactionFromContext(ctx)
	if !ok {
		return asynctask.ArticleFact{}, false, ports.ErrTransactionRequired
	}
	var fact asynctask.ArticleFact
	err := tx.QueryRow(ctx, `SELECT a.id,a.origin_type,a.status,v.id,v.revision_no,v.content_hash,a.lock_version,
NOT EXISTS(SELECT 1 FROM velis.outbox_events o WHERE o.aggregate_type='article' AND o.aggregate_id=a.id::text)
AND NOT EXISTS(SELECT 1 FROM velis.async_tasks t WHERE t.aggregate_type='article' AND t.aggregate_id=a.id::text)
FROM velis.articles a JOIN velis.article_versions v ON v.id=a.current_revision_id WHERE a.id=$1 FOR UPDATE OF a`, articleID).Scan(&fact.ArticleID, &fact.Origin, &fact.Status, &fact.RevisionID, &fact.RevisionNo, &fact.ContentHash, &fact.LockVersion, &ok)
	return fact, err == nil && fact.Status == "published" && ok, err
}
