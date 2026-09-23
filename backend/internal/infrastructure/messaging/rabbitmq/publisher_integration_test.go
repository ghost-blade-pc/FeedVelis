package rabbitmq

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articleevent"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/relay"
	amqp "github.com/rabbitmq/amqp091-go"
)

func TestPublisherConfirmAndMandatoryReturn(t *testing.T) {
	url := os.Getenv("VELIS_TEST_RABBITMQ_URL")
	if url == "" {
		t.Skip("未设置 VELIS_TEST_RABBITMQ_URL")
	}
	connection, err := amqp.Dial(url)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	channel, err := connection.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer channel.Close()
	exchange := "velis.test.events." + time.Now().Format("150405.000000000")
	if err := channel.ExchangeDeclare(exchange, "topic", false, false, false, false, nil); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = channel.ExchangeDelete(exchange, false, false) }()
	queue, err := channel.QueueDeclare("", false, true, true, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := channel.QueueBind(queue.Name, "article.*.v1", exchange, false, nil); err != nil {
		t.Fatal(err)
	}
	publisher, err := NewPublisherOnChannel(channel, exchange)
	if err != nil {
		t.Fatal(err)
	}
	event, _ := articleevent.Deleted(context.Background(), 42, 81, 7, time.Now())
	if result, err := publisher.Publish(context.Background(), event); err != nil || result != relay.PublishConfirmed {
		t.Fatalf("可路由 result=%s err=%v", result, err)
	}
	if err := channel.QueueUnbind(queue.Name, "article.*.v1", exchange, nil); err != nil {
		t.Fatal(err)
	}
	if result, err := publisher.Publish(context.Background(), event); err != nil || result != relay.PublishReturned {
		t.Fatalf("不可路由 result=%s err=%v", result, err)
	}
}

func TestDeclareTopologyIsIdempotentAndRejectsIncompatibleQueue(t *testing.T) {
	url := os.Getenv("VELIS_TEST_RABBITMQ_URL")
	if url == "" {
		t.Skip("未设置 VELIS_TEST_RABBITMQ_URL")
	}
	connection, err := amqp.Dial(url)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	for attempt := 0; attempt < 2; attempt++ {
		channel, channelErr := connection.Channel()
		if channelErr != nil {
			t.Fatal(channelErr)
		}
		if declareErr := DeclareTopology(channel); declareErr != nil {
			t.Fatalf("第 %d 次声明拓扑: %v", attempt+1, declareErr)
		}
		_ = channel.Close()
	}
	channel, err := connection.Channel()
	if err != nil {
		t.Fatal(err)
	}
	_, err = channel.QueueDeclare(ConsumerQueue, true, false, false, false, amqp.Table{"x-queue-type": "classic"})
	if err == nil || !strings.Contains(err.Error(), "PRECONDITION_FAILED") {
		t.Fatalf("不兼容声明应只关闭当前 MQ channel: %v", err)
	}
	// 不兼容 channel 关闭后，新 channel 仍可按稳定声明继续工作。
	recovered, err := connection.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	if err := DeclareTopology(recovered); err != nil {
		t.Fatal(err)
	}
}

func TestPublisherRecoversAfterBrokerRestart(t *testing.T) {
	url := os.Getenv("VELIS_TEST_RABBITMQ_URL")
	if url == "" || os.Getenv("VELIS_TEST_RABBITMQ_RESTART") != "1" {
		t.Skip("未启用 RabbitMQ 重启演练")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	publisher, err := DialPublisher(url)
	if err != nil {
		t.Fatal(err)
	}
	event, err := articleevent.Deleted(ctx, 42, 81, 8, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if result, publishErr := publisher.Publish(ctx, event); publishErr != nil || result != relay.PublishConfirmed {
		t.Fatalf("重启前发布 result=%s err=%v", result, publishErr)
	}
	t.Log("RABBITMQ_RESTART_READY")
	select {
	case <-publisher.NotifyClose(make(chan *amqp.Error, 1)):
	case <-ctx.Done():
		_ = publisher.Close()
		t.Fatal("等待 Broker 断开连接超时")
	}
	_ = publisher.Close()
	var recovered *Publisher
	for ctx.Err() == nil {
		recovered, err = DialPublisher(url)
		if err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if recovered == nil {
		t.Fatalf("Broker 重启后重连失败: %v", err)
	}
	defer recovered.Close()
	event, err = articleevent.Deleted(ctx, 42, 81, 9, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if result, publishErr := recovered.Publish(ctx, event); publishErr != nil || result != relay.PublishConfirmed {
		t.Fatalf("重启后发布 result=%s err=%v", result, publishErr)
	}
}
