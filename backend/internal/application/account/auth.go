package account

import (
	"context"
	"errors"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	accountDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
)

type RegisterInput struct {
	Username string
	Password string
	// Nickname 为 nil 表示省略，省略时使用规范化用户名；显式空串按校验失败处理。
	Nickname *string
}

// Register 创建普通用户；成功不建立会话，注册唯一性由用户名唯一约束保证。
func (s *Service) Register(ctx context.Context, input RegisterInput) (accountDomain.User, error) {
	if !s.deps.RegistrationEnabled {
		return accountDomain.User{}, ErrRegistrationDisabled
	}
	username, err := accountDomain.NormalizeUsername(input.Username)
	if err != nil {
		return accountDomain.User{}, err
	}
	nickname, err := s.resolveNickname(input.Nickname, username)
	if err != nil {
		return accountDomain.User{}, err
	}
	if err := accountDomain.ValidatePassword(input.Password, username, s.deps.Blocklist); err != nil {
		return accountDomain.User{}, err
	}
	hash, err := s.deps.Hasher.Hash(input.Password)
	if err != nil {
		return accountDomain.User{}, err
	}
	id, err := s.newID()
	if err != nil {
		return accountDomain.User{}, err
	}
	now := s.now()
	return s.deps.Users.Create(ctx, accountDomain.User{
		ID:           id,
		Username:     username,
		Nickname:     nickname,
		PasswordHash: hash,
		Role:         accountDomain.RoleUser,
		Status:       accountDomain.StatusActive,
		CreatedAt:    now,
	})
}

type LoginInput struct {
	Username string
	Password string
	// ClientIP 是由 HTTP 层按可信代理规则确定的来源地址。
	ClientIP string
}

type LoginResult struct {
	User             accountDomain.User
	Session          accountDomain.Session
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshToken     string
	RefreshExpiresAt time.Time
}

// Login 校验限流与密码后建立独立会话；用户不存在、密码错误与禁用账户走同一路径与同一错误。
func (s *Service) Login(ctx context.Context, input LoginInput) (LoginResult, error) {
	now := s.now()
	blockedUntil, dimension, err := s.activeThrottle(ctx, input.Username, input.ClientIP, now)
	if err != nil {
		return LoginResult{}, err
	}
	if blockedUntil.After(now) {
		return LoginResult{}, &RateLimitedError{RetryAfter: blockedUntil.Sub(now), Dimension: dimension}
	}

	user, err := s.deps.Users.GetByUsername(ctx, normalizeLookup(input.Username))
	switch {
	case errors.Is(err, accountDomain.ErrNotFound):
		return LoginResult{}, s.rejectLogin(ctx, input, now)
	case err != nil:
		return LoginResult{}, err
	}
	matched, err := s.deps.Hasher.Verify(input.Password, user.PasswordHash)
	if err != nil {
		return LoginResult{}, err
	}
	if !matched || !user.IsActive() {
		return LoginResult{}, s.rejectLogin(ctx, input, now)
	}
	return s.startSession(ctx, user, now)
}

// rejectLogin 记录失败并返回统一凭证错误；成功登录不清除既有失败记录。
func (s *Service) rejectLogin(ctx context.Context, input LoginInput, now time.Time) error {
	if err := s.recordFailure(ctx, input.Username, input.ClientIP, now); err != nil {
		return err
	}
	return ErrInvalidCredentials
}

