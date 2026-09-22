package bootstrap

import (
	"testing"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/config"
)

// TestBuildAssetStoreDegradesWithoutUsableConfiguration 固化 6.7 的装配语义：
// 未配置或配置不可用时都不阻止启动，由上层切换到降级实现。
func TestBuildAssetStoreDegradesWithoutUsableConfiguration(t *testing.T) {
	// 未配置端点：不构造存储，也不报错。
	store, err := buildAssetStore(config.Config{})
	if err != nil || store != nil {
		t.Fatalf("未配置时 store=%v err=%v", store, err)
	}

	// 配置了端点但缺少凭据：返回错误由上层降级，而不是让整个 API 起不来。
	incomplete := config.Config{}
	incomplete.Assets.Endpoint = "localhost:9000"
	incomplete.Assets.UploadEndpoint = "http://localhost:9000"
	incomplete.Assets.Bucket = "velis-article-assets"
	if _, err := buildAssetStore(incomplete); err == nil {
		t.Fatal("缺少凭据必须返回错误")
	}

	// 合法配置：构造出真实适配器。
	complete := incomplete
	complete.Assets.AccessKey, complete.Assets.SecretKey = "velis", "secret"
	store, err = buildAssetStore(complete)
	if err != nil || store == nil {
		t.Fatalf("合法配置 store=%v err=%v", store, err)
	}
}
