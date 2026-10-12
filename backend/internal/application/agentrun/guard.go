package agentrun

import (
	"context"
	"errors"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentconversation"
	run "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
)

// SafeCode 只提取已登记的领域分类；Provider 错误不会成为用户消息或日志。
func SafeCode(err error) run.Code {
	if err == nil {
		return ""
	}
	var value *run.Error
	if errors.As(err, &value) && value.Code.Valid() {
		return value.Code
	}
	return run.CodeInternal
}

type Guard struct {
	authorization CurrentAuthorization
	repository    ExecutionReader
	snapshot      agentconversation.ReadSnapshot
}

func NewGuard(auth CurrentAuthorization, reader ExecutionReader, snapshot agentconversation.ReadSnapshot) *Guard {
	return &Guard{authorization: auth, repository: reader, snapshot: snapshot}
}

// Check 在同一短快照中先确认原登录会话，随后检查当前执行权。
// 检查完成后才允许发起网络调用；写入端仍须在写事务内再次校验。
func (g *Guard) Check(ctx context.Context, execution Execution) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if g == nil || g.authorization == nil || g.repository == nil || g.snapshot == nil {
		return &run.Error{Code: run.CodeDependencyUnavailable}
	}
	return g.snapshot.WithinReadSnapshot(ctx, func(ctx context.Context, _ bool) error {
		if err := g.authorization.CheckSession(ctx, execution.UserID, execution.SessionID); err != nil {
			if SafeCode(err) == run.CodeAuthInvalid {
				return &run.Error{Code: run.CodeAuthInvalid}
			}
			return &run.Error{Code: run.CodeDependencyUnavailable}
		}
		current, now, err := g.repository.ReadExecution(ctx, execution.Scope)
		if err != nil {
			return err
		}
		if current.UserID != execution.UserID || current.SessionID != execution.SessionID ||
			current.ConversationID != execution.ConversationID || current.ID != execution.RunID {
			return run.ErrExecutionLost
		}
		return current.CheckOwner(execution.OwnerToken, now)
	})
}
