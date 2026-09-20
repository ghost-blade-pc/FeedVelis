package security

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
)

func TestPasswordHasherRoundTrip(t *testing.T) {
	hasher, err := NewPasswordHasher(DefaultArgon2Params(), 1)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := hasher.Hash("Abcd123!")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("PHC 前缀 = %q", encoded)
	}
	if strings.Contains(encoded, "Abcd123!") {
		t.Fatal("散列结果不得包含明文")
	}
	ok, err := hasher.Verify("Abcd123!", encoded)
	if err != nil || !ok {
		t.Fatalf("正确密码应校验通过: ok=%t err=%v", ok, err)
	}
	if ok, err := hasher.Verify("Abcd123?", encoded); err != nil || ok {
		t.Fatalf("错误密码必须失败: ok=%t err=%v", ok, err)
	}
}

func TestPasswordHasherUsesIndependentSalt(t *testing.T) {
	hasher, err := NewPasswordHasher(DefaultArgon2Params(), 1)
	if err != nil {
		t.Fatal(err)
	}
	first, err := hasher.Hash("Abcd123!")
	if err != nil {
		t.Fatal(err)
	}
	second, err := hasher.Hash("Abcd123!")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("相同密码必须产生不同散列")
	}
}

func TestPasswordHasherRejectsMalformedOrOversizedParams(t *testing.T) {
	hasher, err := NewPasswordHasher(DefaultArgon2Params(), 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, encoded := range []string{
		"",
		"$argon2i$v=19$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0c2E$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=19$m=19456,t=2$c2FsdHNhbHRzYWx0c2E$aGFzaA",
		"$argon2id$v=19$m=1048576,t=2,p=1$c2FsdHNhbHRzYWx0c2E$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=19$m=19456,t=99,p=1$c2FsdHNhbHRzYWx0c2E$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=19$m=19456,t=2,p=9$c2FsdHNhbHRzYWx0c2E$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaA",
	} {
		if _, err := hasher.Verify("Abcd123!", encoded); err == nil {
			t.Fatalf("畸形或超预算散列必须被拒绝: %q", encoded)
		}
	}
}

func TestPasswordHasherValidatesWriteParams(t *testing.T) {
	for _, params := range []Argon2Params{
		{},
		{MemoryKiB: 1024, Iterations: 2, Parallelism: 1, SaltBytes: 16, KeyBytes: 32},
		{MemoryKiB: 19456, Iterations: 0, Parallelism: 1, SaltBytes: 16, KeyBytes: 32},
		{MemoryKiB: 19456, Iterations: 2, Parallelism: 9, SaltBytes: 16, KeyBytes: 32},
		{MemoryKiB: 19456, Iterations: 2, Parallelism: 1, SaltBytes: 4, KeyBytes: 32},
		{MemoryKiB: 19456, Iterations: 2, Parallelism: 1, SaltBytes: 16, KeyBytes: 8},
	} {
		if _, err := NewPasswordHasher(params, 1); !errors.Is(err, ErrInvalidHasherConfig) {
			t.Fatalf("非法参数必须被拒绝: %+v err=%v", params, err)
		}
	}
	if _, err := NewPasswordHasher(DefaultArgon2Params(), 0); !errors.Is(err, ErrInvalidHasherConfig) {
		t.Fatalf("并发额度必须是正数: %v", err)
	}
}

// 额度占用时立即返回错误而不排队；使用较大参数保证第一次计算仍在进行。
func TestPasswordHasherRejectsWhenSlotBusy(t *testing.T) {
	params := DefaultArgon2Params()
	params.MemoryKiB = 65536
	params.Iterations = 3
	hasher, err := NewPasswordHasher(params, 1)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := hasher.Hash("Abcd123!")
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	_, err = hasher.Hash("Abcd123!")
	if !errors.Is(err, ports.ErrHasherBusy) {
		t.Fatalf("额度占用时应返回 ErrHasherBusy: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("首次计算应成功: %v", err)
	}
	if _, err := hasher.Hash("Abcd123!"); err != nil {
		t.Fatalf("额度释放后应可再次计算: %v", err)
	}
}
