package dlq

import (
	"context"
	"errors"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articleevent"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/relay"
	"testing"
	"time"
)

type messageFake struct {
	body      []byte
	ack, nack int
}

func (m *messageFake) Body() []byte    { return m.body }
func (m *messageFake) Ack() error      { m.ack++; return nil }
func (m *messageFake) Nack(bool) error { m.nack++; return nil }

type sourceFake struct{ messages []*messageFake }

func (s *sourceFake) Get(context.Context) (Message, bool, error) {
	if len(s.messages) == 0 {
		return nil, false, nil
	}
	m := s.messages[0]
	s.messages = s.messages[1:]
	return m, true, nil
}

type publisherFake struct {
	result relay.PublishResult
	err    error
	ids    []string
}

func (p *publisherFake) Publish(_ context.Context, e articleevent.Envelope) (relay.PublishResult, error) {
	p.ids = append(p.ids, e.EventID)
	return p.result, p.err
}
func replayBody() []byte {
	event, _ := articleevent.Deleted(context.Background(), 42, 81, 7, time.Now())
	data, _ := articleevent.Encode(event)
	return data
}
func TestReplayAckOnlyAfterConfirmAndPreservesEventID(t *testing.T) {
	message := &messageFake{body: replayBody()}
	source := &sourceFake{messages: []*messageFake{message}}
	publisher := &publisherFake{result: relay.PublishConfirmed}
	report, err := NewService(source, publisher).Run(context.Background(), 1)
	if err != nil || report.Confirmed != 1 || message.ack != 1 || message.nack != 0 {
		t.Fatalf("report=%+v message=%+v err=%v", report, message, err)
	}
	event, _ := articleevent.Decode(message.body)
	if publisher.ids[0] != event.EventID {
		t.Fatal("event_id 未保留")
	}
}
func TestReplayFailureAndInvalidMessageRemainInDLQ(t *testing.T) {
	for _, test := range []struct {
		name      string
		body      []byte
		publisher *publisherFake
	}{{"发布失败", replayBody(), &publisherFake{err: errors.New("down")}}, {"非法事件", []byte(`{"bad":true}`), &publisherFake{result: relay.PublishConfirmed}}} {
		t.Run(test.name, func(t *testing.T) {
			message := &messageFake{body: test.body}
			report, err := NewService(&sourceFake{messages: []*messageFake{message}}, test.publisher).Run(context.Background(), 1)
			if err == nil || report.Failed != 1 || message.ack != 0 || message.nack != 1 {
				t.Fatalf("report=%+v message=%+v err=%v", report, message, err)
			}
		})
	}
}

func TestReplayHonorsCancellationBeforeReading(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	source := &sourceFake{messages: []*messageFake{{body: replayBody()}}}
	report, err := NewService(source, &publisherFake{result: relay.PublishConfirmed}).Run(ctx, 1)
	if !errors.Is(err, context.Canceled) || report.Confirmed != 0 || len(source.messages) != 1 {
		t.Fatalf("report=%+v remaining=%d err=%v", report, len(source.messages), err)
	}
}
