package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	accountDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
)

type AuditRepository struct{ pool *pgxpool.Pool }

func NewAuditRepository(pool *pgxpool.Pool) *AuditRepository { return &AuditRepository{pool: pool} }

// Record 使用调用方事务写入审计；账户变更与审计必须在同一事务提交或一起回滚。
func (r *AuditRepository) Record(ctx context.Context, entry accountDomain.AuditLog) error {
	_, err := querier(ctx, r.pool).Exec(ctx, `INSERT INTO velis.account_audit_logs
(action, target_user_id, target_username, from_role, to_role, from_status, to_status, source, operation_id, occurred_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::uuid, $10)`,
		entry.Action, pgUUID(entry.TargetUserID), entry.TargetUsername,
		optionalRole(entry.FromRole), optionalRole(entry.ToRole),
		optionalStatus(entry.FromStatus), optionalStatus(entry.ToStatus),
		entry.Source, entry.OperationID, entry.OccurredAt)
	return err
}

func optionalRole(role *accountDomain.Role) any {
	if role == nil {
		return nil
	}
	return string(*role)
}

func optionalStatus(status *accountDomain.Status) any {
	if status == nil {
		return nil
	}
	return string(*status)
}
