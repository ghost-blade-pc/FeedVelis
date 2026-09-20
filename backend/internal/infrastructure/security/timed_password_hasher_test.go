package security

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
)

type stubHasher struct {
	encoded string
	matched bool
	err     error
}

func (s stubHasher) Hash(string) (string, error)         { return s.encoded, s.err }
func (s stubHasher) Verify(string, string) (bool, error) { return s.matched, s.err }

func TestTimedPasswordHasherRecordsDurationWithoutSecrets(t *testing.T) {
	var buffer bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buffer, nil))
	hasher := NewTimedPasswordHasher(stubHasher{encoded: "$argon2id$secret-hash", matched: true}, logger)

	if _, err := hasher.Hash("Plain9Pass!"); err != nil {
		t.Fatalf("散列失败: %v", err)
	}
	if _, err := hasher.Verify("Plain9Pass!", "$argon2id$secret-hash"); err != nil {
		t.Fatalf("校验失败: %v", err)
	}

	logged := buffer.String()
	for _, want := range []string{"operation=password_hash", "operation=password_verify", "result=success", "duration_ms="} {
		if !strings.Contains(logged, want) {
			t.Fatalf("日志缺少 %q：%s", want, logged)
		}
	}
	for _, forbidden := range []string{"Plain9Pass!", "secret-hash"} {
		if strings.Contains(logged, forbidden) {
			t.Fatalf("日志不得包含 %q：%s", forbidden, logged)
		}
	}
}

func TestTimedPasswordHasherKeepsErrorsAndBusyIsolation(t *testing.T) {
	var buffer bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buffer, nil))
	hasher := NewTimedPasswordHasher(stubHasher{err: ports.ErrHasherBusy}, logger)

	if _, err := hasher.Hash("Plain9Pass!"); !errors.Is(err, ports.ErrHasherBusy) {
		t.Fatalf("错误必须原样透传，实际 %v", err)
	}
	if !strings.Contains(buffer.String(), "result=failure") {
		t.Fatalf("失败应记录 result=failure：%s", buffer.String())
	}
}
