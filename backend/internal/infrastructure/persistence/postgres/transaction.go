package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type TxManager struct{ pool *pgxpool.Pool }

func NewTxManager(pool *pgxpool.Pool) *TxManager { return &TxManager{pool: pool} }

func (m *TxManager) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	if _, ok := transactionFromContext(ctx); ok {
		return fn(ctx)
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(context.WithValue(ctx, txContextKey{}, tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type txContextKey struct{}

func transactionFromContext(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(txContextKey{}).(pgx.Tx)
	return tx, ok
}

// querier 优先使用事务内的连接，保证同一用例的写入与读取看到一致结果。
func querier(ctx context.Context, pool *pgxpool.Pool) querierConn {
	if tx, ok := transactionFromContext(ctx); ok {
		return tx
	}
	return pool
}

type querierConn interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}
