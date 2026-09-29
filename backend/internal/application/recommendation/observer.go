package recommendation

import (
	"context"
	"time"
)

type Observer interface {
	ObserveRequest(context.Context, string, string, string, time.Duration)
	AddRecall(context.Context, string, int)
	AddFiltered(context.Context, int)
	AddLatest(context.Context, int)
	AddSource(context.Context, string, int)
}

type NopObserver struct{}

func (NopObserver) ObserveRequest(context.Context, string, string, string, time.Duration) {}
func (NopObserver) AddRecall(context.Context, string, int)                                {}
func (NopObserver) AddFiltered(context.Context, int)                                      {}
func (NopObserver) AddLatest(context.Context, int)                                        {}
func (NopObserver) AddSource(context.Context, string, int)                                {}
