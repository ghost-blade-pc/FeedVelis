package minio

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	miniogo "github.com/minio/minio-go/v7"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
)

// webpFixture 是 1×1 的合法 WebP：golang.org/x/image/webp 只提供解码器，没有编码器。
const webpFixture = "UklGRiQAAABXRUJQVlA4IBgAAAAwAQCdASoBAAEAAwA0JaQAA3AA/vuUAAA="

func TestObjectFailureClassification(t *testing.T) {
	cases := map[string]struct {
		err  error
		want error
	}{
		"对象不存在": {miniogo.ErrorResponse{Code: "NoSuchKey"}, ports.ErrObjectNotFound},
		"网络故障":  {errors.New("connection refused"), ports.ErrAssetStorageUnavailable},
		"服务端错误": {miniogo.ErrorResponse{Code: "InternalError"}, ports.ErrAssetStorageUnavailable},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			if err := objectFailure(testCase.err); !errors.Is(err, testCase.want) {
				t.Fatalf("err = %v，期望 %v", err, testCase.want)
			}
		})
	}
	// 分类错误不得把原始错误文本暴露给上层：其中可能包含端点或预签名地址。
	if message := objectFailure(errors.New("dial tcp 10.0.0.1:9000: connect")).Error(); message != "资产存储暂不可用" {
		t.Fatalf("错误文本 = %q", message)
	}
	if !isMissingPolicy(miniogo.ErrorResponse{Code: "NoSuchBucketPolicy"}) {
		t.Fatal("缺少策略应被识别")
	}
	if isMissingPolicy(miniogo.ErrorResponse{Code: "AccessDenied"}) {
		t.Fatal("越权不得被当成缺少策略")
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	endpoint := os.Getenv("VELIS_TEST_MINIO_ENDPOINT")
	if endpoint == "" {
		t.Skip("未设置 VELIS_TEST_MINIO_ENDPOINT，跳过真实 MinIO 验证")
	}
	accessKey := os.Getenv("VELIS_TEST_MINIO_ACCESS_KEY")
	secretKey := os.Getenv("VELIS_TEST_MINIO_SECRET_KEY")
	bucket := os.Getenv("VELIS_TEST_MINIO_BUCKET")
	if accessKey == "" || secretKey == "" || bucket == "" {
		t.Fatal("真实 MinIO 验证必须同时设置 access key、secret key 和 bucket")
	}
	store, err := NewStore(StoreConfig{Endpoint: endpoint, AccessKey: accessKey, SecretKey: secretKey, Bucket: bucket})
	if err != nil {
		t.Fatalf("创建对象存储适配器: %v", err)
	}
	return store
}

func testObjectKey(name string) string {
	return fmt.Sprintf("store-test/%d/%s", time.Now().UnixNano(), name)
}

