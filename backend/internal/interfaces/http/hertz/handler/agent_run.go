package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/sse"
	agent "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentrun"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
	run "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/middleware"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/presenter"
)

type AgentRunService interface {
	Accept(context.Context, agent.Caller, string, string, agent.Command) (agent.Acceptance, error)
	List(context.Context, agent.Caller, string, string, int, time.Time) (agent.Page, error)
	Detail(context.Context, agent.Caller, string, string) (agent.Detail, error)
	Cancel(context.Context, agent.Caller, string, string) (agent.Summary, error)
	Events(context.Context, agent.Caller, string, string, int64) (agent.EventBatch, error)
}
type AgentRun struct {
	service  AgentRunService
	mu       sync.Mutex
	active   int
	accounts map[string]int
}

func NewAgentRun(service AgentRunService) *AgentRun {
	return &AgentRun{service: service, accounts: map[string]int{}}
}
func runCaller(c *app.RequestContext) (agent.Caller, bool) {
	identity, ok := middleware.IdentityFrom(c)
	if !ok {
		presenter.WriteError(c, 401, "AUTH_SESSION_INVALID", "需要登录", middleware.RequestIDFrom(c))
		return agent.Caller{}, false
	}
	return agent.Caller{UserID: identity.User.ID.String(), SessionID: identity.Session.ID.String()}, true
}
func runIDs(c *app.RequestContext) (string, string, error) {
	cid, err := agentID(c)
	if err != nil {
		return "", "", err
	}
	raw := c.Param("run_id")
	if raw == "" {
		return cid, "", nil
	}
	id, err := account.ParseUUID(raw)
	if err != nil || id.IsZero() {
		return "", "", run.ErrInvalidInput
	}
	return cid, id.String(), nil
}
func (h *AgentRun) reject(c *app.RequestContext, err error) {
	var e *run.Error
	if errors.As(err, &e) {
		status := 500
		switch e.Code {
		case run.CodeInvalidInput:
			status = 400
		case run.CodeAuthInvalid:
			status = 401
		case run.CodeNotFound, run.CodeMessageNotFound:
			status = 404
		case run.CodeInputConflict, run.CodeAttemptLimit, run.CodeInProgress, run.CodeMessageLimit, run.CodeStateConflict:
			status = 409
		case run.CodeGenerationUnavailable, run.CodeDependencyUnavailable:
			status = 503
		}
		code := e.Code
		if !code.Valid() {
			code = run.CodeInternal
		}
		presenter.WriteError(c, status, string(code), "回合请求无法完成", middleware.RequestIDFrom(c))
		return
	}
	(&AgentConversation{}).reject(c, err)
}
func (h *AgentRun) Create(ctx context.Context, c *app.RequestContext) {
	caller, ok := runCaller(c)
	if !ok {
		return
	}
	cid, _, err := runIDs(c)
	if err != nil {
		h.reject(c, err)
		return
	}
	if c.Request.URI().QueryArgs().Len() != 0 {
		h.reject(c, run.ErrInvalidInput)
		return
	}
	cmd, err := agent.DecodeCommand(c.Request.Body())
	if err != nil {
		h.reject(c, err)
		return
	}
	key, err := agentIdempotencyKey(c)
	if err != nil {
		h.reject(c, err)
		return
	}
	result, err := h.service.Accept(ctx, caller, cid, key, cmd)
	if err != nil {
		h.reject(c, err)
		return
	}
	c.Response.Header.Set("Location", "/api/v1/me/agent/conversations/"+cid+"/runs/"+result.Snapshot.ID)
	c.JSON(202, result.Snapshot)
}
func (h *AgentRun) List(ctx context.Context, c *app.RequestContext) {
	caller, ok := runCaller(c)
	if !ok {
		return
	}
	cid, _, err := runIDs(c)
	if err != nil {
		h.reject(c, err)
		return
	}
	limit, cursor, err := agentPage(c)
	if err != nil {
		h.reject(c, err)
		return
	}
	result, err := h.service.List(ctx, caller, cid, cursor, limit, time.Now())
	if err != nil {
		h.reject(c, err)
		return
	}
	c.JSON(200, result)
}
func (h *AgentRun) Detail(ctx context.Context, c *app.RequestContext) { h.read(ctx, c, false) }
func (h *AgentRun) Cancel(ctx context.Context, c *app.RequestContext) { h.read(ctx, c, true) }
func (h *AgentRun) read(ctx context.Context, c *app.RequestContext, cancel bool) {
	caller, ok := runCaller(c)
	if !ok {
		return
	}
	cid, rid, err := runIDs(c)
	if err != nil {
		h.reject(c, err)
		return
	}
	if len(c.Request.Body()) != 0 || c.Request.URI().QueryArgs().Len() != 0 {
		h.reject(c, run.ErrInvalidInput)
		return
	}
	if cancel {
		result, err := h.service.Cancel(ctx, caller, cid, rid)
		if err != nil {
			h.reject(c, err)
			return
		}
		c.JSON(200, result)
		return
	}
	result, err := h.service.Detail(ctx, caller, cid, rid)
	if err != nil {
		h.reject(c, err)
		return
	}
	c.JSON(200, result)
}

