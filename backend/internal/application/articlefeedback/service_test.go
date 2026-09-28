package articlefeedback

import (
	"context"
	"errors"
	"testing"
	"time"

	feedback "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/articlefeedback"
)

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type fakeRepository struct {
	reads    int
	lastTime time.Time
	favorite bool
	negative bool
}

func (r *fakeRepository) RecordRead(_ context.Context, _ string, _ int64, now time.Time) error {
	r.reads++
	r.lastTime = now
	return nil
}
func (r *fakeRepository) SetFavorite(_ context.Context, _ string, _ int64, enabled bool, now time.Time) error {
	r.favorite = enabled
	r.lastTime = now
	return nil
}
func (r *fakeRepository) SetNotInterested(_ context.Context, _ string, _ int64, enabled bool, now time.Time) error {
	r.negative = enabled
	r.lastTime = now
	return nil
}
func (r *fakeRepository) States(_ context.Context, _ string, ids []int64, _ time.Time) ([]feedback.State, error) {
	return []feedback.State{{ArticleID: ids[0]}}, nil
}

func TestServiceValidationAndStates(t *testing.T) {
	const user = "72000000-0000-0000-0000-000000000001"
	now := time.Date(2026, 9, 29, 0, 0, 0, 0, time.FixedZone("CST", 8*3600))
	r := &fakeRepository{}
	s := NewService(r, fixedClock{now})
	if err := s.RecordRead(context.Background(), user, 1); err != nil || r.reads != 1 || r.lastTime.Location() != time.UTC {
		t.Fatalf("阅读用例失败: %v %+v", err, r)
	}
	if err := s.SetFavorite(context.Background(), user, 1, true); err != nil || !r.favorite {
		t.Fatalf("收藏用例失败: %v", err)
	}
	if err := s.SetNotInterested(context.Background(), user, 1, true); err != nil || !r.negative {
		t.Fatalf("负反馈用例失败: %v", err)
	}
	states, err := s.States(context.Background(), user, []int64{1})
	if err != nil || len(states) != 1 {
		t.Fatalf("批量状态失败: %+v %v", states, err)
	}
	for _, ids := range [][]int64{nil, {0}, {1, 1}, make([]int64, 51)} {
		if _, err := s.States(context.Background(), user, ids); !errors.Is(err, feedback.ErrInvalidInput) {
			t.Fatalf("非法列表 %+v 未拒绝: %v", ids, err)
		}
	}
	if err := s.RecordRead(context.Background(), user, 0); !errors.Is(err, feedback.ErrInvalidInput) {
		t.Fatalf("非法 ID: %v", err)
	}
	if err := s.RecordRead(context.Background(), "invalid", 1); !errors.Is(err, feedback.ErrInvalidInput) {
		t.Fatalf("非法用户: %v", err)
	}
}

func TestUTCWindowAndDayBoundaries(t *testing.T) {
	before := time.Date(2026, 9, 28, 23, 59, 59, 0, time.UTC)
	after := before.Add(time.Second)
	if feedback.ReadWindow(before).Equal(feedback.ReadWindow(after)) {
		t.Fatal("跨半小时窗口没有切换")
	}
	if feedback.ReadDay(before).Equal(feedback.ReadDay(after)) {
		t.Fatal("跨 UTC 自然日没有切换")
	}
	if !feedback.ReadWindow(before).Equal(time.Date(2026, 9, 28, 23, 30, 0, 0, time.UTC)) {
		t.Fatal("阅读窗口未对齐")
	}
}
