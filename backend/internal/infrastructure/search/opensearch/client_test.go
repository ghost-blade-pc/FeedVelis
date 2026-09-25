package opensearch

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	projectionApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/searchprojection"
)

func TestNewRequiresConfiguredEndpoints(t *testing.T) {
	if _, err := New(Config{}); !errors.Is(err, projectionApp.ErrNotConfigured) {
		t.Fatalf("未配置 endpoints 必须返回 ErrNotConfigured: %v", err)
	}
}

func TestNewRejectsUnreadableOrInvalidCAFile(t *testing.T) {
	if _, err := New(Config{Endpoints: []string{"https://search.internal:9200"}, CAFile: "/nonexistent/ca.pem"}); err == nil {
		t.Fatal("不可读的 CA 文件必须报错")
	}
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Config{Endpoints: []string{"https://search.internal:9200"}, CAFile: path}); err == nil ||
		!strings.Contains(err.Error(), "不含任何有效证书") {
		t.Fatalf("无效 CA 文件必须报错: %v", err)
	}
}

// TestRedactErrorDropsCredentialsQueryAndBody 覆盖 4.1 的脱敏要求：
// 错误诊断不得包含凭据、查询串或请求细节。
func TestRedactErrorDropsCredentialsQueryAndBody(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		forbid  []string
		contain string
	}{
		{
			name:    "内联凭据",
			input:   `Post "https://velis:s3cr3t@search.internal:9200/_bulk?pipeline=secret&token=hidden": dial tcp: timeout`,
			forbid:  []string{"s3cr3t", "secret", "hidden"},
			contain: "https://***@search.internal:9200/_bulk",
		},
		{
			name:    "查询串",
			input:   `status: 429, error: rejected?token=hidden`,
			forbid:  []string{"hidden"},
			contain: "429",
		},
	}
	for _, item := range cases {
		redacted := redactError(fmt.Errorf("%s", item.input)).Error()
		for _, forbidden := range item.forbid {
			if strings.Contains(redacted, forbidden) {
				t.Fatalf("%s: 脱敏结果泄露 %q: %s", item.name, forbidden, redacted)
			}
		}
		if !strings.Contains(redacted, item.contain) {
			t.Fatalf("%s: 脱敏结果缺少可诊断信息 %q: %s", item.name, item.contain, redacted)
		}
	}
}

func TestRedactErrorTruncatesLongDiagnostics(t *testing.T) {
	redacted := redactError(fmt.Errorf("%s", strings.Repeat("x", 4096)))
	if len(redacted.Error()) > 256 {
		t.Fatalf("诊断必须限长: %d", len(redacted.Error()))
	}
	if redactError(nil) != nil {
		t.Fatal("nil 错误必须原样返回")
	}
}
