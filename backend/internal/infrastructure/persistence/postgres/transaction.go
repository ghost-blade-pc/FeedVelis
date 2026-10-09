package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type TxManager struct {
	pool  *pgxpool.Pool
	begin func(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

func NewTxManager(pool *pgxpool.Pool) *TxManager { return &TxManager{pool: pool} }

func (m *TxManager) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	if _, ok := transactionFromContext(ctx); ok {
		return fn(ctx)
	}
	tx, err := m.beginTransaction(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	state := &transactionState{tx: tx, actions: map[string]func(context.Context) error{}}
	if err := fn(context.WithValue(ctx, txContextKey{}, state)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	for _, id := range state.order {
		_ = state.actions[id](ctx)
	}
	return nil
}

func (m *TxManager) beginTransaction(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	if m.begin != nil {
		return m.begin(ctx, options)
	}
	return m.pool.BeginTx(ctx, options)
}

type transactionState struct {
	tx       pgx.Tx
	readOnly bool
	actions  map[string]func(context.Context) error
	order    []string
}

func (m *TxManager) RegisterAfterCommit(ctx context.Context, id string, action func(context.Context) error) error {
	state, ok := ctx.Value(txContextKey{}).(*transactionState)
	if !ok || state.readOnly {
		return errors.New("提交后动作必须登记在写事务中")
	}
	if id == "" || action == nil {
		return errors.New("提交后动作标识和回调不能为空")
	}
	if _, exists := state.actions[id]; !exists {
		state.actions[id] = action
		state.order = append(state.order, id)
	}
	return nil
}
func (m *TxManager) WithinReadSnapshot(ctx context.Context, fn func(context.Context, bool) error) error {
	if _, exists := transactionFromContext(ctx); exists {
		return fn(ctx, false)
	}
	tx, err := m.beginTransaction(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	state := &transactionState{tx: tx, readOnly: true}
	if err := fn(context.WithValue(ctx, txContextKey{}, state), true); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type txContextKey struct{}

func transactionFromContext(ctx context.Context) (pgx.Tx, bool) {
	state, ok := ctx.Value(txContextKey{}).(*transactionState)
	if !ok {
		return nil, false
	}
	return state.tx, true
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

func (m *TxManager) CacheAllowed(ctx context.Context) bool {
	_, exists := transactionFromContext(ctx)
	return !exists
}
