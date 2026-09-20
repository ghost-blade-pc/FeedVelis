package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	accountDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
)

type AccountRepository struct{ pool *pgxpool.Pool }

func NewAccountRepository(pool *pgxpool.Pool) *AccountRepository {
	return &AccountRepository{pool: pool}
}

const userColumns = `id, username, nickname, password_hash, role, status, version, created_at, updated_at`

func (r *AccountRepository) Create(ctx context.Context, user accountDomain.User) (accountDomain.User, error) {
	created, err := scanUser(querier(ctx, r.pool).QueryRow(ctx, `INSERT INTO velis.users
(id, username, nickname, password_hash, role, status, version, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, 1, $7, $7)
RETURNING `+userColumns,
		pgUUID(user.ID), user.Username, user.Nickname, user.PasswordHash, user.Role, user.Status, user.CreatedAt))
	if err != nil {
		if isUniqueViolation(err) {
			return accountDomain.User{}, accountDomain.ErrUsernameTaken
		}
		return accountDomain.User{}, err
	}
	return created, nil
}

func (r *AccountRepository) GetByUsername(ctx context.Context, username string) (accountDomain.User, error) {
	return r.get(ctx, `username = $1`, username)
}

func (r *AccountRepository) GetByID(ctx context.Context, id accountDomain.UUID) (accountDomain.User, error) {
	return r.get(ctx, `id = $1`, pgUUID(id))
}

func (r *AccountRepository) UpdateNickname(ctx context.Context, id accountDomain.UUID, nickname string, expectedVersion int64, now time.Time) (accountDomain.User, error) {
	updated, err := scanUser(querier(ctx, r.pool).QueryRow(ctx, `UPDATE velis.users
SET nickname = $2, version = version + 1, updated_at = $4
WHERE id = $1 AND version = $3
RETURNING `+userColumns, pgUUID(id), nickname, expectedVersion, now))
	if err == nil {
		return updated, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return accountDomain.User{}, err
	}
	if _, getErr := r.GetByID(ctx, id); getErr != nil {
		return accountDomain.User{}, getErr
	}
	return accountDomain.User{}, accountDomain.ErrVersionConflict
}

func (r *AccountRepository) SetRole(ctx context.Context, id accountDomain.UUID, role accountDomain.Role, now time.Time) (accountDomain.User, error) {
	return r.updateField(ctx, `role = $2, version = version + 1, updated_at = $3`, id, role, now)
}

func (r *AccountRepository) SetStatus(ctx context.Context, id accountDomain.UUID, status accountDomain.Status, now time.Time) (accountDomain.User, error) {
	return r.updateField(ctx, `status = $2, version = version + 1, updated_at = $3`, id, status, now)
}

func (r *AccountRepository) CountActiveAdmins(ctx context.Context) (int, error) {
	var count int
	err := querier(ctx, r.pool).QueryRow(ctx, `SELECT count(*) FROM velis.users WHERE role = 'admin' AND status = 'active'`).Scan(&count)
	return count, err
}

func (r *AccountRepository) get(ctx context.Context, condition string, arg any) (accountDomain.User, error) {
	user, err := scanUser(querier(ctx, r.pool).QueryRow(ctx, `SELECT `+userColumns+` FROM velis.users WHERE `+condition, arg))
	if errors.Is(err, pgx.ErrNoRows) {
		return accountDomain.User{}, accountDomain.ErrNotFound
	}
	return user, err
}

func (r *AccountRepository) updateField(ctx context.Context, assignment string, id accountDomain.UUID, value any, now time.Time) (accountDomain.User, error) {
	updated, err := scanUser(querier(ctx, r.pool).QueryRow(ctx,
		`UPDATE velis.users SET `+assignment+` WHERE id = $1 RETURNING `+userColumns, pgUUID(id), value, now))
	if errors.Is(err, pgx.ErrNoRows) {
		return accountDomain.User{}, accountDomain.ErrNotFound
	}
	return updated, err
}

// AccountTxLocks 提供 CLI 管理操作的事务级锁，必须在事务内调用。
type AccountTxLocks struct{}

func NewAccountTxLocks() *AccountTxLocks { return &AccountTxLocks{} }

// adminScopeAdvisoryLock 是全局管理员锁的键；键值固定，避免与其它模块的 advisory lock 冲突。
const adminScopeAdvisoryLock int64 = 0x56454c4953000001

func (l *AccountTxLocks) LockAdminScope(ctx context.Context) error {
	tx, ok := transactionFromContext(ctx)
	if !ok {
		return errors.New("管理员锁必须在事务内获取")
	}
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, adminScopeAdvisoryLock)
	return err
}

// LockUser 按锁顺序在会话行之前锁定目标用户行。
func (l *AccountTxLocks) LockUser(ctx context.Context, id accountDomain.UUID) (accountDomain.User, error) {
	tx, ok := transactionFromContext(ctx)
	if !ok {
		return accountDomain.User{}, errors.New("用户行锁必须在事务内获取")
	}
	user, err := scanUser(tx.QueryRow(ctx, `SELECT `+userColumns+` FROM velis.users WHERE id = $1 FOR UPDATE`, pgUUID(id)))
	if errors.Is(err, pgx.ErrNoRows) {
		return accountDomain.User{}, accountDomain.ErrNotFound
	}
	return user, err
}

func scanUser(row scanner) (accountDomain.User, error) {
	var user accountDomain.User
	var id pgtype.UUID
	err := row.Scan(&id, &user.Username, &user.Nickname, &user.PasswordHash, &user.Role, &user.Status,
		&user.Version, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		return accountDomain.User{}, err
	}
	user.ID = fromPGUUID(id)
	return user, nil
}

func pgUUID(id accountDomain.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: [16]byte(id), Valid: true}
}

func fromPGUUID(id pgtype.UUID) accountDomain.UUID { return accountDomain.UUID(id.Bytes) }

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
