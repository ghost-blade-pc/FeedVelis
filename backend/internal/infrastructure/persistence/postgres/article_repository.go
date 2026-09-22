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
		return upsertRSSArticle(ctx, tx, candidate, now)
	}
	var result articleDomain.UpsertResult
	var id int64
	err := NewTxManager(r.pool).WithinTransaction(ctx, func(txContext context.Context) error {
		tx, _ := transactionFromContext(txContext)
		var err error
		result, id, err = upsertRSSArticle(txContext, tx, candidate, now)
		return err
	})
	return result, id, err
}

func upsertRSSArticle(ctx context.Context, tx pgx.Tx, candidate articleDomain.Candidate, now time.Time) (articleDomain.UpsertResult, int64, error) {
	a := candidate.Article
	var articleID int64
	var revisionNo int
	var currentHash string
	err := tx.QueryRow(ctx, `SELECT a.id,v.revision_no,v.content_hash FROM velis.articles a
JOIN velis.article_versions v ON v.id=a.current_revision_id
WHERE a.source_id=$1 AND a.dedupe_key=$2 AND a.origin_type='rss' FOR UPDATE`, a.SourceID, a.DedupeKey).
		Scan(&articleID, &revisionNo, &currentHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return insertRSSArticle(ctx, tx, candidate, now)
	}
	if err != nil {
		return "", 0, err
	}
	if currentHash == a.ContentHash {
		_, err = tx.Exec(ctx, `UPDATE velis.articles SET last_seen_at=$2,source_updated_at=$3 WHERE id=$1`, articleID, now, a.SourceUpdatedAt)
		return articleDomain.UpsertUnchanged, articleID, err
	}
	revisionID, err := nextIdentity(ctx, tx, "velis.article_versions")
	if err != nil {
		return "", 0, err
	}
	if err := insertRSSRevision(ctx, tx, revisionID, articleID, revisionNo+1, candidate, now); err != nil {
		return "", 0, err
	}
	_, err = tx.Exec(ctx, `UPDATE velis.articles SET current_revision_id=$2,lock_version=lock_version+1,
source_item_id=$3,canonical_url=$4,author_name=$5,source_published_at=$6,source_updated_at=$7,
last_seen_at=$8,updated_at=$8 WHERE id=$1`, articleID, revisionID, a.SourceItemID,
		a.CanonicalURL, a.AuthorName, a.SourcePublishedAt, a.SourceUpdatedAt, now)
	return articleDomain.UpsertUpdated, articleID, err
}

func insertRSSArticle(ctx context.Context, tx pgx.Tx, candidate articleDomain.Candidate, now time.Time) (articleDomain.UpsertResult, int64, error) {
	articleID, err := nextIdentity(ctx, tx, "velis.articles")
	if err != nil {
		return "", 0, err
	}
	revisionID, err := nextIdentity(ctx, tx, "velis.article_versions")
	if err != nil {
		return "", 0, err
	}
	a := candidate.Article
	command, err := tx.Exec(ctx, `INSERT INTO velis.articles (id,origin_type,source_id,dedupe_key,
source_item_id,canonical_url,author_name,source_published_at,discovered_at,source_updated_at,status,
published_at,current_revision_id,lock_version,last_seen_at,created_at,updated_at)
OVERRIDING SYSTEM VALUE VALUES ($1,'rss',$2,$3,$4,$5,$6,$7,$8,$9,'published',$8,$10,1,$11,$11,$11)
ON CONFLICT (source_id,dedupe_key) DO NOTHING`, articleID, a.SourceID, a.DedupeKey, a.SourceItemID,
		a.CanonicalURL, a.AuthorName, a.SourcePublishedAt, a.DiscoveredAt, a.SourceUpdatedAt, revisionID, now)
	if err != nil {
		return "", 0, err
	}
	if command.RowsAffected() == 0 {
		return "", 0, articleDomain.ErrVersionConflict
	}
	if err := insertRSSRevision(ctx, tx, revisionID, articleID, 1, candidate, now); err != nil {
		return "", 0, err
	}
	return articleDomain.UpsertInserted, articleID, nil
}

