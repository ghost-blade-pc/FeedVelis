// Package articlefeedback 编排登录用户的文章反馈用例。
package articlefeedback

import (
	"context"
	"time"

	"github.com/google/uuid"

	feedback "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/articlefeedback"
)

type Repository interface {
	RecordRead(context.Context, string, int64, time.Time) error
	SetFavorite(context.Context, string, int64, bool, time.Time) error
	SetNotInterested(context.Context, string, int64, bool, time.Time) error
	States(context.Context, string, []int64, time.Time) ([]feedback.State, error)
}

type Clock interface{ Now() time.Time }

type Service struct {
	repository Repository
	clock      Clock
}

func NewService(repository Repository, clock Clock) *Service {
	return &Service{repository: repository, clock: clock}
}

func validate(userID string, articleID int64) error {
	if articleID <= 0 || uuid.Validate(userID) != nil {
		return feedback.ErrInvalidInput
	}
	return nil
}

func (s *Service) RecordRead(ctx context.Context, userID string, articleID int64) error {
	if err := validate(userID, articleID); err != nil {
		return err
	}
	return s.repository.RecordRead(ctx, userID, articleID, s.clock.Now().UTC())
}

func (s *Service) SetFavorite(ctx context.Context, userID string, articleID int64, enabled bool) error {
	if err := validate(userID, articleID); err != nil {
		return err
	}
	return s.repository.SetFavorite(ctx, userID, articleID, enabled, s.clock.Now().UTC())
}

func (s *Service) SetNotInterested(ctx context.Context, userID string, articleID int64, enabled bool) error {
	if err := validate(userID, articleID); err != nil {
		return err
	}
	return s.repository.SetNotInterested(ctx, userID, articleID, enabled, s.clock.Now().UTC())
}

func (s *Service) States(ctx context.Context, userID string, ids []int64) ([]feedback.State, error) {
	if uuid.Validate(userID) != nil || len(ids) < 1 || len(ids) > 50 {
		return nil, feedback.ErrInvalidInput
	}
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, feedback.ErrInvalidInput
		}
		if _, ok := seen[id]; ok {
			return nil, feedback.ErrInvalidInput
		}
		seen[id] = struct{}{}
	}
	return s.repository.States(ctx, userID, ids, s.clock.Now().UTC())
}
