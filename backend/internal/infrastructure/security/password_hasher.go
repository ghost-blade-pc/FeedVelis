package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
)

// Argon2Params 是密码散列参数；初始值见 DefaultArgon2Params。
type Argon2Params struct {
	MemoryKiB   uint32
	Iterations  uint32
	Parallelism uint8
	SaltBytes   int
	KeyBytes    int
}

// DefaultArgon2Params 返回规格初始参数：内存 19456 KiB、迭代 2、并行度 1、盐 16 字节、输出 32 字节。
func DefaultArgon2Params() Argon2Params {
	return Argon2Params{MemoryKiB: 19456, Iterations: 2, Parallelism: 1, SaltBytes: 16, KeyBytes: 32}
}

// 校验已存散列时的上限，避免畸形或恶意参数触发无界计算。
const (
	maxVerifyMemoryKiB   = 65536
	maxVerifyIterations  = 10
	maxVerifyParallelism = 4
	minVerifySaltBytes   = 8
	maxVerifySaltBytes   = 64
	minVerifyKeyBytes    = 16
	maxVerifyKeyBytes    = 64
)

var (
	ErrInvalidHashFormat    = errors.New("密码散列格式无效")
	ErrHashParamsOutOfRange = errors.New("密码散列参数超出允许范围")
	ErrInvalidHasherConfig  = errors.New("密码散列配置无效")
)

var argon2Prefix = fmt.Sprintf("$argon2id$v=%d$", argon2.Version)

// PasswordHasher 实现 ports.PasswordHasher：每个密码使用独立随机盐，
// 进程内最多同时执行一次散列且不排队，额度占用时立即返回 ports.ErrHasherBusy。
type PasswordHasher struct {
	params Argon2Params
	slots  chan struct{}
}

func NewPasswordHasher(params Argon2Params, concurrency int) (*PasswordHasher, error) {
	if err := validateParams(params); err != nil {
		return nil, err
	}
	if concurrency < 1 {
		return nil, fmt.Errorf("%w: 并发额度必须是正数", ErrInvalidHasherConfig)
	}
	return &PasswordHasher{params: params, slots: make(chan struct{}, concurrency)}, nil
}

// Params 返回当前散列参数，供配置校验与日志核对。
func (h *PasswordHasher) Params() Argon2Params { return h.params }

func (h *PasswordHasher) Hash(password string) (string, error) {
	release, err := h.acquire()
	if err != nil {
		return "", err
	}
	defer release()

	salt := make([]byte, h.params.SaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, h.params.Iterations, h.params.MemoryKiB, h.params.Parallelism, uint32(h.params.KeyBytes))
	return encodeHash(h.params, salt, key), nil
}

func (h *PasswordHasher) Verify(password, encoded string) (bool, error) {
	release, err := h.acquire()
	if err != nil {
		return false, err
	}
	defer release()

	params, salt, want, err := decodeHash(encoded)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(password), salt, params.Iterations, params.MemoryKiB, params.Parallelism, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

func (h *PasswordHasher) acquire() (func(), error) {
	select {
	case h.slots <- struct{}{}:
		return func() { <-h.slots }, nil
	default:
		return nil, ports.ErrHasherBusy
	}
}

func encodeHash(params Argon2Params, salt, key []byte) string {
	return fmt.Sprintf("%sm=%d,t=%d,p=%d$%s$%s", argon2Prefix, params.MemoryKiB, params.Iterations, params.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}

func decodeHash(encoded string) (Argon2Params, []byte, []byte, error) {
	if !strings.HasPrefix(encoded, argon2Prefix) || len(encoded) > 512 {
		return Argon2Params{}, nil, nil, ErrInvalidHashFormat
	}
	parts := strings.Split(strings.TrimPrefix(encoded, argon2Prefix), "$")
	if len(parts) != 3 {
		return Argon2Params{}, nil, nil, ErrInvalidHashFormat
	}
	params, err := parseParams(parts[0])
	if err != nil {
		return Argon2Params{}, nil, nil, err
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[1])
	if err != nil {
		return Argon2Params{}, nil, nil, ErrInvalidHashFormat
	}
	key, err := base64.RawStdEncoding.Strict().DecodeString(parts[2])
	if err != nil {
		return Argon2Params{}, nil, nil, ErrInvalidHashFormat
	}
	if len(salt) < minVerifySaltBytes || len(salt) > maxVerifySaltBytes || len(key) < minVerifyKeyBytes || len(key) > maxVerifyKeyBytes {
		return Argon2Params{}, nil, nil, ErrHashParamsOutOfRange
	}
	return params, salt, key, nil
}

func parseParams(raw string) (Argon2Params, error) {
	values := map[string]string{}
	for _, field := range strings.Split(raw, ",") {
		name, value, ok := strings.Cut(field, "=")
		if !ok {
			return Argon2Params{}, ErrInvalidHashFormat
		}
		values[name] = value
	}
	if len(values) != 3 {
		return Argon2Params{}, ErrInvalidHashFormat
	}
	memory, err := parseUint32(values["m"])
	if err != nil {
		return Argon2Params{}, ErrInvalidHashFormat
	}
	iterations, err := parseUint32(values["t"])
	if err != nil {
		return Argon2Params{}, ErrInvalidHashFormat
	}
	parallelism, err := parseUint32(values["p"])
	if err != nil {
		return Argon2Params{}, ErrInvalidHashFormat
	}
	if parallelism > 255 {
		return Argon2Params{}, ErrHashParamsOutOfRange
	}
	params := Argon2Params{MemoryKiB: memory, Iterations: iterations, Parallelism: uint8(parallelism)}
	if memory > maxVerifyMemoryKiB || iterations > maxVerifyIterations || parallelism > maxVerifyParallelism {
		return Argon2Params{}, ErrHashParamsOutOfRange
	}
	return params, nil
}

func parseUint32(raw string) (uint32, error) {
	value, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		return 0, err
	}
	return uint32(value), nil
}

// ValidateArgon2Params 校验写入侧参数，供配置层复用同一份资源预算。
func ValidateArgon2Params(params Argon2Params) error { return validateParams(params) }

// validateParams 校验写入侧参数，保证不会创建超出资源预算的散列。
func validateParams(params Argon2Params) error {
	if params.MemoryKiB < 8192 || params.MemoryKiB > maxVerifyMemoryKiB {
		return fmt.Errorf("%w: 内存必须在 8192～%d KiB 之间", ErrInvalidHasherConfig, maxVerifyMemoryKiB)
	}
	if params.Iterations < 1 || params.Iterations > maxVerifyIterations {
		return fmt.Errorf("%w: 迭代次数必须在 1～%d 之间", ErrInvalidHasherConfig, maxVerifyIterations)
	}
	if params.Parallelism < 1 || params.Parallelism > maxVerifyParallelism {
		return fmt.Errorf("%w: 并行度必须在 1～%d 之间", ErrInvalidHasherConfig, maxVerifyParallelism)
	}
	if params.SaltBytes < minVerifySaltBytes || params.SaltBytes > maxVerifySaltBytes {
		return fmt.Errorf("%w: 盐长度必须在 %d～%d 字节之间", ErrInvalidHasherConfig, minVerifySaltBytes, maxVerifySaltBytes)
	}
	if params.KeyBytes < minVerifyKeyBytes || params.KeyBytes > maxVerifyKeyBytes {
		return fmt.Errorf("%w: 输出长度必须在 %d～%d 字节之间", ErrInvalidHasherConfig, minVerifyKeyBytes, maxVerifyKeyBytes)
	}
	return nil
}
