package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	accountApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/account"
	conversationApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentconversation"
	agent "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentrun"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
	run "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/handler"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/middleware"
)

type runHTTPIdentity struct{ expires time.Time }

func (r runHTTPIdentity) Authenticate(ctx context.Context, token string) (accountApp.Identity, error) {
	if token != "local" {
		return accountApp.Identity{}, account.ErrSessionInvalid
	}
	uid, _ := account.ParseUUID(agentTestUser)
	sid, _ := account.ParseUUID(agentTestSession)
	return accountApp.Identity{User: account.User{ID: uid}, Session: account.Session{ID: sid}, AccessExpiresAt: r.expires}, nil
}
func runHTTPServer(t *testing.T, s handler.AgentRunService, expires time.Time, bind ...string) (string, *handler.AgentRun) {
	t.Helper()
	address := "127.0.0.1:0"
	if len(bind) > 0 {
		address = bind[0]
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	h := server.New(server.WithListener(listener), server.WithSenseClientDisconnection(true), server.WithDisablePrintRoute(true), server.WithHostPorts(listener.Addr().String()))
	endpoint := handler.NewAgentRun(s)
	g := h.Group("/api/v1/me/agent/conversations/:id/runs", middleware.Authenticate(runHTTPIdentity{expires: expires}))
	g.POST("", middleware.RequireJSON(), endpoint.Create)
	g.GET("", endpoint.List)
	g.GET("/:run_id", endpoint.Detail)
	g.POST("/:run_id/cancel", endpoint.Cancel)
	g.GET("/:run_id/events", endpoint.Events)
	done := make(chan error, 1)
	go func() { done <- h.Run() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		_ = h.Shutdown(ctx)
		select {
		case <-done:
		case <-ctx.Done():
			t.Error("SSE服务未停止")
		}
	})
	return fmt.Sprintf("http://127.0.0.1:%d", listener.Addr().(*net.TCPAddr).Port), endpoint
}
func runHTTPRequest(t *testing.T, base, cid, rid, position string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", base+"/api/v1/me/agent/conversations/"+cid+"/runs/"+rid+"/events", nil)
	req.Header.Set("Authorization", "Bearer local")
	if position != "" {
		req.Header.Set("Last-Event-ID", position)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}
func readRunEvent(t *testing.T, scanner *bufio.Scanner) (string, string, agent.Event) {
	t.Helper()
	id, kind := "", ""
	var event agent.Event
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" && kind != "" {
			return id, kind, event
		}
		if strings.HasPrefix(line, "id: ") {
			id = strings.TrimPrefix(line, "id: ")
		}
		if strings.HasPrefix(line, "event: ") {
			kind = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "data: ") {
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Fatalf("流提前结束: %v", scanner.Err())
	return "", "", event
}
func TestAgentRunSSEReplayProjectionAndPositions(t *testing.T) {
	env := newTestEnv(t)
	s, repo, e := claimedRun(t, env)
	appendBlock(t, env, repo, e)
	commit := successCommit(t, env, repo, e)
	if err := postgres.NewTxManager(env.pool).WithinTransaction(t.Context(), func(ctx context.Context) error { return repo.Complete(ctx, e, commit) }); err != nil {
		t.Fatal(err)
	}
	base, _ := runHTTPServer(t, s, time.Now().Add(time.Minute))
	other, _ := runHTTPServer(t, s, time.Now().Add(time.Minute))
	resp := runHTTPRequest(t, base, e.ConversationID, e.RunID, "")
	if resp.StatusCode != 200 || resp.Header.Get("X-Accel-Buffering") != "no" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatal(resp.StatusCode, resp.Header)
	}
	scan := bufio.NewScanner(resp.Body)
	var blockID, last string
	for i := int64(1); i <= 5; i++ {
		id, kind, event := readRunEvent(t, scan)
		if id != handler.EventID(e.RunID, i) || event.Sequence != i {
			t.Fatal(id, event)
		}
		if kind == "answer.block" {
			blockID = id
			if event.Block.Redacted {
				t.Fatal(event)
			}
		}
		last = id
	}
	resp.Body.Close()
	resp = runHTTPRequest(t, other, e.ConversationID, e.RunID, last)
	if resp.StatusCode != 204 {
		t.Fatal(resp.StatusCode)
	}
	resp.Body.Close()
	resp = runHTTPRequest(t, other, e.ConversationID, e.RunID, handler.EventID(e.RunID, 3))
	_, _, before := readRunEvent(t, bufio.NewScanner(resp.Body))
	resp.Body.Close()
	if before.Block == nil || before.Block.Redacted {
		t.Fatal(before)
	}
	if _, err := env.pool.Exec(t.Context(), `UPDATE velis.articles SET status='offline',offline_reason='author',offline_at=now() WHERE id=7201`); err != nil {
		t.Fatal(err)
	}
	resp = runHTTPRequest(t, other, e.ConversationID, e.RunID, handler.EventID(e.RunID, 3))
	id, _, after := readRunEvent(t, bufio.NewScanner(resp.Body))
	resp.Body.Close()
	if id != blockID || !after.Redacted || after.Block == nil || !after.Block.Redacted || len(after.Block.EvidenceIDs) != 0 || len(after.References) != 0 {
		t.Fatal(id, after)
	}
	for _, value := range []string{"bad", handler.EventID(e.RunID, 6), handler.EventID(agentKey(999), 1)} {
		resp = runHTTPRequest(t, base, e.ConversationID, e.RunID, value)
		if resp.StatusCode != 400 {
			t.Fatal(value, resp.StatusCode)
		}
		resp.Body.Close()
	}
	req, _ := http.NewRequest("GET", base+"/api/v1/me/agent/conversations/"+e.ConversationID+"/runs/"+e.RunID+"/events", nil)
	req.Header.Set("Authorization", "Bearer local")
	req.Header.Add("Last-Event-ID", last)
	req.Header.Add("Last-Event-ID", last)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 400 {
		t.Fatal("重复事件位置", resp.StatusCode)
	}
	resp.Body.Close()
	if _, err := env.pool.Exec(t.Context(), `UPDATE velis.agent_runs SET finished_at=now()-interval '25 hours'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Cleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
	resp = runHTTPRequest(t, base, e.ConversationID, e.RunID, last)
	if resp.StatusCode != 410 {
		t.Fatal(resp.StatusCode)
	}
	resp.Body.Close()
}

type gateRunner struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (r *gateRunner) Execute(ctx context.Context, req agent.ExecutionRequest, sink agent.OutputSink) (agent.ExecutionResult, error) {
	r.calls.Add(1)
	r.started <- struct{}{}
	select {
	case <-ctx.Done():
		return agent.ExecutionResult{}, ctx.Err()
	case <-r.release:
	}
	return (&executionRunner{}).Execute(ctx, req, sink)
}
func TestAgentRunSSEDisconnectExpiryAndAuthorization(t *testing.T) {
	for _, mode := range []string{"disconnect", "access_expired", "logout", "deleted"} {
		t.Run(mode, func(t *testing.T) {
			env := newTestEnv(t)
			seedAgentSessions(t, env)
			c, m := agentQuestion(t, env, 1)
			runner := &gateRunner{started: make(chan struct{}, 1), release: make(chan struct{})}
			_, s := startExecutor(t, env, runner, true)
			a, err := s.Accept(t.Context(), agentCaller, c.ID, agentKey(1), agent.Command{MessageID: m.ID})
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-runner.started:
			case <-time.After(3 * time.Second):
				t.Fatal("没有启动")
			}
			expires := time.Now().Add(time.Minute)
			if mode == "access_expired" {
				expires = time.Now().Add(500 * time.Millisecond)
			}
			base, _ := runHTTPServer(t, s, expires)
			resp := runHTTPRequest(t, base, c.ID, a.Snapshot.ID, "")
			if resp.StatusCode != 200 {
				t.Fatal(resp.StatusCode)
			}
			if mode == "disconnect" {
				resp.Body.Close()
			} else {
				if mode == "logout" {
					_, err = env.pool.Exec(t.Context(), `UPDATE velis.auth_sessions SET revoked_at=now(),revoke_reason='logout' WHERE id=$1`, agentTestSession)
				}
				if mode == "deleted" {
					err = newAgentService(env, conversationApp.DefaultLimits()).Delete(t.Context(), agentTestUser, c.ID)
				}
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if err != nil || !strings.Contains(string(body), "stream.error") {
					t.Fatal(mode, string(body), err)
				}
				if strings.Contains(string(body), "event: stream.error\nid:") {
					t.Fatal("瞬态错误携带位置")
				}
			}
			close(runner.release)
			if mode == "disconnect" || mode == "access_expired" {
				repo := postgres.NewAgentRunRepository(env.pool)
				record := awaitRun(t, repo, agent.Scope{UserID: agentTestUser, ConversationID: c.ID, RunID: a.Snapshot.ID}, run.Succeeded, 3*time.Second)
				if record.Run.AssistantReserved || runner.calls.Load() != 1 {
					t.Fatal(record)
				}
			}
		})
	}
}

func TestAgentRunSSEAccountSubscriptionLimit(t *testing.T) {
	env := newTestEnv(t)
	s, _, e := claimedRun(t, env)
	base, _ := runHTTPServer(t, s, time.Now().Add(time.Minute))
	responses := []*http.Response{}
	defer func() {
		for _, r := range responses {
			r.Body.Close()
		}
	}()
	for i := 0; i < 4; i++ {
		r := runHTTPRequest(t, base, e.ConversationID, e.RunID, "")
		if r.StatusCode != 200 {
			t.Fatal(i, r.StatusCode)
		}
		responses = append(responses, r)
	}
	r := runHTTPRequest(t, base, e.ConversationID, e.RunID, "")
	if r.StatusCode != 429 {
		t.Fatal(r.StatusCode)
	}
	r.Body.Close()
	t.Logf("四个订阅并行且不阻塞；第五个返回 429；run=%s", e.RunID)
}
