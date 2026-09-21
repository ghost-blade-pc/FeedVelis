package ports

import (
	"context"
	"errors"
	"io"
	"time"
)

var (
	// ErrAssetStorageUnavailable 表示对象存储不可达或返回非预期错误：
	// 只使资产能力降级，不影响内容主链路。
	ErrAssetStorageUnavailable = errors.New("资产存储暂不可用")
	// ErrObjectNotFound 表示对象不存在。
	ErrObjectNotFound = errors.New("对象不存在")
	// ErrImageUnsupported 表示对象不是受支持的 JPEG、PNG 或 WebP。
	ErrImageUnsupported = errors.New("对象不是受支持的图片格式")
)

// PresignedUpload 是短期直传凭证；有效期由调用方按自己的时钟计算并返回给客户端。
type PresignedUpload struct {
	URL     string
	Method  string
	Headers map[string]string
}

// ObjectInfo 是对象存储返回的可信元数据，不包含客户端声明。
type ObjectInfo struct {
	SizeBytes   int64
	ContentType string
	ETag        string
}

// ImageProbe 是受限读取得到的图片类型与尺寸；类型来自文件签名而不是上传时的声明。
type ImageProbe struct {
	ContentType string
	SizeBytes   int64
	Width       int
	Height      int
}

// ObjectStream 是流式读取句柄。调用方必须关闭 Body，且不得把完整对象缓冲进内存。
type ObjectStream struct {
	Body        io.ReadCloser
	SizeBytes   int64
	ContentType string
	ETag        string
}

// AssetStorage 是私有对象存储的适配器接口。
// 对象键由调用方按领域规则生成，适配器不解释资产身份。
type AssetStorage interface {
	// EnsurePrivateBucket 确认 Bucket 存在且没有任何匿名访问策略。
	EnsurePrivateBucket(context.Context) error
	// PresignUpload 生成短期预签名直传凭证。
	PresignUpload(context.Context, string, time.Duration) (PresignedUpload, error)
	// StatObject 通过 HEAD 读取对象实际大小与类型。
	StatObject(context.Context, string) (ObjectInfo, error)
	// ProbeImage 在 maxProbeBytes 字节内识别图片签名并解码尺寸。
	ProbeImage(context.Context, string, int64) (ImageProbe, error)
	// OpenObject 流式读取对象。
	OpenObject(context.Context, string) (ObjectStream, error)
	// DeleteObject 幂等删除对象：对象不存在视为成功。
	DeleteObject(context.Context, string) error
}
