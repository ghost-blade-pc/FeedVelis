package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	feedbackApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlefeedback"
	feedback "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/articlefeedback"
)

type ArticleFeedbackRepository struct{ pool *pgxpool.Pool }

func NewArticleFeedbackRepository(pool *pgxpool.Pool) *ArticleFeedbackRepository {
	return &ArticleFeedbackRepository{pool: pool}
}

// withPublishedArticle 在一个事务里锁住公开文章。下架更新会等待事务提交，
// 因而不会出现写入检查通过、下架先提交、反馈后提交的竞态。
func (r *ArticleFeedbackRepository) withPublishedArticle(ctx context.Context, articleID int64, fn func(pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var found int64
	err = tx.QueryRow(ctx, `SELECT id FROM velis.articles WHERE id=$1 AND status='published' FOR SHARE`, articleID).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return feedback.ErrArticleNotFound
	}
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *ArticleFeedbackRepository) RecordRead(ctx context.Context, userID string, articleID int64, now time.Time) error {
	return r.withPublishedArticle(ctx, articleID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO velis.article_read_windows(user_id,article_id,window_start,first_seen_at)
VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, userID, articleID, feedback.ReadWindow(now), now)
		return err
	})
}

func (r *ArticleFeedbackRepository) SetFavorite(ctx context.Context, userID string, articleID int64, enabled bool, now time.Time) error {
	return r.withPublishedArticle(ctx, articleID, func(tx pgx.Tx) error {
		if enabled {
			_, err := tx.Exec(ctx, `INSERT INTO velis.article_favorites(user_id,article_id,created_at)
VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, userID, articleID, now)
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM velis.article_favorites WHERE user_id=$1 AND article_id=$2`, userID, articleID)
		return err
	})
}

func (r *ArticleFeedbackRepository) SetNotInterested(ctx context.Context, userID string, articleID int64, enabled bool, now time.Time) error {
	return r.withPublishedArticle(ctx, articleID, func(tx pgx.Tx) error {
		if enabled {
			_, err := tx.Exec(ctx, `INSERT INTO velis.article_not_interested(user_id,article_id,created_at,expires_at)
VALUES($1,$2,$3,$4)
ON CONFLICT (user_id,article_id) DO UPDATE SET created_at=EXCLUDED.created_at,expires_at=EXCLUDED.expires_at
WHERE article_not_interested.expires_at <= $3`, userID, articleID, now, now.Add(feedback.NotInterestedRetention))
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM velis.article_not_interested WHERE user_id=$1 AND article_id=$2`, userID, articleID)
		return err
	})
}

func (r *ArticleFeedbackRepository) States(ctx context.Context, userID string, ids []int64, now time.Time) ([]feedback.State, error) {
	rows, err := r.pool.Query(ctx, `SELECT a.id, f.article_id IS NOT NULL, n.expires_at
FROM unnest($2::bigint[]) WITH ORDINALITY AS requested(id,position)
JOIN velis.articles a ON a.id=requested.id AND a.status='published'
LEFT JOIN velis.article_favorites f ON f.user_id=$1 AND f.article_id=a.id
LEFT JOIN velis.article_not_interested n ON n.user_id=$1 AND n.article_id=a.id AND n.expires_at>$3
ORDER BY requested.position`, userID, ids, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	states := make([]feedback.State, 0, len(ids))
	for rows.Next() {
		var state feedback.State
		if err := rows.Scan(&state.ArticleID, &state.Favorited, &state.NotInterestedExpiresAt); err != nil {
			return nil, err
		}
		state.NotInterested = state.NotInterestedExpiresAt != nil
		states = append(states, state)
	}
	return states, rows.Err()
}

// CleanupExpired 每次最多清理两个表各 limit 行。读取路径仍独立过滤到期事实。
func (r *ArticleFeedbackRepository) CleanupExpired(ctx context.Context, now time.Time, limit int) (int64, error) {
	if limit < 1 || limit > 1000 {
		return 0, feedback.ErrInvalidInput
	}
	readTag, err := r.pool.Exec(ctx, `DELETE FROM velis.article_read_windows WHERE ctid IN (
SELECT ctid FROM velis.article_read_windows WHERE first_seen_at < $1 ORDER BY first_seen_at LIMIT $2)`, now.Add(-feedback.ReadRetention), limit)
	if err != nil {
		return 0, err
	}
	negativeTag, err := r.pool.Exec(ctx, `DELETE FROM velis.article_not_interested WHERE ctid IN (
SELECT ctid FROM velis.article_not_interested WHERE expires_at <= $1 ORDER BY expires_at LIMIT $2)`, now, limit)
	if err != nil {
		return readTag.RowsAffected(), err
	}
	return readTag.RowsAffected() + negativeTag.RowsAffected(), nil
}

// Excluded 独立检查所有候选，不受画像样本 500 篇上限影响。
func (r *ArticleFeedbackRepository) Excluded(ctx context.Context, userID string, candidates []int64, now time.Time) ([]int64, error) {
	rows, err := r.pool.Query(ctx, `SELECT a.id FROM velis.article_not_interested n
JOIN velis.articles a ON a.id=n.article_id AND a.status='published'
WHERE n.user_id=$1 AND n.expires_at>$3 AND a.id=ANY($2::bigint[])`, userID, candidates, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *ArticleFeedbackRepository) Samples(ctx context.Context, userID string, now time.Time) ([]feedbackApp.Sample, error) {
	rows, err := r.pool.Query(ctx, `WITH reads AS (
 SELECT article_id, count(DISTINCT (first_seen_at AT TIME ZONE 'UTC')::date)::int AS days,
 max(first_seen_at) AS latest
 FROM velis.article_read_windows WHERE user_id=$1 AND first_seen_at >= $2
 GROUP BY article_id
), selected AS (
 SELECT a.id,a.source_id,COALESCE(r.days,0) AS days,f.article_id IS NOT NULL AS favorited,
 n.article_id IS NOT NULL AS negative,a.current_revision_id,
 GREATEST(COALESCE(r.latest,'-infinity'::timestamptz),COALESCE(f.created_at,'-infinity'::timestamptz),
 COALESCE(n.created_at,'-infinity'::timestamptz)) AS latest
 FROM velis.articles a
 LEFT JOIN reads r ON r.article_id=a.id
 LEFT JOIN velis.article_favorites f ON f.article_id=a.id AND f.user_id=$1
 LEFT JOIN velis.article_not_interested n ON n.article_id=a.id AND n.user_id=$1 AND n.expires_at>$3
 WHERE a.status='published' AND (r.article_id IS NOT NULL OR f.article_id IS NOT NULL OR n.article_id IS NOT NULL)
 ORDER BY latest DESC,a.id DESC LIMIT 500
)
SELECT s.id,s.days,s.favorited,s.negative,s.source_id,COALESCE(g.keywords,ARRAY[]::text[]),COALESCE(g.topics,ARRAY[]::text[])
FROM selected s
LEFT JOIN velis.ai_current_selections cs ON cs.article_id=s.id AND cs.revision_id=s.current_revision_id
LEFT JOIN velis.ai_generation_results g ON g.id=cs.generation_result_id
ORDER BY s.latest DESC,s.id DESC`, userID, now.Add(-feedback.ReadRetention), now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	samples := make([]feedbackApp.Sample, 0, 500)
	for rows.Next() {
		var item feedbackApp.Sample
		if err := rows.Scan(&item.ArticleID, &item.ReadDays, &item.Favorited, &item.NotInterested, &item.SourceID, &item.Keywords, &item.Topics); err != nil {
			return nil, err
		}
		samples = append(samples, item)
	}
	return samples, rows.Err()
}
