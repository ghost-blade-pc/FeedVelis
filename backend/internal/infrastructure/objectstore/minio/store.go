package minio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	miniogo "github.com/minio/minio-go/v7"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
)

// probeHeaderBytes 是识别图片签名所需的头部字节数；三种受支持格式的最大魔数都远小于它。
const probeHeaderBytes = 32

// StoreConfig 是对象存储适配器所需的连接与 Bucket 配置。
type StoreConfig struct {
	Endpoint       string
	UploadEndpoint string
	AccessKey      string
	SecretKey      string
	UseTLS         bool
	Bucket         string
	transport      http.RoundTripper
}

// Store 是 S3 兼容私有对象存储适配器；Bucket 必须由部署初始化并保持私有。
type Store struct {
	client         *miniogo.Client
	bucket         string
	uploadEndpoint *url.URL
	accessKey      string
	secretKey      string
	signerMu       sync.Mutex
	signingClient  *miniogo.Client
}

var _ ports.AssetStorage = (*Store)(nil)

func NewStore(config StoreConfig) (*Store, error) {
	if strings.TrimSpace(config.Bucket) == "" {
		return nil, errors.New("对象存储 Bucket 不能为空")
	}
	client, err := NewClient(ClientConfig{
		Endpoint: config.Endpoint, AccessKey: config.AccessKey,
		SecretKey: config.SecretKey, UseTLS: config.UseTLS, transport: config.transport,
	})
	if err != nil {
		return nil, err
	}
	uploadEndpoint, err := parseUploadEndpoint(config.UploadEndpoint)
	if err != nil {
		return nil, err
	}
	return &Store{
		client: client, bucket: config.Bucket, uploadEndpoint: uploadEndpoint,
		accessKey: config.AccessKey, secretKey: config.SecretKey,
	}, nil
}

func parseUploadEndpoint(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("解析公共上传端点: %w", err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("公共上传端点必须是不含用户信息、查询、片段或路径前缀的完整 HTTP(S) URL")
	}
	return parsed, nil
}

// EnsurePrivateBucket 只做校验，不隐式创建 Bucket：
// 缺 Bucket 或存在匿名策略都说明部署初始化未按预期完成，应当启动时暴露而不是被掩盖。
func (s *Store) EnsurePrivateBucket(ctx context.Context) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return storageFailure(err)
	}
	if !exists {
		return fmt.Errorf("对象存储 Bucket %q 不存在，请先执行部署初始化脚本", s.bucket)
	}
	policy, err := s.client.GetBucketPolicy(ctx, s.bucket)
	if err != nil && !isMissingPolicy(err) {
		return storageFailure(err)
	}
	if strings.TrimSpace(policy) != "" {
		return fmt.Errorf("对象存储 Bucket %q 配置了访问策略，必须保持私有", s.bucket)
	}
	return nil
}

func (s *Store) PresignUpload(ctx context.Context, objectKey string, ttl time.Duration) (ports.PresignedUpload, error) {
	if strings.TrimSpace(objectKey) == "" || ttl <= 0 {
		return ports.PresignedUpload{}, errors.New("预签名上传需要对象键和正的有效期")
	}
	signingClient, err := s.signer(ctx)
	if err != nil {
		return ports.PresignedUpload{}, storageFailure(err)
	}
	uploadURL, err := signingClient.PresignedPutObject(ctx, s.bucket, objectKey, ttl)
	if err != nil {
		// 预签名地址本身等同于凭据，错误信息不得回显。
		return ports.PresignedUpload{}, storageFailure(err)
	}
	return ports.PresignedUpload{URL: uploadURL.String(), Method: http.MethodPut, Headers: map[string]string{}}, nil
}

// signer 只通过内部客户端发现 region；显式 region 让公共客户端纯离线签名，
// 因而 API 所在网络无需能够访问浏览器使用的公共端点。
func (s *Store) signer(ctx context.Context) (*miniogo.Client, error) {
	s.signerMu.Lock()
	defer s.signerMu.Unlock()
	if s.signingClient != nil {
		return s.signingClient, nil
	}
	region, err := s.client.GetBucketLocation(ctx, s.bucket)
	if err != nil {
		return nil, err
	}
	client, err := NewClient(ClientConfig{
		Endpoint: s.uploadEndpoint.Host, AccessKey: s.accessKey, SecretKey: s.secretKey,
		UseTLS: s.uploadEndpoint.Scheme == "https", Region: region,
	})
	if err != nil {
		return nil, err
	}
	s.signingClient = client
	return client, nil
}

