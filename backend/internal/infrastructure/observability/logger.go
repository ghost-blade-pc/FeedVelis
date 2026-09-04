// Package observability 提供日志、指标和追踪的基础适配。
package observability

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

func NewLogger(output io.Writer, levelName, environment string) (*slog.Logger, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(strings.ToUpper(levelName))); err != nil {
		return nil, fmt.Errorf("无效日志级别 %q: %w", levelName, err)
	}

	options := &slog.HandlerOptions{Level: level}
	if environment == "development" {
		return slog.New(slog.NewTextHandler(output, options)), nil
	}
	return slog.New(slog.NewJSONHandler(output, options)), nil
}
