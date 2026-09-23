// Package asynctask 定义文章异步任务投影的应用模型与端口。
package asynctask

import (
	"context"
	"errors"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articleevent"
)

const ConsumerName = "async-task-projector.v1"

type ConsumeResult string

const (
	ResultApplied ConsumeResult = "applied"
	ResultNoop    ConsumeResult = "noop"
)

var ErrInvalidConsumerName = errors.New("逻辑消费者名称无效")

type Inbox interface {
	Start(context.Context, articleevent.Envelope) (first bool, err error)
	Finish(context.Context, string, ConsumeResult) error
}

type ArticleFact struct {
	ArticleID   int64
	Origin      string
	Status      string
	RevisionID  int64
	RevisionNo  int
	ContentHash string
	LockVersion int64
}

type Task struct {
	ID              string
	ArticleID       int64
	Status          string
	Generation      int64
	RevisionID      int64
	RevisionNo      int
	ContentHash     string
	ObservedVersion int64
}

type TaskStore interface {
	LockArticle(context.Context, int64) (ArticleFact, error)
	LockTask(context.Context, int64) (*Task, error)
	CreatePending(context.Context, string, ArticleFact, time.Time) (Task, error)
	SetPending(context.Context, Task, ArticleFact, time.Time) (Task, error)
	Cancel(context.Context, Task, ArticleFact, time.Time) (Task, error)
	Observe(context.Context, Task, ArticleFact, time.Time) (Task, error)
	UpdateIfGeneration(context.Context, string, int64, string, time.Time) (bool, error)
}
