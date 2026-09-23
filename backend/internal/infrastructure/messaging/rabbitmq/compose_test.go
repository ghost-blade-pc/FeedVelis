package rabbitmq

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComposeAndDefinitionsPinReliableTopology(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..", "..")
	compose, err := os.ReadFile(filepath.Join(root, "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(compose)
	for _, required := range []string{"rabbitmq:4.3.6-management-alpine", "rabbitmq-data:/var/lib/rabbitmq", "definitions.json", "15692:15692"} {
		if !strings.Contains(text, required) {
			t.Errorf("Compose 缺少 %q", required)
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "deploy", "rabbitmq", "definitions.json"))
	if err != nil {
		t.Fatal(err)
	}
	var definitions map[string]any
	if err := json.Unmarshal(data, &definitions); err != nil {
		t.Fatal(err)
	}
	jsonText := string(data)
	for _, required := range []string{EventExchange, ConsumerQueue, DeadExchange, DeadQueue, "at-least-once", "reject-publish", "268435456", "\"delivery-limit\":5"} {
		if !strings.Contains(jsonText, required) {
			t.Errorf("definitions 缺少 %q", required)
		}
	}
	plugins, err := os.ReadFile(filepath.Join(root, "deploy", "rabbitmq", "enabled_plugins"))
	if err != nil || !strings.Contains(string(plugins), "rabbitmq_prometheus") {
		t.Fatalf("Prometheus plugin 未启用: %v", err)
	}
}
