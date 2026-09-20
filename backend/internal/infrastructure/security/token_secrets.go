package security

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// refreshTokenBytes 是刷新令牌的随机字节数。
const refreshTokenBytes = 32

// TokenSecrets 实现 ports.TokenSecrets：生成无填充 Base64URL 的随机刷新令牌并保存 SHA-256 摘要。
type TokenSecrets struct{}

func NewTokenSecrets() *TokenSecrets { return &TokenSecrets{} }

// NewRefreshToken 返回交给浏览器的原始令牌与入库摘要；摘要针对编码后的字符串计算。
func (TokenSecrets) NewRefreshToken() (string, [32]byte, error) {
	raw := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", [32]byte{}, err
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	return encoded, sha256.Sum256([]byte(encoded)), nil
}

func (TokenSecrets) Digest(raw string) [32]byte { return sha256.Sum256([]byte(raw)) }
