package agentconversation

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/idempotency"
	conversation "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentconversation"
)

const unitUser = "83000000-0000-0000-0000-000000000001"
const unitID = "83000000-0000-0000-0000-000000000002"
const unitKey = "83000000-0000-0000-0000-000000000003"

type unitTx struct{}

func (unitTx) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}
func (unitTx) WithinReadSnapshot(ctx context.Context, fn func(context.Context, bool) error) error {
	return fn(ctx, true)
}

type orderRepository struct {
	Repository
	order   *[]string
	now     time.Time
	findErr error
	c       conversation.Conversation
}

func (r orderRepository) LockUser(context.Context, string) (UserState, error) {
	*r.order = append(*r.order, "user")
	return UserState{Now: r.now}, nil
}
func (r orderRepository) Find(_ context.Context, _, _ string, lock bool) (conversation.Conversation, error) {
	name := "ownership"
	if lock {
		name = "conversation"
	}
	*r.order = append(*r.order, name)
	return r.c, r.findErr
}
func (r orderRepository) AppendMessage(_ context.Context, _ string, m conversation.Message) error {
	*r.order = append(*r.order, "append")
	if m.Role != conversation.RoleUser || m.Sequence != 1 || m.Content != " 正文\n" {
		return errors.New("消息未按服务器规则构造")
	}
	return nil
}

type unitDedup struct {
	order    *[]string
	record   idempotency.Record
	acquired bool
	err      error
}

func (r unitDedup) Begin(_ context.Context, _ idempotency.Identity, _ [32]byte, now, expiry time.Time) (idempotency.Record, bool, error) {
	*r.order = append(*r.order, "idempotency")
	if expiry.Sub(now) != 24*time.Hour {
		return idempotency.Record{}, false, errors.New("幂等窗口不是24小时")
	}
	return r.record, r.acquired, r.err
}
func (r unitDedup) Succeed(_ context.Context, _ idempotency.Identity, _ int, _ json.RawMessage, resource, id string, _ time.Time) error {
	*r.order = append(*r.order, "succeed")
	if resource != ResourceType || id != unitID {
		return errors.New("幂等资源关联错误")
	}
	return nil
}

func TestWriteLockOrderAndServerRole(t *testing.T) {
	order := []string{}
	now := time.Now().UTC()
	c, _ := conversation.New(unitID, unitUser, nil, now)
	repo := orderRepository{order: &order, now: now, c: c}
	tx := unitTx{}
	s := NewService(repo, tx, tx, unitDedup{order: &order, acquired: true}, DefaultLimits())
	result, err := s.Append(context.Background(), unitUser, unitID, unitKey, " 正文\r\n")
	if err != nil || result.Status != 201 {
		t.Fatalf("追加: %+v %v", result, err)
	}
	if !reflect.DeepEqual(order, []string{"user", "ownership", "idempotency", "conversation", "append", "succeed"}) {
		t.Fatalf("锁序错误: %v", order)
	}
}

func TestOwnershipBeforeIdempotencyAndPending(t *testing.T) {
	for _, test := range []struct {
		findErr error
		status  idempotency.Status
		want    error
		order   []string
	}{
		{conversation.ErrNotFound, idempotency.StatusSucceeded, conversation.ErrNotFound, []string{"user", "ownership"}},
		{nil, idempotency.StatusPending, idempotency.ErrPending, []string{"user", "ownership", "idempotency"}},
	} {
		order := []string{}
		repo := orderRepository{order: &order, now: time.Now(), findErr: test.findErr}
		tx := unitTx{}
		s := NewService(repo, tx, tx, unitDedup{order: &order, record: idempotency.Record{Status: test.status}}, DefaultLimits())
		if _, err := s.Rename(context.Background(), unitUser, unitID, unitKey, "标题", 99); !errors.Is(err, test.want) {
			t.Fatalf("优先级错误: %v", err)
		}
		if !reflect.DeepEqual(order, test.order) {
			t.Fatalf("所有权之前执行幂等: %v", order)
		}
	}
}

func TestInvalidCommandsDoNotAccessDependencies(t *testing.T) {
	// nil依赖使任何错误的访问立即失败。
	s := NewService(nil, nil, nil, nil, DefaultLimits())
	ctx := context.Background()
	for _, call := range []func() error{
		func() error { _, e := s.Create(ctx, "bad", unitKey, nil); return e },
		func() error { _, e := s.Create(ctx, unitUser, "bad", nil); return e },
		func() error { title := "\n"; _, e := s.Create(ctx, unitUser, unitKey, &title); return e },
		func() error { _, e := s.Append(ctx, unitUser, unitID, unitKey, " \u3000"); return e },
		func() error { _, e := s.Rename(ctx, unitUser, unitID, unitKey, "标题", 0); return e },
		func() error { return s.Delete(ctx, unitUser, "bad") },
		func() error { _, e := s.Detail(ctx, "bad", unitID); return e },
		func() error { _, _, e := s.List(ctx, unitUser, nil, 0); return e },
		func() error { _, _, e := s.History(ctx, unitUser, unitID, -1, 20); return e },
	} {
		if err := call(); !errors.Is(err, conversation.ErrInvalidInput) {
			t.Fatalf("非法命令未拒绝: %v", err)
		}
	}
}

func TestDeletedEnvelopeAndInvalidVersion(t *testing.T) {
	for _, raw := range []string{`{"version":1,"conversation_id":"` + unitID + `","deleted":true}`, `{"version":2,"conversation_id":"` + unitID + `"}`, `{`, `{}`} {
		_, err := decodeOutcome(idempotency.Outcome{Result: json.RawMessage(raw), Replayed: true})
		if err == nil {
			t.Fatal("无效/删除结果不得成功")
		}
	}
}
