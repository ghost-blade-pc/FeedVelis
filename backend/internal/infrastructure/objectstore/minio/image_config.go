package minio

import (
	"image"
	_ "image/jpeg" // 注册标准库 JPEG 配置解码器。
	_ "image/png"  // 注册标准库 PNG 配置解码器。
	"io"

	_ "golang.org/x/image/webp" // 注册 WebP 配置解码器。
)

// DecodeImageConfig 只读取图片头部配置，不解码完整像素数据。
// 调用方仍需使用受限 Reader 控制最多可读取的对象字节数。
func DecodeImageConfig(reader io.Reader) (image.Config, string, error) {
	return image.DecodeConfig(reader)
}
