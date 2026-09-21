package presenter

import (
	"errors"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// 内容写接口的协议头名；Handler 不得自行拼写字面量。
const (
	HeaderIdempotencyKey = "Idempotency-Key"
	HeaderIfMatch        = "If-Match"
	HeaderETag           = "ETag"
)

var (
	// ErrIdempotencyKeyRequired 表示缺少 Idempotency-Key 请求头。
	ErrIdempotencyKeyRequired = errors.New("缺少 Idempotency-Key")
	// ErrIdempotencyKeyInvalid 表示 Idempotency-Key 不是合法 UUID。
	ErrIdempotencyKeyInvalid = errors.New("Idempotency-Key 必须是 UUID")
	// ErrIfMatchRequired 表示修改既有资源时缺少 If-Match 请求头。
	ErrIfMatchRequired = errors.New("缺少 If-Match")
	// ErrIfMatchInvalid 表示 If-Match 不是当前 lock_version 的强 ETag。
	ErrIfMatchInvalid = errors.New("If-Match 必须是强 ETag")
)

// ParseIdempotencyKey 解析必需的幂等键，并规范化为小写连字符形式，
// 使同一 UUID 的不同书写不会绕过同一调用者的幂等身份。
func ParseIdempotencyKey(header string) (string, error) {
	value := strings.TrimSpace(header)
	if value == "" {
		return "", ErrIdempotencyKeyRequired
	}
	parsed, err := uuid.Parse(value)
	if err != nil {
		return "", ErrIdempotencyKeyInvalid
	}
	return parsed.String(), nil
}

// ParseIfMatch 解析强 If-Match，返回显式的 lock_version。
// 只接受双引号包裹的正十进制整数：拒绝弱 ETag、通配符、列表和带前导零的写法。
func ParseIfMatch(header string) (int64, error) {
	value := strings.TrimSpace(header)
	if value == "" {
		return 0, ErrIfMatchRequired
	}
	if len(value) < 3 || value[0] != '"' || value[len(value)-1] != '"' {
		return 0, ErrIfMatchInvalid
	}
	digits := value[1 : len(value)-1]
	if digits[0] == '0' {
		return 0, ErrIfMatchInvalid
	}
	for _, char := range digits {
		if char < '0' || char > '9' {
			return 0, ErrIfMatchInvalid
		}
	}
	version, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || version <= 0 {
		return 0, ErrIfMatchInvalid
	}
	return version, nil
}

// ETag 生成聚合强 ETag，与 ParseIfMatch 接受的格式互逆。
func ETag(lockVersion int64) string {
	return `"` + strconv.FormatInt(lockVersion, 10) + `"`
}
