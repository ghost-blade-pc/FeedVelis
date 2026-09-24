package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/enrichment"
)

type EnrichmentRepository struct{ pool *pgxpool.Pool }

func NewEnrichmentRepository(pool *pgxpool.Pool) *EnrichmentRepository {
	return &EnrichmentRepository{pool: pool}
}

func (r *EnrichmentRepository) Claim(ctx context.Context, request enrichment.ClaimRequest) (*enrichment.ClaimedTask, error) {
	if request.Generation == nil && request.Embedding == nil {
		return nil, nil
	}
	genVersion, embedVersion := "", ""
	if request.Generation != nil {
		genVersion = request.Generation.ProfileVersion
	}
	if request.Embedding != nil {
		embedVersion = request.Embedding.ProfileVersion
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	token := uuid.NewString()
	row := tx.QueryRow(ctx, `WITH candidate AS (
    SELECT t.id FROM velis.async_tasks t
    JOIN velis.articles a ON a.id=t.article_id
    WHERE a.status='published' AND a.current_revision_id=t.revision_id
      AND (((t.status IN ('pending','retry_wait') AND t.next_attempt_at <= $1)
            OR (t.status='running' AND t.lease_expires_at <= $1)))
      AND ((t.stage='generation' AND $5 <> '' AND (t.generation_profile_version IS NULL OR t.generation_profile_version=$5))
        OR (t.stage='embedding' AND $6 <> '' AND (t.embedding_profile_version IS NULL OR t.embedding_profile_version=$6)))
    ORDER BY t.next_attempt_at,t.updated_at,t.id
    FOR UPDATE OF t SKIP LOCKED LIMIT 1
)
UPDATE velis.async_tasks t SET status='running',lease_owner=$2,lease_token=$3,lease_expires_at=$1+$4::interval,
 generation_profile_version=CASE WHEN t.stage='generation' THEN COALESCE(t.generation_profile_version,NULLIF($5,'')) ELSE t.generation_profile_version END,
 embedding_profile_version=COALESCE(t.embedding_profile_version,NULLIF($6,'')),
 generation_attempt=t.generation_attempt+CASE WHEN t.stage='generation' THEN 1 ELSE 0 END,
 embedding_attempt=t.embedding_attempt+CASE WHEN t.stage='embedding' THEN 1 ELSE 0 END,updated_at=$1,last_error_code=NULL,last_error_message=NULL
FROM candidate c,velis.article_versions v
WHERE t.id=c.id AND v.article_id=t.article_id AND v.id=t.revision_id
RETURNING t.id::text,t.generation,t.stage,CASE WHEN t.stage='generation' THEN t.generation_attempt ELSE t.embedding_attempt END,
t.article_id,t.revision_id,v.title,v.language,v.plain_text,t.lease_expires_at,t.generation_repair_used_at IS NOT NULL`, request.Now, request.Owner, token, request.Lease.String(), genVersion, embedVersion)
	var task enrichment.ClaimedTask
	err = row.Scan(&task.ID, &task.Generation, &task.Stage, &task.Attempt, &task.ArticleID, &task.RevisionID, &task.Revision.Title, &task.Revision.Language, &task.Revision.PlainText, &task.LeaseExpiresAt, &task.GenerationRepairUsed)
	if err == pgx.ErrNoRows {
		return nil, tx.Commit(ctx)
	}
	if err != nil {
		return nil, err
	}
	task.LeaseToken = token
	task.Revision.ArticleID, task.Revision.RevisionID = task.ArticleID, task.RevisionID
	if task.Stage == "generation" {
		task.GenerationProfile = cloneProfile(request.Generation)
	}
	task.EmbeddingProfile = cloneProfile(request.Embedding)
	if task.Stage == "embedding" {
		task.GenerationProfile = cloneProfile(request.Generation)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &task, nil
}

func cloneProfile(profile *enrichment.ActiveProfile) *enrichment.ActiveProfile {
	if profile == nil {
		return nil
	}
	copy := *profile
	return &copy
}

func (r *EnrichmentRepository) CurrentGeneration(ctx context.Context, task enrichment.ClaimedTask) (*enrichment.GenerationResult, error) {
	var result enrichment.GenerationResult
	var keywords, topics []string
	err := r.pool.QueryRow(ctx, `SELECT g.id::text,g.article_id,g.revision_id,g.provider,g.model,g.profile_version,g.workflow_version,g.prompt_version,
g.generation_input_hash,g.input_truncated,g.summary,g.keywords,g.topics,g.generated_at
FROM velis.ai_current_selections s JOIN velis.ai_generation_results g ON g.id=s.generation_result_id
JOIN velis.articles a ON a.id=s.article_id AND a.current_revision_id=s.revision_id
WHERE s.article_id=$1 AND s.revision_id=$2 AND a.status='published'`, task.ArticleID, task.RevisionID).Scan(
		&result.ID, &result.ArticleID, &result.RevisionID, &result.Profile.Provider, &result.Profile.Model, &result.Profile.ProfileVersion,
		&result.Profile.WorkflowVersion, &result.Profile.PromptVersion, &result.InputHash, &result.InputTruncated, &result.Content.Summary, &keywords, &topics, &result.GeneratedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	result.Content.Keywords, result.Content.Topics = keywords, topics
	return &result, err
}

func (r *EnrichmentRepository) SaveGeneration(ctx context.Context, task enrichment.ClaimedTask, result enrichment.GenerationResult, needsEmbedding bool, now time.Time) (bool, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if ok, err := fenceTask(ctx, tx, task, "generation", result.Profile.ProfileVersion, now); err != nil || !ok {
		return false, err
	}
	resultID, err := insertGeneration(ctx, tx, result)
	if err != nil {
		return false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO velis.ai_current_selections(article_id,revision_id,generation_result_id,generation_profile_version,updated_at)
VALUES($1,$2,$3,$4,$5) ON CONFLICT(article_id) DO UPDATE SET revision_id=EXCLUDED.revision_id,generation_result_id=EXCLUDED.generation_result_id,
generation_profile_version=EXCLUDED.generation_profile_version,embedding_result_id=CASE WHEN velis.ai_current_selections.revision_id=EXCLUDED.revision_id THEN velis.ai_current_selections.embedding_result_id ELSE NULL END,
embedding_profile_version=CASE WHEN velis.ai_current_selections.revision_id=EXCLUDED.revision_id THEN velis.ai_current_selections.embedding_profile_version ELSE NULL END,updated_at=EXCLUDED.updated_at`, task.ArticleID, task.RevisionID, resultID, result.Profile.ProfileVersion, now)
	if err != nil {
		return false, err
	}
	if needsEmbedding {
		_, err = tx.Exec(ctx, `UPDATE velis.async_tasks SET stage='embedding',status='pending',generation_completed_at=$4,next_attempt_at=$4,
lease_owner=NULL,lease_token=NULL,lease_expires_at=NULL,updated_at=$4 WHERE id=$1 AND generation=$2 AND lease_token=$3`, task.ID, task.Generation, task.LeaseToken, now)
	} else {
		_, err = tx.Exec(ctx, `UPDATE velis.async_tasks SET status='succeeded',generation_completed_at=$4,completed_at=$4,
lease_owner=NULL,lease_token=NULL,lease_expires_at=NULL,updated_at=$4 WHERE id=$1 AND generation=$2 AND lease_token=$3`, task.ID, task.Generation, task.LeaseToken, now)
	}
	if err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func insertGeneration(ctx context.Context, tx pgx.Tx, result enrichment.GenerationResult) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `INSERT INTO velis.ai_generation_results(id,article_id,revision_id,provider,model,profile_version,workflow_version,prompt_version,generation_input_hash,input_truncated,summary,keywords,topics,generated_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
ON CONFLICT(article_id,revision_id,provider,model,profile_version,workflow_version,prompt_version,generation_input_hash) DO NOTHING RETURNING id::text`,
		result.ID, result.ArticleID, result.RevisionID, result.Profile.Provider, result.Profile.Model, result.Profile.ProfileVersion, result.Profile.WorkflowVersion, result.Profile.PromptVersion, result.InputHash, result.InputTruncated, result.Content.Summary, result.Content.Keywords, result.Content.Topics, result.GeneratedAt).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != pgx.ErrNoRows {
		return "", err
	}
	err = tx.QueryRow(ctx, `SELECT id::text FROM velis.ai_generation_results WHERE article_id=$1 AND revision_id=$2 AND provider=$3 AND model=$4 AND profile_version=$5 AND workflow_version=$6 AND prompt_version=$7 AND generation_input_hash=$8`,
		result.ArticleID, result.RevisionID, result.Profile.Provider, result.Profile.Model, result.Profile.ProfileVersion, result.Profile.WorkflowVersion, result.Profile.PromptVersion, result.InputHash).Scan(&id)
	return id, err
}

func (r *EnrichmentRepository) SaveEmbedding(ctx context.Context, task enrichment.ClaimedTask, result enrichment.EmbeddingResult, now time.Time) (bool, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if ok, err := fenceTask(ctx, tx, task, "embedding", result.Profile.ProfileVersion, now); err != nil || !ok {
		return false, err
	}
	var resultID string
	err = tx.QueryRow(ctx, `INSERT INTO velis.ai_embedding_results(id,article_id,revision_id,generation_result_id,provider,model,profile_version,embedding_input_version,embedding_input_hash,dimensions,vector,generated_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::real[],$12)
ON CONFLICT(article_id,revision_id,generation_result_id,provider,model,profile_version,embedding_input_version,embedding_input_hash,dimensions) DO NOTHING RETURNING id::text`,
		result.ID, result.ArticleID, result.RevisionID, result.GenerationResultID, result.Profile.Provider, result.Profile.Model, result.Profile.ProfileVersion, result.Profile.InputVersion, result.InputHash, result.Profile.Dimensions, result.Vector, result.GeneratedAt).Scan(&resultID)
	if err == pgx.ErrNoRows {
		err = tx.QueryRow(ctx, `SELECT id::text FROM velis.ai_embedding_results WHERE article_id=$1 AND revision_id=$2 AND generation_result_id=$3 AND provider=$4 AND model=$5 AND profile_version=$6 AND embedding_input_version=$7 AND embedding_input_hash=$8 AND dimensions=$9`, result.ArticleID, result.RevisionID, result.GenerationResultID, result.Profile.Provider, result.Profile.Model, result.Profile.ProfileVersion, result.Profile.InputVersion, result.InputHash, result.Profile.Dimensions).Scan(&resultID)
	}
	if err != nil {
		return false, err
	}
	tag, err := tx.Exec(ctx, `UPDATE velis.ai_current_selections SET embedding_result_id=$3,embedding_profile_version=$4,updated_at=$5 WHERE article_id=$1 AND revision_id=$2`, task.ArticleID, task.RevisionID, resultID, result.Profile.ProfileVersion, now)
	if err != nil || tag.RowsAffected() != 1 {
		return false, err
	}
	_, err = tx.Exec(ctx, `UPDATE velis.async_tasks SET status='succeeded',embedding_completed_at=$4,completed_at=$4,lease_owner=NULL,lease_token=NULL,lease_expires_at=NULL,updated_at=$4 WHERE id=$1 AND generation=$2 AND lease_token=$3`, task.ID, task.Generation, task.LeaseToken, now)
	if err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func fenceTask(ctx context.Context, tx pgx.Tx, task enrichment.ClaimedTask, stage, profile string, now time.Time) (bool, error) {
	var exists bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM velis.async_tasks t JOIN velis.articles a ON a.id=t.article_id
WHERE t.id=$1 AND t.generation=$2 AND t.lease_token=$3 AND t.status='running' AND t.stage=$4 AND t.lease_expires_at>$6
AND a.status='published' AND a.current_revision_id=t.revision_id AND t.revision_id=$5
AND CASE WHEN $4='generation' THEN t.generation_profile_version ELSE t.embedding_profile_version END=$7 FOR UPDATE OF t)`, task.ID, task.Generation, task.LeaseToken, stage, task.RevisionID, now, profile).Scan(&exists)
	return exists, err
}

func (r *EnrichmentRepository) Fail(ctx context.Context, update enrichment.FailureUpdate) (bool, error) {
	status := "failed"
	if update.Retry {
		status = "retry_wait"
	}
	profile := ""
	if update.Task.Stage == "generation" && update.Task.GenerationProfile != nil {
		profile = update.Task.GenerationProfile.ProfileVersion
	}
	if update.Task.Stage == "embedding" && update.Task.EmbeddingProfile != nil {
		profile = update.Task.EmbeddingProfile.ProfileVersion
	}
	tag, err := r.pool.Exec(ctx, `UPDATE velis.async_tasks t SET status=$4,last_error_code=$5,last_error_message=$6,next_attempt_at=$7,
lease_owner=NULL,lease_token=NULL,lease_expires_at=NULL,updated_at=$8 FROM velis.articles a
WHERE t.id=$1 AND t.generation=$2 AND t.lease_token=$3 AND t.status='running' AND t.lease_expires_at>$8
AND t.stage=$9 AND t.revision_id=$10 AND a.id=t.article_id AND a.status='published' AND a.current_revision_id=t.revision_id
AND CASE WHEN t.stage='generation' THEN t.generation_profile_version ELSE t.embedding_profile_version END=$11`,
		update.Task.ID, update.Task.Generation, update.Task.LeaseToken, status, string(update.Code), truncateDBError(update.Message), update.NextAttemptAt, update.Now,
		update.Task.Stage, update.Task.RevisionID, profile)
	return tag.RowsAffected() == 1, err
}

func (r *EnrichmentRepository) ReserveGenerationRepair(ctx context.Context, task enrichment.ClaimedTask, now time.Time) (bool, error) {
	if task.GenerationProfile == nil {
		return false, nil
	}
	tag, err := r.pool.Exec(ctx, `UPDATE velis.async_tasks t SET generation_repair_used_at=$4,updated_at=$4
FROM velis.articles a
WHERE t.id=$1 AND t.generation=$2 AND t.lease_token=$3 AND t.status='running' AND t.stage='generation'
AND t.lease_expires_at>$4 AND t.generation_repair_used_at IS NULL AND t.revision_id=$5
AND t.generation_profile_version=$6 AND a.id=t.article_id AND a.status='published' AND a.current_revision_id=t.revision_id`,
		task.ID, task.Generation, task.LeaseToken, now, task.RevisionID, task.GenerationProfile.ProfileVersion)
	return tag.RowsAffected() == 1, err
}

func (r *EnrichmentRepository) RecordCalls(ctx context.Context, task enrichment.ClaimedTask, profile enrichment.ActiveProfile, calls []enrichment.CallRecord, now time.Time) error {
	for _, call := range calls {
		if call.Kind == "" {
			continue
		}
		var inputTokens, outputTokens, totalTokens any
		if call.Usage.InputTokens != nil {
			inputTokens = *call.Usage.InputTokens
		}
		if call.Usage.OutputTokens != nil {
			outputTokens = *call.Usage.OutputTokens
		}
		if call.Usage.TotalTokens != nil {
			totalTokens = *call.Usage.TotalTokens
		}
		structuredOutput := any(nil)
		if task.Stage == "generation" {
			structuredOutput = profile.StructuredOutput
		}
		_, err := r.pool.Exec(ctx, `INSERT INTO velis.ai_model_calls(id,task_id,task_generation,stage,call_kind,attempt,provider,model,profile_version,workflow_version,prompt_version,embedding_input_version,input_hash,input_tokens,output_tokens,total_tokens,duration_ms,status,error_code,error_message,structured_output_mode,error_reason,created_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)`, uuid.NewString(), task.ID, task.Generation, task.Stage, call.Kind, task.Attempt, profile.Provider, profile.Model, profile.ProfileVersion, nullString(profile.WorkflowVersion), nullString(profile.PromptVersion), nullString(profile.InputVersion), call.InputHash, inputTokens, outputTokens, totalTokens, call.Duration.Milliseconds(), call.Status, nullErrorCode(call.ErrorCode), nullString(truncateDBError(call.ErrorBrief)), structuredOutput, nullString(call.ErrorReason), now)
		if err != nil {
			return err
		}
	}
	return nil
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func nullErrorCode(value enrichment.ErrorCode) any {
	if value == "" {
		return nil
	}
	return string(value)
}
func truncateDBError(value string) string {
	if len(value) > 512 {
		return value[:512]
	}
	return value
}

var _ enrichment.ExecutionStore = (*EnrichmentRepository)(nil)

func (r *EnrichmentRepository) String() string {
	return fmt.Sprintf("EnrichmentRepository(%p)", r.pool)
}
