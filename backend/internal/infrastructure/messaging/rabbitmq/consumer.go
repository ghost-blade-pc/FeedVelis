package rabbitmq

import (
	"context"
	"errors"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articleevent"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/asynctask"
)

var ErrDatabaseUnavailable = errors.New("PostgreSQL 整体不可用")

type Projector interface {
	Project(context.Context, articleevent.Envelope) (asynctask.Outcome, error)
}
type Delivery interface {
	Body() []byte
	Ack() error
	Reject(bool) error
	Nack(bool) error
}

type Consumer struct {
	url               string
	prefetch          int
	projector         Projector
	isDatabaseFailure func(error) bool
}

func NewConsumer(url string, prefetch int, projector Projector, isDatabaseFailure func(error) bool) *Consumer {
	return &Consumer{url: url, prefetch: prefetch, projector: projector, isDatabaseFailure: isDatabaseFailure}
}

func (c *Consumer) Handle(ctx context.Context, delivery Delivery) error {
	event, err := articleevent.Decode(delivery.Body())
	if err != nil {
		return delivery.Reject(false)
	}
	_, err = c.projector.Project(ctx, event)
	if err == nil {
		return delivery.Ack()
	}
	if c.isDatabaseFailure != nil && c.isDatabaseFailure(err) {
		return fmt.Errorf("%w: %v", ErrDatabaseUnavailable, err)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	// RabbitMQ 4.3 的 quorum delivery-limit 只累计失败投递；AMQP 0-9-1
	// basic.nack(requeue=true) 仅算显式返回，不推进 delivery-count。使用
	// basic.reject(requeue=true) 才能在未分类单消息错误上最终进入 DLQ。
	return delivery.Reject(true)
}

func (c *Consumer) Run(ctx context.Context) error {
	connection, err := amqp.Dial(c.url)
	if err != nil {
		return err
	}
	defer connection.Close()
	channel, err := connection.Channel()
	if err != nil {
		return err
	}
	defer channel.Close()
	if err := DeclareTopology(channel); err != nil {
		return err
	}
	if err := channel.Qos(c.prefetch, 0, false); err != nil {
		return fmt.Errorf("设置 prefetch: %w", err)
	}
	deliveries, err := channel.Consume(ConsumerQueue, "", false, false, false, false, nil)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case delivery, ok := <-deliveries:
			if !ok {
				return errors.New("RabbitMQ delivery channel 已关闭")
			}
			wrapped := amqpDelivery{delivery: delivery}
			if err := c.Handle(ctx, wrapped); err != nil {
				if errors.Is(err, ErrDatabaseUnavailable) {
					return err
				}
				if ctx.Err() != nil {
					return nil
				}
			}
		}
	}
}

type amqpDelivery struct{ delivery amqp.Delivery }

func (d amqpDelivery) Body() []byte              { return d.delivery.Body }
func (d amqpDelivery) Ack() error                { return d.delivery.Ack(false) }
func (d amqpDelivery) Reject(requeue bool) error { return d.delivery.Reject(requeue) }
func (d amqpDelivery) Nack(requeue bool) error   { return d.delivery.Nack(false, requeue) }
