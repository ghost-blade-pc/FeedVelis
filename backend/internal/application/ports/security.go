// Package ports 定义 Application 依赖的技术端口。
package ports

import (
	"errors"
	"time"
)

// ErrHasherBusy 表示进程内的密码散列额度已被占用；调用方应立即拒绝，不排队。
var ErrHasherBusy = errors.New("密码散列容量已占用")

// PasswordHasher 负责密码散列与校验；实现必须使用独立随机盐，并且不返回或记录明文。
type PasswordHasher interface {
	Hash(password string) (string, error)
	Verify(password, encoded string) (bool, error)
}

// AccessClaims 是访问令牌声明：不含角色、用户名或账户状态，鉴权取数据库当前值。
type AccessClaims struct {
	Subject   string
	SessionID string
	TokenID   string
	IssuedAt  time.Time
	NotBefore time.Time
	ExpiresAt time.Time
}

// TokenSigner 只使用主动密钥签发，并允许用主动或上一把密钥校验。
type TokenSigner interface {
	Sign(AccessClaims) (string, error)
	Verify(token string) (AccessClaims, error)
}

// ThrottleKeys 生成登录失败限流的查找键。
// 实现必须使用配置密钥做 HMAC-SHA-256，区分账号与 IP 维度，
// 不得让调用方或数据库接触原始用户名与 IP。
type ThrottleKeys interface {
	AccountKey(normalizedUsername string) []byte
	IPKey(rawIP string) []byte
}

// TokenSecrets 生成刷新令牌并计算摘要；原始令牌只交给调用方写 Cookie，不得落库或写日志。
type TokenSecrets interface {
	NewRefreshToken() (raw string, digest [32]byte, err error)
	Digest(raw string) [32]byte
}
