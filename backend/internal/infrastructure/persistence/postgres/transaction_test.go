package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

type fakeTransaction struct {
	pgx.Tx
	committed   bool
	commitError error
	rollbacks   int
}

func (t *fakeTransaction) Commit(context.Context) error {
	if t.commitError != nil {
		return t.commitError
	}
	t.committed = true
	return nil
}
func (t *fakeTransaction) Rollback(context.Context) error { t.rollbacks++; return nil }
func TestAfterCommitOutermostDedupRollbackAndCommitFailure(t *testing.T) {
	for _, mode := range []string{"success", "rollback", "commit_failure"} {
		t.Run(mode, func(t *testing.T) {
			tx := &fakeTransaction{}
			failure := errors.New("事务失败")
			if mode == "commit_failure" {
				tx.commitError = failure
			}
			begins, calls := 0, 0
			m := &TxManager{begin: func(context.Context, pgx.TxOptions) (pgx.Tx, error) { begins++; return tx, nil }}
			err := m.WithinTransaction(context.Background(), func(outer context.Context) error {
				for i := 0; i < 2; i++ {
					if err := m.WithinTransaction(outer, func(inner context.Context) error {
						return m.RegisterAfterCommit(inner, "latest", func(context.Context) error {
							if !tx.committed {
								t.Fatal("提交前执行")
							}
							calls++
							return errors.New("回调失败")
						})
					}); err != nil {
						return err
					}
				}
				if calls != 0 {
					t.Fatal("内层提前回调")
				}
				if mode == "rollback" {
					return failure
				}
				return nil
			})
			if begins != 1 || tx.rollbacks != 1 {
				t.Fatal("嵌套未复用或资源未释放")
			}
			if mode == "success" {
				if err != nil || calls != 1 {
					t.Fatal(err, calls)
				}
			} else if err != failure || calls != 0 {
				t.Fatal(err, calls)
			}
		})
	}
}
func TestReadSnapshotOptionsAndExistingTransactionBypass(t *testing.T) {
	tx := &fakeTransaction{}
	m := &TxManager{begin: func(_ context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
		if opts.IsoLevel != pgx.RepeatableRead || opts.AccessMode != pgx.ReadOnly {
			t.Fatal(opts)
		}
		return tx, nil
	}}
	if err := m.WithinReadSnapshot(context.Background(), func(view context.Context, allowed bool) error {
		if !allowed || querier(view, nil) != tx {
			t.Fatal("快照上下文未绑定")
		}
		if err := m.RegisterAfterCommit(view, "action", func(context.Context) error { return nil }); err == nil {
			t.Fatal("只读事务接受提交后动作")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), txContextKey{}, &transactionState{tx: tx})
	if err := m.WithinReadSnapshot(ctx, func(_ context.Context, allowed bool) error {
		if allowed {
			t.Fatal("嵌套快照允许回填")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.RegisterAfterCommit(context.Background(), "action", func(context.Context) error { return nil }); err == nil {
		t.Fatal("无事务接受动作")
	}
}
