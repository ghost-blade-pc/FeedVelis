package integration

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	articleApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/article"
	idempotencyApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/idempotency"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/content/markdown"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

const (
	bindingAuthorID = "58000000-0000-0000-0000-000000000001"
	bindingPolicy   = 20
	bindingBytes    = 50 << 20
)

func seedBindingAuthor(t *testing.T, env *testEnv, now time.Time) {
	t.Helper()
	if _, err := env.pool.Exec(context.Background(), `INSERT INTO velis.users
(id,username,nickname,password_hash,role,status,created_at,updated_at)
VALUES ($1,'binding_author','绑定作者','hash','user','active',$2,$2)`, bindingAuthorID, now); err != nil {
		t.Fatal(err)
	}
}

// seedReadyAssets 直接写入 n 条已确认资产，每张 sizeBytes 字节。
func seedReadyAssets(t *testing.T, env *testEnv, prefix string, count int, sizeBytes int64, now time.Time) []string {
	t.Helper()
	ctx := context.Background()
	// 同一测试内多次播种时前缀不同，用前缀派生 ID 段避免主键冲突。
	segment := 0
	for _, char := range prefix {
		segment = (segment*31 + int(char)) % 0xffff
	}
	assetIDs := make([]string, 0, count)
	for index := range count {
		assetID := fmt.Sprintf("59000000-0000-0000-%04x-%012d", segment, index+1)
		assetIDs = append(assetIDs, assetID)
		if _, err := env.pool.Exec(ctx, `INSERT INTO velis.article_assets
(id,owner_user_id,object_key,status,content_type,size_bytes,width,height,checksum,quota_counted_at,confirmed_at,created_at,updated_at)
VALUES ($1,$2,$3,'ready','image/png',$4,10,10,'etag',$5,$5,$5,$5)`,
			assetID, bindingAuthorID, prefix+"/"+assetID, sizeBytes, now); err != nil {
			t.Fatal(err)
		}
	}
	return assetIDs
}

func newBindingService(t *testing.T, env *testEnv, now time.Time) (*articleApp.UserService, *postgres.ArticleAssetRepository) {
	t.Helper()
	repository := postgres.NewArticleRepository(env.pool)
	idempotency := idempotencyApp.NewService(postgres.NewIdempotencyRepository(env.pool),
		postgres.NewTxManager(env.pool), 24*time.Hour)
	service := articleApp.NewUserService(repository, markdown.NewUserRenderer(),
		postgres.NewArticleAssetRepository(env.pool), idempotency, &articleTestClock{now: now},
		articleApp.AssetPolicy{MaxImages: bindingPolicy, MaxTotalBytes: bindingBytes})
	return service, postgres.NewArticleAssetRepository(env.pool)
}

