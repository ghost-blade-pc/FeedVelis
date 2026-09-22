package minio

import (
	"context"
	"net/url"
	"testing"
	"time"
)

func TestClientWithExplicitRegionPresignsWithoutEndpointAccess(t *testing.T) {
	client, err := NewClient(ClientConfig{
		Endpoint: "127.0.0.1:1", AccessKey: "access", SecretKey: "secret", Region: "us-east-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	presigned, err := client.PresignedPutObject(context.Background(), "private-assets", "objects/example", time.Minute)
	if err != nil {
		t.Fatalf("显式 region 后预签名不应访问端点: %v", err)
	}
	parsed, err := url.Parse(presigned.String())
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Host != "127.0.0.1:1" || parsed.Query().Get("X-Amz-Signature") == "" {
		t.Fatalf("预签名 URL = %q", presigned)
	}
}
