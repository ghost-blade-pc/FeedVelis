package account

import (
	"context"
	"errors"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	accountDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
)

const auditSourceCLI = "local_cli"

// AdminDeps 是本地管理用例的依赖。发布顺序要求 CLI 在注入 JWT、限流与来源配置之前可用，
// 因此这里不包含签名、限流与令牌随机数等仅认证运行时需要的端口。
type AdminDeps struct {
	Users     accountDomain.Repository
	Sessions  accountDomain.SessionRepository
	Audits    accountDomain.AuditRepository
	Locks     ports.AccountLocks
	Hasher    ports.PasswordHasher
	Blocklist accountDomain.Blocklist
	Tx        ports.TxManager
	Clock     ports.Clock
}

// AdminService 承载本地运维 CLI 的账户管理用例。
type AdminService struct{ deps AdminDeps }

func NewAdminService(deps AdminDeps) (*AdminService, error) {
	switch {
	case deps.Users == nil, deps.Sessions == nil, deps.Audits == nil, deps.Locks == nil,
		deps.Hasher == nil, deps.Blocklist == nil, deps.Tx == nil, deps.Clock == nil:
		return nil, errors.New("account 管理用例缺少必要依赖")
	}
	return &AdminService{deps: deps}, nil
}

func (s *AdminService) now() time.Time { return s.deps.Clock.Now().UTC() }

type InitAdminInput struct {
	Username    string
	Password    string
	OperationID string
}

// InitAdmin 仅在不存在有效管理员时创建管理员；同名账户不会被覆盖或提权。
func (s *AdminService) InitAdmin(ctx context.Context, input InitAdminInput) (accountDomain.User, error) {
	username, err := accountDomain.NormalizeUsername(input.Username)
	if err != nil {
		return accountDomain.User{}, err
	}
	nickname := accountDomain.DefaultNickname(username)
	if err := accountDomain.ValidatePassword(input.Password, username, s.deps.Blocklist); err != nil {
		return accountDomain.User{}, err
	}
	// 散列在事务外完成，避免昂贵计算占住全局管理员锁。
	hash, err := s.deps.Hasher.Hash(input.Password)
	if err != nil {
		return accountDomain.User{}, err
	}
	id, err := accountDomain.NewUUID()
	if err != nil {
		return accountDomain.User{}, err
	}
	now := s.now()
	role := accountDomain.RoleAdmin
	status := accountDomain.StatusActive
	var created accountDomain.User
	err = s.deps.Tx.WithinTransaction(ctx, func(ctx context.Context) error {
		if err := s.deps.Locks.LockAdminScope(ctx); err != nil {
			return err
		}
		count, err := s.deps.Users.CountActiveAdmins(ctx)
		if err != nil {
			return err
		}
		if count > 0 {
			return ErrAdminAlreadyExists
		}
		user, err := s.deps.Users.Create(ctx, accountDomain.User{
			ID:           id,
			Username:     username,
			Nickname:     nickname,
			PasswordHash: hash,
			Role:         accountDomain.RoleAdmin,
			Status:       accountDomain.StatusActive,
			CreatedAt:    now,
		})
		if err != nil {
			return err
		}
		if err := s.deps.Audits.Record(ctx, accountDomain.AuditLog{
			Action:         "init_admin",
			TargetUserID:   user.ID,
			TargetUsername: user.Username,
			ToRole:         &role,
			ToStatus:       &status,
			Source:         auditSourceCLI,
			OperationID:    input.OperationID,
			OccurredAt:     now,
		}); err != nil {
			return err
		}
		created = user
		return nil
	})
	return created, err
}

type SetRoleInput struct {
	Username    string
	Role        accountDomain.Role
	OperationID string
}

