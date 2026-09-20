// Package account 编排注册、登录、会话与账户管理用例。
package account

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	accountDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
)

// Deps 是认证与会话用例的技术依赖与配置；缺少必填项时构造函数直接失败。
type Deps struct {
	Users        accountDomain.Repository
	Sessions     accountDomain.SessionRepository
	Tokens       accountDomain.RefreshTokenRepository
	Throttle     accountDomain.ThrottleRepository
	Hasher       ports.PasswordHasher
	Signer       ports.TokenSigner
	Blocklist    accountDomain.Blocklist
	ThrottleKeys ports.ThrottleKeys
	Secrets      ports.TokenSecrets
	Tx           ports.TxManager
	Clock        ports.Clock

	Policy              accountDomain.ThrottlePolicy
	AccessTTL           time.Duration
	SessionTTL          time.Duration
	RegistrationEnabled bool
}

type Service struct{ deps Deps }

func NewService(deps Deps) (*Service, error) {
	switch {
	case deps.Users == nil, deps.Sessions == nil, deps.Tokens == nil, deps.Throttle == nil,
		deps.Hasher == nil, deps.Signer == nil,
		deps.Blocklist == nil, deps.ThrottleKeys == nil, deps.Secrets == nil,
		deps.Tx == nil, deps.Clock == nil:
		return nil, errors.New("account 用例缺少必要依赖")
	}
	if err := deps.Policy.Validate(); err != nil {
		return nil, err
	}
	if deps.AccessTTL <= 0 || deps.SessionTTL <= 0 {
		return nil, errors.New("account 用例的令牌期限必须为正")
	}
	return &Service{deps: deps}, nil
}

func (s *Service) now() time.Time { return s.deps.Clock.Now().UTC() }

// normalizeLookup 在用户名不合法时仍给出稳定的限流键来源，避免通过畸形输入绕过计数。
func normalizeLookup(rawUsername string) string {
	return strings.ToLower(strings.TrimSpace(rawUsername))
}

func (s *Service) accountKey(rawUsername string) []byte {
	return s.deps.ThrottleKeys.AccountKey(normalizeLookup(rawUsername))
}

// recordFailure 按账号与 IP 两个维度计数；任一维度写入失败都视为依赖故障，不做静默忽略。
func (s *Service) recordFailure(ctx context.Context, rawUsername, clientIP string, at time.Time) error {
	if _, err := s.deps.Throttle.RecordFailure(ctx, accountDomain.DimensionAccount, s.accountKey(rawUsername), at, s.deps.Policy); err != nil {
		return err
	}
	if clientIP == "" {
		return nil
	}
	_, err := s.deps.Throttle.RecordFailure(ctx, accountDomain.DimensionIP, s.deps.ThrottleKeys.IPKey(clientIP), at, s.deps.Policy)
	return err
}

// activeThrottle 返回账号与 IP 维度中仍在生效的最晚限制截止时间，并给出触发该截止时间的维度。
func (s *Service) activeThrottle(ctx context.Context, rawUsername, clientIP string, now time.Time) (time.Time, accountDomain.FailureDimension, error) {
	var (
		latest    time.Time
		dimension accountDomain.FailureDimension
	)
	for _, probe := range []struct {
		dimension accountDomain.FailureDimension
		key       []byte
	}{
		{accountDomain.DimensionAccount, s.accountKey(rawUsername)},
		{accountDomain.DimensionIP, s.deps.ThrottleKeys.IPKey(clientIP)},
	} {
		if probe.dimension == accountDomain.DimensionIP && clientIP == "" {
			continue
		}
		blockedUntil, err := s.deps.Throttle.ActiveBlock(ctx, probe.dimension, probe.key, now)
		if err != nil {
			return time.Time{}, "", err
		}
		if blockedUntil != nil && blockedUntil.After(latest) {
			latest = *blockedUntil
			dimension = probe.dimension
		}
	}
	return latest, dimension, nil
}

func (s *Service) resolveNickname(raw *string, normalizedUsername string) (string, error) {
	if raw == nil {
		return accountDomain.DefaultNickname(normalizedUsername), nil
	}
	return accountDomain.NormalizeNickname(*raw)
}

func (s *Service) newID() (accountDomain.UUID, error) { return accountDomain.NewUUID() }
