// Package minio 提供 S3 兼容对象存储适配器。
package minio

import (
	"fmt"

	miniogo "github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// ClientConfig 是创建对象存储客户端所需的最小连接配置。
type ClientConfig struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	UseTLS    bool
}

// NewClient 创建不依赖环境凭据的 S3 兼容客户端。
func NewClient(config ClientConfig) (*miniogo.Client, error) {
	if config.Endpoint == "" {
		return nil, fmt.Errorf("对象存储端点不能为空")
	}
	if config.AccessKey == "" || config.SecretKey == "" {
		return nil, fmt.Errorf("对象存储访问凭据不能为空")
	}
	return miniogo.New(config.Endpoint, &miniogo.Options{
		Creds:  credentials.NewStaticV4(config.AccessKey, config.SecretKey, ""),
		Secure: config.UseTLS,
	})
}
