package rabbitmq

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articleevent"
	amqp "github.com/rabbitmq/amqp091-go"
)

func TestDLQReplayPreservesEventAndKeepsFailure(t *testing.T) {
	url := os.Getenv("VELIS_TEST_RABBITMQ_URL")
	if url == "" {
		t.Skip("未设置 VELIS_TEST_RABBITMQ_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	connection, channel := openTestRabbit(t, url)
	defer connection.Close()
	if _, err := channel.QueuePurge(ConsumerQueue, false); err != nil {
		t.Fatal(err)
	}
	if _, err := channel.QueuePurge(DeadQueue, false); err != nil {
		t.Fatal(err)
	}
	event, err := articleevent.Deleted(ctx, 42, 81, 10, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	body, err := articleevent.Encode(event)
	if err != nil {
		t.Fatal(err)
	}
	publishDirectToQueue(t, ctx, channel, DeadQueue, body)
	report, err := (DLQReplay{URL: url}).Run(ctx, 1)
	if err != nil || report.Confirmed != 1 || report.Failed != 0 {
		t.Fatalf("重放报告=%+v err=%v", report, err)
	}
	replayed := getDeliveryBefore(t, ctx, channel, ConsumerQueue)
	decoded, err := articleevent.Decode(replayed.Body)
	if err != nil || decoded.EventID != event.EventID || decoded.EventType != event.EventType {
		t.Fatalf("重放事件=%+v err=%v", decoded, err)
	}
	if err := replayed.Ack(false); err != nil {
		t.Fatal(err)
	}
	if queue, err := channel.QueueInspect(DeadQueue); err != nil || queue.Messages != 0 {
		t.Fatalf("成功后 DLQ messages=%d err=%v", queue.Messages, err)
	}

	publishDirectToQueue(t, ctx, channel, DeadQueue, []byte(`{"bad":true}`))
	report, err = (DLQReplay{URL: url}).Run(ctx, 1)
	if err == nil || report.Failed != 1 || report.Confirmed != 0 {
		t.Fatalf("非法事件报告=%+v err=%v", report, err)
	}
	failed := getDeliveryBefore(t, ctx, channel, DeadQueue)
	if string(failed.Body) != `{"bad":true}` {
		t.Fatalf("失败消息正文被改变: %q", failed.Body)
	}
	if err := failed.Ack(false); err != nil {
		t.Fatal(err)
	}
}

func publishDirectToQueue(t *testing.T, ctx context.Context, channel *amqp.Channel, queue string, body []byte) {
	t.Helper()
	if err := channel.PublishWithContext(ctx, "", queue, false, false, amqp.Publishing{
		DeliveryMode: amqp.Persistent,
		ContentType:  "application/json",
		Body:         body,
	}); err != nil {
		t.Fatal(err)
	}
}
