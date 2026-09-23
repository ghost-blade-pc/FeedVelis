package bootstrap

import (
	"io"
	"log/slog"
	"testing"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/config"
)

func TestWorkerComponentsDisableOnlyMQWhenURLIsEmpty(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Default()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	components, err := buildWorkerComponents(cfg, nil, logger, "worker-test")
	if err != nil {
		t.Fatal(err)
	}
	names := componentNames(components)
	for _, required := range []string{"feed", "outbox-cleanup", "metrics"} {
		if !names[required] {
			t.Errorf("MQ 关闭时缺少 %s", required)
		}
	}
	for _, disabled := range []string{"relay", "consumer"} {
		if names[disabled] {
			t.Errorf("MQ 关闭时不应装配 %s", disabled)
		}
	}
	cfg = config.Default()
	cfg.RabbitMQ.URL = "amqp://guest:guest@localhost:5672/"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	components, err = buildWorkerComponents(cfg, nil, logger, "worker-test")
	if err != nil {
		t.Fatal(err)
	}
	names = componentNames(components)
	if !names["relay"] || !names["consumer"] {
		t.Fatalf("MQ 开启后组件缺失: %+v", names)
	}
}
func componentNames(components []WorkerComponent) map[string]bool {
	result := map[string]bool{}
	for _, component := range components {
		result[component.Name] = true
	}
	return result
}
