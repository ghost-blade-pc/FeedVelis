package bootstrap

import (
	"strings"
	"testing"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/config"
)

func TestRecommendationRequiresProductionAPIKeyButNotWorkerConfig(t *testing.T) {
	cfg := config.Default()
	cfg.App.Environment = "production"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := buildRecommendation(cfg, nil, nil); err == nil || !strings.Contains(err.Error(), "VELIS_RECOMMEND_CURSOR_KEY") {
		t.Fatalf("生产 API 缺少共享推荐密钥未拒绝: %v", err)
	}
}