func (s *Store) StatObject(ctx context.Context, objectKey string) (ports.ObjectInfo, error) {
	object, err := s.client.StatObject(ctx, s.bucket, objectKey, miniogo.StatObjectOptions{})
	if err != nil {
		return ports.ObjectInfo{}, objectFailure(err)
	}
	return ports.ObjectInfo{SizeBytes: object.Size, ContentType: object.ContentType, ETag: object.ETag}, nil
}

// ProbeImage 只读取 maxProbeBytes 字节：先用受限头部识别签名并解码尺寸，
// 任何一个字节超过预算都会让解码失败，从而拒绝无法在预算内确认的对象。
func (s *Store) ProbeImage(ctx context.Context, objectKey string, maxProbeBytes int64) (ports.ImageProbe, error) {
	if maxProbeBytes <= 0 {
		return ports.ImageProbe{}, errors.New("图片探测必须给定正的字节预算")
	}
	object, err := s.client.GetObject(ctx, s.bucket, objectKey, miniogo.GetObjectOptions{})
	if err != nil {
		return ports.ImageProbe{}, objectFailure(err)
	}
	defer func() { _ = object.Close() }()
	// Stat 会真正发出请求，让 NoSuchKey 在这里就暴露，而不是等到读取时才发现。
	info, err := object.Stat()
	if err != nil {
		return ports.ImageProbe{}, objectFailure(err)
	}
	header := make([]byte, probeHeaderBytes)
	read, err := io.ReadFull(io.LimitReader(object, int64(len(header))), header)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return ports.ImageProbe{}, objectFailure(err)
	}
	limited := io.MultiReader(bytes.NewReader(header[:read]), io.LimitReader(object, maxProbeBytes-int64(read)))
	config, format, err := DecodeImageConfig(limited)
	if err != nil {
		return ports.ImageProbe{}, ports.ErrImageUnsupported
	}
	contentType, ok := imageContentType(format)
	if !ok {
		return ports.ImageProbe{}, ports.ErrImageUnsupported
	}
	return ports.ImageProbe{ContentType: contentType, SizeBytes: info.Size, Width: config.Width, Height: config.Height}, nil
}

func (s *Store) OpenObject(ctx context.Context, objectKey string) (ports.ObjectStream, error) {
	object, err := s.client.GetObject(ctx, s.bucket, objectKey, miniogo.GetObjectOptions{})
	if err != nil {
		return ports.ObjectStream{}, objectFailure(err)
	}
	info, err := object.Stat()
	if err != nil {
		_ = object.Close()
		return ports.ObjectStream{}, objectFailure(err)
	}
	return ports.ObjectStream{Body: object, SizeBytes: info.Size, ContentType: info.ContentType, ETag: info.ETag}, nil
}

// DeleteObject 幂等：对象已不存在同样返回成功，使清理任务可以安全重试。
func (s *Store) DeleteObject(ctx context.Context, objectKey string) error {
	if err := s.client.RemoveObject(ctx, s.bucket, objectKey, miniogo.RemoveObjectOptions{}); err != nil {
		if errors.Is(objectFailure(err), ports.ErrObjectNotFound) {
			return nil
		}
		return storageFailure(err)
	}
	return nil
}

func imageContentType(format string) (string, bool) {
	switch format {
	case "jpeg":
		return "image/jpeg", true
	case "png":
		return "image/png", true
	case "webp":
		return "image/webp", true
	default:
		return "", false
	}
}

func isMissingPolicy(err error) bool {
	if err == nil {
		return false
	}
	response := miniogo.ToErrorResponse(err)
	return response.Code == "NoSuchBucketPolicy" || response.StatusCode == http.StatusNotFound
}

// objectFailure 区分“对象不存在”和“存储不可用”，让调用方只对前者返回 404。
func objectFailure(err error) error {
	if err == nil {
		return nil
	}
	switch miniogo.ToErrorResponse(err).Code {
	case "NoSuchKey", "NoSuchObject", "NotFound":
		return ports.ErrObjectNotFound
	default:
		return storageFailure(err)
	}
}

// storageError 对上层只暴露稳定分类；原始错误保留在链上供日志查看，且不进 Error() 文本。
type storageError struct{ cause error }

func (e storageError) Error() string { return "资产存储暂不可用" }

func (e storageError) Unwrap() []error { return []error{ports.ErrAssetStorageUnavailable, e.cause} }

func storageFailure(err error) error { return storageError{cause: err} }
