package rabbitmq

import (
	"context"
	"fmt"
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articleevent"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/relay"
)

type Publisher struct {
	connection *amqp.Connection
	channel    *amqp.Channel
	exchange   string
	returns    <-chan amqp.Return
	mu         sync.Mutex
}

func DialPublisher(url string) (*Publisher, error) {
	connection, err := amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("连接 RabbitMQ: %w", err)
	}
	channel, err := connection.Channel()
	if err != nil {
		_ = connection.Close()
		return nil, err
	}
	if err := DeclareTopology(channel); err != nil {
		_ = channel.Close()
		_ = connection.Close()
		return nil, err
	}
	publisher, err := NewPublisherOnChannel(channel, EventExchange)
	if err != nil {
		_ = channel.Close()
		_ = connection.Close()
		return nil, err
	}
	publisher.connection = connection
	return publisher, nil
}

func NewPublisherOnChannel(channel *amqp.Channel, exchange string) (*Publisher, error) {
	if channel == nil || exchange == "" {
		return nil, fmt.Errorf("Publisher 参数无效")
	}
	if err := channel.Confirm(false); err != nil {
		return nil, fmt.Errorf("启用 publisher confirm: %w", err)
	}
	return &Publisher{channel: channel, exchange: exchange, returns: channel.NotifyReturn(make(chan amqp.Return, 1))}, nil
}

func (p *Publisher) Publish(ctx context.Context, event articleevent.Envelope) (relay.PublishResult, error) {
	data, err := articleevent.Encode(event)
	if err != nil {
		return "", err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	confirmation, err := p.channel.PublishWithDeferredConfirmWithContext(ctx, p.exchange, event.EventType, true, false, amqp.Publishing{DeliveryMode: amqp.Persistent, ContentType: "application/json", MessageId: event.EventID, Type: event.EventType, Timestamp: event.OccurredAt, Body: data})
	if err != nil {
		return "", err
	}
	if confirmation == nil {
		return "", fmt.Errorf("Broker 未启用 confirm")
	}
	ack, err := confirmation.WaitContext(ctx)
	if err != nil {
		return "", err
	}
	select {
	case returned, ok := <-p.returns:
		if !ok {
			return "", fmt.Errorf("RabbitMQ return 通道已关闭")
		}
		if returned.MessageId == event.EventID {
			return relay.PublishReturned, nil
		}
		return "", fmt.Errorf("收到不匹配的 return: message_id=%q want=%q", returned.MessageId, event.EventID)
	default:
	}
	if !ack {
		return relay.PublishNacked, nil
	}
	return relay.PublishConfirmed, nil
}

func (p *Publisher) NotifyClose(receiver chan *amqp.Error) <-chan *amqp.Error {
	if p.connection != nil {
		return p.connection.NotifyClose(receiver)
	}
	return p.channel.NotifyClose(receiver)
}
func (p *Publisher) Close() error {
	if p.channel != nil {
		_ = p.channel.Close()
	}
	if p.connection != nil {
		return p.connection.Close()
	}
	return nil
}
