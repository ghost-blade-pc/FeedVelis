package minio

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	miniogo "github.com/minio/minio-go/v7"
)

func TestComposePrivateBucketAllowsPresignedUpload(t *testing.T) {
	endpoint := os.Getenv("VELIS_TEST_MINIO_ENDPOINT")
	if endpoint == "" {
		t.Skip("未设置 VELIS_TEST_MINIO_ENDPOINT，跳过真实 MinIO 验证")
	}
	accessKey := os.Getenv("VELIS_TEST_MINIO_ACCESS_KEY")
	secretKey := os.Getenv("VELIS_TEST_MINIO_SECRET_KEY")
	bucket := os.Getenv("VELIS_TEST_MINIO_BUCKET")
	webOrigin := os.Getenv("VELIS_TEST_MINIO_WEB_ORIGIN")
	uploadEndpoint := os.Getenv("VELIS_TEST_MINIO_UPLOAD_ENDPOINT")
	if accessKey == "" || secretKey == "" || bucket == "" {
		t.Fatal("真实 MinIO 验证必须同时设置 access key、secret key 和 bucket")
	}
	if webOrigin == "" {
		t.Fatal("真实 MinIO 验证必须设置精确 Web Origin")
	}
	if uploadEndpoint == "" {
		t.Fatal("真实 MinIO 验证必须设置公共上传 endpoint")
	}
	parsedUploadEndpoint, err := url.Parse(uploadEndpoint)
	if err != nil {
		t.Fatalf("公共上传 endpoint 无效: %v", err)
	}
	if parsedUploadEndpoint.Host == endpoint {
		t.Fatal("真实 MinIO 验证要求内部与公共 endpoint 使用不同 authority")
	}

	client, err := NewClient(ClientConfig{Endpoint: endpoint, AccessKey: accessKey, SecretKey: secretKey})
	if err != nil {
		t.Fatalf("创建 MinIO 客户端: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	objectKey := fmt.Sprintf("compose-test/%d", time.Now().UnixNano())
	body := pngFixtureBytes(t)
	t.Cleanup(func() {
		_ = client.RemoveObject(context.Background(), bucket, objectKey, miniogo.RemoveObjectOptions{})
	})

	store, err := NewStore(StoreConfig{Endpoint: endpoint, UploadEndpoint: uploadEndpoint, AccessKey: accessKey, SecretKey: secretKey, Bucket: bucket})
	if err != nil {
		t.Fatalf("创建双端点 Store: %v", err)
	}
	presigned, err := store.PresignUpload(ctx, objectKey, 15*time.Minute)
	if err != nil {
		t.Fatalf("生成预签名上传地址: %v", err)
	}
	if !strings.HasPrefix(presigned.URL, strings.TrimSuffix(uploadEndpoint, "/")+"/") || strings.Contains(presigned.URL, endpoint) {
		t.Fatalf("预签名地址未使用独立公共 endpoint: %s", presigned.URL)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, presigned.URL, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("预签名上传失败: %v", err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("预签名上传状态 = %d，期望 200", response.StatusCode)
	}
	if info, err := store.StatObject(ctx, objectKey); err != nil || info.SizeBytes != int64(len(body)) {
		t.Fatalf("内部客户端 Stat 上传对象 = %+v err=%v", info, err)
	}
	if probe, err := store.ProbeImage(ctx, objectKey, 1<<20); err != nil || probe.ContentType != "image/png" || probe.Width != 1 || probe.Height != 1 {
		t.Fatalf("内部客户端 Probe 上传对象 = %+v err=%v", probe, err)
	}

	publicURL := strings.TrimSuffix(uploadEndpoint, "/") + "/" + bucket + "/" + objectKey
	publicRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, publicURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	publicResponse, err := http.DefaultClient.Do(publicRequest)
	if err != nil {
		t.Fatalf("未授权读取请求失败: %v", err)
	}
	_, _ = io.Copy(io.Discard, publicResponse.Body)
	_ = publicResponse.Body.Close()
	if publicResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("未授权读取状态 = %d，期望 403", publicResponse.StatusCode)
	}

	assertCORSOrigin(t, ctx, publicURL, webOrigin, webOrigin)
	assertCORSOrigin(t, ctx, publicURL, "https://evil.example.com", "")
}

func assertCORSOrigin(t *testing.T, ctx context.Context, target, origin, wantAllowOrigin string) {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodOptions, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", origin)
	request.Header.Set("Access-Control-Request-Method", http.MethodPut)
	request.Header.Set("Access-Control-Request-Headers", "content-type")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("CORS 预检请求失败: %v", err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if got := response.Header.Get("Access-Control-Allow-Origin"); got != wantAllowOrigin {
		t.Fatalf("Origin %q 的 Access-Control-Allow-Origin = %q，期望 %q", origin, got, wantAllowOrigin)
	}
}
