package postgres

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/asynctask"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
)

type AsyncTaskRepository struct{}

func NewAsyncTaskRepository() *AsyncTaskRepository { return &AsyncTaskRepository{} }

func requireTx(ctx context.Context) (pgx.Tx, error) {
	tx, ok := transactionFromContext(ctx)
	if !ok {
		return nil, ports.ErrTransactionRequired
	}
	return tx, nil
}

func (r *AsyncTaskRepository) LockArticle(ctx context.Context, articleID int64) (asynctask.ArticleFact, error) {
	tx, err := requireTx(ctx)
	if err != nil {
		return asynctask.ArticleFact{}, err
	}
	var fact asynctask.ArticleFact
	err = tx.QueryRow(ctx, `SELECT a.id,a.origin_type,a.status,v.id,v.revision_no,v.content_hash,a.lock_version
FROM velis.articles a JOIN velis.article_versions v ON v.id=a.current_revision_id
WHERE a.id=$1 FOR UPDATE OF a`, articleID).Scan(&fact.ArticleID, &fact.Origin, &fact.Status, &fact.RevisionID, &fact.RevisionNo, &fact.ContentHash, &fact.LockVersion)
	return fact, err
}

func (r *AsyncTaskRepository) LockTask(ctx context.Context, articleID int64) (*asynctask.Task, error) {
	tx, err := requireTx(ctx)
	if err != nil {
		return nil, err
	}
	var task asynctask.Task
	err = tx.QueryRow(ctx, `SELECT id::text,article_id,status,generation,revision_id,revision_no,content_hash,observed_aggregate_version
FROM velis.async_tasks WHERE task_type='article.enrichment' AND aggregate_type='article' AND aggregate_id=$1 FOR UPDATE`, strconv.FormatInt(articleID, 10)).Scan(&task.ID, &task.ArticleID, &task.Status, &task.Generation, &task.RevisionID, &task.RevisionNo, &task.ContentHash, &task.ObservedVersion)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return &task, err
}

func (r *AsyncTaskRepository) CreatePending(ctx context.Context, id string, fact asynctask.ArticleFact, now time.Time) (asynctask.Task, error) {
	tx, err := requireTx(ctx)
	if err != nil {
		return asynctask.Task{}, err
	}
	var task asynctask.Task
	err = tx.QueryRow(ctx, `INSERT INTO velis.async_tasks(id,task_type,aggregate_type,aggregate_id,article_id,revision_id,revision_no,content_hash,status,generation,observed_aggregate_version,created_at,updated_at)
VALUES($1,'article.enrichment','article',$2,$3,$4,$5,$6,'pending',1,$7,$8,$8)
RETURNING id::text,article_id,status,generation,revision_id,revision_no,content_hash,observed_aggregate_version`, id, strconv.FormatInt(fact.ArticleID, 10), fact.ArticleID, fact.RevisionID, fact.RevisionNo, fact.ContentHash, fact.LockVersion, now).Scan(&task.ID, &task.ArticleID, &task.Status, &task.Generation, &task.RevisionID, &task.RevisionNo, &task.ContentHash, &task.ObservedVersion)
	return task, err
}

func (r *AsyncTaskRepository) SetPending(ctx context.Context, current asynctask.Task, fact asynctask.ArticleFact, now time.Time) (asynctask.Task, error) {
	return r.change(ctx, current, fact, "pending", now)
}
func (r *AsyncTaskRepository) Cancel(ctx context.Context, current asynctask.Task, fact asynctask.ArticleFact, now time.Time) (asynctask.Task, error) {
	return r.change(ctx, current, fact, "canceled", now)
}

func (r *AsyncTaskRepository) Observe(ctx context.Context, current asynctask.Task, fact asynctask.ArticleFact, now time.Time) (asynctask.Task, error) {
	tx, err := requireTx(ctx)
	if err != nil {
		return asynctask.Task{}, err
	}
	var task asynctask.Task
	err = tx.QueryRow(ctx, `UPDATE velis.async_tasks SET observed_aggregate_version=GREATEST(observed_aggregate_version,$3),updated_at=$4
WHERE id=$1 AND generation=$2 RETURNING id::text,article_id,status,generation,revision_id,revision_no,content_hash,observed_aggregate_version`, current.ID, current.Generation, fact.LockVersion, now).Scan(&task.ID, &task.ArticleID, &task.Status, &task.Generation, &task.RevisionID, &task.RevisionNo, &task.ContentHash, &task.ObservedVersion)
	if err == pgx.ErrNoRows {
		return asynctask.Task{}, fmt.Errorf("任务 generation 已变化")
	}
	return task, err
}

func (r *AsyncTaskRepository) change(ctx context.Context, current asynctask.Task, fact asynctask.ArticleFact, status string, now time.Time) (asynctask.Task, error) {
	tx, err := requireTx(ctx)
	if err != nil {
		return asynctask.Task{}, err
	}
	var task asynctask.Task
	err = tx.QueryRow(ctx, `UPDATE velis.async_tasks SET revision_id=$3,revision_no=$4,content_hash=$5,status=$6::varchar,generation=generation+1,observed_aggregate_version=$7,updated_at=$8::timestamptz,canceled_at=CASE WHEN $6::varchar='canceled' THEN $8::timestamptz ELSE NULL END,
stage='generation',generation_profile_version=NULL,embedding_profile_version=NULL,generation_attempt=0,embedding_attempt=0,generation_repair_used_at=NULL,next_attempt_at=$8::timestamptz,last_error_code=NULL,last_error_message=NULL,lease_owner=NULL,lease_token=NULL,lease_expires_at=NULL,generation_completed_at=NULL,embedding_completed_at=NULL,completed_at=NULL
WHERE id=$1 AND generation=$2 RETURNING id::text,article_id,status,generation,revision_id,revision_no,content_hash,observed_aggregate_version`, current.ID, current.Generation, fact.RevisionID, fact.RevisionNo, fact.ContentHash, status, fact.LockVersion, now).Scan(&task.ID, &task.ArticleID, &task.Status, &task.Generation, &task.RevisionID, &task.RevisionNo, &task.ContentHash, &task.ObservedVersion)
	if err == pgx.ErrNoRows {
		return asynctask.Task{}, fmt.Errorf("任务 generation 已变化")
	}
	return task, err
}

func (r *AsyncTaskRepository) UpdateIfGeneration(ctx context.Context, id string, expected int64, status string, now time.Time) (bool, error) {
	if status != "pending" && status != "canceled" {
		return false, fmt.Errorf("任务状态无效")
	}
	tx, err := requireTx(ctx)
	if err != nil {
		return false, err
	}
	tag, err := tx.Exec(ctx, `UPDATE velis.async_tasks SET status=$3::varchar,updated_at=$4::timestamptz,canceled_at=CASE WHEN $3::varchar='canceled' THEN $4::timestamptz ELSE NULL END WHERE id=$1 AND generation=$2`, id, expected, status, now)
	return tag.RowsAffected() == 1, err
}
