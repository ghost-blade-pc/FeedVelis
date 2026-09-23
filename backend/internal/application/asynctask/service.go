package asynctask

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articleevent"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
)

var amqpCredentialPattern = regexp.MustCompile(`(?i)(amqps?://[^:/\s]+:)[^@/\s]+@`)

type Outcome string

const (
	OutcomeApplied   Outcome = "applied"
	OutcomeNoop      Outcome = "noop"
	OutcomeDuplicate Outcome = "duplicate"
)

type Service struct {
	tx       ports.TxManager
	inbox    Inbox
	tasks    TaskStore
	now      func() time.Time
	logger   *slog.Logger
	workerID string
}

func (s *Service) WithLogger(logger *slog.Logger, workerID string) *Service {
	s.logger = logger
	s.workerID = workerID
	return s
}

func NewService(tx ports.TxManager, inbox Inbox, tasks TaskStore, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{tx: tx, inbox: inbox, tasks: tasks, now: now}
}

func (s *Service) Project(ctx context.Context, event articleevent.Envelope) (Outcome, error) {
	if err := event.Validate(); err != nil {
		return "", err
	}
	articleID, err := strconv.ParseInt(event.Aggregate.ID, 10, 64)
	if err != nil {
		return "", fmt.Errorf("文章聚合 ID 无效: %w", err)
	}
	outcome := OutcomeNoop
	taskID := ""
	err = s.tx.WithinTransaction(ctx, func(txContext context.Context) error {
		first, startErr := s.inbox.Start(txContext, event)
		if startErr != nil {
			return startErr
		}
		if !first {
			outcome = OutcomeDuplicate
			return nil
		}
		fact, lockErr := s.tasks.LockArticle(txContext, articleID)
		if lockErr != nil {
			return lockErr
		}
		current, taskErr := s.tasks.LockTask(txContext, articleID)
		if taskErr != nil {
			return taskErr
		}
		changed := false
		if current != nil {
			taskID = current.ID
		}
		now := s.now().UTC()
		if fact.Status == "published" {
			if current == nil {
				var task Task
				task, taskErr = s.tasks.CreatePending(txContext, uuid.NewString(), fact, now)
				taskID = task.ID
				changed = taskErr == nil
			} else if current.Status != "pending" || current.RevisionID != fact.RevisionID || current.ContentHash != fact.ContentHash {
				var task Task
				task, taskErr = s.tasks.SetPending(txContext, *current, fact, now)
				taskID = task.ID
				changed = taskErr == nil
			} else if current.ObservedVersion < fact.LockVersion {
				var task Task
				task, taskErr = s.tasks.Observe(txContext, *current, fact, now)
				taskID = task.ID
			}
		} else if current != nil && current.Status == "pending" {
			var task Task
			task, taskErr = s.tasks.Cancel(txContext, *current, fact, now)
			taskID = task.ID
			changed = taskErr == nil
		} else if current != nil && current.ObservedVersion < fact.LockVersion {
			var task Task
			task, taskErr = s.tasks.Observe(txContext, *current, fact, now)
			taskID = task.ID
		}
		if taskErr != nil {
			return taskErr
		}
		result := ResultNoop
		if changed {
			result = ResultApplied
			outcome = OutcomeApplied
		}
		return s.inbox.Finish(txContext, event.EventID, result)
	})
	if s.logger != nil {
		attributes := []any{"trace_id", event.TraceID, "event_id", event.EventID, "task_id", taskID, "worker_id", s.workerID}
		if err != nil {
			s.logger.Error("文章异步任务投影失败", append(attributes, "error", truncateProjectionError(err))...)
		} else {
			s.logger.Info("文章异步任务投影完成", append(attributes, "result", outcome)...)
		}
	}
	return outcome, err
}

func truncateProjectionError(err error) string {
	message := amqpCredentialPattern.ReplaceAllString(err.Error(), `${1}***@`)
	const limit = 256
	if len(message) > limit {
		return message[:limit] + "…"
	}
	return message
}
