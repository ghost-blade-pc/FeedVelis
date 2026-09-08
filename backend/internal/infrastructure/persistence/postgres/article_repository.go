package postgres

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
)

type ArticleRepository struct{ pool *pgxpool.Pool }

func NewArticleRepository(pool *pgxpool.Pool) *ArticleRepository {
	return &ArticleRepository{pool: pool}
}

func (r *ArticleRepository) Upsert(ctx context.Context, candidate articleDomain.Candidate, now time.Time) (articleDomain.UpsertResult, int64, error) {
	if tx, ok := transactionFromContext(ctx); ok {
		return upsertArticle(ctx, tx, candidate, now)
	}
	var result articleDomain.UpsertResult
	var id int64
	err := NewTxManager(r.pool).WithinTransaction(ctx, func(txContext context.Context) error {
		tx, _ := transactionFromContext(txContext)
		var err error
		result, id, err = upsertArticle(txContext, tx, candidate, now)
		return err
	})
	return result, id, err
}

func upsertArticle(ctx context.Context, tx pgx.Tx, candidate articleDomain.Candidate, now time.Time) (articleDomain.UpsertResult, int64, error) {
	a := candidate.Article
	var id int64
	err := tx.QueryRow(ctx, `
INSERT INTO velis.articles (source_id, dedupe_key, source_item_id, canonical_url, title, author_name, excerpt, language,
 source_published_at, discovered_at, source_updated_at, content_hash, status, last_seen_at, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$14,$14)
ON CONFLICT (source_id, dedupe_key) DO NOTHING RETURNING id`,
		a.SourceID, a.DedupeKey, a.SourceItemID, a.CanonicalURL, a.Title, a.AuthorName, a.Excerpt, a.Language,
		a.SourcePublishedAt, a.DiscoveredAt, a.SourceUpdatedAt, a.ContentHash, a.Status, now).Scan(&id)
	if err == nil {
		if err := insertContent(ctx, tx, id, candidate.Content, now); err != nil {
			return "", 0, err
		}
		return articleDomain.UpsertInserted, id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", 0, err
	}

	var currentHash string
	if err := tx.QueryRow(ctx, `SELECT id, content_hash FROM velis.articles WHERE source_id=$1 AND dedupe_key=$2 FOR UPDATE`, a.SourceID, a.DedupeKey).Scan(&id, &currentHash); err != nil {
		return "", 0, err
	}
	if currentHash == a.ContentHash {
		_, err = tx.Exec(ctx, `UPDATE velis.articles SET last_seen_at=$2, source_updated_at=$3 WHERE id=$1`, id, now, a.SourceUpdatedAt)
		if err != nil {
			return "", 0, err
		}
		return articleDomain.UpsertUnchanged, id, nil
	}

	_, err = tx.Exec(ctx, `UPDATE velis.articles SET source_item_id=$2, canonical_url=$3, title=$4, author_name=$5,
	 excerpt=$6, language=$7, source_published_at=$8, source_updated_at=$9, content_hash=$10,
	 last_seen_at=$11, updated_at=$11 WHERE id=$1`, id, a.SourceItemID, a.CanonicalURL,
		a.Title, a.AuthorName, a.Excerpt, a.Language, a.SourcePublishedAt, a.SourceUpdatedAt, a.ContentHash, now)
	if err != nil {
		return "", 0, err
	}
	c := candidate.Content
	_, err = tx.Exec(ctx, `UPDATE velis.article_contents SET raw_description=$2, raw_content=$3,
 raw_description_truncated=$4, raw_content_truncated=$5, sanitized_html=$6, plain_text=$7,
 sanitizer_version=$8, updated_at=$9 WHERE article_id=$1`, id, c.RawDescription, c.RawContent,
		c.RawDescriptionTruncated, c.RawContentTruncated, c.SanitizedHTML, c.PlainText, c.SanitizerVersion, now)
	if err != nil {
		return "", 0, err
	}
	return articleDomain.UpsertUpdated, id, nil
}

func insertContent(ctx context.Context, tx pgx.Tx, id int64, c articleDomain.Content, now time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO velis.article_contents (article_id, raw_description, raw_content,
 raw_description_truncated, raw_content_truncated, sanitized_html, plain_text, sanitizer_version, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)`, id, c.RawDescription, c.RawContent,
		c.RawDescriptionTruncated, c.RawContentTruncated, c.SanitizedHTML, c.PlainText, c.SanitizerVersion, now)
	return err
}

func (r *ArticleRepository) ListPublished(ctx context.Context, cursor *articleDomain.Cursor, limit int) ([]articleDomain.ListItem, error) {
	query := `SELECT a.id, a.title, a.canonical_url, s.id, s.title, s.site_url, a.author_name, a.excerpt,
 a.source_published_at, a.discovered_at, a.sort_at
FROM velis.articles a JOIN velis.sources s ON s.id=a.source_id
WHERE a.status='published'`
	args := []any{}
	if cursor != nil {
		query += ` AND (a.sort_at, a.id) < ($1, $2)`
		args = append(args, cursor.SortAt, cursor.ArticleID)
	}
	query += ` ORDER BY a.sort_at DESC, a.id DESC LIMIT $` + strconv.Itoa(len(args)+1)
	args = append(args, limit)
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]articleDomain.ListItem, 0)
	for rows.Next() {
		var item articleDomain.ListItem
		if err := rows.Scan(&item.ID, &item.Title, &item.CanonicalURL, &item.Source.ID, &item.Source.Title,
			&item.Source.SiteURL, &item.AuthorName, &item.Excerpt, &item.SourcePublishedAt, &item.DiscoveredAt, &item.SortAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
