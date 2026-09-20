package account

import (
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	MinUsernameBytes = 3
	MaxUsernameBytes = 32
	MaxNicknameRunes = 32
)

type Role string

const (
	RoleUser  Role = "user"
	RoleAdmin Role = "admin"
)

func (r Role) Valid() bool { return r == RoleUser || r == RoleAdmin }

type Status string

const (
	StatusActive   Status = "active"
	StatusDisabled Status = "disabled"
)

func (s Status) Valid() bool { return s == StatusActive || s == StatusDisabled }

var (
	ErrInvalidUsername = errors.New("用户名无效")
	ErrInvalidNickname = errors.New("昵称无效")
	ErrInvalidRole     = errors.New("角色无效")
	ErrInvalidStatus   = errors.New("账户状态无效")
	ErrNotFound        = errors.New("账户不存在")
	ErrUsernameTaken   = errors.New("用户名已存在")
	ErrVersionConflict = errors.New("账户已被其他操作修改")
	ErrLastAdmin       = errors.New("不能禁用或降级最后一位有效管理员")
)

// User 是账户聚合；Username 始终保存规范化形式，PasswordHash 保存 PHC 字符串。
type User struct {
	ID           UUID
	Username     string
	Nickname     string
	PasswordHash string
	Role         Role
	Status       Status
	Version      int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (u User) IsAdmin() bool { return u.Role == RoleAdmin }

func (u User) IsActive() bool { return u.Status == StatusActive }

// NormalizeUsername 校验用户名并返回规范化（ASCII 小写）形式。
func NormalizeUsername(raw string) (string, error) {
	if raw != strings.TrimSpace(raw) || len(raw) < MinUsernameBytes || len(raw) > MaxUsernameBytes {
		return "", ErrInvalidUsername
	}
	normalized := make([]byte, len(raw))
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case c >= 'A' && c <= 'Z':
			normalized[i] = c + ('a' - 'A')
		case c >= 'a' && c <= 'z':
			normalized[i] = c
		case (c >= '0' && c <= '9' || c == '_') && i > 0:
			normalized[i] = c
		default:
			return "", ErrInvalidUsername
		}
	}
	return string(normalized), nil
}

// NormalizeNickname 去除 Unicode 首尾空白后校验昵称，拒绝空值、换行与控制字符。
func NormalizeNickname(raw string) (string, error) {
	if !utf8.ValidString(raw) {
		return "", ErrInvalidNickname
	}
	value := strings.TrimSpace(raw)
	if value == "" || utf8.RuneCountInString(value) > MaxNicknameRunes {
		return "", ErrInvalidNickname
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.In(r, unicode.Zl, unicode.Zp) {
			return "", ErrInvalidNickname
		}
	}
	return value, nil
}

// DefaultNickname 在注册省略昵称时使用规范化用户名。
func DefaultNickname(normalizedUsername string) string { return normalizedUsername }

// CheckLastAdmin 在禁用或降级管理员前校验；remaining 是不含目标账户的有效管理员数量。
func (u User) CheckLastAdmin(remaining int) error {
	if u.Role == RoleAdmin && u.IsActive() && remaining < 1 {
		return ErrLastAdmin
	}
	return nil
}
