package postgres

import (
	"context"
	"strconv"
	"strings"
	"time"

	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
)

// ListRecommendationLatest 在数据库中过滤冻结候选与本人当前有效负反馈，避免深页空转。
func (r *ArticleRepository) ListRecommendationLatest(ctx context.Context, cursor *articleDomain.Cursor, limit int, skip []int64, userID string, now, startedAt time.Time) ([]articleDomain.ListItem, error) {
	query := `SELECT a.id,a.origin_type,v.title,a.canonical_url,s.id,s.title,s.site_url,v.source_author_name,
v.excerpt,a.source_published_at,a.discovered_at,COALESCE(a.source_published_at,a.published_at),u.id::text,u.nickname,
g.summary,g.keywords,g.topics,g.generated_at FROM velis.articles a
JOIN velis.article_versions v ON v.id=a.current_revision_id LEFT JOIN velis.sources s ON s.id=a.source_id
LEFT JOIN velis.users u ON u.id=a.author_user_id
LEFT JOIN velis.ai_current_selections cs ON cs.article_id=a.id AND cs.revision_id=a.current_revision_id
LEFT JOIN velis.ai_generation_results g ON g.id=cs.generation_result_id AND g.article_id=a.id AND g.revision_id=a.current_revision_id
WHERE a.status='published' AND a.published_at<=$6 AND NOT (a.id=ANY($1::bigint[]))
AND ($2::uuid IS NULL OR NOT EXISTS (
 SELECT 1 FROM velis.article_not_interested n WHERE n.article_id=a.id AND n.user_id=$2::uuid AND n.expires_at>$3))`
	args := []any{skip, nil, now}
	if userID != "" {
		args[1] = userID
	}
	if cursor != nil {
		query += ` AND (COALESCE(a.source_published_at,a.published_at),a.id)<($4,$5)`
		args = append(args, cursor.SortAt, cursor.ArticleID)
	}
	// 使用固定第 6 个参数保存首查时间，键集参数则随有无 cursor 变化。
	if cursor == nil {
		query = strings.ReplaceAll(query, "a.published_at<=$6", "a.published_at<=$4")
	}
	args = append(args, startedAt)
	query += ` ORDER BY COALESCE(a.source_published_at,a.published_at) DESC,a.id DESC LIMIT $` + strconv.Itoa(len(args)+1)
	args = append(args, limit)
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]articleDomain.ListItem, 0, limit)
	for rows.Next() {
		item, _, err := scanPublished(rows, false)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
