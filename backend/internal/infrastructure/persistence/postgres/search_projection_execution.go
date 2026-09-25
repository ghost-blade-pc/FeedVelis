package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	projectionApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/searchprojection"
	projectionDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/searchprojection"
)

var _ projectionApp.ExecutionStore = (*SearchProjectionRepository)(nil)

// Claim 以有界批次认领到期槽位：认领事务只负责发放租约，外部请求期间不持有任何行锁。
// 已过期租约的槽位会被重新认领，因此进程退出后同一 generation 可以被安全重试。
func (r *SearchProjectionRepository) Claim(ctx context.Context, request projectionApp.ClaimRequest) ([]projectionApp.ClaimedJob, error) {
	if request.BatchSize < 1 {
		return nil, nil
	}
	now := request.Now.UTC()
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	token := uuid.NewString()
	rows, err := tx.Query(ctx, `WITH candidate AS (
    SELECT j.article_id FROM velis.search_projection_jobs j
    WHERE ((j.status IN ('pending','retry_wait') AND j.next_attempt_at <= $1)
        OR (j.status = 'running' AND j.lease_expires_at <= $1))
      AND EXISTS (SELECT 1 FROM velis.search_projection_deliveries d
                  WHERE d.article_id = j.article_id
                    AND (d.required_generation <> j.generation OR d.status <> 'succeeded'))
    ORDER BY j.next_attempt_at, j.article_id
    FOR UPDATE OF j SKIP LOCKED LIMIT $2
)
UPDATE velis.search_projection_jobs j SET status='running',lease_owner=$3,lease_token=$4,
lease_expires_at=$1+$5::interval,attempt=j.attempt+1,next_attempt_at=$1,updated_at=$1,
last_error_code=NULL,last_error_message=NULL
FROM candidate c WHERE j.article_id=c.article_id
RETURNING j.article_id,j.action::text,j.generation,j.article_lock_version,j.revision_id,
j.generation_result_id::text,j.embedding_result_id::text,j.attempt`, now, request.BatchSize, request.Owner, token, request.Lease.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := make([]projectionApp.ClaimedJob, 0, request.BatchSize)
	for rows.Next() {
		var job projectionApp.ClaimedJob
		var action string
		var generationResultID, embeddingResultID *string
		var lockVersion, revisionID int64
		if err := rows.Scan(&job.ArticleID, &action, &job.Generation, &lockVersion, &revisionID,
			&generationResultID, &embeddingResultID, &job.Attempt); err != nil {
			return nil, err
		}
		target, err := projectionDomain.NewTarget(projectionDomain.Action(action), lockVersion, revisionID, generationResultID, embeddingResultID)
		if err != nil {
			return nil, err
		}
		job.Target = target
		job.Lease = projectionDomain.Lease{Owner: request.Owner, Token: token, ExpiresAt: now.Add(request.Lease)}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(jobs) == 0 {
		return nil, tx.Commit(ctx)
	}
	// 同一批次共享一个租约 token：完成写回必须同时匹配它、generation 与物理索引。
	if _, err := tx.Exec(ctx, `UPDATE velis.search_projection_deliveries d
SET status='running',attempt=d.attempt+1,updated_at=$2
FROM velis.search_projection_jobs j
WHERE j.article_id=d.article_id AND j.lease_token=$1
  AND d.required_generation=j.generation AND d.status IN ('pending','retry_wait','running')`, token, now); err != nil {
		return nil, err
	}
	for index := range jobs {
		// 只带出仍需投递的 delivery：已收敛的索引不参与本次重试。
		deliveries, err := loadUnfinishedDeliveries(ctx, tx, jobs[index].ArticleID, jobs[index].Generation)
		if err != nil {
			return nil, err
		}
		jobs[index].Deliveries = deliveries
	}
	return jobs, tx.Commit(ctx)
}

// Complete 按 delivery 写回执行结果，并在整批 delivery 都不再执行时派生槽位总状态。
// 租约被替代、generation 已增加或 delivery 仍停在上一个 generation 时返回 false。
func (r *SearchProjectionRepository) Complete(ctx context.Context, completion projectionApp.Completion) (bool, error) {
	now := completion.Now.UTC()
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// 加锁顺序固定为 job → delivery，与文章写路径的 article → job → delivery 一致。
	// 用一条 FOR UPDATE OF d,j 会让加锁顺序取决于执行计划，与重建路径形成 ABBA 死锁。
	var lockedGeneration int64
	err = tx.QueryRow(ctx, `SELECT generation FROM velis.search_projection_jobs
WHERE article_id=$1 AND generation=$2 AND lease_token=$3 AND status='running' AND lease_expires_at>$4
FOR UPDATE`, completion.ArticleID, completion.Generation, completion.LeaseToken, now).Scan(&lockedGeneration)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, tx.Commit(ctx)
	}
	if err != nil {
		return false, err
	}
	var attempt int
	var requiredGeneration int64
	err = tx.QueryRow(ctx, `SELECT attempt,required_generation FROM velis.search_projection_deliveries
WHERE article_id=$1 AND physical_index=$2 FOR UPDATE`, completion.ArticleID, completion.PhysicalIndex).
		Scan(&attempt, &requiredGeneration)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, tx.Commit(ctx)
	}
	if err != nil {
		return false, err
	}
	if requiredGeneration != completion.Generation {
		return false, tx.Commit(ctx)
	}
	outcome := completion.Outcome.Normalize()
	failure := outcome.Failure()
	if _, err := tx.Exec(ctx, `UPDATE velis.search_projection_deliveries SET status=$3,next_attempt_at=$4,
last_result=$5,last_error_code=$6,last_error_message=$7,updated_at=$8
WHERE article_id=$1 AND physical_index=$2`, completion.ArticleID, completion.PhysicalIndex,
		string(outcome.StatusFor(attempt, completion.MaxAttempts)), completion.NextAttemptAt.UTC(),
		string(outcome.Result), failureColumn(failure, true), failureColumn(failure, false), now); err != nil {
		return false, err
	}
	if err := settleProjectionJob(ctx, tx, completion.ArticleID, now); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// settleProjectionJob 在整批 delivery 都不再执行时汇总槽位状态。
// 同批仍有 running 的 delivery 时保持 running 与租约：进程退出后由租约到期触发安全重试。
func settleProjectionJob(ctx context.Context, tx pgx.Tx, articleID int64, now time.Time) error {
	job, err := lockProjectionJob(ctx, tx, articleID)
	if err != nil || job == nil {
		return err
	}
	deliveries, err := loadDeliveries(ctx, tx, articleID)
	if err != nil {
		return err
	}
	for _, delivery := range deliveries {
		if delivery.Status == projectionDomain.StatusRunning {
			return nil
		}
	}
	status := projectionDomain.ConvergedStatus(job.Generation, deliveries)
	nextAttempt := now
	var failure *projectionDomain.Failure
	for _, delivery := range deliveries {
		switch delivery.Status {
		case projectionDomain.StatusPending, projectionDomain.StatusRetryWait:
			if delivery.NextAttemptAt.Before(nextAttempt) {
				nextAttempt = delivery.NextAttemptAt
			}
		case projectionDomain.StatusFailed:
			if failure == nil {
				failure = delivery.LastError
			}
		}
	}
	var completedAt *time.Time
	if status == projectionDomain.StatusSucceeded {
		completedAt = &now
	}
	if status != projectionDomain.StatusFailed {
		failure = nil
	}
	_, err = tx.Exec(ctx, `UPDATE velis.search_projection_jobs SET status=$2,next_attempt_at=$3,completed_at=$4,
last_error_code=$5,last_error_message=$6,lease_owner=NULL,lease_token=NULL,lease_expires_at=NULL,updated_at=$7
WHERE article_id=$1`, articleID, string(status), nextAttempt.UTC(), completedAt,
		failureColumn(failure, true), failureColumn(failure, false), now)
	return err
}

// loadUnfinishedDeliveries 只返回仍需投递的 delivery，使重试不会重发已成功的索引。
func loadUnfinishedDeliveries(ctx context.Context, tx pgx.Tx, articleID, generation int64) ([]projectionDomain.Delivery, error) {
	return queryDeliveries(ctx, tx, `SELECT article_id,physical_index,required_generation,status::text,attempt,
next_attempt_at,last_result,last_error_code,last_error_message
FROM velis.search_projection_deliveries
WHERE article_id=$1 AND (required_generation<>$2 OR status<>'succeeded') ORDER BY physical_index`, articleID, generation)
}

func loadDeliveries(ctx context.Context, tx pgx.Tx, articleID int64) ([]projectionDomain.Delivery, error) {
	return queryDeliveries(ctx, tx, `SELECT article_id,physical_index,required_generation,status::text,attempt,
next_attempt_at,last_result,last_error_code,last_error_message
FROM velis.search_projection_deliveries WHERE article_id=$1 ORDER BY physical_index`, articleID)
}

func queryDeliveries(ctx context.Context, tx pgx.Tx, query string, args ...any) ([]projectionDomain.Delivery, error) {
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	deliveries := make([]projectionDomain.Delivery, 0)
	for rows.Next() {
		var delivery projectionDomain.Delivery
		var status string
		var result, code, message *string
		if err := rows.Scan(&delivery.ArticleID, &delivery.PhysicalIndex, &delivery.RequiredGeneration,
			&status, &delivery.Attempt, &delivery.NextAttemptAt, &result, &code, &message); err != nil {
			return nil, err
		}
		delivery.Status = projectionDomain.Status(status)
		if result != nil {
			delivery.LastResult = projectionDomain.Result(*result)
		}
		delivery.NextAttemptAt = delivery.NextAttemptAt.UTC()
		if code != nil {
			delivery.LastError = &projectionDomain.Failure{Code: *code}
			if message != nil {
				delivery.LastError.Message = *message
			}
		}
		deliveries = append(deliveries, delivery)
	}
	return deliveries, rows.Err()
}

// failureColumn 把诊断拆成 code 与 message 两列；nil 诊断两列都为空。
func failureColumn(failure *projectionDomain.Failure, code bool) any {
	if failure == nil {
		return nil
	}
	if code {
		return failure.Code
	}
	if failure.Message == "" {
		return nil
	}
	return failure.Message
}
