package account

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
)

// UUID 是 RFC 4122 的 16 字节值对象；Domain 不引入第三方 UUID 库，SQL 适配留在 Infrastructure。
type UUID [16]byte

var ErrInvalidUUID = errors.New("UUID 无效")

// NewUUID 生成 RFC 4122 v4 UUID。
func NewUUID() (UUID, error) {
	var value UUID
	if _, err := rand.Read(value[:]); err != nil {
		return UUID{}, err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return value, nil
}

// ParseUUID 接受标准 36 位带连字符形式，以及 32 位紧凑十六进制形式。
func ParseUUID(raw string) (UUID, error) {
	compact := raw
	if len(raw) == 36 {
		if raw[8] != '-' || raw[13] != '-' || raw[18] != '-' || raw[23] != '-' {
			return UUID{}, ErrInvalidUUID
		}
		compact = raw[:8] + raw[9:13] + raw[14:18] + raw[19:23] + raw[24:]
	}
	if len(compact) != 32 {
		return UUID{}, ErrInvalidUUID
	}
	decoded, err := hex.DecodeString(compact)
	if err != nil {
		return UUID{}, ErrInvalidUUID
	}
	var value UUID
	copy(value[:], decoded)
	return value, nil
}

func (u UUID) String() string {
	buf := make([]byte, 0, 36)
	for i, b := range u {
		switch i {
		case 4, 6, 8, 10:
			buf = append(buf, '-')
		}
		buf = append(buf, hexDigits[b>>4], hexDigits[b&0x0f])
	}
	return string(buf)
}

func (u UUID) IsZero() bool {
	return u == UUID{}
}

const hexDigits = "0123456789abcdef"