// upload 通过预签名地址真实上传对象，顺带验证直传链路本身可用。
func upload(t *testing.T, store *Store, objectKey string, body []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	presigned, err := store.PresignUpload(ctx, objectKey, 15*time.Minute)
	if err != nil {
		t.Fatalf("生成预签名上传地址: %v", err)
	}
	if presigned.Method != http.MethodPut || presigned.URL == "" {
		t.Fatalf("预签名结果 = %+v", presigned)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, presigned.URL, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("预签名上传失败: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("预签名上传状态 = %d", response.StatusCode)
	}
	t.Cleanup(func() {
		_ = store.DeleteObject(context.Background(), objectKey)
	})
}

func encodePNG(t *testing.T, width, height int) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func encodeJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, width, height)), nil); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestStorePrivateBucketAndFaultMapping(t *testing.T) {
	store := newTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := store.EnsurePrivateBucket(ctx); err != nil {
		t.Fatalf("已初始化且私有的 Bucket 不应被拒绝: %v", err)
	}

	missing, err := NewStore(StoreConfig{Endpoint: os.Getenv("VELIS_TEST_MINIO_ENDPOINT"),
		AccessKey: os.Getenv("VELIS_TEST_MINIO_ACCESS_KEY"), SecretKey: os.Getenv("VELIS_TEST_MINIO_SECRET_KEY"),
		Bucket: "velis-absent-bucket"})
	if err != nil {
		t.Fatal(err)
	}
	// 缺 Bucket 属于部署初始化问题，必须与“存储不可用”区分，否则会被当成可重试的降级。
	err = missing.EnsurePrivateBucket(ctx)
	if err == nil {
		t.Fatal("缺少 Bucket 必须报错")
	}
	if errors.Is(err, ports.ErrAssetStorageUnavailable) {
		t.Fatalf("缺少 Bucket 不应归类为存储不可用: %v", err)
	}

	// 不可达端点必须被收敛为稳定的降级错误。
	unreachable, err := NewStore(StoreConfig{Endpoint: "127.0.0.1:1", AccessKey: "key", SecretKey: "secret", Bucket: "velis-article-assets"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unreachable.StatObject(ctx, testObjectKey("gone")); !errors.Is(err, ports.ErrAssetStorageUnavailable) {
		t.Fatalf("不可达端点 err = %v", err)
	}
	if err := unreachable.EnsurePrivateBucket(ctx); !errors.Is(err, ports.ErrAssetStorageUnavailable) {
		t.Fatalf("不可达端点 Bucket 检查 err = %v", err)
	}
	if _, err := unreachable.PresignUpload(ctx, testObjectKey("gone"), time.Minute); !errors.Is(err, ports.ErrAssetStorageUnavailable) {
		t.Fatalf("不可达端点预签名 err = %v", err)
	}
}

func TestStoreProbesAllSupportedImageFormats(t *testing.T) {
	store := newTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	webpBytes, err := base64.StdEncoding.DecodeString(webpFixture)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name        string
		body        []byte
		contentType string
		width       int
		height      int
	}{
		{"png", encodePNG(t, 640, 480), "image/png", 640, 480},
		{"jpeg", encodeJPEG(t, 320, 240), "image/jpeg", 320, 240},
		{"webp", webpBytes, "image/webp", 1, 1},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			objectKey := testObjectKey("image-" + testCase.name)
			upload(t, store, objectKey, testCase.body)

			info, err := store.StatObject(ctx, objectKey)
			if err != nil || info.SizeBytes != int64(len(testCase.body)) {
				t.Fatalf("HEAD 结果 = %+v err=%v", info, err)
			}
			probe, err := store.ProbeImage(ctx, objectKey, 1<<20)
			if err != nil {
				t.Fatalf("图片探测失败: %v", err)
			}
			// 类型必须来自文件签名，而不是上传时的声明。
			if probe.ContentType != testCase.contentType || probe.Width != testCase.width ||
				probe.Height != testCase.height || probe.SizeBytes != int64(len(testCase.body)) {
				t.Fatalf("探测结果 = %+v", probe)
			}
		})
	}
}

func TestStoreRejectsNonImageAndMissingObjects(t *testing.T) {
	store := newTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	textKey := testObjectKey("not-an-image")
	upload(t, store, textKey, []byte("这不是图片"))
	if _, err := store.ProbeImage(ctx, textKey, 1<<20); !errors.Is(err, ports.ErrImageUnsupported) {
		t.Fatalf("非图片对象 err = %v", err)
	}
	// 头部预算过小同样无法确认签名，必须拒绝而不是猜测。
	if _, err := store.ProbeImage(ctx, textKey, 4); !errors.Is(err, ports.ErrImageUnsupported) {
		t.Fatalf("预算不足 err = %v", err)
	}

	missingKey := testObjectKey("absent")
	if _, err := store.StatObject(ctx, missingKey); !errors.Is(err, ports.ErrObjectNotFound) {
		t.Fatalf("HEAD 缺失对象 err = %v", err)
	}
	if _, err := store.ProbeImage(ctx, missingKey, 1<<20); !errors.Is(err, ports.ErrObjectNotFound) {
		t.Fatalf("探测缺失对象 err = %v", err)
	}
	if _, err := store.OpenObject(ctx, missingKey); !errors.Is(err, ports.ErrObjectNotFound) {
		t.Fatalf("读取缺失对象 err = %v", err)
	}
}

func TestStoreStreamsObjectAndDeletesIdempotently(t *testing.T) {
	store := newTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	objectKey := testObjectKey("stream")
	body := encodePNG(t, 96, 64)
	upload(t, store, objectKey, body)

	stream, err := store.OpenObject(ctx, objectKey)
	if err != nil {
		t.Fatalf("流式读取失败: %v", err)
	}
	read, err := io.ReadAll(stream.Body)
	_ = stream.Body.Close()
	if err != nil || !bytes.Equal(read, body) {
		t.Fatalf("读取字节数 = %d err = %v", len(read), err)
	}
	if stream.SizeBytes != int64(len(body)) || stream.ETag == "" {
		t.Fatalf("流元数据 = %+v", stream)
	}

	if err := store.DeleteObject(ctx, objectKey); err != nil {
		t.Fatalf("删除对象失败: %v", err)
	}
	// 幂等：对象已不存在仍然成功，使清理任务可安全重试。
	if err := store.DeleteObject(ctx, objectKey); err != nil {
		t.Fatalf("重复删除必须成功: %v", err)
	}
	if _, err := store.StatObject(ctx, objectKey); !errors.Is(err, ports.ErrObjectNotFound) {
		t.Fatalf("删除后仍可 HEAD: %v", err)
	}
}
