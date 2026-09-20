package account

import "errors"

const (
	MinPasswordBytes = 8
	MaxPasswordBytes = 20
)

var (
	ErrPasswordLength         = errors.New("密码长度必须为 8～20 个字符")
	ErrPasswordCharset        = errors.New("密码只能使用半角可打印字符")
	ErrPasswordClasses        = errors.New("密码需包含大写字母、小写字母、数字、标点中的至少三类")
	ErrPasswordTooCommon      = errors.New("密码过于常见")
	ErrPasswordEqualsUsername = errors.New("密码不能与用户名相同")
	ErrBlocklistUnavailable   = errors.New("常见密码表不可用")
)

// Blocklist 是随应用发布的本地常见密码表。
// Contains 接收 ASCII 小写化后的完整密码，实现必须做完整匹配，不得做子串匹配或外部查询。
type Blocklist interface {
	Contains(loweredPassword string) bool
}

// ValidatePassword 校验密码字符集、长度、字符类别、弱密码表和与用户名的关系。
// 密码按原始字节处理：不 trim、不做大小写转换，也不做 Unicode 规范化。
func ValidatePassword(raw, normalizedUsername string, blocklist Blocklist) error {
	if len(raw) < MinPasswordBytes || len(raw) > MaxPasswordBytes {
		return ErrPasswordLength
	}
	lowered := make([]byte, len(raw))
	var hasUpper, hasLower, hasDigit, hasPunct bool
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c < 0x21 || c > 0x7e {
			return ErrPasswordCharset
		}
		switch {
		case c >= 'A' && c <= 'Z':
			hasUpper = true
			lowered[i] = c + ('a' - 'A')
		case c >= 'a' && c <= 'z':
			hasLower = true
			lowered[i] = c
		case c >= '0' && c <= '9':
			hasDigit = true
			lowered[i] = c
		default:
			hasPunct = true
			lowered[i] = c
		}
	}
	classes := 0
	for _, present := range [...]bool{hasUpper, hasLower, hasDigit, hasPunct} {
		if present {
			classes++
		}
	}
	if classes < 3 {
		return ErrPasswordClasses
	}
	if raw == normalizedUsername {
		return ErrPasswordEqualsUsername
	}
	if blocklist == nil {
		return ErrBlocklistUnavailable
	}
	if blocklist.Contains(string(lowered)) {
		return ErrPasswordTooCommon
	}
	return nil
}
