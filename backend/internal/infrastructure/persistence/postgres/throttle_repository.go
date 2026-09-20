package postgres

import (
	"context"
	"encoding/binary"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	accountDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
)

type ThrottleRepository struct {
	pool *pgxpool.Pool
	tx   *TxManager
}

func NewThrottleRepository(pool *pgxpool.Pool) *ThrottleRepository {
	return &ThrottleRepository{pool: pool, tx: NewTxManager(pool)}
}

func (r *ThrottleRepository) ActiveBlock(ctx context.Context, dimension accountDomain.FailureDimension, key []byte, now time.Time) (*time.Time, error) {
	var blockedUntil time.Time
	err := querier(ctx, r.pool).QueryRow(ctx, `SELECT blocked_until FROM velis.login_blocks
WHERE dimension = $1 AND lookup_key = $2 AND blocked_until > $3`, string(dimension), key, now).Scan(&blockedUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &blockedUntil, nil
}

// RecordFailure 统计滚动窗口内的失败次数并在达到阈值时写入限制。
// 限制生效期间不新增失败记录、不延长截止时间；同一维度键的并发写入由会话级 advisory lock 序列化。
func (r *ThrottleRepository) RecordFailure(ctx context.Context, dimension accountDomain.FailureDimension, key []byte, at time.Time, policy accountDomain.ThrottlePolicy) (*time.Time, error) {
	var blockedUntil *time.Time
	err := r.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		if _, err := querier(ctx, r.pool).Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, advisoryLockKey(key)); err != nil {
			return err
		}
		var activeUntil time.Time
		err := querier(ctx, r.pool).QueryRow(ctx, `SELECT blocked_until FROM velis.login_blocks
WHERE dimension = $1 AND lookup_key = $2 AND blocked_until > $3`, string(dimension), key, at).Scan(&activeUntil)
		if err == nil {
			blockedUntil = &activeUntil
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err := querier(ctx, r.pool).Exec(ctx, `INSERT INTO velis.login_failure_events
(dimension, lookup_key, occurred_at) VALUES ($1, $2, $3)`, string(dimension), key, at); err != nil {
			return err
		}
		var failures int
		if err := querier(ctx, r.pool).QueryRow(ctx, `SELECT count(*) FROM velis.login_failure_events
WHERE dimension = $1 AND lookup_key = $2 AND occurred_at > $3`,
			string(dimension), key, at.Add(-policy.Window(dimension))).Scan(&failures); err != nil {
			return err
		}
		if failures < policy.Limit(dimension) {
			return nil
		}
		until := policy.BlockedUntil(at)
		if _, err := querier(ctx, r.pool).Exec(ctx, `INSERT INTO velis.login_blocks
(dimension, lookup_key, blocked_until, updated_at) VALUES ($1, $2, $3, $4)
ON CONFLICT (dimension, lookup_key) DO UPDATE
SET blocked_until = EXCLUDED.blocked_until, updated_at = EXCLUDED.updated_at
WHERE velis.login_blocks.blocked_until <= EXCLUDED.updated_at`,
			string(dimension), key, until, at); err != nil {
			return err
		}
		blockedUntil = &until
		return nil
	})
	if err != nil {
		return nil, err
	}
	return blockedUntil, nil
}

// advisoryLockKey 取 HMAC 查找键的前 8 字节作为会话级锁键；查找键本身均匀分布，无需再哈希。
func advisoryLockKey(key []byte) int64 {
	var buf [8]byte
	copy(buf[:], key)
	return int64(binary.BigEndian.Uint64(buf[:]))
}
