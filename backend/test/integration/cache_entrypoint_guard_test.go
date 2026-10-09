package integration

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestCacheReadMakeEntrypointsRequireEnvironment(t *testing.T) {
	for _, target := range []string{"integration-redis", "integration-cache-reads"} {
		keys := []string{"VELIS_TEST_REDIS_ADDRESS"}
		if target == "integration-cache-reads" {
			keys = []string{"VELIS_TEST_DATABASE_URL", "VELIS_TEST_REDIS_ADDRESS", "VELIS_TEST_OPENSEARCH_URL"}
		}
		for _, missing := range keys {
			t.Run(target+"/"+missing, func(t *testing.T) {
				cmd := exec.Command("make", "-C", "../../..", target)
				for _, v := range os.Environ() {
					if !strings.HasPrefix(v, "VELIS_TEST_") && !strings.HasPrefix(v, "MAKEFLAGS=") {
						cmd.Env = append(cmd.Env, v)
					}
				}
				for _, key := range keys {
					if key != missing {
						cmd.Env = append(cmd.Env, key+"=unused")
					}
				}
				out, err := cmd.CombinedOutput()
				if err == nil || !strings.Contains(string(out), "未设置 "+missing) || strings.Contains(string(out), "go test") {
					t.Fatalf("入口未在测试前失败: %s %v", out, err)
				}
			})
		}
	}
}
