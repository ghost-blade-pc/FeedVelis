package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
)

// ThrottleKeys 实现 ports.ThrottleKeys：用配置密钥为账号与 IP 维度派生 HMAC-SHA-256 查找键。
// 两个维度使用不同的消息前缀做域分离，数据库只保存查找键，不保存用户名或 IP。
type ThrottleKeys struct{ secret []byte }

func NewThrottleKeys(secret []byte) (*ThrottleKeys, error) {
	if len(secret) < MinSigningKeyBytes {
		return nil, errors.New("限流密钥解码后不得少于 32 字节")
	}
	return &ThrottleKeys{secret: append([]byte(nil), secret...)}, nil
}

func (k *ThrottleKeys) AccountKey(normalizedUsername string) []byte {
	return k.derive("account", normalizedUsername)
}

func (k *ThrottleKeys) IPKey(rawIP string) []byte { return k.derive("ip", rawIP) }

func (k *ThrottleKeys) derive(dimension, value string) []byte {
	mac := hmac.New(sha256.New, k.secret)
	mac.Write([]byte(dimension))
	mac.Write([]byte{0})
	mac.Write([]byte(value))
	return mac.Sum(nil)
}
