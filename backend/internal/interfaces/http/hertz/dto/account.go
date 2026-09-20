package dto

import "time"

type RegisterRequest struct {
	Username string  `json:"username"`
	Password string  `json:"password"`
	Nickname *string `json:"nickname"`
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// UpdateNicknameRequest 只允许昵称字段，未知字段由严格解码拒绝。
type UpdateNicknameRequest struct {
	Nickname *string `json:"nickname"`
}

type AccountResponse struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Nickname  string    `json:"nickname"`
	Role      string    `json:"role"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// SessionResponse 是登录与刷新返回的访问令牌信息；刷新令牌只通过 HttpOnly Cookie 传递。
type SessionResponse struct {
	AccessToken string          `json:"access_token"`
	ExpiresAt   time.Time       `json:"expires_at"`
	Account     AccountResponse `json:"account"`
}
