package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	accountApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/account"
	assetApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/asset"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/health"
	idempotencyApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/idempotency"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	accountDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
	assetDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/asset"
	miniostore "github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/objectstore/minio"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
	httpapi "github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/dto"
)

const assetE2EUserID = "68000000-0000-4000-8000-000000000001"

type assetE2EClock struct{ now time.Time }

func (c assetE2EClock) Now() time.Time { return c.now }

type assetE2EAccount struct{ identity accountApp.Identity }

func (a assetE2EAccount) Register(context.Context, accountApp.RegisterInput) (accountDomain.User, error) {
	return accountDomain.User{}, accountApp.ErrRegistrationDisabled
}
func (a assetE2EAccount) Login(context.Context, accountApp.LoginInput) (accountApp.LoginResult, error) {
	return accountApp.LoginResult{}, accountDomain.ErrSessionInvalid
}
func (a assetE2EAccount) Refresh(context.Context, string) (accountApp.RefreshResult, error) {
	return accountApp.RefreshResult{}, accountDomain.ErrSessionInvalid
}
func (a assetE2EAccount) LogoutByRefreshToken(context.Context, string) error { return nil }
func (a assetE2EAccount) Authenticate(_ context.Context, token string) (accountApp.Identity, error) {
	if token != "asset-e2e-token" {
		return accountApp.Identity{}, accountDomain.ErrSessionInvalid
	}
	return a.identity, nil
}
func (a assetE2EAccount) UpdateNickname(context.Context, accountApp.Identity, string) (accountDomain.User, error) {
	return accountDomain.User{}, accountDomain.ErrNotFound
}

