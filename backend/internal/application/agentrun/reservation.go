package agentrun

import (
	"context"

	conversation "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentconversation"
	run "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
)

// WithAssistantReservation 在最终写事务内重新检查当前消息上限，然后才允许保存回调执行。
// 回调仍须复核证据并原子保存完整回答；本方法不会生成或自动追加助手消息。
func (s *Service) WithAssistantReservation(ctx context.Context, execution Execution, save func(context.Context, conversation.Conversation, Record) error) error {
	if save == nil {
		return run.ErrInvalidInput
	}
	return s.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		if _, err := s.repository.LockUser(ctx, execution.UserID); err != nil {
			return err
		}
		if err := s.authorization.CheckSession(ctx, execution.UserID, execution.SessionID); err != nil {
			return err
		}
		c, err := s.repository.LockConversation(ctx, execution.UserID, execution.ConversationID)
		if err != nil {
			return err
		}
		record, err := s.repository.LockRun(ctx, execution.Scope)
		if err != nil {
			return err
		}
		if record.Run.SessionID != execution.SessionID {
			return run.ErrExecutionLost
		}
		now, err := s.repository.DatabaseTime(ctx)
		if err != nil {
			return err
		}
		if err := record.Run.CheckOwner(execution.OwnerToken, now); err != nil {
			return err
		}
		if !record.Run.AssistantReserved {
			return run.ErrMessageLimit
		}
		reservations, err := s.repository.ConversationActive(ctx, c.ID)
		if err != nil {
			return err
		}
		if err := run.CheckAssistantCommit(c.MessageCount, reservations, s.config.MaxMessages); err != nil {
			return err
		}
		return save(ctx, c, record)
	})
}
