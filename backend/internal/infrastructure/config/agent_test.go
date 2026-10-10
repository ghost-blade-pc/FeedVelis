package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAgentDefaultsBoundsAndAPIKey(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	a := cfg.Agent
	if a.Enabled || a.MaxConversationsPerUser != 50 || a.MaxMessagesPerConversation != 200 || a.MaxMessageChars != 4000 || a.CursorTTL != time.Hour || a.Tools.Timeout != 5*time.Second || a.Tools.MaxOutputBytes != 131072 {
		t.Fatalf("默认值: %+v", a)
	}
	for _, mutate := range []func(*AgentConfig){
		func(a *AgentConfig) { a.MaxConversationsPerUser = 0 }, func(a *AgentConfig) { a.MaxConversationsPerUser = 501 },
		func(a *AgentConfig) { a.MaxMessagesPerConversation = 0 }, func(a *AgentConfig) { a.MaxMessagesPerConversation = 2001 },
		func(a *AgentConfig) { a.MaxMessageChars = 0 }, func(a *AgentConfig) { a.MaxMessageChars = 16001 },
		func(a *AgentConfig) { a.CursorTTLRaw = "59s" }, func(a *AgentConfig) { a.CursorTTLRaw = "25h" },
		func(a *AgentConfig) { a.Tools.TimeoutRaw = "99ms" }, func(a *AgentConfig) { a.Tools.TimeoutRaw = "11s" },
		func(a *AgentConfig) { a.Tools.MaxOutputBytes = 65535 }, func(a *AgentConfig) { a.Tools.MaxOutputBytes = 1048577 },
	} {
		a := defaultAgentConfig()
		mutate(&a)
		if validateAgentConfig(&a) == nil {
			t.Fatalf("非法配置未拒绝: %+v", a)
		}
	}
	for _, upper := range []bool{false, true} {
		a := defaultAgentConfig()
		if upper {
			a.MaxConversationsPerUser = 500
			a.MaxMessagesPerConversation = 2000
			a.MaxMessageChars = 16000
			a.CursorTTLRaw = "24h"
			a.Tools.TimeoutRaw = "10s"
			a.Tools.MaxOutputBytes = 1048576
		} else {
			a.MaxConversationsPerUser = 1
			a.MaxMessagesPerConversation = 1
			a.MaxMessageChars = 1
			a.CursorTTLRaw = "1m"
			a.Tools.TimeoutRaw = "100ms"
			a.Tools.MaxOutputBytes = 65536
		}
		if err := validateAgentConfig(&a); err != nil {
			t.Fatal("合法边界被拒绝", err)
		}
	}
	cfg.App.Environment = "production"
	cfg.Agent.Enabled = true
	if err := validateAgentConfig(&cfg.Agent); err != nil {
		t.Fatal("共享配置不得要求API密钥", err)
	}
	if _, _, err := cfg.AgentAPIKey(); err != nil {
		t.Fatal("认证关闭不应要求密钥")
	}
	cfg.Auth.Enabled = true
	if _, _, err := cfg.AgentAPIKey(); err == nil {
		t.Fatal("非开发API缺少密钥必须失败")
	}
	cfg.Agent.Enabled = false
	if _, _, err := cfg.AgentAPIKey(); err != nil {
		t.Fatal("Agent关闭不应要求密钥")
	}
	cfg.Agent.Enabled = true
	cfg.App.Environment = "development"
	if key, ephemeral, err := cfg.AgentAPIKey(); err != nil || len(key) != 32 || !ephemeral {
		t.Fatal("开发临时密钥错误")
	}
}

func TestAgentYAMLAndEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("agent:\n  enabled: true\n  max_message_chars: 100\n  cursor_ttl: 2h\n  tools:\n    timeout: 3s\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VELIS_AGENT_MAX_MESSAGE_CHARS", "200")
	t.Setenv("VELIS_AGENT_MAX_CONVERSATIONS_PER_USER", "40")
	t.Setenv("VELIS_AGENT_MAX_MESSAGES_PER_CONVERSATION", "150")
	t.Setenv("VELIS_AGENT_TOOL_MAX_OUTPUT_BYTES", "65536")
	t.Setenv("VELIS_AGENT_TOOL_TIMEOUT", "4s")
	t.Setenv("VELIS_AGENT_CURSOR_TTL", "3h")
	t.Setenv("VELIS_AGENT_CURSOR_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Agent.Enabled || cfg.Agent.MaxMessageChars != 200 || cfg.Agent.MaxConversationsPerUser != 40 || cfg.Agent.MaxMessagesPerConversation != 150 || cfg.Agent.CursorTTL != 3*time.Hour || cfg.Agent.Tools.Timeout != 4*time.Second || cfg.Agent.Tools.MaxOutputBytes != 65536 || len(cfg.Agent.CursorKey.Bytes()) != 32 {
		t.Fatalf("覆盖错误: %+v", cfg.Agent)
	}
	for _, raw := range []string{"bad", base64.StdEncoding.EncodeToString(make([]byte, 31)), base64.StdEncoding.EncodeToString(make([]byte, 33))} {
		t.Setenv("VELIS_AGENT_CURSOR_KEY", raw)
		if _, err := Load(path); err == nil {
			t.Fatal("非法密钥未拒绝")
		}
	}
}