func assetE2EPNG(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, 32, 24))); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestAssetAPIWithPostgresAndMinIOEndToEnd(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	endpoint := os.Getenv("VELIS_TEST_MINIO_ENDPOINT")
	if endpoint == "" {
		t.Skip("未设置 VELIS_TEST_MINIO_ENDPOINT，跳过 PostgreSQL + MinIO API E2E")
	}
	accessKey, secretKey, bucket := os.Getenv("VELIS_TEST_MINIO_ACCESS_KEY"), os.Getenv("VELIS_TEST_MINIO_SECRET_KEY"), os.Getenv("VELIS_TEST_MINIO_BUCKET")
	if accessKey == "" || secretKey == "" || bucket == "" {
		t.Fatal("API E2E 必须同时设置 MinIO access key、secret key 和 bucket")
	}

	ctx := context.Background()
	now := time.Date(2026, 9, 21, 8, 0, 0, 0, time.UTC)
	if _, err := env.pool.Exec(ctx, `INSERT INTO velis.users
(id,username,nickname,password_hash,role,status,created_at,updated_at)
VALUES ($1,'asset_e2e_user','资产 E2E 用户','hash','user','active',$2,$2)`, assetE2EUserID, now); err != nil {
		t.Fatal(err)
	}
	store, err := miniostore.NewStore(miniostore.StoreConfig{
		Endpoint: endpoint, AccessKey: accessKey, SecretKey: secretKey, Bucket: bucket,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsurePrivateBucket(ctx); err != nil {
		t.Fatalf("MinIO Bucket 不可用或并非私有: %v", err)
	}

	repository := postgres.NewArticleAssetRepository(env.pool)
	idempotency := idempotencyApp.NewService(postgres.NewIdempotencyRepository(env.pool), postgres.NewTxManager(env.pool), 24*time.Hour)
	clock := assetE2EClock{now: now}
	service := assetApp.NewService(repository, store, idempotency, clock, assetApp.Policy{
		Limits:         assetDomain.Limits{MaxFileBytes: 10 << 20, MaxWidth: 8192, MaxHeight: 8192, MaxPixels: 40000000},
		UserQuotaBytes: 1 << 30, PendingLimit: 20, UploadTTL: 15 * time.Minute, ProbeBytes: 10 << 20,
	})
	userID, err := accountDomain.ParseUUID(assetE2EUserID)
	if err != nil {
		t.Fatal(err)
	}
	identity := accountApp.Identity{User: accountDomain.User{ID: userID, Username: "asset_e2e_user", Nickname: "资产 E2E 用户", Role: accountDomain.RoleUser, Status: accountDomain.StatusActive},
		Session: accountDomain.NewSession(accountDomain.UUID{1}, userID, now, time.Hour)}
	h := httpapi.NewServer(httpapi.Options{
		Address: "127.0.0.1:0", ShutdownTimeout: time.Second,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Health: health.NewService(env.pool), Assets: service,
		Auth: &httpapi.AuthOptions{Service: assetE2EAccount{identity: identity}, AllowedOrigin: "http://localhost:5173", Clock: clock, Assets: service},
	})

	body := assetE2EPNG(t)
	createBody := []byte(fmt.Sprintf(`{"content_type":"image/png","size_bytes":%d}`, len(body)))
	create := ut.PerformRequest(h.Engine, consts.MethodPost, "/api/v1/me/assets", &ut.Body{Body: bytes.NewReader(createBody), Len: len(createBody)},
		ut.Header{Key: "Content-Type", Value: "application/json"}, ut.Header{Key: "Authorization", Value: "Bearer asset-e2e-token"},
		ut.Header{Key: "Idempotency-Key", Value: "68000000-0000-4000-8000-000000000011"})
	if create.Code != consts.StatusCreated {
		t.Fatalf("创建资产 status=%d body=%s", create.Code, create.Body.String())
	}
	var upload dto.AssetUpload
	if err := json.Unmarshal(create.Body.Bytes(), &upload); err != nil {
		t.Fatal(err)
	}
	var objectKey string
	if err := env.pool.QueryRow(ctx, `SELECT object_key FROM velis.article_assets WHERE id=$1`, upload.Asset.ID).Scan(&objectKey); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.DeleteObject(context.Background(), objectKey) })

	request, err := http.NewRequestWithContext(ctx, upload.UploadMethod, upload.UploadURL, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range upload.UploadHeaders {
		request.Header.Set(name, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("预签名上传: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		t.Fatalf("预签名上传 status=%d", response.StatusCode)
	}

	confirm := ut.PerformRequest(h.Engine, consts.MethodPost, "/api/v1/me/assets/"+upload.Asset.ID+"/confirm", nil,
		ut.Header{Key: "Authorization", Value: "Bearer asset-e2e-token"},
		ut.Header{Key: "Idempotency-Key", Value: "68000000-0000-4000-8000-000000000012"})
	if confirm.Code != consts.StatusOK {
		t.Fatalf("确认资产 status=%d body=%s", confirm.Code, confirm.Body.String())
	}
	ownerRead := ut.PerformRequest(h.Engine, consts.MethodGet, "/api/v1/assets/"+upload.Asset.ID+"/content", nil,
		ut.Header{Key: "Authorization", Value: "Bearer asset-e2e-token"})
	if ownerRead.Code != consts.StatusOK || !bytes.Equal(ownerRead.Body.Bytes(), body) {
		t.Fatalf("作者预览 status=%d bytes=%d", ownerRead.Code, len(ownerRead.Body.Bytes()))
	}
	if anonymous := ut.PerformRequest(h.Engine, consts.MethodGet, "/api/v1/assets/"+upload.Asset.ID+"/content", nil); anonymous.Code != consts.StatusNotFound {
		t.Fatalf("未绑定资产匿名读取 status=%d", anonymous.Code)
	}

	articleID, revisionID := int64(980001), int64(990001)
	seedAssetE2EArticle(t, env, articleID, revisionID, now)
	if err := repository.BindArticleAssets(ctx, ports.ArticleAssetBinding{OwnerUserID: assetE2EUserID, ArticleID: articleID,
		RevisionID: revisionID, AssetIDs: []string{upload.Asset.ID}, MaxImages: 20, MaxTotalBytes: 50 << 20, Now: now}); err != nil {
		t.Fatal(err)
	}
	anonymous := ut.PerformRequest(h.Engine, consts.MethodGet, "/api/v1/assets/"+upload.Asset.ID+"/content", nil)
	if anonymous.Code != consts.StatusOK || !bytes.Equal(anonymous.Body.Bytes(), body) || anonymous.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("公开流式读取 status=%d bytes=%d", anonymous.Code, len(anonymous.Body.Bytes()))
	}
	if _, err := env.pool.Exec(ctx, `UPDATE velis.articles SET status='offline',offline_reason='author',offline_at=$2,updated_at=$2 WHERE id=$1`, articleID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if hidden := ut.PerformRequest(h.Engine, consts.MethodGet, "/api/v1/assets/"+upload.Asset.ID+"/content", nil); hidden.Code != consts.StatusNotFound {
		t.Fatalf("下架后匿名读取 status=%d", hidden.Code)
	}

	// 用另一项未绑定 ready 资产验证真实对象删除；失败时状态保留，后续运行可重试。
	orphan := createAndConfirmAssetE2E(t, h, env, store, body, "68000000-0000-4000-8000-000000000021", "68000000-0000-4000-8000-000000000022")
	if _, err := env.pool.Exec(ctx, `UPDATE velis.article_assets SET confirmed_at=$2,updated_at=$2 WHERE id=$1`, orphan.id, now.Add(-8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	cleanup, err := assetApp.NewCleanupService(assetApp.CleanupDeps{Repository: repository, Storage: store, Clock: clock,
		Batch: 20, PendingTTL: 24 * time.Hour, UnboundTTL: 7 * 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	result, err := cleanup.Run(ctx)
	if err != nil || result.Deleted < 1 {
		t.Fatalf("孤儿清理 result=%+v err=%v", result, err)
	}
	if _, err := store.StatObject(ctx, orphan.objectKey); err == nil {
		t.Fatal("孤儿对象清理后仍可读取")
	}
}

type assetE2ERef struct{ id, objectKey string }

func createAndConfirmAssetE2E(t *testing.T, h *server.Hertz, env *testEnv, store *miniostore.Store, body []byte, createKey, confirmKey string) assetE2ERef {
	t.Helper()
	createBody := []byte(fmt.Sprintf(`{"content_type":"image/png","size_bytes":%d}`, len(body)))
	created := ut.PerformRequest(h.Engine, consts.MethodPost, "/api/v1/me/assets",
		&ut.Body{Body: bytes.NewReader(createBody), Len: len(createBody)},
		ut.Header{Key: "Content-Type", Value: "application/json"}, ut.Header{Key: "Authorization", Value: "Bearer asset-e2e-token"},
		ut.Header{Key: "Idempotency-Key", Value: createKey})
	if created.Code != consts.StatusCreated {
		t.Fatalf("创建孤儿资产 status=%d body=%s", created.Code, created.Body.String())
	}
	var upload dto.AssetUpload
	if err := json.Unmarshal(created.Body.Bytes(), &upload); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(context.Background(), upload.UploadMethod, upload.UploadURL, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range upload.UploadHeaders {
		request.Header.Set(name, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		t.Fatalf("上传孤儿资产 status=%d", response.StatusCode)
	}
	confirmed := ut.PerformRequest(h.Engine, consts.MethodPost, "/api/v1/me/assets/"+upload.Asset.ID+"/confirm", nil,
		ut.Header{Key: "Authorization", Value: "Bearer asset-e2e-token"}, ut.Header{Key: "Idempotency-Key", Value: confirmKey})
	if confirmed.Code != consts.StatusOK {
		t.Fatalf("确认孤儿资产 status=%d body=%s", confirmed.Code, confirmed.Body.String())
	}
	var objectKey string
	if err := env.pool.QueryRow(context.Background(), `SELECT object_key FROM velis.article_assets WHERE id=$1`, upload.Asset.ID).Scan(&objectKey); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.DeleteObject(context.Background(), objectKey) })
	return assetE2ERef{id: upload.Asset.ID, objectKey: objectKey}
}

func seedAssetE2EArticle(t *testing.T, env *testEnv, articleID, revisionID int64, now time.Time) {
	t.Helper()
	statement := fmt.Sprintf(`BEGIN;
SET CONSTRAINTS ALL DEFERRED;
INSERT INTO velis.articles (id,origin_type,author_user_id,status,discovered_at,published_at,current_revision_id,lock_version,last_seen_at,created_at,updated_at)
OVERRIDING SYSTEM VALUE VALUES (%d,'user','%s','published','%s','%s',%d,1,'%s','%s','%s');
INSERT INTO velis.article_versions (id,article_id,revision_no,title,user_markdown,sanitized_html,plain_text,excerpt,language,content_hash,sanitizer_version,created_by_user_id,created_at)
OVERRIDING SYSTEM VALUE VALUES (%d,%d,1,'E2E','正文','<p>正文</p>','正文','正文','zh-CN',repeat('a',64),1,'%s','%s');
COMMIT;`, articleID, assetE2EUserID, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), revisionID,
		now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), revisionID, articleID,
		assetE2EUserID, now.Format(time.RFC3339Nano))
	if _, err := env.pool.Exec(context.Background(), statement); err != nil {
		t.Fatal(err)
	}
}
