package hertz

import (
	"context"
	"github.com/cloudwego/hertz/pkg/common/ut"
	agent "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentrun"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
	run "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/handler"
	"log/slog"
	"strings"
	"testing"
	"time"
)

const runTestCID = "11000000-0000-4000-8000-000000000001"
const runTestRID = "22000000-0000-4000-8000-000000000001"

type fakeRuns struct {
	handler.AgentRunService
	calls int
	err   error
}

func (f *fakeRuns) Accept(_ context.Context, _ agent.Caller, cid, _ string, _ agent.Command) (agent.Acceptance, error) {
	f.calls++
	return agent.Acceptance{Snapshot: agent.AcceptedSnapshot{ID: runTestRID, ConversationID: cid, Status: run.Accepted}}, f.err
}
func (f *fakeRuns) Detail(context.Context, agent.Caller, string, string) (agent.Detail, error) {
	return agent.Detail{}, f.err
}
func (f *fakeRuns) Cancel(context.Context, agent.Caller, string, string) (agent.Summary, error) {
	return agent.Summary{}, f.err
}
func (f *fakeRuns) List(context.Context, agent.Caller, string, string, int, time.Time) (agent.Page, error) {
	return agent.Page{Items: []agent.Summary{}}, f.err
}
func (f *fakeRuns) Events(context.Context, agent.Caller, string, string, int64) (agent.EventBatch, error) {
	return agent.EventBatch{LastSequence: 2, Terminal: true}, f.err
}
func TestAgentRunHTTPContract(t *testing.T) {
	a := &fakeAccount{}
	a.identity.User = testUser()
	a.identity.User.Role = account.RoleAdmin
	a.identity.Session.ID = account.UUID{2}
	f := &fakeRuns{}
	h := newAuthServer(t, a, false)
	// 使用生产路由装配，包含认证中间件。
	h = NewServer(Options{Logger: slog.Default(), Auth: &AuthOptions{Service: a, AgentRuns: handler.NewAgentRun(f)}})
	root := "/api/v1/me/agent/conversations/" + runTestCID + "/runs"
	body := `{"message_id":"33000000-0000-4000-8000-000000000001"}`
	for _, bad := range []string{`{}`, `{"message_id":null}`, `{"message_id":"x"}`, `{"message_id":"33000000-0000-4000-8000-000000000001","unknown":1}`, `{"message_id":"33000000-0000-4000-8000-000000000001","constraints":{"keyword":null}}`, body[:len(body)-1] + `,"message_id":"33000000-0000-4000-8000-000000000001"}`, strings.Repeat(" ", 16384) + body} {
		resp := ut.PerformRequest(h.Engine, "POST", root, &ut.Body{Body: strings.NewReader(bad), Len: len(bad)}, ut.Header{Key: "Authorization", Value: "Bearer local"}, ut.Header{Key: "Content-Type", Value: "application/json"}, ut.Header{Key: "Idempotency-Key", Value: runTestRID})
		if resp.Code != 400 {
			t.Fatalf("严格输入 status=%d body=%s", resp.Code, resp.Body.String())
		}
	}
	resp := ut.PerformRequest(h.Engine, "POST", root, &ut.Body{Body: strings.NewReader(body), Len: len(body)}, ut.Header{Key: "Content-Type", Value: "application/json"})
	if resp.Code != 401 {
		t.Fatal("认证必须先行", resp.Code)
	}
	for _, headers := range [][]ut.Header{{{Key: "Authorization", Value: "Bearer local"}, {Key: "Content-Type", Value: "application/json"}}, {{Key: "Authorization", Value: "Bearer local"}, {Key: "Content-Type", Value: "application/json"}, {Key: "Idempotency-Key", Value: runTestRID}, {Key: "Idempotency-Key", Value: runTestRID}}} {
		r := ut.PerformRequest(h.Engine, "POST", root, &ut.Body{Body: strings.NewReader(body), Len: len(body)}, headers...)
		if r.Code != 400 {
			t.Fatal(r.Code)
		}
	}
	resp = ut.PerformRequest(h.Engine, "POST", root, &ut.Body{Body: strings.NewReader(body), Len: len(body)}, ut.Header{Key: "Authorization", Value: "Bearer local"}, ut.Header{Key: "Content-Type", Value: "application/json"}, ut.Header{Key: "Idempotency-Key", Value: runTestRID})
	if resp.Code != 202 || resp.Header().Get("Location") != root+"/"+runTestRID || f.calls != 1 {
		t.Fatalf("接受 status=%d calls=%d headers=%v", resp.Code, f.calls, resp.Header())
	}
	for _, path := range []string{root + "?x=1", root + "?limit=0", root + "?limit=1&limit=2", root + "/" + runTestRID + "?token=x"} {
		r := ut.PerformRequest(h.Engine, "GET", path, nil, ut.Header{Key: "Authorization", Value: "Bearer local"})
		if r.Code != 400 {
			t.Fatal(path, r.Code)
		}
	}
	for _, code := range []run.Code{run.CodeNotFound, run.CodeInProgress, run.CodeGenerationUnavailable, run.CodeAuthInvalid} {
		f.err = &run.Error{Code: code}
		r := ut.PerformRequest(h.Engine, "GET", root+"/"+runTestRID, nil, ut.Header{Key: "Authorization", Value: "Bearer local"})
		want := map[run.Code]int{run.CodeNotFound: 404, run.CodeInProgress: 409, run.CodeGenerationUnavailable: 503, run.CodeAuthInvalid: 401}[code]
		if r.Code != want {
			t.Fatal(code, r.Code)
		}
	}
	f.err = run.ErrNotFound
	r := ut.PerformRequest(h.Engine, "GET", root+"/"+runTestRID+"/events", nil, ut.Header{Key: "Authorization", Value: "Bearer local"}, ut.Header{Key: "Last-Event-ID", Value: "bad"})
	if r.Code != 404 {
		t.Fatal("管理员不得绕过归属", r.Code)
	}
	f.err = nil
	for _, value := range []string{"bad", "v1:" + runTestCID + ":1", "v1:" + runTestRID + ":0", "v1:" + runTestRID + ":03", "v1:" + runTestRID + ":3"} {
		r := ut.PerformRequest(h.Engine, "GET", root+"/"+runTestRID+"/events", nil, ut.Header{Key: "Authorization", Value: "Bearer local"}, ut.Header{Key: "Last-Event-ID", Value: value})
		if r.Code != 400 || !strings.Contains(r.Body.String(), "INVALID_AGENT_EVENT_ID") {
			t.Fatal(value, r.Code, r.Body.String())
		}
	}
	r = ut.PerformRequest(h.Engine, "GET", root+"/"+runTestRID+"/events", nil, ut.Header{Key: "Authorization", Value: "Bearer local"}, ut.Header{Key: "Last-Event-ID", Value: handler.EventID(runTestRID, 2)})
	if r.Code != 204 {
		t.Fatal(r.Code)
	}
}
