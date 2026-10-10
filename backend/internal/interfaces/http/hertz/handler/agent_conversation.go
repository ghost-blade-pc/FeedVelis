package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strconv"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	agent "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentconversation"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/idempotency"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/strictjson"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
	conversation "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentconversation"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/middleware"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/presenter"
	"github.com/jackc/pgx/v5/pgconn"
)

type AgentConversationService interface {
	Create(context.Context, string, string, *string) (agent.Result, error)
	Append(context.Context, string, string, string, string) (agent.Result, error)
	Rename(context.Context, string, string, string, string, int64) (agent.Result, error)
	Delete(context.Context, string, string) error
	Detail(context.Context, string, string) (conversation.Conversation, error)
	List(context.Context, string, *agent.ListBoundary, int) ([]conversation.Conversation, bool, error)
	History(context.Context, string, string, int64, int) ([]conversation.Message, bool, error)
}

type AgentConversation struct {
	service  AgentConversationService
	codec    *agent.CursorCodec
	ttl      time.Duration
	maxBody  int
	now      func() time.Time
	observer AgentConversationObserver
}

type AgentConversationObserver interface {
	ObserveConversation(context.Context, string, string, time.Duration)
}

func (h *AgentConversation) WithObserver(observer AgentConversationObserver) *AgentConversation {
	h.observer = observer
	return h
}

func (h *AgentConversation) Observe(operation string) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		started := time.Now()
		ctx = articlesearch.WithRequestID(ctx, middleware.RequestIDFrom(c))
		c.Next(ctx)
		if h.observer == nil {
			return
		}
		result := "success"
		if c.Response.StatusCode() >= 400 {
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			_ = json.Unmarshal(c.Response.Body(), &body)
			result = body.Error.Code
		}
		h.observer.ObserveConversation(ctx, operation, result, time.Since(started))
	}
}

func NewAgentConversation(service AgentConversationService, codec *agent.CursorCodec, ttl time.Duration, maxChars int) *AgentConversation {
	budget := max(64*1024, 12*maxChars+4096)
	return &AgentConversation{service: service, codec: codec, ttl: ttl, maxBody: min(256*1024, budget), now: time.Now}
}

func (h *AgentConversation) Create(ctx context.Context, c *app.RequestContext) {
	user, ok := feedbackUser(c)
	if !ok {
		return
	}
	var input struct {
		Title *string `json:"title"`
	}
	if c.Request.URI().QueryArgs().Len() > 0 || strictjson.Decode(c.Request.Body(), 4096, &input, "title") != nil {
		h.reject(c, conversation.ErrInvalidInput)
		return
	}
	key, err := agentIdempotencyKey(c)
	if err != nil {
		h.reject(c, err)
		return
	}
	result, err := h.service.Create(ctx, user, key, input.Title)
	h.result(c, result, err, true)
}

func agentID(c *app.RequestContext) (string, error) {
	id, err := account.ParseUUID(c.Param("id"))
	if err != nil || id.IsZero() {
		return "", conversation.ErrInvalidInput
	}
	return id.String(), nil
}

func agentIdempotencyKey(c *app.RequestContext) (string, error) {
	if len(c.Request.Header.PeekAll(presenter.HeaderIdempotencyKey)) > 1 {
		return "", presenter.ErrIdempotencyKeyInvalid
	}
	return presenter.ParseIdempotencyKey(string(c.Request.Header.Peek(presenter.HeaderIdempotencyKey)))
}

func (h *AgentConversation) Append(ctx context.Context, c *app.RequestContext) {
	user, ok := feedbackUser(c)
	if !ok {
		return
	}
	id, err := agentID(c)
	if err != nil {
		h.reject(c, err)
		return
	}
	var input struct {
		Content *string `json:"content"`
	}
	if c.Request.URI().QueryArgs().Len() > 0 || strictjson.Decode(c.Request.Body(), h.maxBody, &input, "content") != nil || input.Content == nil {
		h.reject(c, conversation.ErrInvalidInput)
		return
	}
	key, err := agentIdempotencyKey(c)
	if err != nil {
		h.reject(c, err)
		return
	}
	result, err := h.service.Append(ctx, user, id, key, *input.Content)
	h.result(c, result, err, false)
}

