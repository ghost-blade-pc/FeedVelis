package ports

import (
	"context"
	"errors"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articleevent"
)

var ErrTransactionRequired = errors.New("Outbox 写入必须位于业务事务中")

// Outbox 只表达应用事件持久化，不暴露 SQL、pgx 或 AMQP 类型。
type Outbox interface {
	Append(context.Context, articleevent.Envelope) error
	Get(context.Context, string) (articleevent.Envelope, error)
	ExistsAggregateEvent(context.Context, string, int64, string) (bool, error)
}
