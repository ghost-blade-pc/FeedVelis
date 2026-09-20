package security

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
)

const (
	// MaxAccessTokenBytes 是访问令牌长度上限，超出直接拒绝而不解析。
	MaxAccessTokenBytes = 4096
	// AccessTokenType 是访问令牌的 token_type 声明值。
	AccessTokenType = "access"
	// MinSigningKeyBytes 是 HMAC 密钥解码后的最小长度。
	MinSigningKeyBytes = 32
	kidMaxLength       = 32
)

var (
	ErrInvalidSignerConfig = errors.New("令牌签名配置无效")
	ErrUnknownKeyID        = errors.New("令牌 kid 未知")
	ErrTokenTooLarge       = errors.New("访问令牌超出长度上限")
	ErrInvalidAccessToken  = errors.New("访问令牌无效")
)

// SigningKey 是一把 HS256 密钥。
type SigningKey struct {
	KID string
	Key []byte
}

// JWTSigner 只用主动密钥签名，并允许用主动或上一把密钥校验；算法固定为 HS256。
type JWTSigner struct {
	issuer   string
	audience string
	active   SigningKey
	previous *SigningKey
	leeway   time.Duration
	now      func() time.Time
}

func NewJWTSigner(issuer, audience string, active SigningKey, previous *SigningKey, leeway time.Duration, now func() time.Time) (*JWTSigner, error) {
	if strings.TrimSpace(issuer) == "" || strings.TrimSpace(audience) == "" {
		return nil, fmt.Errorf("%w: issuer 与 audience 不能为空", ErrInvalidSignerConfig)
	}
	if err := validateSigningKey(active); err != nil {
		return nil, err
	}
	if previous != nil {
		if err := validateSigningKey(*previous); err != nil {
			return nil, err
		}
		if previous.KID == active.KID {
			return nil, fmt.Errorf("%w: active 与 previous 的 kid 必须不同", ErrInvalidSignerConfig)
		}
	}
	if leeway < 0 {
		return nil, fmt.Errorf("%w: 时钟容差不能为负", ErrInvalidSignerConfig)
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &JWTSigner{issuer: issuer, audience: audience, active: active, previous: previous, leeway: leeway, now: now}, nil
}

func (s *JWTSigner) Sign(claims ports.AccessClaims) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, accessTokenClaims{
		TokenType: AccessTokenType,
		SessionID: claims.SessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.issuer,
			Subject:   claims.Subject,
			Audience:  jwt.ClaimStrings{s.audience},
			ExpiresAt: jwt.NewNumericDate(claims.ExpiresAt.UTC()),
			NotBefore: jwt.NewNumericDate(claims.NotBefore.UTC()),
			IssuedAt:  jwt.NewNumericDate(claims.IssuedAt.UTC()),
			ID:        claims.TokenID,
		},
	})
	token.Header["kid"] = s.active.KID
	signed, err := token.SignedString(s.active.Key)
	if err != nil {
		return "", err
	}
	if len(signed) > MaxAccessTokenBytes {
		return "", ErrTokenTooLarge
	}
	return signed, nil
}

func (s *JWTSigner) Verify(raw string) (ports.AccessClaims, error) {
	if len(raw) == 0 || len(raw) > MaxAccessTokenBytes {
		return ports.AccessClaims{}, ErrTokenTooLarge
	}
	claims := &accessTokenClaims{}
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(s.issuer),
		jwt.WithAudience(s.audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(s.leeway),
		jwt.WithStrictDecoding(),
		jwt.WithTimeFunc(s.now),
	)
	token, err := parser.ParseWithClaims(raw, claims, s.keyFunc)
	if err != nil || !token.Valid {
		return ports.AccessClaims{}, ErrInvalidAccessToken
	}
	if claims.TokenType != AccessTokenType || claims.Subject == "" || claims.SessionID == "" || claims.ID == "" ||
		claims.NotBefore == nil || claims.IssuedAt == nil || claims.ExpiresAt == nil {
		return ports.AccessClaims{}, ErrInvalidAccessToken
	}
	return ports.AccessClaims{
		Subject:   claims.Subject,
		SessionID: claims.SessionID,
		TokenID:   claims.ID,
		IssuedAt:  claims.IssuedAt.Time.UTC(),
		NotBefore: claims.NotBefore.Time.UTC(),
		ExpiresAt: claims.ExpiresAt.Time.UTC(),
	}, nil
}

// keyFunc 只按 kid 选择密钥；算法已由 WithValidMethods 固定为 HS256。
func (s *JWTSigner) keyFunc(token *jwt.Token) (any, error) {
	if typ, ok := token.Header["typ"].(string); !ok || typ != "JWT" {
		return nil, ErrInvalidAccessToken
	}
	kid, ok := token.Header["kid"].(string)
	if !ok {
		return nil, ErrUnknownKeyID
	}
	switch {
	case kid == s.active.KID:
		return s.active.Key, nil
	case s.previous != nil && kid == s.previous.KID:
		return s.previous.Key, nil
	default:
		return nil, ErrUnknownKeyID
	}
}

type accessTokenClaims struct {
	TokenType string `json:"token_type"`
	SessionID string `json:"sid"`
	jwt.RegisteredClaims
}

func validateSigningKey(key SigningKey) error {
	if !validKeyID(key.KID) {
		return fmt.Errorf("%w: kid 必须是 1～%d 位字母、数字、点、下划线或短横线", ErrInvalidSignerConfig, kidMaxLength)
	}
	if len(key.Key) < MinSigningKeyBytes {
		return fmt.Errorf("%w: 密钥解码后不得少于 %d 字节", ErrInvalidSignerConfig, MinSigningKeyBytes)
	}
	return nil
}

func validKeyID(kid string) bool {
	if len(kid) == 0 || len(kid) > kidMaxLength {
		return false
	}
	for i := 0; i < len(kid); i++ {
		c := kid[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}
