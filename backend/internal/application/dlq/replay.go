package dlq

import (
	"context"
	"fmt"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articleevent"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/relay"
)

type Message interface {
	Body() []byte
	Ack() error
	Nack(bool) error
}
type Source interface {
	Get(context.Context) (Message, bool, error)
}
type Report struct {
	Confirmed int
	Failed    int
}
type Service struct {
	source    Source
	publisher relay.Publisher
}

func NewService(source Source, publisher relay.Publisher) *Service {
	return &Service{source: source, publisher: publisher}
}
func (s *Service) Run(ctx context.Context, limit int) (Report, error) {
	if limit < 1 || limit > 1000 {
		return Report{}, fmt.Errorf("limit 必须介于 1 和 1000")
	}
	report := Report{}
	for index := 0; index < limit; index++ {
		if ctx.Err() != nil {
			return report, ctx.Err()
		}
		message, ok, err := s.source.Get(ctx)
		if err != nil {
			return report, err
		}
		if !ok {
			return report, nil
		}
		event, err := articleevent.Decode(message.Body())
		if err != nil {
			report.Failed++
			_ = message.Nack(true)
			return report, fmt.Errorf("DLQ 消息契约无效")
		}
		result, err := s.publisher.Publish(ctx, event)
		if err != nil || result != relay.PublishConfirmed {
			report.Failed++
			_ = message.Nack(true)
			if err == nil {
				err = fmt.Errorf("重发未获得 confirm")
			}
			return report, err
		}
		if err = message.Ack(); err != nil {
			report.Failed++
			return report, err
		}
		report.Confirmed++
	}
	return report, nil
}
