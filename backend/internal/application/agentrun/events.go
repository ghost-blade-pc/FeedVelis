package agentrun

import (
	"context"
	run "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
)

// Events 每批重新检查登录和归属，并在同一快照中投影当前可见性。
func (s *Service) Events(ctx context.Context, caller Caller, cid, rid string, after int64) (EventBatch, error) {
	caller, err := normalizeCaller(caller)
	if err != nil {
		return EventBatch{}, err
	}
	cid, err = normalizedID(cid)
	if err != nil {
		return EventBatch{}, err
	}
	rid, err = normalizedID(rid)
	if err != nil {
		return EventBatch{}, err
	}
	var batch EventBatch
	err = s.snapshot.WithinReadSnapshot(ctx, func(ctx context.Context, _ bool) error {
		if err := s.authorizeConversation(ctx, caller, cid); err != nil {
			return err
		}
		reader, ok := s.repository.(EventReader)
		if !ok {
			return &run.Error{Code: run.CodeDependencyUnavailable}
		}
		var err error
		batch, err = reader.ReadEvents(ctx, Scope{caller.UserID, cid, rid}, after, 16)
		if err != nil {
			return err
		}
		if s.projector == nil {
			return &run.Error{Code: run.CodeDependencyUnavailable}
		}
		batch.Events, err = s.projector.Events(ctx, batch.Events)
		return err
	})
	return batch, err
}
