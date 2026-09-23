package asynctask

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articleevent"
)

type txFake struct{}

func (txFake) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

type inboxFake struct {
	first  bool
	result ConsumeResult
}

func (i *inboxFake) Start(context.Context, articleevent.Envelope) (bool, error) { return i.first, nil }
func (i *inboxFake) Finish(_ context.Context, _ string, result ConsumeResult) error {
	i.result = result
	return nil
}

type storeFake struct {
	fact      ArticleFact
	task      *Task
	operation string
	lockErr   error
}

func (s *storeFake) LockArticle(context.Context, int64) (ArticleFact, error) {
	return s.fact, s.lockErr
}
func (s *storeFake) LockTask(context.Context, int64) (*Task, error) { return s.task, nil }
func (s *storeFake) CreatePending(_ context.Context, id string, fact ArticleFact, _ time.Time) (Task, error) {
	s.operation = "create"
	return Task{ID: id, Generation: 1, RevisionID: fact.RevisionID, Status: "pending"}, nil
}

func TestProjectionLogsCorrelationAndRedactsFailure(t *testing.T) {
	traceID := strings.Repeat("a", 32)
	ctx, err := articleevent.WithTraceID(context.Background(), traceID)
	if err != nil {
		t.Fatal(err)
	}
	fact := ArticleFact{ArticleID: 42, Origin: "user", Status: "published", RevisionID: 81, RevisionNo: 1, ContentHash: strings.Repeat("b", 64), LockVersion: 1}
	event, err := articleevent.Published(ctx, articleevent.PublicFact{ArticleID: 42, OriginType: "user", RevisionID: 81, RevisionNo: 1, ContentHash: fact.ContentHash, LockVersion: 1}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	service := NewService(txFake{}, &inboxFake{first: true}, &storeFake{fact: fact}, nil).WithLogger(logger, "worker-1")
	if _, err := service.Project(ctx, event); err != nil {
		t.Fatal(err)
	}
	line := output.String()
	for _, expected := range []string{traceID, event.EventID, `"task_id"`, `"worker_id":"worker-1"`, `"result":"applied"`} {
		if !strings.Contains(line, expected) {
			t.Fatalf("日志缺少 %q: %s", expected, line)
		}
	}
	if strings.Contains(line, fact.ContentHash) {
		t.Fatalf("日志不应包含事件正文/内容摘要: %s", line)
	}

	output.Reset()
	secret := "very-secret-password"
	failed := NewService(txFake{}, &inboxFake{first: true}, &storeFake{lockErr: errors.New("连接 amqp://user:" + secret + "@localhost/vhost 失败")}, nil).WithLogger(logger, "worker-1")
	if _, err := failed.Project(ctx, event); err == nil {
		t.Fatal("预期投影失败")
	}
	line = output.String()
	if strings.Contains(line, secret) || !strings.Contains(line, "amqp://user:***@localhost") || !strings.Contains(line, `"event_id"`) {
		t.Fatalf("失败日志未脱敏或缺少关联字段: %s", line)
	}
}
func (s *storeFake) SetPending(_ context.Context, task Task, _ ArticleFact, _ time.Time) (Task, error) {
	s.operation = "update"
	task.Generation++
	task.Status = "pending"
	return task, nil
}
func (s *storeFake) Cancel(_ context.Context, task Task, _ ArticleFact, _ time.Time) (Task, error) {
	s.operation = "cancel"
	task.Generation++
	task.Status = "canceled"
	return task, nil
}
func (s *storeFake) Observe(_ context.Context, task Task, fact ArticleFact, _ time.Time) (Task, error) {
	s.operation = "observe"
	task.ObservedVersion = fact.LockVersion
	return task, nil
}
func (s *storeFake) UpdateIfGeneration(context.Context, string, int64, string, time.Time) (bool, error) {
	return false, nil
}

func TestProjectConvergesToCurrentArticleFact(t *testing.T) {
	baseFact := ArticleFact{ArticleID: 42, Status: "published", RevisionID: 81, RevisionNo: 3, ContentHash: strings.Repeat("a", 64), LockVersion: 7}
	baseTask := Task{ID: "task", ArticleID: 42, Status: "pending", Generation: 1, RevisionID: 81, RevisionNo: 3, ContentHash: baseFact.ContentHash, ObservedVersion: 7}
	tests := []struct {
		name   string
		fact   ArticleFact
		task   *Task
		wantOp string
		want   Outcome
	}{
		{"首次发布", baseFact, nil, "create", OutcomeApplied},
		{"修订合并", func() ArticleFact {
			v := baseFact
			v.RevisionID = 82
			v.ContentHash = strings.Repeat("b", 64)
			v.LockVersion = 8
			return v
		}(), &baseTask, "update", OutcomeApplied},
		{"相同目标", baseFact, &baseTask, "", OutcomeNoop},
		{"下架", func() ArticleFact { v := baseFact; v.Status = "offline"; v.LockVersion = 8; return v }(), &baseTask, "cancel", OutcomeApplied},
		{"删除且无任务", func() ArticleFact { v := baseFact; v.Status = "deleted"; return v }(), nil, "", OutcomeNoop},
		{"重新发布", baseFact, &Task{ID: "task", Status: "canceled", Generation: 2, RevisionID: 81, ContentHash: baseFact.ContentHash, ObservedVersion: 6}, "update", OutcomeApplied},
	}
	event, _ := articleevent.Published(context.Background(), articleevent.PublicFact{ArticleID: 42, OriginType: "user", RevisionID: 81, RevisionNo: 3, ContentHash: baseFact.ContentHash, LockVersion: 7}, time.Now())
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inbox := &inboxFake{first: true}
			store := &storeFake{fact: test.fact, task: test.task}
			got, err := NewService(txFake{}, inbox, store, func() time.Time { return time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC) }).Project(context.Background(), event)
			if err != nil || got != test.want || store.operation != test.wantOp {
				t.Fatalf("outcome=%s op=%s err=%v", got, store.operation, err)
			}
		})
	}
}

func TestProjectDuplicateDoesNotReadOrChangeTask(t *testing.T) {
	inbox := &inboxFake{first: false}
	store := &storeFake{}
	event, _ := articleevent.Deleted(context.Background(), 42, 81, 8, time.Now())
	got, err := NewService(txFake{}, inbox, store, nil).Project(context.Background(), event)
	if err != nil || got != OutcomeDuplicate || store.operation != "" {
		t.Fatalf("outcome=%s op=%s err=%v", got, store.operation, err)
	}
}
