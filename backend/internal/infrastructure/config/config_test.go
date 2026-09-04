package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadYAMLAndEnvironmentOverride(t *testing.T) {
	t.Setenv("VELIS_HTTP_ADDRESS", "127.0.0.1:9090")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := []byte("app:\n  name: test-velis\nhttp:\n  shutdown_timeout: 3s\ndatabase:\n  connect_timeout: 2s\nworker:\n  heartbeat_interval: 1s\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.App.Name != "test-velis" {
		t.Fatalf("App.Name = %q", cfg.App.Name)
	}
	if cfg.HTTP.Address != "127.0.0.1:9090" {
		t.Fatalf("HTTP.Address = %q", cfg.HTTP.Address)
	}
	if cfg.HTTP.ShutdownTimeout != 3*time.Second {
		t.Fatalf("ShutdownTimeout = %v", cfg.HTTP.ShutdownTimeout)
	}
}

func TestValidateRejectsInvalidConnectionLimits(t *testing.T) {
	cfg := Default()
	cfg.Database.MinConnections = 10
	cfg.Database.MaxConnections = 5

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() expected error")
	}
}
