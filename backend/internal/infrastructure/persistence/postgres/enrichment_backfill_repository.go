package postgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/enrichment"
)

type EnrichmentBackfillRepository struct{ pool *pgxpool.Pool }

func NewEnrichmentBackfillRepository(pool *pgxpool.Pool) *EnrichmentBackfillRepository {
	return &EnrichmentBackfillRepository{pool: pool}
}

func (r *EnrichmentBackfillRepository) Candidates(ctx context.Context, request enrichment.BackfillRequest) ([]int64, bool, error) {
	rows, err := r.pool.Query(ctx, `SELECT a.id FROM velis.articles a
LEFT JOIN velis.ai_current_selections s ON s.article_id=a.id AND s.revision_id=a.current_revision_id
WHERE a.status='published' AND (
($1 IN ('generation','all') AND (($2='missing-only' AND s.generation_result_id IS NULL) OR ($2='outdated-only' AND s.generation_result_id IS NOT NULL AND s.generation_profile_version<>$3)))
OR ($1 IN ('embedding','all') AND s.generation_result_id IS NOT NULL AND (($2='missing-only' AND s.embedding_result_id IS NULL) OR ($2='outdated-only' AND s.embedding_result_id IS NOT NULL AND (s.embedding_profile_version<>$4 OR NOT EXISTS(SELECT 1 FROM velis.ai_embedding_results e WHERE e.id=s.embedding_result_id AND e.generation_result_id=s.generation_result_id)))))
) ORDER BY a.id LIMIT $5`, request.Stage, request.Mode, request.GenerationProfile, request.EmbeddingProfile, request.Limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	ids := make([]int64, 0, request.Limit+1)
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
	more := len(ids) > request.Limit
	if more {
		ids = ids[:request.Limit]
	}
	return ids, more, nil
}

func (r *EnrichmentBackfillRepository) AdvanceCandidate(ctx context.Context, articleID int64, request enrichment.BackfillRequest) (bool, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var revisionID int64
	var generationID, embeddingID *string
	var generationProfile, embeddingProfile *string
	var embeddingMatchesGeneration bool
	err = tx.QueryRow(ctx, `SELECT a.current_revision_id,s.generation_result_id::text,s.embedding_result_id::text,s.generation_profile_version,s.embedding_profile_version,
COALESCE(EXISTS(SELECT 1 FROM velis.ai_embedding_results e WHERE e.id=s.embedding_result_id AND e.generation_result_id=s.generation_result_id),false)
FROM velis.articles a LEFT JOIN velis.ai_current_selections s ON s.article_id=a.id AND s.revision_id=a.current_revision_id WHERE a.id=$1 AND a.status='published' FOR UPDATE OF a`, articleID).Scan(&revisionID, &generationID, &embeddingID, &generationProfile, &embeddingProfile, &embeddingMatchesGeneration)
	if err == pgx.ErrNoRows {
		return false, tx.Commit(ctx)
	}
	if err != nil {
		return false, err
	}
	needGeneration := request.Stage == "generation" || request.Stage == "all"
	needEmbedding := request.Stage == "embedding" || request.Stage == "all"
	if request.Mode == "missing-only" {
		needGeneration = needGeneration && generationID == nil
		needEmbedding = needEmbedding && generationID != nil && embeddingID == nil
	} else {
		needGeneration = needGeneration && generationID != nil && (generationProfile == nil || *generationProfile != request.GenerationProfile)
		needEmbedding = needEmbedding && generationID != nil && embeddingID != nil && (embeddingProfile == nil || *embeddingProfile != request.EmbeddingProfile || !embeddingMatchesGeneration)
	}
	if !needGeneration && !needEmbedding {
		return false, tx.Commit(ctx)
	}
	stage := "embedding"
	if needGeneration {
		stage = "generation"
	}
	var taskID string
	var taskGeneration int64
	var currentStage, currentStatus string
	var currentGenProfile, currentEmbedProfile *string
	err = tx.QueryRow(ctx, `SELECT id::text,generation,stage,status,generation_profile_version,embedding_profile_version FROM velis.async_tasks WHERE article_id=$1 FOR UPDATE`, articleID).Scan(&taskID, &taskGeneration, &currentStage, &currentStatus, &currentGenProfile, &currentEmbedProfile)
	genTarget, embedTarget := any(nil), any(nil)
	if needGeneration {
		genTarget = request.GenerationProfile
	}
	if needEmbedding {
		embedTarget = request.EmbeddingProfile
	}
	if err == pgx.ErrNoRows {
		var revisionNo int
		var hash string
		if err = tx.QueryRow(ctx, `SELECT revision_no,content_hash FROM velis.article_versions WHERE article_id=$1 AND id=$2`, articleID, revisionID).Scan(&revisionNo, &hash); err != nil {
			return false, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO velis.async_tasks(id,task_type,aggregate_type,aggregate_id,article_id,revision_id,revision_no,content_hash,status,generation,observed_aggregate_version,created_at,updated_at,stage,generation_profile_version,embedding_profile_version,next_attempt_at)
VALUES($1,'article.enrichment','article',$2,$3,$4,$5,$6,'pending',1,(SELECT lock_version FROM velis.articles WHERE id=$3),clock_timestamp(),clock_timestamp(),$7,$8,$9,clock_timestamp())`, uuid.NewString(), articleID, articleID, revisionID, revisionNo, hash, stage, genTarget, embedTarget)
	} else if err == nil {
		if currentStatus == "pending" || currentStatus == "running" || currentStatus == "retry_wait" {
			if currentStage == stage && ((stage == "generation" && currentGenProfile != nil && *currentGenProfile == request.GenerationProfile) || (stage == "embedding" && currentEmbedProfile != nil && *currentEmbedProfile == request.EmbeddingProfile)) {
				return false, tx.Commit(ctx)
			}
		}
		_, err = tx.Exec(ctx, `UPDATE velis.async_tasks SET generation=generation+1,stage=$2,status='pending',generation_profile_version=COALESCE($3,generation_profile_version),embedding_profile_version=COALESCE($4,embedding_profile_version),generation_attempt=0,embedding_attempt=0,next_attempt_at=clock_timestamp(),last_error_code=NULL,last_error_message=NULL,lease_owner=NULL,lease_token=NULL,lease_expires_at=NULL,generation_completed_at=NULL,embedding_completed_at=NULL,completed_at=NULL,canceled_at=NULL,updated_at=clock_timestamp() WHERE id=$1`, taskID, stage, genTarget, embedTarget)
	}
	if err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

var _ enrichment.BackfillStore = (*EnrichmentBackfillRepository)(nil)
