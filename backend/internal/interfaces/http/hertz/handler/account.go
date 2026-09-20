package handler

import (
	"context"
	"log/slog"
	"net"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	accountApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/account"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	accountDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/dto"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/middleware"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/presenter"
)

const (
	maxAuthBodyBytes     = 4 << 10
	maxNicknameBodyBytes = 1 << 10
)

// AccountService 是账户用例在本层的消费方接口。
type AccountService interface {
	Register(context.Context, accountApp.RegisterInput) (accountDomain.User, error)
	Login(context.Context, accountApp.LoginInput) (accountApp.LoginResult, error)
	Refresh(context.Context, string) (accountApp.RefreshResult, error)
	LogoutByRefreshToken(context.Context, string) error
	Authenticate(context.Context, string) (accountApp.Identity, error)
	UpdateNickname(context.Context, accountApp.Identity, string) (accountDomain.User, error)
}

// AccountPolicy 是认证端点需要的运行时策略。
type AccountPolicy struct {
	Cookies        CookiePolicy
	TrustedProxies []*net.IPNet
	Clock          ports.Clock
	Logger         *slog.Logger
}

type Account struct {
	service AccountService
	policy  AccountPolicy
}

func NewAccount(service AccountService, policy AccountPolicy) *Account {
	if policy.Clock == nil {
		policy.Clock = systemClock{}
	}
	return &Account{service: service, policy: policy}
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

func (h *Account) Register(ctx context.Context, c *app.RequestContext) {
	var request dto.RegisterRequest
	if !decodeBody(c, maxAuthBodyBytes, &request) {
		return
	}
	user, err := h.service.Register(ctx, accountApp.RegisterInput{
		Username: request.Username, Password: request.Password, Nickname: request.Nickname,
	})
	if err != nil {
		h.reject(ctx, c, "register", err)
		return
	}
	h.log(ctx, c, "register", "success", user.ID, accountDomain.UUID{})
	c.JSON(consts.StatusCreated, toAccountResponse(user))
}

func (h *Account) Login(ctx context.Context, c *app.RequestContext) {
	var request dto.LoginRequest
	if !decodeBody(c, maxAuthBodyBytes, &request) {
		return
	}
	result, err := h.service.Login(ctx, accountApp.LoginInput{
		Username: request.Username,
		Password: request.Password,
		ClientIP: middleware.ClientIP(c, h.policy.TrustedProxies),
	})
	if err != nil {
		h.reject(ctx, c, "login", err)
		return
	}
	h.establishSession(c, result.User, result.Session, result.AccessToken, result.AccessExpiresAt, result.RefreshToken)
	h.log(ctx, c, "login", "success", result.User.ID, result.Session.ID)
}

func (h *Account) Refresh(ctx context.Context, c *app.RequestContext) {
	result, err := h.service.Refresh(ctx, string(c.Cookie(h.policy.Cookies.RefreshName)))
	if err != nil {
		// 刷新失败（含重放导致的会话撤销）必须清除 Cookie，前端据此引导重新登录。
		h.policy.Cookies.ClearSessionCookies(c)
		h.reject(ctx, c, "refresh", err)
		return
	}
	h.establishSession(c, result.User, result.Session, result.AccessToken, result.AccessExpiresAt, result.RefreshToken)
	h.log(ctx, c, "refresh", "success", result.Session.UserID, result.Session.ID)
}

func (h *Account) Logout(ctx context.Context, c *app.RequestContext) {
	raw := string(c.Cookie(h.policy.Cookies.RefreshName))
	if err := h.service.LogoutByRefreshToken(ctx, raw); err != nil {
		h.reject(ctx, c, "logout", err)
		return
	}
	h.policy.Cookies.ClearSessionCookies(c)
	h.log(ctx, c, "logout", "success", accountDomain.UUID{}, accountDomain.UUID{})
	c.Status(consts.StatusNoContent)
}

func (h *Account) GetMe(ctx context.Context, c *app.RequestContext) {
	identity, ok := middleware.IdentityFrom(c)
	if !ok {
		presenter.WriteError(c, consts.StatusUnauthorized, presenter.CodeSessionInvalid, "登录状态无效，请重新登录", middleware.RequestIDFrom(c))
		return
	}
	h.log(ctx, c, "account_me", "success", identity.User.ID, identity.Session.ID)
	c.JSON(consts.StatusOK, toAccountResponse(identity.User))
}

func (h *Account) UpdateMe(ctx context.Context, c *app.RequestContext) {
	var request dto.UpdateNicknameRequest
	if !decodeBody(c, maxNicknameBodyBytes, &request) {
		return
	}
	if request.Nickname == nil {
		presenter.WriteError(c, consts.StatusBadRequest, presenter.CodeValidationFailed, "昵称不能为空", middleware.RequestIDFrom(c))
		return
	}
	identity, ok := middleware.IdentityFrom(c)
	if !ok {
		presenter.WriteError(c, consts.StatusUnauthorized, presenter.CodeSessionInvalid, "登录状态无效，请重新登录", middleware.RequestIDFrom(c))
		return
	}
	user, err := h.service.UpdateNickname(ctx, identity, *request.Nickname)
	if err != nil {
		h.reject(ctx, c, "account_update", err)
		return
	}
	h.log(ctx, c, "account_update", "success", user.ID, identity.Session.ID)
	c.JSON(consts.StatusOK, toAccountResponse(user))
}

// establishSession 写入 Cookie 并返回访问令牌与账户信息；刷新令牌不进入响应正文。
func (h *Account) establishSession(c *app.RequestContext, user accountDomain.User, session accountDomain.Session,
	accessToken string, accessExpiresAt time.Time, refreshToken string) {
	csrfValue, err := NewCSRFValue()
	if err != nil {
		presenter.WriteMapping(c, presenter.MapAuthError(err), middleware.RequestIDFrom(c))
		return
	}
	h.policy.Cookies.SetSessionCookies(c, refreshToken, csrfValue, session.RemainingTTL(h.policy.Clock.Now()))
	c.JSON(consts.StatusOK, dto.SessionResponse{
		AccessToken: accessToken,
		ExpiresAt:   accessExpiresAt.UTC(),
		Account:     toAccountResponse(user),
	})
}

func (h *Account) reject(ctx context.Context, c *app.RequestContext, operation string, err error) {
	mapping := presenter.MapAuthError(err)
	attributes := append([]any{"code", mapping.Code}, logAttributes(err)...)
	h.log(ctx, c, operation+"_failure", "failure", accountDomain.UUID{}, accountDomain.UUID{}, attributes...)
	presenter.WriteMapping(c, mapping, middleware.RequestIDFrom(c))
}

// log 只记录操作、结果、request ID 与确认身份后的用户/会话 ID，不记录用户名、IP、令牌或摘要。
func (h *Account) log(ctx context.Context, c *app.RequestContext, operation, result string,
	userID, sessionID accountDomain.UUID, extra ...any) {
	if h.policy.Logger == nil {
		return
	}
	attributes := []any{
		"operation", operation,
		"result", result,
		"request_id", middleware.RequestIDFrom(c),
	}
	if !userID.IsZero() {
		attributes = append(attributes, "user_id", userID.String())
	}
	if !sessionID.IsZero() {
		attributes = append(attributes, "session_id", sessionID.String())
	}
	attributes = append(attributes, extra...)
	h.policy.Logger.InfoContext(ctx, "auth request", attributes...)
}

func decodeBody(c *app.RequestContext, maxBytes int, target any) bool {
	if err := presenter.DecodeStrictJSON(c.Request.Body(), maxBytes, target); err != nil {
		mapping := presenter.MapAuthError(err)
		presenter.WriteMapping(c, mapping, middleware.RequestIDFrom(c))
		return false
	}
	return true
}

func toAccountResponse(user accountDomain.User) dto.AccountResponse {
	return dto.AccountResponse{
		ID: user.ID.String(), Username: user.Username, Nickname: user.Nickname,
		Role: string(user.Role), Status: string(user.Status), CreatedAt: user.CreatedAt.UTC(),
	}
}
