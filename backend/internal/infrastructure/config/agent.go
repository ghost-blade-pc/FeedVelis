package config

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"time"
)

type AgentConfig struct {
	Enabled                    bool             `yaml:"enabled"`
	MaxConversationsPerUser    int              `yaml:"max_conversations_per_user"`
	MaxMessagesPerConversation int              `yaml:"max_messages_per_conversation"`
	MaxMessageChars            int              `yaml:"max_message_chars"`
	CursorTTLRaw               string           `yaml:"cursor_ttl"`
	CursorTTL                  time.Duration    `yaml:"-"`
	CursorKey                  SecretBytes      `yaml:"-"`
	Tools                      AgentToolsConfig `yaml:"tools"`
}

type AgentToolsConfig struct {
	TimeoutRaw     string        `yaml:"timeout"`
	Timeout        time.Duration `yaml:"-"`
	MaxOutputBytes int           `yaml:"max_output_bytes"`
}

func defaultAgentConfig() AgentConfig {
	return AgentConfig{MaxConversationsPerUser: 50, MaxMessagesPerConversation: 200, MaxMessageChars: 4000,
		CursorTTLRaw: "1h", Tools: AgentToolsConfig{TimeoutRaw: "5s", MaxOutputBytes: 128 * 1024}}
}

func applyAgentEnvironment(cfg *Config) error {
	a := &cfg.Agent
	if err := setBool(&a.Enabled, "VELIS_AGENT_ENABLED"); err != nil {
		return err
	}
	setString(&a.CursorTTLRaw, "VELIS_AGENT_CURSOR_TTL")
	setString(&a.Tools.TimeoutRaw, "VELIS_AGENT_TOOL_TIMEOUT")
	for target, key := range map[*int]string{
		&a.MaxConversationsPerUser:    "VELIS_AGENT_MAX_CONVERSATIONS_PER_USER",
		&a.MaxMessagesPerConversation: "VELIS_AGENT_MAX_MESSAGES_PER_CONVERSATION",
		&a.MaxMessageChars:            "VELIS_AGENT_MAX_MESSAGE_CHARS",
		&a.Tools.MaxOutputBytes:       "VELIS_AGENT_TOOL_MAX_OUTPUT_BYTES",
	} {
		if err := setInt(target, key); err != nil {
			return err
		}
	}
	if encoded, ok := os.LookupEnv("VELIS_AGENT_CURSOR_KEY"); ok && encoded != "" {
		decoded, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil || len(decoded) != 32 {
			return errors.New("VELIS_AGENT_CURSOR_KEY 必须为解码后恰好32字节的 Base64")
		}
		a.CursorKey = SecretBytes(decoded)
	}
	return nil
}

func validateAgentConfig(a *AgentConfig) error {
	if a.MaxConversationsPerUser < 1 || a.MaxConversationsPerUser > 500 || a.MaxMessagesPerConversation < 1 || a.MaxMessagesPerConversation > 2000 || a.MaxMessageChars < 1 || a.MaxMessageChars > 16000 {
		return errors.New("agent 配额范围无效：会话1–500，消息1–2000，正文1–16000字符")
	}
	var err error
	if a.CursorTTL, err = durationWithin("agent.cursor_ttl", a.CursorTTLRaw, time.Minute, 24*time.Hour); err != nil {
		return err
	}
	if a.Tools.Timeout, err = durationWithin("agent.tools.timeout", a.Tools.TimeoutRaw, 100*time.Millisecond, 10*time.Second); err != nil {
		return err
	}
	if a.Tools.MaxOutputBytes < 65536 || a.Tools.MaxOutputBytes > 1048576 {
		return errors.New("agent.tools.max_output_bytes 必须介于65536与1048576")
	}
	return nil
}

// AgentAPIKey 仅在API真正开启会话路由时调用；其他进程不要求此密钥。
func (cfg Config) AgentAPIKey() (key []byte, ephemeral bool, err error) {
	if !cfg.Auth.Enabled || !cfg.Agent.Enabled {
		return nil, false, nil
	}
	if cfg.Agent.CursorKey.IsSet() {
		return cfg.Agent.CursorKey.Bytes(), false, nil
	}
	if cfg.App.Environment != "development" {
		return nil, false, errors.New("API 启用 Agent 会话必须配置 VELIS_AGENT_CURSOR_KEY")
	}
	key = make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		return nil, false, errors.New("生成临时 Agent 游标密钥失败")
	}
	return key, true, nil
}
