package agentrun

import (
	"context"
	run "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
)

func (s *Service) Cancel(ctx context.Context, caller Caller, conversationID, runID string) (Summary, error) {
	caller, err := normalizeCaller(caller)
	if err != nil {
		return Summary{}, err
	}
	conversationID, err = normalizedID(conversationID)
	if err != nil {
		return Summary{}, err
	}
	runID, err = normalizedID(runID)
	if err != nil {
		return Summary{}, err
	}
	repo, ok := s.repository.(Repository)
	if !ok {
		return Summary{}, &run.Error{Code: run.CodeDependencyUnavailable}
	}
	var result Summary
	err = s.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		if _, err := repo.LockUser(ctx, caller.UserID); err != nil {
			return err
		}
		if err := s.authorizeConversation(ctx, caller, conversationID); err != nil {
			return err
		}
		record, err := repo.Terminate(ctx, Scope{caller.UserID, conversationID, runID}, run.Canceled, run.CodeCanceled)
		if err != nil {
			return err
		}
		result = summarize(record)
		return nil
	})
	return result, err
}
