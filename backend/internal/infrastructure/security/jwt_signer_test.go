package security

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
)

var (
	testIssuer   = "velis-api"
	testAudience = "velis-web"
)

func testKey(kid string, fill byte) SigningKey {
	key := make([]byte, 32)
	for i := range key {
		key[i] = fill
	}
	return SigningKey{KID: kid, Key: key}
}

func testClaims(now time.Time) ports.AccessClaims {
	return ports.AccessClaims{
		Subject:   "3f2504e0-4f89-41d3-9a0c-0305e82c3301",
		SessionID: "9c8b7a6d-5e4f-4321-8abc-0123456789ab",
		TokenID:   "11111111-2222-4333-8444-555555555555",
		IssuedAt:  now,
		NotBefore: now,
		ExpiresAt: now.Add(15 * time.Minute),
	}
}

func newTestSigner(t *testing.T, active SigningKey, previous *SigningKey, now time.Time) *JWTSigner {
	t.Helper()
	signer, err := NewJWTSigner(testIssuer, testAudience, active, previous, 30*time.Second, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func TestJWTSignerRoundTrip(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	signer := newTestSigner(t, testKey("k1", 1), nil, now)
	claims := testClaims(now)
	signed, err := signer.Sign(claims)
	if err != nil {
		t.Fatal(err)
	}
	header, _, err := jwt.NewParser().ParseUnverified(signed, jwt.MapClaims{})
	if err != nil {
		t.Fatal(err)
	}
	if header.Header["alg"] != "HS256" || header.Header["typ"] != "JWT" || header.Header["kid"] != "k1" {
		t.Fatalf("表头 = %+v", header.Header)
	}
	verified, err := signer.Verify(signed)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Subject != claims.Subject || verified.SessionID != claims.SessionID || verified.TokenID != claims.TokenID {
		t.Fatalf("声明 = %+v", verified)
	}
	if !verified.ExpiresAt.Equal(claims.ExpiresAt) {
		t.Fatalf("到期时间 = %v", verified.ExpiresAt)
	}
}

func TestJWTSignerAcceptsPreviousKeyDuringRotation(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	oldSigner := newTestSigner(t, testKey("k1", 1), nil, now)
	signed, err := oldSigner.Sign(testClaims(now))
	if err != nil {
		t.Fatal(err)
	}
	previous := testKey("k1", 1)
	rotated := newTestSigner(t, testKey("k2", 2), &previous, now)
	if _, err := rotated.Verify(signed); err != nil {
		t.Fatalf("上一把密钥签发的令牌仍应可校验: %v", err)
	}
	newToken, err := rotated.Sign(testClaims(now))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oldSigner.Verify(newToken); err == nil {
		t.Fatal("移除旧密钥后不得接受新密钥签发的令牌")
	}
}

func TestJWTSignerRejectsInvalidTokens(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	active := testKey("k1", 1)
	signer := newTestSigner(t, active, nil, now)
	valid, err := signer.Sign(testClaims(now))
	if err != nil {
		t.Fatal(err)
	}

	craft := func(header map[string]any, claims jwt.MapClaims, method jwt.SigningMethod, key any) string {
		t.Helper()
		token := jwt.NewWithClaims(method, claims)
		for name, value := range header {
			token.Header[name] = value
		}
		signed, err := token.SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return signed
	}
	base := func() jwt.MapClaims {
		return jwt.MapClaims{
			"iss": testIssuer, "aud": testAudience, "sub": "user", "sid": "session", "jti": "token",
			"iat": now.Unix(), "nbf": now.Unix(), "exp": now.Add(15 * time.Minute).Unix(), "token_type": "access",
		}
	}

	cases := map[string]string{
		"未知 kid": craft(map[string]any{"kid": "k9"}, base(), jwt.SigningMethodHS256, active.Key),
		"缺少 typ": craft(map[string]any{"kid": "k1", "typ": nil}, base(), jwt.SigningMethodHS256, active.Key),
		"缺少 sid": craft(map[string]any{"kid": "k1"}, func() jwt.MapClaims {
			claims := base()
			delete(claims, "sid")
			return claims
		}(), jwt.SigningMethodHS256, active.Key),
		"错误 token_type": craft(map[string]any{"kid": "k1"}, func() jwt.MapClaims {
			claims := base()
			claims["token_type"] = "refresh"
			return claims
		}(), jwt.SigningMethodHS256, active.Key),
		"错误 issuer": craft(map[string]any{"kid": "k1"}, func() jwt.MapClaims {
			claims := base()
			claims["iss"] = "other"
			return claims
		}(), jwt.SigningMethodHS256, active.Key),
		"错误 audience": craft(map[string]any{"kid": "k1"}, func() jwt.MapClaims {
			claims := base()
			claims["aud"] = "other"
			return claims
		}(), jwt.SigningMethodHS256, active.Key),
		"已过期": craft(map[string]any{"kid": "k1"}, func() jwt.MapClaims {
			claims := base()
			claims["exp"] = now.Add(-2 * time.Minute).Unix()
			claims["iat"] = now.Add(-10 * time.Minute).Unix()
			claims["nbf"] = now.Add(-10 * time.Minute).Unix()
			return claims
		}(), jwt.SigningMethodHS256, active.Key),
		"签发时间在未来": craft(map[string]any{"kid": "k1"}, func() jwt.MapClaims {
			claims := base()
			claims["iat"] = now.Add(time.Hour).Unix()
			claims["nbf"] = now.Add(time.Hour).Unix()
			claims["exp"] = now.Add(2 * time.Hour).Unix()
			return claims
		}(), jwt.SigningMethodHS256, active.Key),
		"算法替换为 none":    craft(map[string]any{"kid": "k1"}, base(), jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType),
		"算法替换为 HS384":   craft(map[string]any{"kid": "k1"}, base(), jwt.SigningMethodHS384, active.Key),
		"签名被篡改":         valid[:len(valid)-2] + "ab",
		"非严格 Base64URL": valid[:len(valid)-1] + "=",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := signer.Verify(raw); err == nil {
				t.Fatalf("%s 必须被拒绝", name)
			}
		})
	}
	if _, err := signer.Verify(valid); err != nil {
		t.Fatalf("基准令牌应有效: %v", err)
	}
	if _, err := signer.Verify(strings.Repeat("a", MaxAccessTokenBytes+1)); !errors.Is(err, ErrTokenTooLarge) {
		t.Fatalf("超长令牌应被拒绝: %v", err)
	}
}

func TestJWTSignerRequiresConfiguredKeys(t *testing.T) {
	if _, err := NewJWTSigner("", testAudience, testKey("k1", 1), nil, 0, nil); !errors.Is(err, ErrInvalidSignerConfig) {
		t.Fatalf("空 issuer 必须被拒绝: %v", err)
	}
	if _, err := NewJWTSigner(testIssuer, testAudience, SigningKey{KID: "k1", Key: make([]byte, 16)}, nil, 0, nil); !errors.Is(err, ErrInvalidSignerConfig) {
		t.Fatalf("过短密钥必须被拒绝: %v", err)
	}
	if _, err := NewJWTSigner(testIssuer, testAudience, SigningKey{KID: "bad kid", Key: make([]byte, 32)}, nil, 0, nil); !errors.Is(err, ErrInvalidSignerConfig) {
		t.Fatalf("非法 kid 必须被拒绝: %v", err)
	}
	previous := testKey("k1", 1)
	if _, err := NewJWTSigner(testIssuer, testAudience, testKey("k1", 2), &previous, 0, nil); !errors.Is(err, ErrInvalidSignerConfig) {
		t.Fatalf("重复 kid 必须被拒绝: %v", err)
	}
}