// TestBindingRejectsOverLimitBatchesWithoutPartialRevision 覆盖 20 张与 50 MiB 两条边界：
// 超限时不得留下文章、修订或任何已绑定资产。
func TestBindingRejectsOverLimitBatchesWithoutPartialRevision(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	now := fixedNow()
	seedBindingAuthor(t, env, now)
	// 绑定边界用例需要一篇文章承载引用，直接播种的文章属于他人账户。
	seedAssetUsers(t, env, now)
	service, assetRepository := newBindingService(t, env, now)
	ctx := context.Background()

	// 21 张（每张 1 KiB）：数量超限。
	tooMany := seedReadyAssets(t, env, "binding/many", 21, 1<<10, now)
	// 20 张各 3 MiB：数量合规但总量 60 MiB 超限。
	tooLarge := seedReadyAssets(t, env, "binding/large", 20, 3<<20, now)

	cases := []struct {
		name     string
		key      string
		assetIDs []string
	}{
		{"数量超过 20 张", "59000000-0000-0000-0000-0000000000a1", tooMany},
		{"引用总量超过 50 MiB", "59000000-0000-0000-0000-0000000000a2", tooLarge},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			markdownBody := ""
			for _, assetID := range testCase.assetIDs {
				markdownBody += "![](asset:" + assetID + ")\n"
			}
			_, _, err := service.Create(ctx, articleApp.CreateUserArticleCommand{AuthorUserID: bindingAuthorID,
				IdempotencyKey: testCase.key, Title: "超限稿件", Markdown: markdownBody,
				InitialStatus: articleDomain.StatusPublished})
			if err == nil {
				t.Fatal("超限批次必须被拒绝")
			}
			// 21 张在渲染阶段就被拒（内容错误），总量超限则走到绑定边界；两者都必须整篇失败。
			if !errors.Is(err, articleApp.ErrAssetOwnership) && !errors.Is(err, ports.ErrContentInvalid) {
				t.Fatalf("超限绑定 err = %v", err)
			}
		})
	}

	// 绑定边界不信任调用方：即使绕过渲染器直接提交 21 个资产，也必须整批拒绝。
	articleID, revisionID := seedArticleForAssets(t, env, 8201, 9201)
	bindErr := assetRepository.BindArticleAssets(ctx, binding(bindingAuthorID, articleID, revisionID, tooMany, now))
	if !errors.Is(bindErr, ports.ErrArticleAssetUnavailable) {
		t.Fatalf("绕过渲染器的 21 张 err = %v", bindErr)
	}

	// 失败不得产生部分修订：既没有文章，也没有引用关系，资产也仍未被绑定。
	var articles, references, bound int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.articles WHERE author_user_id=$1`, bindingAuthorID).Scan(&articles); err != nil || articles != 0 {
		t.Fatalf("文章数 = %d err=%v", articles, err)
	}
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.article_asset_references`).Scan(&references); err != nil || references != 0 {
		t.Fatalf("引用数 = %d err=%v", references, err)
	}
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.article_assets
WHERE owner_user_id=$1 AND bound_article_id IS NOT NULL`, bindingAuthorID).Scan(&bound); err != nil || bound != 0 {
		t.Fatalf("已绑定资产 = %d err=%v", bound, err)
	}
}

// TestBindingAcceptsBoundaryBatchAndWritesReferencesAtomically 覆盖恰好等于上限的组合。
func TestBindingAcceptsBoundaryBatchAndWritesReferencesAtomically(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	now := fixedNow()
	seedBindingAuthor(t, env, now)
	service, repository := newBindingService(t, env, now)
	ctx := context.Background()

	// 20 张各 2 MiB = 40 MiB：数量与总量都恰好在上限内。
	assetIDs := seedReadyAssets(t, env, "binding/ok", 20, 2<<20, now)
	body := ""
	for _, assetID := range assetIDs {
		body += "![](asset:" + assetID + ")\n"
	}
	created, _, err := service.Create(ctx, articleApp.CreateUserArticleCommand{AuthorUserID: bindingAuthorID,
		IdempotencyKey: "59000000-0000-0000-0000-0000000000f1", Title: "边界稿件", Markdown: body,
		InitialStatus: articleDomain.StatusPublished})
	if err != nil {
		t.Fatalf("边界批次应被接受: %v", err)
	}
	// 同一次写入建立 20 条修订引用，且每张资产都绑定到该文章。
	references, err := repository.ListRevisionAssetIDs(ctx, created.Article.RevisionID)
	if err != nil || len(references) != len(assetIDs) {
		t.Fatalf("修订引用数 = %d err=%v", len(references), err)
	}
	detail, err := service.Get(ctx, bindingAuthorID, created.Article.ID)
	if err != nil || len(detail.AssetIDs) != len(assetIDs) {
		t.Fatalf("作者详情引用 = %d err=%v", len(detail.AssetIDs), err)
	}

	// 同一文章的新修订复用已绑定资产：不必重新绑定也不报错。
	updated, _, err := service.Update(ctx, articleApp.UpdateUserArticleCommand{AuthorUserID: bindingAuthorID,
		ArticleID: created.Article.ID, ExpectedVersion: created.Article.LockVersion,
		IdempotencyKey: "59000000-0000-0000-0000-0000000000f2", Title: "边界稿件（修订）", Markdown: body + "\n补充说明"})
	if err != nil || updated.Article.RevisionNumber != 2 {
		t.Fatalf("同文章复用失败 = %+v err=%v", updated.Article, err)
	}
	reused, err := repository.ListRevisionAssetIDs(ctx, updated.Article.RevisionID)
	if err != nil || len(reused) != len(assetIDs) {
		t.Fatalf("新修订引用 = %d err=%v", len(reused), err)
	}
	// 跨文章复用仍被拒绝：这批资产已经属于上一篇文章。
	_, _, err = service.Create(ctx, articleApp.CreateUserArticleCommand{AuthorUserID: bindingAuthorID,
		IdempotencyKey: "59000000-0000-0000-0000-0000000000f3", Title: "跨文章复用", Markdown: body,
		InitialStatus: articleDomain.StatusPublished})
	if !errors.Is(err, articleApp.ErrAssetOwnership) {
		t.Fatalf("跨文章复用 err = %v", err)
	}
	var articles int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.articles WHERE author_user_id=$1`, bindingAuthorID).Scan(&articles); err != nil || articles != 1 {
		t.Fatalf("跨文章复用后文章数 = %d err=%v", articles, err)
	}
}

