package postgres

import (
	"context"

	searchApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
)

// ListPublishedWithIdentity 在同一条批量 SQL 中读取公开卡片与通过归属校验的向量身份。
func (r *ArticleRepository) ListPublishedWithIdentity(ctx context.Context, ids []int64) ([]searchApp.CurrentArticle, error) {
	if len(ids) == 0 {
		return []searchApp.CurrentArticle{}, nil
	}
	rows, err := querier(ctx, r.pool).Query(ctx, `SELECT a.id,a.origin_type,v.title,a.canonical_url,s.id,s.title,s.site_url,v.source_author_name,
v.excerpt,a.source_published_at,a.discovered_at,COALESCE(a.source_published_at,a.published_at),u.id::text,u.nickname,
g.summary,g.keywords,g.topics,g.generated_at,a.current_revision_id,g.id::text,e.id::text,e.profile_version
FROM velis.articles a
JOIN velis.article_versions v ON v.id=a.current_revision_id
LEFT JOIN velis.sources s ON s.id=a.source_id
LEFT JOIN velis.users u ON u.id=a.author_user_id
LEFT JOIN velis.ai_current_selections cs ON cs.article_id=a.id AND cs.revision_id=a.current_revision_id
LEFT JOIN velis.ai_generation_results g ON g.id=cs.generation_result_id AND g.article_id=a.id AND g.revision_id=a.current_revision_id
LEFT JOIN velis.ai_embedding_results e ON e.id=cs.embedding_result_id AND e.article_id=a.id
 AND e.revision_id=a.current_revision_id AND e.generation_result_id=g.id
WHERE a.status='published' AND a.id=ANY($1::bigint[])`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]searchApp.CurrentArticle, 0, len(ids))
	for rows.Next() {
		var identity searchApp.VectorIdentity
		var generation, embedding, profile *string
		item, _, err := scanPublished(extendedScanner{rows, []any{&identity.RevisionID, &generation, &embedding, &profile}}, false)
		if err != nil {
			return nil, err
		}
		identity.GenerationID = valueOrEmpty(generation)
		identity.EmbeddingID = valueOrEmpty(embedding)
		identity.Profile = valueOrEmpty(profile)
		items = append(items, searchApp.CurrentArticle{Item: item, Identity: identity})
	}
	return items, rows.Err()
}

type extendedScanner struct {
	row   rowScanner
	extra []any
}

func (s extendedScanner) Scan(dest ...any) error { return s.row.Scan(append(dest, s.extra...)...) }
