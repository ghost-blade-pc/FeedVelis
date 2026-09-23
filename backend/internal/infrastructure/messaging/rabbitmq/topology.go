package rabbitmq

import (
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	EventExchange  = "velis.events.v1"
	ConsumerQueue  = "velis.async-task-projector.v1"
	DeadExchange   = "velis.dlx.v1"
	DeadQueue      = "velis.async-task-projector.dlq.v1"
	DeadRoutingKey = "async-task-projector.dead.v1"
)

// DeclareTopology 声明应用拥有的稳定拓扑。delivery-limit 与 at-least-once DLX 由运维 policy 管理。
func DeclareTopology(channel *amqp.Channel) error {
	if channel == nil {
		return fmt.Errorf("RabbitMQ channel 不能为空")
	}
	if err := channel.ExchangeDeclare(EventExchange, "topic", true, false, false, false, nil); err != nil {
		return fmt.Errorf("声明事件 exchange: %w", err)
	}
	if err := channel.ExchangeDeclare(DeadExchange, "topic", true, false, false, false, nil); err != nil {
		return fmt.Errorf("声明 DLX: %w", err)
	}
	if _, err := channel.QueueDeclare(ConsumerQueue, true, false, false, false, amqp.Table{"x-queue-type": "quorum"}); err != nil {
		return fmt.Errorf("声明消费队列（请核对 policy 前置条件）: %w", err)
	}
	if _, err := channel.QueueDeclare(DeadQueue, true, false, false, false, amqp.Table{"x-queue-type": "quorum"}); err != nil {
		return fmt.Errorf("声明 DLQ: %w", err)
	}
	if err := channel.QueueBind(ConsumerQueue, "article.*.v1", EventExchange, false, nil); err != nil {
		return fmt.Errorf("绑定消费队列: %w", err)
	}
	if err := channel.QueueBind(DeadQueue, DeadRoutingKey, DeadExchange, false, nil); err != nil {
		return fmt.Errorf("绑定 DLQ: %w", err)
	}
	return nil
}