func (h *AgentConversation) Rename(ctx context.Context, c *app.RequestContext) {
	user, ok := feedbackUser(c)
	if !ok {
		return
	}
	id, err := agentID(c)
	if err != nil {
		h.reject(c, err)
		return
	}
	var input struct {
		Title *string `json:"title"`
	}
	if c.Request.URI().QueryArgs().Len() > 0 || strictjson.Decode(c.Request.Body(), 4096, &input, "title") != nil || input.Title == nil {
		h.reject(c, conversation.ErrInvalidInput)
		return
	}
	key, err := agentIdempotencyKey(c)
	if err != nil {
		h.reject(c, err)
		return
	}
	if len(c.Request.Header.PeekAll(presenter.HeaderIfMatch)) > 1 {
		h.reject(c, presenter.ErrIfMatchInvalid)
		return
	}
	version, err := presenter.ParseIfMatch(string(c.Request.Header.Peek(presenter.HeaderIfMatch)))
	if err != nil {
		h.reject(c, err)
		return
	}
	result, err := h.service.Rename(ctx, user, id, key, *input.Title, version)
	h.result(c, result, err, true)
}

func (h *AgentConversation) Detail(ctx context.Context, c *app.RequestContext) {
	user, ok := feedbackUser(c)
	if !ok {
		return
	}
	id, err := agentID(c)
	if err != nil {
		h.reject(c, err)
		return
	}
	if len(c.Request.Body()) > 0 || c.Request.URI().QueryArgs().Len() > 0 {
		h.reject(c, conversation.ErrInvalidInput)
		return
	}
	result, err := h.service.Detail(ctx, user, id)
	if err != nil {
		h.reject(c, err)
		return
	}
	c.Response.Header.Set(presenter.HeaderETag, presenter.ETag(result.TitleVersion))
	c.JSON(200, result)
}

func (h *AgentConversation) Delete(ctx context.Context, c *app.RequestContext) {
	user, ok := feedbackUser(c)
	if !ok {
		return
	}
	id, err := agentID(c)
	if err != nil {
		h.reject(c, err)
		return
	}
	if len(c.Request.Body()) > 0 || c.Request.URI().QueryArgs().Len() > 0 {
		h.reject(c, conversation.ErrInvalidInput)
		return
	}
	if err := h.service.Delete(ctx, user, id); err != nil {
		h.reject(c, err)
		return
	}
	c.SetStatusCode(204)
}

func agentPage(c *app.RequestContext) (int, string, error) {
	if len(c.Request.Body()) > 0 {
		return 0, "", conversation.ErrInvalidInput
	}
	limit := 20
	cursor := ""
	seen := map[string]bool{}
	var invalid error
	c.Request.URI().QueryArgs().VisitAll(func(key, value []byte) {
		name := string(key)
		if seen[name] || (name != "limit" && name != "cursor") {
			invalid = conversation.ErrInvalidInput
			return
		}
		seen[name] = true
		if name == "limit" {
			parsed, err := strconv.Atoi(string(value))
			if err != nil || parsed < 1 || parsed > 50 || strconv.Itoa(parsed) != string(value) {
				invalid = conversation.ErrInvalidInput
				return
			}
			limit = parsed
		} else {
			if len(value) == 0 || len(value) > 4096 {
				invalid = agent.ErrInvalidCursor
				return
			}
			cursor = string(value)
		}
	})
	return limit, cursor, invalid
}

func (h *AgentConversation) List(ctx context.Context, c *app.RequestContext) {
	user, ok := feedbackUser(c)
	if !ok {
		return
	}
	limit, raw, err := agentPage(c)
	if err != nil {
		h.reject(c, err)
		return
	}
	now := h.now().UTC()
	value := agent.Cursor{UserID: user, Purpose: agent.ListPurpose, StartedAt: now, ExpiresAt: now.Add(h.ttl)}
	var boundary *agent.ListBoundary
	if raw != "" {
		value, err = h.codec.Decode(raw, user, agent.ListPurpose, "", now)
		if err != nil {
			h.reject(c, err)
			return
		}
		boundary = &agent.ListBoundary{ActivityAt: value.ActivityAt, ID: value.ID}
	}
	items, more, err := h.service.List(ctx, user, boundary, limit)
	if err != nil {
		h.reject(c, err)
		return
	}
	var next *string
	if more && len(items) > 0 {
		last := items[len(items)-1]
		value.ID = last.ID
		value.ActivityAt = last.LastActivityAt
		encoded, err := h.codec.Encode(value)
		if err != nil {
			h.reject(c, err)
			return
		}
		next = &encoded
	}
	c.JSON(200, map[string]any{"items": items, "next_cursor": next, "has_more": more})
}

