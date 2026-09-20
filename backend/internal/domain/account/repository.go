package account

import (
	"context"
	"time"
)

// Repository 是账户事实源。Username 参数始终是规范化用户名。
type Repository interface {
	Create(context.Context, User) (User, error)
	GetByUsername(context.Context, string) (User, error)
	GetByID(context.Context, UUID) (User, error)
	UpdateNickname(context.Context, UUID, string, int64, time.Time) (User, error)
	SetRole(context.Context, UUID, Role, time.Time) (User, error)
	SetStatus(context.Context, UUID, Status, time.Time) (User, error)
	CountActiveAdmins(context.Context) (int, error)
}

// SessionRepository 维护登录会话；撤销必须幂等。
type SessionRepository interface {
	Create(context.Context, Session) error
	GetByID(context.Context, UUID) (Session, error)
	Revoke(context.Context, UUID, RevokeReason, time.Time) error
	RevokeAllForUser(context.Context, UUID, RevokeReason, time.Time) (int64, error)
}

// RefreshTokenRepository 负责刷新轮换。
// Rotate 必须在同一事务内锁会话、消费旧令牌并插入后继令牌；已消费令牌返回 ErrRefreshReplay
// 并撤销对应会话，未知摘要返回 ErrRefreshUnknown 且不影响其他会话。
type RefreshTokenRepository interface {
	Create(context.Context, RefreshToken) error
	// FindByDigest 按摘要定位令牌，供退出等不需要轮换的用例使用；未知摘要返回 ErrRefreshUnknown。
	FindByDigest(context.Context, [32]byte) (RefreshToken, error)
	Rotate(context.Context, [32]byte, RefreshToken, time.Time) (Session, error)
}

// ThrottleRepository 保存登录失败计数与限制状态，跨实例共享。
// key 是维度对应的 HMAC-SHA-256 查找键，不得保存原始用户名或 IP。
type ThrottleRepository interface {
	ActiveBlock(context.Context, FailureDimension, []byte, time.Time) (*time.Time, error)
	RecordFailure(context.Context, FailureDimension, []byte, time.Time, ThrottlePolicy) (*time.Time, error)
}

// AuditLog 是一次成功管理操作的审计记录，与账户变更同事务提交。
type AuditLog struct {
	ID             int64
	Action         string
	TargetUserID   UUID
	TargetUsername string
	FromRole       *Role
	ToRole         *Role
	FromStatus     *Status
	ToStatus       *Status
	Source         string
	OperationID    string
	OccurredAt     time.Time
}

type AuditRepository interface {
	Record(context.Context, AuditLog) error
}

// CleanupRepository 由 Worker 分批清理保留期外的会话、令牌与限流状态；用户与审计不参与清理。
type CleanupRepository interface {
	DeleteExpiredSessions(context.Context, time.Time, int) (int64, error)
	DeleteExpiredRefreshTokens(context.Context, time.Time, int) (int64, error)
	DeleteStaleFailures(context.Context, time.Time, int) (int64, error)
	DeleteExpiredBlocks(context.Context, time.Time, int) (int64, error)
}
