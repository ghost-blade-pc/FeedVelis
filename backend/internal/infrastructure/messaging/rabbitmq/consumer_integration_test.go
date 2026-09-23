package rabbitmq

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articleevent"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/asynctask"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/relay"
	amqp "github.com/rabbitmq/amqp091-go"
)

type failingProjector struct{}

func (failingProjector) Project(context.Context, articleevent.Envelope) (asynctask.Outcome, error) {
	return "", errors.New("注入的单消息处理失败")
}

type labeledProjector struct {
	label string
	seen  chan<- string
}

func (p labeledProjector) Project(ctx context.Context, _ articleevent.Envelope) (asynctask.Outcome, error) {
	select {
	case p.seen <- p.label:
		return asynctask.OutcomeNoop, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func TestConsumerRedeliveryAndDeliveryLimitToDLQ(t *testing.T) {
	url := os.Getenv("VELIS_TEST_RABBITMQ_URL")
	if url == "" {
		t.Skip("未设置 VELIS_TEST_RABBITMQ_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	connection, channel := openTestRabbit(t, url)
	if _, err := channel.QueuePurge(ConsumerQueue, false); err != nil {
		t.Fatal(err)
	}
	if _, err := channel.QueuePurge(DeadQueue, false); err != nil {
		t.Fatal(err)
	}
	event, err := articleevent.Deleted(ctx, 42, 81, 7, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	publisher, err := NewPublisherOnChannel(channel, EventExchange)
	if err != nil {
		t.Fatal(err)
	}
	if result, publishErr := publisher.Publish(ctx, event); publishErr != nil || result != relay.PublishConfirmed {
		t.Fatalf("发布 result=%s err=%v", result, publishErr)
	}
	delivery, ok, err := channel.Get(ConsumerQueue, false)
	if err != nil || !ok {
		t.Fatalf("首次领取 ok=%t err=%v", ok, err)
	}
	if delivery.Redelivered {
		t.Fatal("首次领取不应标记 redelivered")
	}
	// 断开连接且不 Ack；Broker 必须把未确认消息重新入队。
	_ = connection.Close()
	connection, channel = openTestRabbit(t, url)
	defer connection.Close()
	redelivered := getDeliveryBefore(t, ctx, channel, ConsumerQueue)
	if !redelivered.Redelivered {
		t.Fatal("断连前未 Ack 的消息没有标记为重投")
	}
	if err := redelivered.Ack(false); err != nil {
		t.Fatal(err)
	}

	if _, err := channel.QueuePurge(ConsumerQueue, false); err != nil {
		t.Fatal(err)
	}
	if _, err := channel.QueuePurge(DeadQueue, false); err != nil {
		t.Fatal(err)
	}
	publisher, err = NewPublisherOnChannel(channel, EventExchange)
	if err != nil {
		t.Fatal(err)
	}
	if result, publishErr := publisher.Publish(ctx, event); publishErr != nil || result != relay.PublishConfirmed {
		t.Fatalf("再次发布 result=%s err=%v", result, publishErr)
	}
	consumer := NewConsumer(url, 16, failingProjector{}, func(error) bool { return false })
	consumerChannel, err := connection.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer consumerChannel.Close()
	if err := consumerChannel.Qos(16, 0, false); err != nil {
		t.Fatal(err)
	}
	deliveries, err := consumerChannel.Consume(ConsumerQueue, "delivery-limit-test", false, false, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	attempts := 0
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for ctx.Err() == nil {
		select {
		case message, open := <-deliveries:
			if !open {
				t.Fatal("delivery channel 提前关闭")
			}
			attempts++
			if handleErr := consumer.Handle(context.Background(), amqpDelivery{delivery: message}); handleErr != nil {
				t.Fatal(handleErr)
			}
		case <-ticker.C:
			dead, found, getErr := channel.Get(DeadQueue, true)
			if getErr != nil {
				t.Fatal(getErr)
			}
			if found {
				decoded, decodeErr := articleevent.Decode(dead.Body)
				if decodeErr != nil || decoded.EventID != event.EventID {
					t.Fatalf("DLQ 消息不一致: event=%+v err=%v", decoded, decodeErr)
				}
				if attempts < 5 {
					t.Fatalf("delivery-limit 过早触发: attempts=%d", attempts)
				}
				return
			}
		case <-ctx.Done():
		}
	}
	t.Fatalf("等待 delivery-limit/DLQ 超时，尝试次数=%d", attempts)
}

func TestMultipleConsumersCompeteForDeliveries(t *testing.T) {
	url := os.Getenv("VELIS_TEST_RABBITMQ_URL")
	if url == "" {
		t.Skip("未设置 VELIS_TEST_RABBITMQ_URL")
	}
	connection, channel := openTestRabbit(t, url)
	defer connection.Close()
	if _, err := channel.QueuePurge(ConsumerQueue, false); err != nil {
		t.Fatal(err)
	}
	if _, err := channel.QueuePurge(DeadQueue, false); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	seen := make(chan string, 6)
	done := make(chan error, 2)
	for _, label := range []string{"worker-a", "worker-b"} {
		consumer := NewConsumer(url, 1, labeledProjector{label: label, seen: seen}, func(error) bool { return false })
		go func() { done <- consumer.Run(ctx) }()
	}
	for ctx.Err() == nil {
		queue, err := channel.QueueInspect(ConsumerQueue)
		if err != nil {
			t.Fatal(err)
		}
		if queue.Consumers == 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	publisher, err := NewPublisherOnChannel(channel, EventExchange)
	if err != nil {
		t.Fatal(err)
	}
	for version := int64(1); version <= 6; version++ {
		event, eventErr := articleevent.Deleted(ctx, 42, 81, version, time.Now().UTC())
		if eventErr != nil {
			t.Fatal(eventErr)
		}
		if result, publishErr := publisher.Publish(ctx, event); publishErr != nil || result != relay.PublishConfirmed {
			t.Fatalf("发布 %d result=%s err=%v", version, result, publishErr)
		}
	}
	counts := map[string]int{}
	for total := 0; total < 6; total++ {
		select {
		case label := <-seen:
			counts[label]++
		case <-ctx.Done():
			t.Fatalf("等待多 Consumer 竞争超时: %+v", counts)
		}
	}
	if counts["worker-a"] == 0 || counts["worker-b"] == 0 {
		t.Fatalf("消息未由两个 Worker 竞争消费: %+v", counts)
	}
	cancel()
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func openTestRabbit(t *testing.T, url string) (*amqp.Connection, *amqp.Channel) {
	t.Helper()
	connection, err := amqp.Dial(url)
	if err != nil {
		t.Fatal(err)
	}
	channel, err := connection.Channel()
	if err != nil {
		_ = connection.Close()
		t.Fatal(err)
	}
	if err := DeclareTopology(channel); err != nil {
		_ = channel.Close()
		_ = connection.Close()
		t.Fatal(err)
	}
	return connection, channel
}

func getDeliveryBefore(t *testing.T, ctx context.Context, channel *amqp.Channel, queue string) amqp.Delivery {
	t.Helper()
	for ctx.Err() == nil {
		delivery, found, err := channel.Get(queue, false)
		if err != nil {
			t.Fatal(err)
		}
		if found {
			return delivery
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("等待 RabbitMQ 消息超时")
	return amqp.Delivery{}
}