func EventID(rid string, seq int64) string { return fmt.Sprintf("v1:%s:%d", rid, seq) }
func parseEventID(raw, rid string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	parts := strings.Split(raw, ":")
	if len(parts) != 3 || parts[0] != "v1" || parts[1] != rid {
		return 0, run.ErrInvalidInput
	}
	seq, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || seq < 1 || strconv.FormatInt(seq, 10) != parts[2] {
		return 0, run.ErrInvalidInput
	}
	return seq, nil
}
func (h *AgentRun) acquire(user string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.active >= 64 || h.accounts[user] >= 4 {
		return false
	}
	h.active++
	h.accounts[user]++
	return true
}
func (h *AgentRun) release(user string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.active--
	h.accounts[user]--
	if h.accounts[user] == 0 {
		delete(h.accounts, user)
	}
}
func (h *AgentRun) Events(parent context.Context, c *app.RequestContext) {
	caller, ok := runCaller(c)
	if !ok {
		return
	}
	cid, rid, err := runIDs(c)
	if err != nil {
		h.reject(c, err)
		return
	}
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()
	// 登录、归属和回合存在性先于位置错误，避免位置接口泄露他人的回合。
	batch, err := h.service.Events(ctx, caller, cid, rid, 0)
	if err != nil {
		h.reject(c, err)
		return
	}
	if len(c.Request.Body()) != 0 || c.Request.URI().QueryArgs().Len() != 0 {
		h.reject(c, run.ErrInvalidInput)
		return
	}
	headers := c.Request.Header.PeekAll("Last-Event-ID")
	after, err := parseEventID(string(c.Request.Header.Peek("Last-Event-ID")), rid)
	if len(headers) > 1 || err != nil || after > batch.LastSequence {
		presenter.WriteError(c, 400, "INVALID_AGENT_EVENT_ID", "事件位置无效", middleware.RequestIDFrom(c))
		return
	}
	if batch.Expired {
		presenter.WriteError(c, 410, "AGENT_EVENTS_EXPIRED", "事件已过期，请读取回合详情", middleware.RequestIDFrom(c))
		return
	}
	if batch.Terminal && after == batch.LastSequence {
		c.SetStatusCode(204)
		return
	}
	if !h.acquire(caller.UserID) {
		presenter.WriteError(c, 429, "AGENT_SUBSCRIPTION_LIMIT_EXCEEDED", "订阅数量已达上限", middleware.RequestIDFrom(c))
		return
	}
	defer h.release(caller.UserID)
	identity, _ := middleware.IdentityFrom(c)
	if !identity.AccessExpiresAt.IsZero() {
		var stop context.CancelFunc
		ctx, stop = context.WithDeadline(ctx, identity.AccessExpiresAt)
		defer stop()
	}
	writer := sse.NewWriter(c)
	c.Response.Header.Set("Cache-Control", "no-store")
	c.Response.Header.Set("X-Accel-Buffering", "no")
	c.Response.Header.Set("X-Content-Type-Options", "nosniff")
	c.SetStatusCode(200)
	conn := c.GetConn()
	write := func(f func() error) error {
		if conn != nil {
			if err := conn.SetWriteTimeout(5 * time.Second); err != nil {
				return err
			}
		}
		return f()
	}
	defer func() {
		_ = write(writer.Close)
		if conn != nil {
			_ = conn.SetWriteTimeout(0)
		}
	}()
	streamError := func(code string) {
		body, _ := json.Marshal(map[string]any{"version": 1, "error_code": code})
		_ = write(func() error { return writer.WriteEvent("", "stream.error", body) })
	}
	if err := write(writer.WriteKeepAlive); err != nil {
		return
	}
	poll := time.NewTicker(250 * time.Millisecond)
	defer poll.Stop()
	heartbeat := time.NewTicker(10 * time.Second)
	defer heartbeat.Stop()
	for {
		if err := ctx.Err(); err != nil {
			if !identity.AccessExpiresAt.IsZero() && !time.Now().Before(identity.AccessExpiresAt) {
				streamError("AUTH_SESSION_INVALID")
			}
			return
		}
		batch, err = h.service.Events(ctx, caller, cid, rid, after)
		if err != nil {
			streamError(string(agent.SafeCode(err)))
			return
		}
		if batch.Expired {
			streamError("AGENT_EVENTS_EXPIRED")
			return
		}
		for _, event := range batch.Events {
			if ctx.Err() != nil {
				return
			}
			data, err := json.Marshal(event)
			if err != nil {
				streamError("INTERNAL_ERROR")
				return
			}
			if err := write(func() error { return writer.WriteEvent(EventID(rid, event.Sequence), string(event.Kind), data) }); err != nil {
				return
			}
			after = event.Sequence
		}
		if batch.Terminal && after >= batch.LastSequence {
			return
		}
		if len(batch.Events) == 16 {
			continue
		}
		select {
		case <-ctx.Done():
		case <-poll.C:
		case <-heartbeat.C:
			if err := write(writer.WriteKeepAlive); err != nil {
				return
			}
		}
	}
}
