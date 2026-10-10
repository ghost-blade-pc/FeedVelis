package postgres

import (
	"context"
	"errors"

	articleApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/article"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
	"github.com/jackc/pgx/v5"
)

// ReadPublicText 单条SQL读取当前公开修订；不读取草稿、不渲染HTML、不写阅读反馈。
func (r *ArticleRepository) ReadPublicText(ctx context.Context, id int64, maxChars int) (articleApp.PublicText, error) {
	if id < 1 || maxChars < 1 || maxChars > 8000 {
		return articleApp.PublicText{}, articleDomain.ErrInvalidArgument
	}
	row := querier(ctx, r.pool).QueryRow(ctx, `SELECT a.id,a.origin_type,v.title,a.canonical_url,s.id,s.title,s.site_url,v.source_author_name,
v.excerpt,a.source_published_at,a.discovered_at,COALESCE(a.source_published_at,a.published_at),u.id::text,u.nickname,
g.summary,g.keywords,g.topics,g.generated_at,g.generation_method,a.current_revision_id,left(v.plain_text,$2+1)`+publicFactJoins+`
WHERE a.status='published' AND a.id=$1`, id, maxChars)
	var revision int64
	var content string
	item, _, err := scanPublished(extendedScanner{row, []any{&revision, &content}}, false)
	if errors.Is(err, pgx.ErrNoRows) {
		return articleApp.PublicText{}, articleDomain.ErrNotFound
	}
	if err != nil {
		return articleApp.PublicText{}, err
	}
	item.RevisionID = revision
	chars := []rune(content)
	truncated := len(chars) > maxChars
	if truncated {
		content = string(chars[:maxChars])
	}
	return articleApp.PublicText{Item: item, Content: content, Truncated: truncated}, nil
}
