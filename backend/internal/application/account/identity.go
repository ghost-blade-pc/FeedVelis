package account

import (
	"context"

	accountDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
)

// Identity 是当前调用者上下文：账户、会话与角色状态全部取自数据库当前值，令牌不携带这些声明。
type Identity struct {
	User    accountDomain.User
	Session accountDomain.Session
	TokenID string
}

// Authenticate 校验访问令牌，并确认会话与账户仍然有效；数据库不可用时返回底层错误。
func (s *Service) Authenticate(ctx context.Context, accessToken string) (Identity, error) {
	if accessToken == "" {
		return Identity{}, ErrUnauthorized
	}
	claims, err := s.deps.Signer.Verify(accessToken)
	if err != nil {
		return Identity{}, accountDomain.ErrSessionInvalid
	}
	sessionID, err := accountDomain.ParseUUID(claims.SessionID)
	if err != nil {
		return Identity{}, accountDomain.ErrSessionInvalid
	}
	userID, err := accountDomain.ParseUUID(claims.Subject)
	if err != nil {
		return Identity{}, accountDomain.ErrSessionInvalid
	}
	session, err := s.deps.Sessions.GetByID(ctx, sessionID)
	if err != nil {
		return Identity{}, err
	}
	if !session.IsActive(s.now()) || session.UserID != userID {
		return Identity{}, accountDomain.ErrSessionInvalid
	}
	user, err := s.deps.Users.GetByID(ctx, userID)
	if err != nil {
		return Identity{}, err
	}
	if !user.IsActive() {
		return Identity{}, accountDomain.ErrSessionInvalid
	}
	return Identity{User: user, Session: session, TokenID: claims.TokenID}, nil
}

// UpdateNickname 只允许修改本人昵称，使用读取时版本做乐观锁。
func (s *Service) UpdateNickname(ctx context.Context, identity Identity, rawNickname string) (accountDomain.User, error) {
	nickname, err := accountDomain.NormalizeNickname(rawNickname)
	if err != nil {
		return accountDomain.User{}, err
	}
	return s.deps.Users.UpdateNickname(ctx, identity.User.ID, nickname, identity.User.Version, s.now())
}
