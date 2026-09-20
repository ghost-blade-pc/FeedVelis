package security

import (
	"log/slog"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
)

// TimedPasswordHasher 包装密码散列实现，按调用记录耗时。
// 指标载体是结构化日志字段：只写操作、结果与耗时，不写口令、散列结果或用户标识。
type TimedPasswordHasher struct {
	inner  ports.PasswordHasher
	logger *slog.Logger
}

func NewTimedPasswordHasher(inner ports.PasswordHasher, logger *slog.Logger) *TimedPasswordHasher {
	return &TimedPasswordHasher{inner: inner, logger: logger}
}

func (h *TimedPasswordHasher) Hash(password string) (string, error) {
	startedAt := time.Now()
	encoded, err := h.inner.Hash(password)
	h.observe("password_hash", startedAt, err)
	return encoded, err
}

func (h *TimedPasswordHasher) Verify(password, encoded string) (bool, error) {
	startedAt := time.Now()
	matched, err := h.inner.Verify(password, encoded)
	h.observe("password_verify", startedAt, err)
	return matched, err
}

func (h *TimedPasswordHasher) observe(operation string, startedAt time.Time, err error) {
	if h.logger == nil {
		return
	}
	result := "success"
	if err != nil {
		result = "failure"
	}
	h.logger.Info("密码散列", "operation", operation, "result", result,
		"duration_ms", time.Since(startedAt).Milliseconds())
}