func insertRSSRevision(ctx context.Context, tx pgx.Tx, revisionID, articleID int64, revisionNo int, candidate articleDomain.Candidate, now time.Time) error {
	a, c := candidate.Article, candidate.Content
	_, err := tx.Exec(ctx, `INSERT INTO velis.article_versions (id,article_id,revision_no,title,
source_author_name,raw_description,raw_content,raw_description_truncated,raw_content_truncated,
sanitized_html,plain_text,excerpt,language,content_hash,sanitizer_version,created_at)
OVERRIDING SYSTEM VALUE VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
		revisionID, articleID, revisionNo, a.Title, a.AuthorName, c.RawDescription, c.RawContent,
		c.RawDescriptionTruncated, c.RawContentTruncated, c.SanitizedHTML, c.PlainText,
		a.Excerpt, a.Language, a.ContentHash, c.SanitizerVersion, now)
	return err
}

func (r *ArticleRepository) CreateUserArticle(ctx context.Context, authorID string, revision articleDomain.RevisionData, publish bool, now time.Time) (articleDomain.StoredArticle, error) {
	var stored articleDomain.StoredArticle
	err := NewTxManager(r.pool).WithinTransaction(ctx, func(txContext context.Context) error {
		tx, _ := transactionFromContext(txContext)
		articleID, err := nextIdentity(txContext, tx, "velis.articles")
		if err != nil {
			return err
		}
		revisionID, err := nextIdentity(txContext, tx, "velis.article_versions")
		if err != nil {
			return err
		}
		status := articleDomain.StatusDraft
		var publishedAt *time.Time
		if publish {
			status = articleDomain.StatusPublished
			value := now.UTC()
			publishedAt = &value
		}
		_, err = tx.Exec(txContext, `INSERT INTO velis.articles (id,origin_type,author_user_id,status,
published_at,discovered_at,current_revision_id,lock_version,last_seen_at,created_at,updated_at)
OVERRIDING SYSTEM VALUE VALUES ($1,'user',$2,$3,$4,$5,$6,1,$5,$5,$5)`, articleID, authorID, status, publishedAt, now, revisionID)
		if err != nil {
			return err
		}
		if err := insertUserRevision(txContext, tx, revisionID, articleID, 1, authorID, revision, now); err != nil {
			return err
		}
		stored, err = scanStored(tx.QueryRow(txContext, storedSQL+` WHERE a.id=$1`, articleID))
		return err
	})
	return stored, err
}

func (r *ArticleRepository) GetUserArticle(ctx context.Context, articleID int64, authorID string) (articleDomain.StoredArticle, error) {
	stored, err := scanStored(querier(ctx, r.pool).QueryRow(ctx, storedSQL+
		` WHERE a.id=$1 AND a.origin_type='user' AND a.author_user_id=$2 AND a.status<>'deleted'`, articleID, authorID))
	if errors.Is(err, pgx.ErrNoRows) {
		return articleDomain.StoredArticle{}, articleDomain.ErrNotFound
	}
	return stored, err
}

// GetAdminArticle 只暴露管理员可管理的公开或管理员下架文章；草稿和作者下架保持不可见。
func (r *ArticleRepository) GetAdminArticle(ctx context.Context, articleID int64) (articleDomain.StoredArticle, error) {
	stored, err := scanStored(querier(ctx, r.pool).QueryRow(ctx, storedSQL+`
 WHERE a.id=$1 AND (a.status='published' OR (a.status='offline' AND a.offline_reason='admin'))`, articleID))
	if errors.Is(err, pgx.ErrNoRows) {
		return articleDomain.StoredArticle{}, articleDomain.ErrNotFound
	}
	return stored, err
}

func (r *ArticleRepository) ListUserArticles(ctx context.Context, authorID string, beforeID int64, limit int) ([]articleDomain.StoredArticle, error) {
	rows, err := querier(ctx, r.pool).Query(ctx, storedSQL+`
 WHERE a.origin_type='user' AND a.author_user_id=$1 AND a.status<>'deleted'
 AND ($2::bigint=0 OR a.id<$2) ORDER BY a.id DESC LIMIT $3`, authorID, beforeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]articleDomain.StoredArticle, 0)
	for rows.Next() {
		item, scanErr := scanStored(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *ArticleRepository) UpdateUserRevision(ctx context.Context, articleID int64, authorID string, expected int64, revision articleDomain.RevisionData, now time.Time) (articleDomain.StoredArticle, bool, error) {
	var stored articleDomain.StoredArticle
	changed := false
	err := NewTxManager(r.pool).WithinTransaction(ctx, func(txContext context.Context) error {
		tx, _ := transactionFromContext(txContext)
		var lockVersion int64
		var revisionNo int
		var currentHash string
		err := tx.QueryRow(txContext, `SELECT a.lock_version,v.revision_no,v.content_hash FROM velis.articles a
JOIN velis.article_versions v ON v.id=a.current_revision_id
WHERE a.id=$1 AND a.origin_type='user' AND a.author_user_id=$2 AND a.status<>'deleted' FOR UPDATE`, articleID, authorID).
			Scan(&lockVersion, &revisionNo, &currentHash)
		if errors.Is(err, pgx.ErrNoRows) {
			return articleDomain.ErrNotFound
		}
		if err != nil {
			return err
		}
		if lockVersion != expected {
			return articleDomain.ErrVersionConflict
		}
		if currentHash != revision.ContentHash {
			revisionID, err := nextIdentity(txContext, tx, "velis.article_versions")
			if err != nil {
				return err
			}
			if err := insertUserRevision(txContext, tx, revisionID, articleID, revisionNo+1, authorID, revision, now); err != nil {
				return err
			}
			if _, err := tx.Exec(txContext, `UPDATE velis.articles SET current_revision_id=$2,
lock_version=lock_version+1,updated_at=$3 WHERE id=$1`, articleID, revisionID, now); err != nil {
				return err
			}
			changed = true
		}
		stored, err = scanStored(tx.QueryRow(txContext, storedSQL+` WHERE a.id=$1`, articleID))
		return err
	})
	return stored, changed, err
}

func (r *ArticleRepository) SetArticleState(ctx context.Context, articleID, expected int64, status articleDomain.Status, reason *articleDomain.OfflineReason, actor *string, publishedAt, deletedAt *time.Time, now time.Time) (articleDomain.StoredArticle, error) {
	if _, ok := transactionFromContext(ctx); !ok {
		var stored articleDomain.StoredArticle
		err := NewTxManager(r.pool).WithinTransaction(ctx, func(txContext context.Context) error {
			var err error
			stored, err = r.SetArticleState(txContext, articleID, expected, status, reason, actor, publishedAt, deletedAt, now)
			return err
		})
		return stored, err
	}
	command, err := querier(ctx, r.pool).Exec(ctx, `UPDATE velis.articles SET status=$3::varchar,offline_reason=$4::varchar,
offline_by_user_id=$5::uuid,offline_at=CASE WHEN $4::varchar IS NULL THEN NULL ELSE $8::timestamptz END,
published_at=COALESCE(published_at,$6::timestamptz),deleted_at=$7::timestamptz,
lock_version=lock_version+1,updated_at=$8::timestamptz
WHERE id=$1 AND lock_version=$2`, articleID, expected, status, reason, actor, publishedAt, deletedAt, now)
	if err != nil {
		return articleDomain.StoredArticle{}, err
	}
	if command.RowsAffected() == 0 {
		return articleDomain.StoredArticle{}, articleDomain.ErrVersionConflict
	}
	// 软删除立即在数据库侧撤销资产授权并标记待删除；对象删除由本地清理任务幂等重试。
	// 下架不在此列：下架保留资产，只有删除才是终态。
	if status == articleDomain.StatusDeleted {
		if _, err := querier(ctx, r.pool).Exec(ctx, `UPDATE velis.article_assets
SET status='delete_pending',delete_requested_at=$2,updated_at=$2
WHERE bound_article_id=$1 AND status='ready'`, articleID, now); err != nil {
			return articleDomain.StoredArticle{}, err
		}
	}
	return scanStored(querier(ctx, r.pool).QueryRow(ctx, storedSQL+` WHERE a.id=$1`, articleID))
}

func insertUserRevision(ctx context.Context, tx pgx.Tx, revisionID, articleID int64, revisionNo int, authorID string, revision articleDomain.RevisionData, now time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO velis.article_versions (id,article_id,revision_no,title,user_markdown,
sanitized_html,plain_text,excerpt,language,content_hash,sanitizer_version,created_by_user_id,created_at)
OVERRIDING SYSTEM VALUE VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, revisionID,
		articleID, revisionNo, revision.Title, revision.Markdown, revision.SanitizedHTML, revision.PlainText,
		revision.Excerpt, revision.Language, revision.ContentHash, revision.SanitizerVersion, authorID, now)
	return err
}

const storedSQL = `SELECT a.id,a.origin_type,a.author_user_id::text,a.status,a.offline_reason,
a.published_at,a.current_revision_id,v.revision_no,a.lock_version,v.title,v.user_markdown,
v.sanitized_html,v.plain_text,v.excerpt,v.language,v.content_hash,v.sanitizer_version,a.created_at,a.updated_at
FROM velis.articles a JOIN velis.article_versions v ON v.id=a.current_revision_id`

func scanStored(row pgx.Row) (articleDomain.StoredArticle, error) {
	var value articleDomain.StoredArticle
	err := row.Scan(&value.ID, &value.Origin, &value.AuthorUserID, &value.Status, &value.OfflineReason,
		&value.PublishedAt, &value.RevisionID, &value.RevisionNumber, &value.LockVersion,
		&value.Revision.Title, &value.Revision.Markdown, &value.Revision.SanitizedHTML,
		&value.Revision.PlainText, &value.Revision.Excerpt, &value.Revision.Language,
		&value.Revision.ContentHash, &value.Revision.SanitizerVersion, &value.CreatedAt, &value.UpdatedAt)
	return value, err
}

func nextIdentity(ctx context.Context, tx pgx.Tx, table string) (int64, error) {
	var id int64
	err := tx.QueryRow(ctx, `SELECT nextval(pg_get_serial_sequence($1,'id'))`, table).Scan(&id)
	return id, err
}

func (r *ArticleRepository) GetPublished(ctx context.Context, articleID int64) (articleDomain.Detail, error) {
	row := r.pool.QueryRow(ctx, `SELECT a.id,a.origin_type,v.title,a.canonical_url,s.id,s.title,s.site_url,
v.source_author_name,v.excerpt,a.source_published_at,a.discovered_at,a.published_at,
u.id::text,u.nickname,v.sanitized_html
FROM velis.articles a JOIN velis.article_versions v ON v.id=a.current_revision_id
	LEFT JOIN velis.sources s ON s.id=a.source_id LEFT JOIN velis.users u ON u.id=a.author_user_id
WHERE a.id=$1 AND a.status='published'`, articleID)
	item, html, err := scanPublished(row, true)
	if errors.Is(err, pgx.ErrNoRows) {
		return articleDomain.Detail{}, articleDomain.ErrNotFound
	}
	return articleDomain.Detail{Item: item, SanitizedHTML: html}, err
}

func (r *ArticleRepository) ListPublished(ctx context.Context, cursor *articleDomain.Cursor, limit int) ([]articleDomain.ListItem, error) {
	query := `SELECT a.id,a.origin_type,v.title,a.canonical_url,s.id,s.title,s.site_url,v.source_author_name,
v.excerpt,a.source_published_at,a.discovered_at,COALESCE(a.source_published_at,a.published_at),u.id::text,u.nickname FROM velis.articles a
JOIN velis.article_versions v ON v.id=a.current_revision_id LEFT JOIN velis.sources s ON s.id=a.source_id
LEFT JOIN velis.users u ON u.id=a.author_user_id WHERE a.status='published'`
	args := []any{}
	if cursor != nil {
		query += ` AND (COALESCE(a.source_published_at,a.published_at),a.id)<($1,$2)`
		args = append(args, cursor.SortAt, cursor.ArticleID)
	}
	query += ` ORDER BY COALESCE(a.source_published_at,a.published_at) DESC,a.id DESC LIMIT $` + strconv.Itoa(len(args)+1)
	args = append(args, limit)
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]articleDomain.ListItem, 0)
	for rows.Next() {
		item, _, err := scanPublished(rows, false)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

type rowScanner interface{ Scan(...any) error }

func scanPublished(row rowScanner, withHTML bool) (articleDomain.ListItem, *string, error) {
	var item articleDomain.ListItem
	var canonicalURL *string
	var sourceID *int64
	var sourceTitle, sourceSiteURL, sourceAuthorName, authorID, authorNickname *string
	var html *string
	targets := []any{&item.ID, &item.Origin, &item.Title, &canonicalURL, &sourceID, &sourceTitle,
		&sourceSiteURL, &sourceAuthorName, &item.Excerpt, &item.SourcePublishedAt,
		&item.DiscoveredAt, &item.SortAt, &authorID, &authorNickname}
	if withHTML {
		targets = append(targets, &html)
	}
	if err := row.Scan(targets...); err != nil {
		return articleDomain.ListItem{}, nil, err
	}
	if item.Origin == articleDomain.OriginRSS {
		item.CanonicalURL = *canonicalURL
		item.Source = articleDomain.SourceSummary{ID: *sourceID, Title: *sourceTitle, SiteURL: sourceSiteURL}
		item.AuthorName = sourceAuthorName
	} else {
		item.Author = &articleDomain.AuthorSummary{ID: *authorID, Nickname: *authorNickname}
	}
	return item, html, nil
}