// SetRole 变更角色：值相同时幂等成功且不撤销会话、不新增审计；实际变更时撤销全部会话并写审计。
func (s *AdminService) SetRole(ctx context.Context, input SetRoleInput) (accountDomain.User, bool, error) {
	if !input.Role.Valid() {
		return accountDomain.User{}, false, accountDomain.ErrInvalidRole
	}
	now := s.now()
	var (
		target  accountDomain.User
		changed bool
	)
	err := s.deps.Tx.WithinTransaction(ctx, func(ctx context.Context) error {
		locked, err := s.lockTarget(ctx, input.Username)
		if err != nil {
			return err
		}
		target = locked
		if locked.Role == input.Role {
			return nil
		}
		if err := s.ensureAdminRemains(ctx, locked); err != nil {
			return err
		}
		updated, err := s.deps.Users.SetRole(ctx, locked.ID, input.Role, now)
		if err != nil {
			return err
		}
		if _, err := s.deps.Sessions.RevokeAllForUser(ctx, locked.ID, accountDomain.RevokeReasonRoleChanged, now); err != nil {
			return err
		}
		from := locked.Role
		if err := s.deps.Audits.Record(ctx, accountDomain.AuditLog{
			Action:         "set_role",
			TargetUserID:   locked.ID,
			TargetUsername: locked.Username,
			FromRole:       &from,
			ToRole:         &input.Role,
			Source:         auditSourceCLI,
			OperationID:    input.OperationID,
			OccurredAt:     now,
		}); err != nil {
			return err
		}
		target, changed = updated, true
		return nil
	})
	return target, changed, err
}

type SetStatusInput struct {
	Username    string
	Status      accountDomain.Status
	OperationID string
}

// SetStatus 变更启用状态：禁用时撤销全部会话，重新启用不恢复旧会话。
func (s *AdminService) SetStatus(ctx context.Context, input SetStatusInput) (accountDomain.User, bool, error) {
	if !input.Status.Valid() {
		return accountDomain.User{}, false, accountDomain.ErrInvalidStatus
	}
	now := s.now()
	var (
		target  accountDomain.User
		changed bool
	)
	err := s.deps.Tx.WithinTransaction(ctx, func(ctx context.Context) error {
		locked, err := s.lockTarget(ctx, input.Username)
		if err != nil {
			return err
		}
		target = locked
		if locked.Status == input.Status {
			return nil
		}
		if err := s.ensureAdminRemains(ctx, locked); err != nil {
			return err
		}
		updated, err := s.deps.Users.SetStatus(ctx, locked.ID, input.Status, now)
		if err != nil {
			return err
		}
		if input.Status == accountDomain.StatusDisabled {
			if _, err := s.deps.Sessions.RevokeAllForUser(ctx, locked.ID, accountDomain.RevokeReasonDisabled, now); err != nil {
				return err
			}
		}
		from := locked.Status
		if err := s.deps.Audits.Record(ctx, accountDomain.AuditLog{
			Action:         "set_status",
			TargetUserID:   locked.ID,
			TargetUsername: locked.Username,
			FromStatus:     &from,
			ToStatus:       &input.Status,
			Source:         auditSourceCLI,
			OperationID:    input.OperationID,
			OccurredAt:     now,
		}); err != nil {
			return err
		}
		target, changed = updated, true
		return nil
	})
	return target, changed, err
}

// lockTarget 按固定锁顺序取全局管理员锁与目标用户行锁，必须在事务内调用。
func (s *AdminService) lockTarget(ctx context.Context, rawUsername string) (accountDomain.User, error) {
	username, err := accountDomain.NormalizeUsername(rawUsername)
	if err != nil {
		return accountDomain.User{}, err
	}
	if err := s.deps.Locks.LockAdminScope(ctx); err != nil {
		return accountDomain.User{}, err
	}
	locked, err := s.deps.Users.GetByUsername(ctx, username)
	if err != nil {
		return accountDomain.User{}, err
	}
	return s.deps.Locks.LockUser(ctx, locked.ID)
}

// ensureAdminRemains 在会移除有效管理员的变更之前校验剩余管理员数量。
func (s *AdminService) ensureAdminRemains(ctx context.Context, target accountDomain.User) error {
	if !target.IsAdmin() || !target.IsActive() {
		return nil
	}
	count, err := s.deps.Users.CountActiveAdmins(ctx)
	if err != nil {
		return err
	}
	return target.CheckLastAdmin(count - 1)
}
