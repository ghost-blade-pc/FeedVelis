package rabbitmq

import (
	"context"
	"fmt"
	"strings"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/dlq"
	amqp "github.com/rabbitmq/amqp091-go"
)

type DLQReplay struct{ URL string }

func (r DLQReplay) Run(ctx context.Context, limit int) (dlq.Report, error) {
	if strings.TrimSpace(r.URL) == "" {
		return dlq.Report{}, fmt.Errorf("未配置 RabbitMQ，无法重放 DLQ")
	}
	connection, err := amqp.Dial(r.URL)
	if err != nil {
		return dlq.Report{}, err
	}
	defer connection.Close()
	channel, err := connection.Channel()
	if err != nil {
		return dlq.Report{}, err
	}
	defer channel.Close()
	if err = DeclareTopology(channel); err != nil {
		return dlq.Report{}, err
	}
	publisher, err := NewPublisherOnChannel(channel, EventExchange)
	if err != nil {
		return dlq.Report{}, err
	}
	return dlq.NewService(dlqSource{channel: channel}, publisher).Run(ctx, limit)
}

type dlqSource struct{ channel *amqp.Channel }

func (s dlqSource) Get(_ context.Context) (dlq.Message, bool, error) {
	delivery, ok, err := s.channel.Get(DeadQueue, false)
	if err != nil || !ok {
		return nil, ok, err
	}
	return replayDelivery{delivery: delivery}, true, nil
}

type replayDelivery struct{ delivery amqp.Delivery }

func (d replayDelivery) Body() []byte            { return d.delivery.Body }
func (d replayDelivery) Ack() error              { return d.delivery.Ack(false) }
func (d replayDelivery) Nack(requeue bool) error { return d.delivery.Nack(false, requeue) }
