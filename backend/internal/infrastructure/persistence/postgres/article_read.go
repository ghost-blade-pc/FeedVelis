package postgres

import (
	"context"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlecache"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articleread"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
)

const publicFactJoins = ` FROM velis.articles a
JOIN velis.article_versions v ON v.id=a.current_revision_id AND v.article_id=a.id
LEFT JOIN velis.sources s ON s.id=a.source_id
LEFT JOIN velis.users u ON u.id=a.author_user_id
LEFT JOIN velis.ai_current_selections cs ON cs.article_id=a.id AND cs.revision_id=a.current_revision_id
LEFT JOIN velis.ai_generation_results g ON g.id=cs.generation_result_id AND g.article_id=a.id AND g.revision_id=a.current_revision_id
LEFT JOIN velis.ai_embedding_results e ON e.id=cs.embedding_result_id AND e.article_id=a.id
 AND e.revision_id=a.current_revision_id AND e.generation_result_id=g.id`

func (r *ArticleRepository) ListPublicFacts(ctx context.Context, ids []int64) ([]articleread.CurrentFact, error) {
	if len(ids) == 0 {
		return []articleread.CurrentFact{}, nil
	}
	if len(ids) > 100 {
		return nil, articleDomain.ErrInvalidArgument
	}
	rows, err := querier(ctx, r.pool).Query(ctx, `SELECT a.id,a.origin_type,a.canonical_url,s.id,s.title,s.site_url,
a.source_published_at,a.discovered_at,COALESCE(a.source_published_at,a.published_at),u.id::text,u.nickname,
a.current_revision_id,g.id::text,e.id::text,e.profile_version`+publicFactJoins+`
WHERE a.status='published' AND a.id=ANY($1::bigint[])`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]articleread.CurrentFact, 0, len(ids))
	for rows.Next() {
		var f articleread.CurrentFact
		var canonical, sourceTitle, site, author, nickname, generation, embedding, profile *string
		var sourceID *int64
		if err := rows.Scan(&f.Item.ID, &f.Item.Origin, &canonical, &sourceID, &sourceTitle, &site, &f.Item.SourcePublishedAt, &f.Item.DiscoveredAt, &f.Item.SortAt, &author, &nickname, &f.Identity.RevisionID, &generation, &embedding, &profile); err != nil {
			return nil, err
		}
		f.Identity.ArticleID = f.Item.ID
		f.Identity.GenerationID = valueOrEmpty(generation)
		f.VectorIdentity = articlesearch.VectorIdentity{RevisionID: f.Identity.RevisionID, GenerationID: f.Identity.GenerationID, EmbeddingID: valueOrEmpty(embedding), Profile: valueOrEmpty(profile)}
		if f.Item.Origin == articleDomain.OriginRSS {
			f.Item.CanonicalURL = valueOrEmpty(canonical)
			f.Item.Source = articleDomain.SourceSummary{ID: *sourceID, Title: valueOrEmpty(sourceTitle), SiteURL: site}
		} else {
			f.Item.Author = &articleDomain.AuthorSummary{ID: valueOrEmpty(author), Nickname: valueOrEmpty(nickname)}
		}
		result = append(result, f)
	}
	return result, rows.Err()
}

func (r *ArticleRepository) ListPublicFragments(ctx context.Context, ids []articlecache.CardIdentity) ([]articlecache.CardFragment, error) {
	if len(ids) == 0 {
		return []articlecache.CardFragment{}, nil
	}
	if len(ids) > 100 {
		return nil, articleDomain.ErrInvalidArgument
	}
	articles, revisions, generations := make([]int64, len(ids)), make([]int64, len(ids)), make([]string, len(ids))
	for i, id := range ids {
		articles[i] = id.ArticleID
		revisions[i] = id.RevisionID
		generations[i] = id.GenerationID
	}
	rows, err := querier(ctx, r.pool).Query(ctx, `SELECT a.id,a.current_revision_id,g.id::text,a.origin_type,v.title,v.excerpt,v.source_author_name,
g.summary,g.keywords,g.topics,g.generated_at,g.generation_method`+publicFactJoins+`
JOIN unnest($1::bigint[],$2::bigint[],$3::text[]) wanted(article_id,revision_id,generation_id)
 ON wanted.article_id=a.id AND wanted.revision_id=a.current_revision_id AND wanted.generation_id=COALESCE(g.id::text,'')
WHERE a.status='published'`, articles, revisions, generations)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]articlecache.CardFragment, 0, len(ids))
	for rows.Next() {
		var f articlecache.CardFragment
		var generation, summary, method *string
		var generatedAt *time.Time
		var enhancement articleDomain.Enhancement
		if err := rows.Scan(&f.Identity.ArticleID, &f.Identity.RevisionID, &generation, &f.Origin, &f.Title, &f.Excerpt, &f.SourceAuthorName, &summary, &enhancement.Keywords, &enhancement.Topics, &generatedAt, &method); err != nil {
			return nil, err
		}
		f.Identity.GenerationID = valueOrEmpty(generation)
		if f.Origin != articleDomain.OriginRSS {
			f.SourceAuthorName = nil
		}
		if summary != nil && generatedAt != nil {
			enhancement.Summary = *summary
			enhancement.Method = valueOrEmpty(method)
			enhancement.GeneratedAt = generatedAt.UTC()
			f.Enhancement = &enhancement
		}
		result = append(result, f)
	}
	return result, rows.Err()
}

func (r *ArticleRepository) ListLatestCandidates(ctx context.Context, q articlecache.LatestQuery) (articlecache.LatestPage, error) {
	if q.Limit < 1 || q.Limit > 50 {
		return articlecache.LatestPage{}, articleDomain.ErrInvalidArgument
	}
	query := `SELECT id,COALESCE(source_published_at,published_at) FROM velis.articles WHERE status='published'`
	args := []any{q.Limit + 1}
	if q.Position != nil {
		query += ` AND (COALESCE(source_published_at,published_at),id)<($2,$3)`
		args = append(args, q.Position.SortAt, q.Position.ArticleID)
	}
	query += ` ORDER BY COALESCE(source_published_at,published_at) DESC,id DESC LIMIT $1`
	rows, err := querier(ctx, r.pool).Query(ctx, query, args...)
	if err != nil {
		return articlecache.LatestPage{}, err
	}
	defer rows.Close()
	page := articlecache.LatestPage{Candidates: []articlecache.Candidate{}}
	for rows.Next() {
		var c articlecache.Candidate
		if err := rows.Scan(&c.ArticleID, &c.SortAt); err != nil {
			return articlecache.LatestPage{}, err
		}
		page.Candidates = append(page.Candidates, c)
	}
	page.Exhausted = len(page.Candidates) < q.Limit+1
	return page, rows.Err()
}
