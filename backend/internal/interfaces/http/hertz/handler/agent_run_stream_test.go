package handler

import (
	"bufio"
	"context"
	"fmt"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/network/standard"
	accountApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/account"
	agent "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentrun"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentevidence"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/middleware"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

const streamRID = "31000000-0000-4000-8000-000000000001"

type streamIdentity struct{}

func (streamIdentity) Authenticate(context.Context, string) (accountApp.Identity, error) {
	return accountApp.Identity{User: account.User{ID: account.UUID{1}}, Session: account.Session{ID: account.UUID{2}}, AccessExpiresAt: time.Now().Add(time.Minute)}, nil
}

type streamService struct {
	AgentRunService
	large    bool
	deadline chan time.Duration
}

func (s streamService) Events(ctx context.Context, _ agent.Caller, _, rid string, after int64) (agent.EventBatch, error) {
	if s.deadline != nil {
		d, _ := ctx.Deadline()
		select {
		case s.deadline <- time.Until(d):
		default:
		}
	}
	b := agent.EventBatch{Events: []agent.Event{}}
	if s.large {
		b.LastSequence = 15
		b.Terminal = true
		for i := after + 1; i <= 15; i++ {
			b.Events = append(b.Events, agent.Event{Version: 1, RunID: rid, Sequence: i, Kind: agent.AnswerBlock, Block: &agentevidence.Block{ID: fmt.Sprint(i), Text: strings.Repeat("x", 32*1024), EvidenceIDs: []string{}}})
		}
	}
	return b, nil
}

type smallWriteListener struct{ net.Listener }

func (l smallWriteListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil {
		_ = c.(*net.TCPConn).SetWriteBuffer(1024)
	}
	return c, err
}
func streamServer(t *testing.T, s streamService) (string, *AgentRun) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var h *server.Hertz
	if s.large {
		h = server.New(server.WithListener(smallWriteListener{ln}), server.WithTransport(standard.NewTransporter), server.WithDisablePrintRoute(true))
	} else {
		h = server.New(server.WithListener(ln), server.WithSenseClientDisconnection(true), server.WithDisablePrintRoute(true))
	}
	endpoint := NewAgentRun(s)
	h.GET("/api/v1/me/agent/conversations/:id/runs/:run_id/events", middleware.Authenticate(streamIdentity{}), endpoint.Events)
	go func() { _ = h.Run() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		_ = h.Shutdown(ctx)
	})
	return ln.Addr().String(), endpoint
}
func TestAgentRunSubscriptionInstanceLimit(t *testing.T) {
	h := NewAgentRun(nil)
	for i := 0; i < 64; i++ {
		if !h.acquire(fmt.Sprint(i / 4)) {
			t.Fatal(i)
		}
	}
	if h.acquire("new") {
		t.Fatal("实例额度超过64")
	}
	for i := 0; i < 64; i++ {
		h.release(fmt.Sprint(i / 4))
	}
	if h.active != 0 || len(h.accounts) != 0 {
		t.Fatal("未释放", h.active, h.accounts)
	}
}
func TestAgentRunSlowTCPWriterBoundAndParallelSubscription(t *testing.T) {
	address, h := streamServer(t, streamService{large: true})
	conn, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.(*net.TCPConn).SetReadBuffer(1024)
	path := "/api/v1/me/agent/conversations/" + streamRID + "/runs/" + streamRID + "/events"
	started := time.Now()
	fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: localhost\r\nAuthorization: Bearer local\r\n\r\n", path)
	time.Sleep(100 * time.Millisecond)
	req, _ := http.NewRequest("GET", "http://"+address+path, nil)
	req.Header.Set("Authorization", "Bearer local")
	client := &http.Client{Timeout: 3 * time.Second}
	r, err := client.Do(req)
	if err != nil {
		t.Fatal("慢订阅阻塞并行订阅", err)
	}
	if r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	r.Body.Close()
	deadline := started.Add(7 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		active := h.active
		h.mu.Unlock()
		if active == 0 {
			elapsed := time.Since(started)
			if elapsed < 4*time.Second {
				t.Fatalf("未触发真实阻塞 %v", elapsed)
			}
			t.Logf("慢 TCP 写阻塞后释放订阅: %v", elapsed)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("五秒写超时未释放订阅")
}
func TestAgentRunHeartbeatAndNinetySecondDeadline(t *testing.T) {
	deadlines := make(chan time.Duration, 1)
	address, _ := streamServer(t, streamService{deadline: deadlines})
	req, _ := http.NewRequest("GET", "http://"+address+"/api/v1/me/agent/conversations/"+streamRID+"/runs/"+streamRID+"/events", nil)
	req.Header.Set("Authorization", "Bearer local")
	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	scan := bufio.NewScanner(resp.Body)
	var first time.Time
	for scan.Scan() {
		if strings.HasPrefix(scan.Text(), ":") {
			if first.IsZero() {
				first = time.Now()
				continue
			}
			gap := time.Since(first)
			if gap < 9*time.Second || gap > 11*time.Second {
				t.Fatal(gap)
			}
			d := <-deadlines
			if d < 89*time.Second || d > 90*time.Second {
				t.Fatal("订阅期限", d)
			}
			t.Logf("心跳间隔=%v，初始订阅deadline=%v", gap, d)
			return
		}
	}
	t.Fatal("心跳缺失", scan.Err())
}
