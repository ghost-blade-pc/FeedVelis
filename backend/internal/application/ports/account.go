package ports

import (
	"context"

	accountDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
)

// AccountLocks 提供账户管理用例的事务级锁；实现必须在调用方事务内生效。
// 锁顺序固定为：全局管理员锁 → 目标用户行 → 会话行 → 审计插入。
type AccountLocks interface {
	LockAdminScope(context.Context) error
	LockUser(context.Context, accountDomain.UUID) (accountDomain.User, error)
}
