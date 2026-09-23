package relay

import (
	"context"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articleevent"
)

type ClaimedEvent struct {
	Event      articleevent.Envelope
	LeaseToken string
	Attempts   int
}

type OutboxStore interface {
	Claim(context.Context, string, int, time.Duration) ([]ClaimedEvent, error)
	Confirm(context.Context, string, string) (bool, error)
	Fail(context.Context, string, string, string, string, time.Duration) (bool, error)
}

type PublishResult string

const (
	PublishConfirmed PublishResult = "confirmed"
	PublishReturned  PublishResult = "returned"
	PublishNacked    PublishResult = "nacked"
)

type Publisher interface {
	Publish(context.Context, articleevent.Envelope) (PublishResult, error)
}
