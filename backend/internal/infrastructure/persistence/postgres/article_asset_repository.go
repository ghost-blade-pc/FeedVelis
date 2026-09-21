package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/asset"
)

// ArticleAssetRepository 持久化私有图片资产，并提供文章修订引用与匿名授权查询。
type ArticleAssetRepository struct{ pool *pgxpool.Pool }

var _ asset.Repository = (*ArticleAssetRepository)(nil)

func NewArticleAssetRepository(pool *pgxpool.Pool) *ArticleAssetRepository {
	return &ArticleAssetRepository{pool: pool}
}

const assetColumns = `id::text,owner_user_id::text,bound_article_id,object_key,status,content_type,
size_bytes,width,height,checksum,quota_counted_at,confirmed_at,delete_requested_at,deleted_at,created_at,updated_at`

func (r *ArticleAssetRepository) Create(ctx context.Context, value asset.Asset) error {
	_, err := querier(ctx, r.pool).Exec(ctx, `INSERT INTO velis.article_assets
(id,owner_user_id,bound_article_id,object_key,status,content_type,size_bytes,width,height,checksum,
quota_counted_at,confirmed_at,delete_requested_at,deleted_at,created_at,updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
		value.ID(), value.OwnerUserID(), value.BoundArticleID(), value.ObjectKey(), string(value.Status()),
		mediaColumn(value, func(m asset.Media) any { return m.ContentType }),
		mediaColumn(value, func(m asset.Media) any { return m.SizeBytes }),
		mediaColumn(value, func(m asset.Media) any { return m.Width }),
		mediaColumn(value, func(m asset.Media) any { return m.Height }),
		mediaColumn(value, func(m asset.Media) any { return m.Checksum }),
		value.QuotaCountedAt(), value.ConfirmedAt(), value.DeleteRequestedAt(), value.DeletedAt(),
		value.CreatedAt(), value.UpdatedAt())
	return err
}

// Save 写回聚合当前状态。quota_counted_at 使用 COALESCE：同一资产并发确认时额度只计入一次。
func (r *ArticleAssetRepository) Save(ctx context.Context, value asset.Asset) error {
	_, err := querier(ctx, r.pool).Exec(ctx, `UPDATE velis.article_assets SET
bound_article_id=$2,status=$3,content_type=$4,size_bytes=$5,width=$6,height=$7,checksum=$8,
quota_counted_at=COALESCE(quota_counted_at,$9),confirmed_at=$10,delete_requested_at=$11,deleted_at=$12,updated_at=$13
WHERE id=$1`,
		value.ID(), value.BoundArticleID(), string(value.Status()),
		mediaColumn(value, func(m asset.Media) any { return m.ContentType }),
		mediaColumn(value, func(m asset.Media) any { return m.SizeBytes }),
		mediaColumn(value, func(m asset.Media) any { return m.Width }),
		mediaColumn(value, func(m asset.Media) any { return m.Height }),
		mediaColumn(value, func(m asset.Media) any { return m.Checksum }),
		value.QuotaCountedAt(), value.ConfirmedAt(), value.DeleteRequestedAt(), value.DeletedAt(), value.UpdatedAt())
	return err
}

func (r *ArticleAssetRepository) Get(ctx context.Context, assetID string) (asset.Asset, error) {
	return scanAsset(querier(ctx, r.pool).QueryRow(ctx,
		`SELECT `+assetColumns+` FROM velis.article_assets WHERE id=$1`, assetID))
}

// LockOwner 锁定用户行，串行化同一用户的额度检查与计入；必须在调用方事务内执行。
func (r *ArticleAssetRepository) LockOwner(ctx context.Context, ownerUserID string) error {
	_, err := querier(ctx, r.pool).Exec(ctx, `SELECT 1 FROM velis.users WHERE id=$1 FOR UPDATE`, ownerUserID)
	return err
}

// Usage 汇总用户当前计入额度的字节总量与待确认数量；已删除资产释放额度。
func (r *ArticleAssetRepository) Usage(ctx context.Context, ownerUserID string) (asset.Usage, error) {
	var usage asset.Usage
	err := querier(ctx, r.pool).QueryRow(ctx, `SELECT
COALESCE(SUM(size_bytes) FILTER (WHERE quota_counted_at IS NOT NULL AND status <> 'deleted'),0),
COUNT(*) FILTER (WHERE status='pending')
FROM velis.article_assets WHERE owner_user_id=$1`, ownerUserID).Scan(&usage.CountedBytes, &usage.PendingCount)
	return usage, err
}

// ListRevisionAssetIDs 按引用写入顺序返回某个内容修订引用的资产标识；无引用时返回空切片。
func (r *ArticleAssetRepository) ListRevisionAssetIDs(ctx context.Context, revisionID int64) ([]string, error) {
	rows, err := querier(ctx, r.pool).Query(ctx, `SELECT asset_id::text FROM velis.article_asset_references
WHERE article_version_id=$1 ORDER BY created_at, asset_id`, revisionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	assetIDs := make([]string, 0)
	for rows.Next() {
		var assetID string
		if err := rows.Scan(&assetID); err != nil {
			return nil, err
		}
		assetIDs = append(assetIDs, assetID)
	}
	return assetIDs, rows.Err()
}

// IsPubliclyReferenced 判断资产是否被某篇文章的当前 published 修订引用。
// 下架、草稿、删除以及已进入删除流程的资产都不再授权匿名读取。
func (r *ArticleAssetRepository) IsPubliclyReferenced(ctx context.Context, assetID string) (bool, error) {
	var referenced bool
	err := querier(ctx, r.pool).QueryRow(ctx, `SELECT EXISTS (
SELECT 1 FROM velis.article_asset_references r
JOIN velis.article_versions v ON v.id=r.article_version_id
JOIN velis.articles a ON a.id=v.article_id AND a.current_revision_id=v.id AND a.status='published'
JOIN velis.article_assets x ON x.id=r.asset_id AND x.status='ready'
WHERE r.asset_id=$1)`, assetID).Scan(&referenced)
	return referenced, err
}

// Delete 移除一条资产行；只用于从未确认过的待上传资产，其对象已在上一步删除。
func (r *ArticleAssetRepository) Delete(ctx context.Context, assetID string) error {
	_, err := querier(ctx, r.pool).Exec(ctx, `DELETE FROM velis.article_assets WHERE id=$1`, assetID)
	return err
}

// ListExpiredPending 返回超过保留期仍未确认的待上传资产，按创建时间升序。
func (r *ArticleAssetRepository) ListExpiredPending(ctx context.Context, before time.Time, limit int) ([]asset.Asset, error) {
	return r.listAssets(ctx, `WHERE status='pending' AND created_at < $1 ORDER BY created_at,id LIMIT $2`, before, limit)
}

// ListUnboundReady 返回超过保留期仍未绑定任何文章的已确认资产。
func (r *ArticleAssetRepository) ListUnboundReady(ctx context.Context, before time.Time, limit int) ([]asset.Asset, error) {
	return r.listAssets(ctx, `WHERE status='ready' AND bound_article_id IS NULL AND confirmed_at < $1
ORDER BY confirmed_at,id LIMIT $2`, before, limit)
}

// ListDeletePending 返回等待对象清理的资产，按请求删除时间升序，使先请求的先清理。
func (r *ArticleAssetRepository) ListDeletePending(ctx context.Context, limit int) ([]asset.Asset, error) {
	return r.listAssets(ctx, `WHERE status='delete_pending' ORDER BY delete_requested_at,id LIMIT $1`, limit)
}

func (r *ArticleAssetRepository) listAssets(ctx context.Context, clause string, args ...any) ([]asset.Asset, error) {
	rows, err := querier(ctx, r.pool).Query(ctx, `SELECT `+assetColumns+` FROM velis.article_assets `+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	assets := make([]asset.Asset, 0)
	for rows.Next() {
		stored, scanErr := scanAsset(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		assets = append(assets, stored)
	}
	return assets, rows.Err()
}

// BindArticleAssets 在文章修订事务内绑定资产：先整批加锁并校验，全部通过后才写入。
// 所有拒绝原因统一收敛为 ErrArticleAssetUnavailable，避免泄露他人资产的私有元数据。
func (r *ArticleAssetRepository) BindArticleAssets(ctx context.Context, binding ports.ArticleAssetBinding) error {
	if _, ok := transactionFromContext(ctx); !ok {
		return NewTxManager(r.pool).WithinTransaction(ctx, func(txContext context.Context) error {
			return r.BindArticleAssets(txContext, binding)
		})
	}
	assetIDs := distinctIDs(binding.AssetIDs)
	// 数量上限在绑定边界独立成立：即使调用方绕过渲染器也不能写入超量引用。
	if len(assetIDs) > binding.MaxImages {
		return ports.ErrArticleAssetUnavailable
	}
	connection := querier(ctx, r.pool)
	// 一次加锁读齐，行锁保证并发绑定同一资产时只有一方成功。
	rows, err := connection.Query(ctx, `SELECT `+assetColumns+` FROM velis.article_assets
WHERE id = ANY($1::uuid[]) ORDER BY id FOR UPDATE`, assetIDs)
	if err != nil {
		return err
	}
	defer rows.Close()
	loaded := make([]asset.Asset, 0, len(assetIDs))
	for rows.Next() {
		stored, scanErr := scanAsset(rows)
		if scanErr != nil {
			return scanErr
		}
		loaded = append(loaded, stored)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	// 校验阶段不写任何数据：任一资产不合规都整批拒绝。
	if len(loaded) != len(assetIDs) {
		return ports.ErrArticleAssetUnavailable
	}
	var totalBytes int64
	bound := make([]asset.Asset, 0, len(loaded))
	for _, stored := range loaded {
		if !stored.OwnedBy(binding.OwnerUserID) {
			return ports.ErrArticleAssetUnavailable
		}
		if err := stored.Bind(binding.ArticleID, binding.Now); err != nil {
			return ports.ErrArticleAssetUnavailable
		}
		media := stored.Media()
		if media == nil {
			return ports.ErrArticleAssetUnavailable
		}
		totalBytes += media.SizeBytes
		bound = append(bound, stored)
	}
	if binding.MaxTotalBytes > 0 && totalBytes > binding.MaxTotalBytes {
		return ports.ErrArticleAssetUnavailable
	}
	for _, stored := range bound {
		if err := r.Save(ctx, stored); err != nil {
			return err
		}
		if _, err := connection.Exec(ctx, `INSERT INTO velis.article_asset_references
(article_version_id,asset_id,created_at) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`,
			binding.RevisionID, stored.ID(), binding.Now); err != nil {
			return err
		}
	}
	return nil
}

// distinctIDs 去重并保持稳定顺序：重复引用同一资产不应被当成多张图片计入上限。
func distinctIDs(assetIDs []string) []string {
	seen := make(map[string]struct{}, len(assetIDs))
	result := make([]string, 0, len(assetIDs))
	for _, assetID := range assetIDs {
		if _, exists := seen[assetID]; exists {
			continue
		}
		seen[assetID] = struct{}{}
		result = append(result, assetID)
	}
	return result
}

// mediaColumn 在 pending 状态下写 NULL：数据库 CHECK 要求未确认资产不得携带媒体信息。
func mediaColumn(value asset.Asset, pick func(asset.Media) any) any {
	media := value.Media()
	if media == nil {
		return nil
	}
	return pick(*media)
}

func scanAsset(row pgx.Row) (asset.Asset, error) {
	var id, ownerID, objectKey, status string
	var boundArticleID *int64
	var contentType, checksum *string
	var sizeBytes *int64
	var width, height *int
	var quotaCountedAt, confirmedAt, deleteRequestedAt, deletedAt *time.Time
	var createdAt, updatedAt time.Time
	err := row.Scan(&id, &ownerID, &boundArticleID, &objectKey, &status, &contentType, &sizeBytes,
		&width, &height, &checksum, &quotaCountedAt, &confirmedAt, &deleteRequestedAt, &deletedAt,
		&createdAt, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return asset.Asset{}, asset.ErrNotFound
	}
	if err != nil {
		return asset.Asset{}, err
	}
	var media *asset.Media
	if contentType != nil && sizeBytes != nil && width != nil && height != nil {
		value := asset.Media{ContentType: *contentType, SizeBytes: *sizeBytes, Width: *width, Height: *height}
		if checksum != nil {
			value.Checksum = *checksum
		}
		media = &value
	}
	return asset.Restore(id, ownerID, objectKey, asset.Status(status), boundArticleID, media,
		quotaCountedAt, confirmedAt, deleteRequestedAt, deletedAt, createdAt, updatedAt), nil
}