// TestBindingRejectsUnconfirmedAndForeignAssetsAtomically 覆盖他人资产与未确认资产：
// 与 6.1 的仓储级用例不同，这里从用例入口验证整个修订一起回滚。
func TestBindingRejectsUnconfirmedAndForeignAssetsAtomically(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	now := fixedNow()
	seedBindingAuthor(t, env, now)
	// 他人资产需要一个真实存在的所有者账户。
	seedAssetUsers(t, env, now)
	service, _ := newBindingService(t, env, now)
	ctx := context.Background()

	ready := seedReadyAssets(t, env, "binding/mixed", 1, 1<<20, now)
	// 第二条资产属于其他用户。
	const foreignID = "59000000-0000-0000-0000-0000000000e1"
	if _, err := env.pool.Exec(ctx, `INSERT INTO velis.article_assets
(id,owner_user_id,object_key,status,content_type,size_bytes,width,height,quota_counted_at,confirmed_at,created_at,updated_at)
VALUES ($1,$2,'binding/foreign','ready','image/png',1024,10,10,$3,$3,$3,$3)`, foreignID, assetOwnerID, now); err != nil {
		t.Fatal(err)
	}
	// 第三条资产仍是待确认状态。
	const pendingID = "59000000-0000-0000-0000-0000000000e2"
	if _, err := env.pool.Exec(ctx, `INSERT INTO velis.article_assets
(id,owner_user_id,object_key,status,created_at,updated_at)
VALUES ($1,$2,'binding/pending','pending',$3,$3)`, pendingID, bindingAuthorID, now); err != nil {
		t.Fatal(err)
	}

	// 合法资产排在前面：绑定必须整批校验，不能在遇到非法项前先写入。
	body := "![](asset:" + ready[0] + ")\n![](asset:" + foreignID + ")\n"
	_, _, err := service.Create(ctx, articleApp.CreateUserArticleCommand{AuthorUserID: bindingAuthorID,
		IdempotencyKey: "59000000-0000-0000-0000-0000000000e3", Title: "混合批次", Markdown: body,
		InitialStatus: articleDomain.StatusPublished})
	if !errors.Is(err, articleApp.ErrAssetOwnership) {
		t.Fatalf("他人资产 err = %v", err)
	}

	body = "![](asset:" + ready[0] + ")\n![](asset:" + pendingID + ")\n"
	_, _, err = service.Create(ctx, articleApp.CreateUserArticleCommand{AuthorUserID: bindingAuthorID,
		IdempotencyKey: "59000000-0000-0000-0000-0000000000e4", Title: "未确认批次", Markdown: body,
		InitialStatus: articleDomain.StatusPublished})
	if !errors.Is(err, articleApp.ErrAssetOwnership) {
		t.Fatalf("未确认资产 err = %v", err)
	}

	var articles, references, bound int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.articles WHERE author_user_id=$1`, bindingAuthorID).Scan(&articles); err != nil || articles != 0 {
		t.Fatalf("文章数 = %d err=%v", articles, err)
	}
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.article_asset_references`).Scan(&references); err != nil || references != 0 {
		t.Fatalf("引用数 = %d err=%v", references, err)
	}
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.article_assets WHERE id=ANY($1::uuid[]) AND bound_article_id IS NOT NULL`,
		[]string{ready[0], foreignID, pendingID}).Scan(&bound); err != nil || bound != 0 {
		t.Fatalf("已绑定资产 = %d err=%v", bound, err)
	}
}
