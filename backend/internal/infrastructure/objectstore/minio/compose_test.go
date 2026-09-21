package minio

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
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
	if accessKey == "" || secretKey == "" || bucket == "" {
		t.Fatal("真实 MinIO 验证必须同时设置 access key、secret key 和 bucket")
	}
	if webOrigin == "" {
		t.Fatal("真实 MinIO 验证必须设置精确 Web Origin")
	}

	client, err := NewClient(ClientConfig{Endpoint: endpoint, AccessKey: accessKey, SecretKey: secretKey})
	if err != nil {
		t.Fatalf("创建 MinIO 客户端: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	objectKey := fmt.Sprintf("compose-test/%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_ = client.RemoveObject(context.Background(), bucket, objectKey, miniogo.RemoveObjectOptions{})
	})

	uploadURL, err := client.PresignedPutObject(ctx, bucket, objectKey, 15*time.Minute)
	if err != nil {
		t.Fatalf("生成预签名上传地址: %v", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL.String(), bytes.NewBufferString("private-object"))
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

	publicURL := "http://" + endpoint + "/" + bucket + "/" + objectKey
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
