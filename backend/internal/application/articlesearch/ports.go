package articlesearch

import (
	"context"
	"time"

	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
)

type QueryIndex interface {
	CreatePIT(context.Context, time.Duration) (string, error)
	Search(context.Context, IndexRequest) (CandidateBatch, error)
	ClosePIT(context.Context, string) error
}

type PublicArticleReader interface {
	ListPublishedByIDs(context.Context, []int64) ([]articleDomain.ListItem, error)
}

type Result string

const (
	ResultSuccess               Result = "success"
	ResultValidation            Result = "validation"
	ResultInvalidCursor         Result = "invalid_cursor"
	ResultSearchUnavailable     Result = "search_unavailable"
	ResultDependencyUnavailable Result = "dependency_unavailable"
	ResultInternal              Result = "internal"
)

type Stage string

const (
	StageTotal      Stage = "total"
	StageOpenSearch Stage = "opensearch"
	StagePostgres   Stage = "postgres"
)

type PITOperation string

const (
	PITCreate  PITOperation = "create"
	PITDelete  PITOperation = "delete"
	PITFailure PITOperation = "failure"
)

type Observer interface {
	ObserveRequest(context.Context, Result, time.Duration)
	ObserveStage(context.Context, Stage, time.Duration)
	AddCandidates(context.Context, int)
	AddFiltered(context.Context, int)
	ObserveScanLimit(context.Context)
	ObservePIT(context.Context, PITOperation)
}

type NopObserver struct{}

func (NopObserver) ObserveRequest(context.Context, Result, time.Duration) {}
func (NopObserver) ObserveStage(context.Context, Stage, time.Duration)    {}
func (NopObserver) AddCandidates(context.Context, int)                    {}
func (NopObserver) AddFiltered(context.Context, int)                      {}
func (NopObserver) ObserveScanLimit(context.Context)                      {}
func (NopObserver) ObservePIT(context.Context, PITOperation)              {}
