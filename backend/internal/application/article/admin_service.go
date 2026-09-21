package article

import (
	"context"
	"encoding/json"
	"time"

	idempotencyApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/idempotency"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	accountDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
)

type AdminArticleRepository interface {
	GetAdminArticle(context.Context, int64) (articleDomain.StoredArticle, error)
	SetArticleState(context.Context, int64, int64, articleDomain.Status, *articleDomain.OfflineReason, *string, *time.Time, *time.Time, time.Time) (articleDomain.StoredArticle, error)
}

type AdminActor struct {
	UserID string
	Role   accountDomain.Role
}

type AdminService struct {
	repository  AdminArticleRepository
	idempotency *idempotencyApp.Service
	clock       ports.Clock
}

func NewAdminService(repository AdminArticleRepository, idempotency *idempotencyApp.Service, clock ports.Clock) *AdminService {
	return &AdminService{repository: repository, idempotency: idempotency, clock: clock}
}

type AdminArticleCommand struct {
	Actor           AdminActor
	ArticleID       int64
	ExpectedVersion int64
	IdempotencyKey  string
}

func (s *AdminService) Offline(ctx context.Context, command AdminArticleCommand) (UserArticleResult, bool, error) {
	return s.changeState(ctx, "admin.article.offline", command, false)
}

func (s *AdminService) Restore(ctx context.Context, command AdminArticleCommand) (UserArticleResult, bool, error) {
	return s.changeState(ctx, "admin.article.restore", command, true)
}

func (s *AdminService) changeState(ctx context.Context, operation string, command AdminArticleCommand, restore bool) (UserArticleResult, bool, error) {
	if command.Actor.Role != accountDomain.RoleAdmin || command.Actor.UserID == "" {
		return UserArticleResult{}, false, articleDomain.ErrForbidden
	}
	payload := struct {
		ArticleID int64 `json:"article_id"`
		Expected  int64 `json:"expected_version"`
	}{command.ArticleID, command.ExpectedVersion}
	now := s.clock.Now().UTC()
	outcome, err := s.idempotency.Execute(ctx, idempotencyApp.Command{
		Identity: idempotencyApp.Identity{ActorUserID: command.Actor.UserID, Operation: operation, Key: command.IdempotencyKey}, Payload: payload, Now: now,
	}, func(txContext context.Context) (any, string, string, error) {
		current, getErr := s.repository.GetAdminArticle(txContext, command.ArticleID)
		if getErr != nil {
			return nil, "", "", getErr
		}
		if current.LockVersion != command.ExpectedVersion {
			return nil, "", "", articleDomain.ErrVersionConflict
		}
		status := articleDomain.StatusOffline
		var reason *articleDomain.OfflineReason
		var actor *string
		if restore {
			if current.Status != articleDomain.StatusOffline || current.OfflineReason == nil || *current.OfflineReason != articleDomain.OfflineByAdmin {
				return nil, "", "", articleDomain.ErrInvalidState
			}
			status = articleDomain.StatusPublished
		} else {
			if current.Status != articleDomain.StatusPublished {
				return nil, "", "", articleDomain.ErrInvalidState
			}
			value := articleDomain.OfflineByAdmin
			reason, actor = &value, &command.Actor.UserID
		}
		stored, setErr := s.repository.SetArticleState(txContext, current.ID, command.ExpectedVersion, status, reason, actor, nil, nil, now)
		if setErr != nil {
			return nil, "", "", setErr
		}
		return UserArticleResult{Article: stored}, "article", jsonNumber(stored.ID), nil
	})
	if err != nil {
		return UserArticleResult{}, false, err
	}
	var result UserArticleResult
	if err := json.Unmarshal(outcome.Result, &result); err != nil {
		return UserArticleResult{}, false, err
	}
	return result, outcome.Replayed, nil
}