func (s *Service) startSession(ctx context.Context, user accountDomain.User, now time.Time) (LoginResult, error) {
	sessionID, err := s.newID()
	if err != nil {
		return LoginResult{}, err
	}
	session := accountDomain.NewSession(sessionID, user.ID, now, s.deps.SessionTTL)
	rawToken, digest, err := s.deps.Secrets.NewRefreshToken()
	if err != nil {
		return LoginResult{}, err
	}
	tokenID, err := s.newID()
	if err != nil {
		return LoginResult{}, err
	}
	refreshToken, err := session.NewRefreshToken(tokenID, digest, now)
	if err != nil {
		return LoginResult{}, err
	}
	var result LoginResult
	err = s.deps.Tx.WithinTransaction(ctx, func(txCtx context.Context) error {
		if err := s.deps.Sessions.Create(txCtx, session); err != nil {
			return err
		}
		return s.deps.Tokens.Create(txCtx, refreshToken)
	})
	if err != nil {
		return LoginResult{}, err
	}
	accessExpiry, err := session.AccessExpiry(now, s.deps.AccessTTL)
	if err != nil {
		return LoginResult{}, err
	}
	accessToken, err := s.issueAccessToken(user, session, accessExpiry, now)
	if err != nil {
		return LoginResult{}, err
	}
	result = LoginResult{
		User:             user,
		Session:          session,
		AccessToken:      accessToken,
		AccessExpiresAt:  accessExpiry,
		RefreshToken:     rawToken,
		RefreshExpiresAt: session.ExpiresAt,
	}
	return result, nil
}

type RefreshResult struct {
	User             accountDomain.User
	Session          accountDomain.Session
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshToken     string
	RefreshExpiresAt time.Time
}

// Refresh 单次轮换刷新令牌：旧令牌被消费并签发后继令牌，重放由仓储层撤销对应会话。
func (s *Service) Refresh(ctx context.Context, rawRefreshToken string) (RefreshResult, error) {
	if rawRefreshToken == "" {
		return RefreshResult{}, accountDomain.ErrRefreshUnknown
	}
	now := s.now()
	nextID, err := s.newID()
	if err != nil {
		return RefreshResult{}, err
	}
	rawToken, digest, err := s.deps.Secrets.NewRefreshToken()
	if err != nil {
		return RefreshResult{}, err
	}
	session, err := s.deps.Tokens.Rotate(ctx, s.deps.Secrets.Digest(rawRefreshToken),
		accountDomain.RefreshToken{ID: nextID, Digest: digest, IssuedAt: now}, now)
	if err != nil {
		return RefreshResult{}, err
	}
	user, err := s.deps.Users.GetByID(ctx, session.UserID)
	if err != nil {
		return RefreshResult{}, err
	}
	if !user.IsActive() {
		return RefreshResult{}, accountDomain.ErrSessionInvalid
	}
	accessExpiry, err := session.AccessExpiry(now, s.deps.AccessTTL)
	if err != nil {
		return RefreshResult{}, err
	}
	accessToken, err := s.issueAccessToken(user, session, accessExpiry, now)
	if err != nil {
		return RefreshResult{}, err
	}
	return RefreshResult{
		User:             user,
		Session:          session,
		AccessToken:      accessToken,
		AccessExpiresAt:  accessExpiry,
		RefreshToken:     rawToken,
		RefreshExpiresAt: session.ExpiresAt,
	}, nil
}

// Logout 撤销当前会话；已撤销、已过期或未知令牌都幂等成功，不影响其他设备会话。
func (s *Service) Logout(ctx context.Context, sessionID accountDomain.UUID) error {
	return s.deps.Sessions.Revoke(ctx, sessionID, accountDomain.RevokeReasonLogout, s.now())
}

// LogoutByRefreshToken 按刷新 Cookie 撤销对应会话；未知令牌不报错，避免通过退出接口探测令牌有效性。
func (s *Service) LogoutByRefreshToken(ctx context.Context, rawRefreshToken string) error {
	if rawRefreshToken == "" {
		return nil
	}
	token, err := s.deps.Tokens.FindByDigest(ctx, s.deps.Secrets.Digest(rawRefreshToken))
	if errors.Is(err, accountDomain.ErrRefreshUnknown) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.deps.Sessions.Revoke(ctx, token.SessionID, accountDomain.RevokeReasonLogout, s.now())
}

func (s *Service) issueAccessToken(user accountDomain.User, session accountDomain.Session, expiresAt, now time.Time) (string, error) {
	tokenID, err := s.newID()
	if err != nil {
		return "", err
	}
	return s.deps.Signer.Sign(ports.AccessClaims{
		Subject:   user.ID.String(),
		SessionID: session.ID.String(),
		TokenID:   tokenID.String(),
		IssuedAt:  now,
		NotBefore: now,
		ExpiresAt: expiresAt,
	})
}