func (h *AgentConversation) History(ctx context.Context, c *app.RequestContext) {
	user, ok := feedbackUser(c)
	if !ok {
		return
	}
	id, err := agentID(c)
	if err != nil {
		h.reject(c, err)
		return
	}
	limit, raw, err := agentPage(c)
	if err != nil {
		h.reject(c, err)
		return
	}
	// 合法格式之后先确认归属，再处理解密后的绑定；服务History再次在快照内核对。
	if _, err := h.service.Detail(ctx, user, id); err != nil {
		h.reject(c, err)
		return
	}
	now := h.now().UTC()
	value := agent.Cursor{UserID: user, Purpose: agent.HistoryPurpose, ConversationID: id, StartedAt: now, ExpiresAt: now.Add(h.ttl)}
	if raw != "" {
		value, err = h.codec.Decode(raw, user, agent.HistoryPurpose, id, now)
		if err != nil {
			h.reject(c, err)
			return
		}
	}
	items, more, err := h.service.History(ctx, user, id, value.BeforeSequence, limit)
	if err != nil {
		h.reject(c, err)
		return
	}
	var next *string
	if more && len(items) > 0 {
		value.BeforeSequence = items[0].Sequence
		encoded, err := h.codec.Encode(value)
		if err != nil {
			h.reject(c, err)
			return
		}
		next = &encoded
	}
	c.JSON(200, map[string]any{"items": items, "next_cursor": next, "has_more": more})
}

func (h *AgentConversation) result(c *app.RequestContext, result agent.Result, err error, etag bool) {
	if err != nil {
		h.reject(c, err)
		return
	}
	if etag {
		var snapshot struct {
			Version int64 `json:"title_version"`
		}
		if json.Unmarshal(result.Snapshot, &snapshot) != nil || snapshot.Version < 1 {
			h.reject(c, errors.New("会话快照无效"))
			return
		}
		c.Response.Header.Set(presenter.HeaderETag, presenter.ETag(snapshot.Version))
	}
	c.Data(result.Status, "application/json; charset=utf-8", result.Snapshot)
}

func (h *AgentConversation) reject(c *app.RequestContext, err error) {
	status, code, message := 500, "INTERNAL_ERROR", "会话服务暂不可用"
	var pgErr *pgconn.PgError
	var connectErr *pgconn.ConnectError
	var networkErr net.Error
	switch {
	case errors.Is(err, conversation.ErrInvalidInput):
		status, code, message = 400, "VALIDATION_FAILED", "会话参数无效"
	case errors.Is(err, agent.ErrInvalidCursor):
		status, code, message = 400, "INVALID_CURSOR", "分页游标无效或已过期"
	case errors.Is(err, presenter.ErrIdempotencyKeyRequired):
		status, code, message = 400, "IDEMPOTENCY_KEY_REQUIRED", "缺少幂等键"
	case errors.Is(err, presenter.ErrIdempotencyKeyInvalid):
		status, code, message = 400, "IDEMPOTENCY_KEY_INVALID", "幂等键无效"
	case errors.Is(err, presenter.ErrIfMatchRequired):
		status, code, message = 400, "IF_MATCH_REQUIRED", "缺少标题版本"
	case errors.Is(err, presenter.ErrIfMatchInvalid):
		status, code, message = 400, "IF_MATCH_INVALID", "标题版本格式无效"
	case errors.Is(err, conversation.ErrNotFound):
		status, code, message = 404, "AGENT_CONVERSATION_NOT_FOUND", "会话不存在"
	case errors.Is(err, idempotency.ErrKeyReused):
		status, code, message = 409, "IDEMPOTENCY_KEY_REUSED", "幂等键已用于不同请求"
	case errors.Is(err, idempotency.ErrPending):
		status, code, message = 409, "IDEMPOTENCY_IN_PROGRESS", "请求正在执行"
	case errors.Is(err, conversation.ErrTitleVersion):
		status, code, message = 409, "AGENT_TITLE_VERSION_CONFLICT", "会话标题版本冲突"
	case errors.Is(err, conversation.ErrConversationLimit):
		status, code, message = 409, "AGENT_CONVERSATION_LIMIT_EXCEEDED", "会话数量已达上限"
	case errors.Is(err, conversation.ErrMessageLimit):
		status, code, message = 409, "AGENT_MESSAGE_LIMIT_EXCEEDED", "会话消息数量已达上限"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, io.EOF), errors.As(err, &networkErr), errors.As(err, &pgErr), errors.As(err, &connectErr), pgconn.SafeToRetry(err):
		status, code, message = 503, "DEPENDENCY_UNAVAILABLE", "会话存储暂不可用"
	}
	presenter.WriteError(c, status, code, message, middleware.RequestIDFrom(c))
}
