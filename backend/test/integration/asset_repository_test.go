package integration

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/asset"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

const (
	assetOwnerID = "52000000-0000-0000-0000-000000000001"
	assetOtherID = "52000000-0000-0000-0000-000000000002"
)

var assetLimits = asset.Limits{MaxFileBytes: 10 << 20, MaxWidth: 8192, MaxHeight: 8192, MaxPixels: 40000000}

func seedAssetUsers(t *testing.T, env *testEnv, now time.Time) {
	t.Helper()
	for _, user := range []struct{ id, username string }{{assetOwnerID, "asset_owner"}, {assetOtherID, "asset_other"}} {
		if _, err := env.pool.Exec(context.Background(), `INSERT INTO velis.users
(id,username,nickname,password_hash,role,status,created_at,updated_at)
VALUES ($1,$2,$3,'hash','user','active',$4,$4)`, user.id, user.username, user.username, now); err != nil {
			t.Fatal(err)
		}
	}
}

// newAsset 通过仓储创建一条 pending 资产，返回其聚合。
func newAsset(t *testing.T, env *testEnv, repository *postgres.ArticleAssetRepository, assetID string, now time.Time) asset.Asset {
	t.Helper()
	ctx := context.Background()
	pending, err := asset.NewPending(assetID, assetOwnerID, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Create(ctx, pending); err != nil {
		t.Fatal(err)
	}
	stored, err := repository.Get(ctx, assetID)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func confirmAsset(t *testing.T, repository *postgres.ArticleAssetRepository, assetID string, now time.Time) asset.Asset {
	t.Helper()
	ctx := context.Background()
	stored, err := repository.Get(ctx, assetID)
	if err != nil {
		t.Fatal(err)
	}
	media, err := asset.NewMedia(asset.ContentTypePNG, 2048, 640, 480, "sha256:test", assetLimits)
	if err != nil {
		t.Fatal(err)
	}
	if err := stored.Confirm(assetOwnerID, media, now); err != nil {
		t.Fatal(err)
	}
	// 确认与额度计入是两步：用例在所有者行锁内调用 MarkQuotaCounted，仓储只负责写回。
	stored.MarkQuotaCounted(now)
	if err := repository.Save(ctx, stored); err != nil {
		t.Fatal(err)
	}
	confirmed, err := repository.Get(ctx, assetID)
	if err != nil {
		t.Fatal(err)
	}
	return confirmed
}

func TestAssetRoundTripAndOwnership(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	now := fixedNow()
	seedAssetUsers(t, env, now)
	repository := postgres.NewArticleAssetRepository(env.pool)
	const assetID = "52000000-0000-0000-0000-00000000a001"

	pending := newAsset(t, env, repository, assetID, now)
	if pending.Status() != asset.StatusPending || pending.Media() != nil || pending.BoundArticleID() != nil {
		t.Fatalf("重建的待上传资产 = %+v", pending)
	}
	if pending.ObjectKey() != "article-assets/"+assetOwnerID+"/"+assetID+"/original" {
		t.Fatalf("对象键 = %q", pending.ObjectKey())
	}
	if !pending.OwnedBy(assetOwnerID) || pending.OwnedBy(assetOtherID) {
		t.Fatal("归属判断错误")
	}

	confirmed := confirmAsset(t, repository, assetID, now)
	if confirmed.Status() != asset.StatusReady || confirmed.Media() == nil ||
		confirmed.Media().ContentType != asset.ContentTypePNG || confirmed.Media().SizeBytes != 2048 ||
		confirmed.Media().Width != 640 || confirmed.Media().Height != 480 {
		t.Fatalf("确认后的资产 = %+v", confirmed)
	}
	if _, err := repository.Get(context.Background(), "52000000-0000-0000-0000-00000000dead"); !errors.Is(err, asset.ErrNotFound) {
		t.Fatalf("不存在的资产 err = %v", err)
	}
}

func TestAssetQuotaCountingAndOwnerLock(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	now := fixedNow()
	seedAssetUsers(t, env, now)
	repository := postgres.NewArticleAssetRepository(env.pool)
	ctx := context.Background()
	const firstID = "52000000-0000-0000-0000-00000000b001"
	const secondID = "52000000-0000-0000-0000-00000000b002"

	newAsset(t, env, repository, firstID, now)
	newAsset(t, env, repository, secondID, now)
	usage, err := repository.Usage(ctx, assetOwnerID)
	if err != nil || usage.PendingCount != 2 || usage.CountedBytes != 0 {
		t.Fatalf("待确认用量 = %+v err=%v", usage, err)
	}

	first := confirmAsset(t, repository, firstID, now)
	usage, err = repository.Usage(ctx, assetOwnerID)
	if err != nil || usage.CountedBytes != 2048 || usage.PendingCount != 1 {
		t.Fatalf("确认一条后的用量 = %+v err=%v", usage, err)
	}
	// 额度只按已计入的资产统计：域内重复计费被拒绝，写回也不会改变用量。
	if first.MarkQuotaCounted(now.Add(time.Hour)) {
		t.Fatal("同一资产不得重复计费")
	}
	if err := repository.Save(ctx, first); err != nil {
		t.Fatal(err)
	}
	usage, err = repository.Usage(ctx, assetOwnerID)
	if err != nil || usage.CountedBytes != 2048 {
		t.Fatalf("重复保存后的用量 = %+v err=%v", usage, err)
	}

	// 已删除资产释放额度。
	deleted, err := repository.Get(ctx, firstID)
	if err != nil {
		t.Fatal(err)
	}
	if err := deleted.RequestDelete(now); err != nil {
		t.Fatal(err)
	}
	if err := deleted.MarkDeleted(now); err != nil {
		t.Fatal(err)
	}
	if err := repository.Save(ctx, deleted); err != nil {
		t.Fatal(err)
	}
	usage, err = repository.Usage(ctx, assetOwnerID)
	if err != nil || usage.CountedBytes != 0 {
		t.Fatalf("删除后的用量 = %+v err=%v", usage, err)
	}

	// 其他人看到的用量互不影响。
	otherUsage, err := repository.Usage(ctx, assetOtherID)
	if err != nil || otherUsage.CountedBytes != 0 || otherUsage.PendingCount != 0 {
		t.Fatalf("他人用量 = %+v err=%v", otherUsage, err)
	}
}

func TestConcurrentConfirmCountsQuotaOnce(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	now := fixedNow()
	seedAssetUsers(t, env, now)
	repository := postgres.NewArticleAssetRepository(env.pool)
	txManager := postgres.NewTxManager(env.pool)
	ctx := context.Background()
	const assetID = "52000000-0000-0000-0000-00000000c001"

	newAsset(t, env, repository, assetID, now)

	// 两个并发确认：LockOwner 把同一用户的记账串行化，quota_counted_at 用 COALESCE 兜底，
	// 因此无论两个事务谁先提交，额度都只增加一次。
	var wait sync.WaitGroup
	errs := make([]error, 2)
	for index := range errs {
		wait.Add(1)
		go func(slot int) {
			defer wait.Done()
			errs[slot] = txManager.WithinTransaction(ctx, func(txContext context.Context) error {
				stored, err := repository.Get(txContext, assetID)
				if err != nil {
					return err
				}
				if err := repository.LockOwner(txContext, assetOwnerID); err != nil {
					return err
				}
				media, err := asset.NewMedia(asset.ContentTypeJPEG, 4096, 320, 240, "sha256:race", assetLimits)
				if err != nil {
					return err
				}
				if err := stored.Confirm(assetOwnerID, media, now); err != nil {
					return err
				}
				// 计费标记由仓储以 COALESCE 保证只写一次，这里只需写回聚合。
				stored.MarkQuotaCounted(now)
				return repository.Save(txContext, stored)
			})
		}(index)
	}
	wait.Wait()
	for slot, err := range errs {
		if err != nil {
			t.Fatalf("并发确认 %d 失败: %v", slot, err)
		}
	}

	usage, err := repository.Usage(ctx, assetOwnerID)
	if err != nil || usage.CountedBytes != 4096 || usage.PendingCount != 0 {
		t.Fatalf("并发确认后的用量 = %+v err=%v", usage, err)
	}
	stored, err := repository.Get(ctx, assetID)
	if err != nil || stored.Status() != asset.StatusReady || stored.QuotaCountedAt() == nil {
		t.Fatalf("并发确认后的资产 = %+v err=%v", stored, err)
	}
}

func TestBindRejectsForeignAndUnconfirmedAssets(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	now := fixedNow()
	seedAssetUsers(t, env, now)
	repository := postgres.NewArticleAssetRepository(env.pool)
	ctx := context.Background()
	const readyID = "52000000-0000-0000-0000-00000000d001"
	const pendingID = "52000000-0000-0000-0000-00000000d002"

	newAsset(t, env, repository, readyID, now)
	newAsset(t, env, repository, pendingID, now)
	confirmAsset(t, repository, readyID, now)

	articleID, revisionID := seedArticleForAssets(t, env, 8101, 9101)

	// 未确认资产、他人资产都不得绑定，且都收敛为同一个不泄露原因的端口错误。
	if err := repository.BindArticleAssets(ctx, binding(assetOwnerID, articleID, revisionID, []string{pendingID}, now)); !errors.Is(err, ports.ErrArticleAssetUnavailable) {
		t.Fatalf("未确认资产绑定 err = %v", err)
	}
	if err := repository.BindArticleAssets(ctx, binding(assetOtherID, articleID, revisionID, []string{readyID}, now)); !errors.Is(err, ports.ErrArticleAssetUnavailable) {
		t.Fatalf("他人资产绑定 err = %v", err)
	}
	if err := repository.BindArticleAssets(ctx, binding(assetOwnerID, articleID, revisionID, []string{"52000000-0000-0000-0000-00000000dead"}, now)); !errors.Is(err, ports.ErrArticleAssetUnavailable) {
		t.Fatalf("未知资产绑定 err = %v", err)
	}
	// 失败路径不得留下部分绑定。
	stored, err := repository.Get(ctx, readyID)
	if err != nil || stored.BoundArticleID() != nil {
		t.Fatalf("失败绑定改变了资产 = %+v err=%v", stored, err)
	}

	if err := repository.BindArticleAssets(ctx, binding(assetOwnerID, articleID, revisionID, []string{readyID}, now)); err != nil {
		t.Fatal(err)
	}
	// 同一文章复用保持幂等，跨文章复用被拒绝。
	if err := repository.BindArticleAssets(ctx, binding(assetOwnerID, articleID, revisionID, []string{readyID}, now)); err != nil {
		t.Fatalf("重复绑定 err = %v", err)
	}
	otherArticleID, otherRevisionID := seedArticleForAssets(t, env, 8102, 9102)
	if err := repository.BindArticleAssets(ctx, binding(assetOwnerID, otherArticleID, otherRevisionID, []string{readyID}, now)); !errors.Is(err, ports.ErrArticleAssetUnavailable) {
		t.Fatalf("跨文章复用 err = %v", err)
	}

	assetIDs, err := repository.ListRevisionAssetIDs(ctx, revisionID)
	if err != nil || len(assetIDs) != 1 || assetIDs[0] != readyID {
		t.Fatalf("修订引用 = %v err=%v", assetIDs, err)
	}
}

func TestIsPubliclyReferencedFollowsArticleVisibility(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	now := fixedNow()
	seedAssetUsers(t, env, now)
	repository := postgres.NewArticleAssetRepository(env.pool)
	ctx := context.Background()
	const assetID = "52000000-0000-0000-0000-00000000e001"

	newAsset(t, env, repository, assetID, now)
	confirmAsset(t, repository, assetID, now)
	referenced, err := repository.IsPubliclyReferenced(ctx, assetID)
	if err != nil || referenced {
		t.Fatalf("未被引用时 = %t err=%v", referenced, err)
	}

	articleID, revisionID := seedArticleForAssets(t, env, 8103, 9103)
	if err := repository.BindArticleAssets(ctx, binding(assetOwnerID, articleID, revisionID, []string{assetID}, now)); err != nil {
		t.Fatal(err)
	}
	referenced, err = repository.IsPubliclyReferenced(ctx, assetID)
	if err != nil || !referenced {
		t.Fatalf("当前公开修订引用时 = %t err=%v", referenced, err)
	}

	// 下架立即撤销匿名授权，即使对象尚未物理清理。
	if _, err := env.pool.Exec(ctx, `UPDATE velis.articles SET status='offline',offline_reason='admin',
offline_at=$2,offline_by_user_id=$3,lock_version=lock_version+1,updated_at=$2 WHERE id=$1`, articleID, now, assetOwnerID); err != nil {
		t.Fatal(err)
	}
	referenced, err = repository.IsPubliclyReferenced(ctx, assetID)
	if err != nil || referenced {
		t.Fatalf("下架后 = %t err=%v", referenced, err)
	}

	// 恢复后重新授权；进入删除流程的资产不再授权。
	if _, err := env.pool.Exec(ctx, `UPDATE velis.articles SET status='published',offline_reason=NULL,
offline_at=NULL,offline_by_user_id=NULL,lock_version=lock_version+1,updated_at=$2 WHERE id=$1`, articleID, now); err != nil {
		t.Fatal(err)
	}
	referenced, err = repository.IsPubliclyReferenced(ctx, assetID)
	if err != nil || !referenced {
		t.Fatalf("恢复后 = %t err=%v", referenced, err)
	}
	if _, err := env.pool.Exec(ctx, `UPDATE velis.article_assets SET status='delete_pending',
delete_requested_at=$2,updated_at=$2 WHERE id=$1`, assetID, now); err != nil {
		t.Fatal(err)
	}
	referenced, err = repository.IsPubliclyReferenced(ctx, assetID)
	if err != nil || referenced {
		t.Fatalf("待删除资产 = %t err=%v", referenced, err)
	}
}

// binding 组装一次绑定请求；默认上限与生产配置一致（20 张、50 MiB）。
func binding(ownerID string, articleID, revisionID int64, assetIDs []string, now time.Time) ports.ArticleAssetBinding {
	return ports.ArticleAssetBinding{OwnerUserID: ownerID, ArticleID: articleID, RevisionID: revisionID,
		AssetIDs: assetIDs, MaxImages: 20, MaxTotalBytes: 50 << 20, Now: now}
}

// seedArticleForAssets 直接插入一篇公开用户文章。
// 与 i2_migration_test 一致：显式 ID、多语句、延后约束，让文章与首个修订互相引用。
// 使用字面量而非绑定参数，因为多语句只能走简单查询协议。
func seedArticleForAssets(t *testing.T, env *testEnv, articleID, revisionID int64) (int64, int64) {
	t.Helper()
	statement := fmt.Sprintf(`BEGIN;
SET CONSTRAINTS ALL DEFERRED;
INSERT INTO velis.articles (id, origin_type, author_user_id, status, discovered_at, published_at,
current_revision_id, lock_version, last_seen_at, created_at, updated_at)
OVERRIDING SYSTEM VALUE VALUES (%d, 'user', '%s', 'published', now(), now(), %d, 1, now(), now(), now());
INSERT INTO velis.article_versions (id, article_id, revision_no, title, user_markdown, sanitized_html,
plain_text, excerpt, language, content_hash, sanitizer_version, created_by_user_id, created_at)
OVERRIDING SYSTEM VALUE VALUES (%d, %d, 1, '标题', '正文', '<p>正文</p>', '正文', '正文',
'zh-CN', repeat('a', 64), 1, '%s', now());
COMMIT;`, articleID, assetOwnerID, revisionID, revisionID, articleID, assetOwnerID)
	if _, err := env.pool.Exec(context.Background(), statement); err != nil {
		t.Fatal(err)
	}
	return articleID, revisionID
}
