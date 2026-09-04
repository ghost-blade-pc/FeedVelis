package hertz

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/health"
)

type readyChecker struct{ err error }

func (c readyChecker) Ping(context.Context) error { return c.err }

func TestLiveAndRequestID(t *testing.T) {
	h := newTestServer(nil)
	response := ut.PerformRequest(h.Engine, consts.MethodGet, "/livez", nil)
	if response.Code != consts.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if id := string(response.Header().Peek("X-Request-ID")); len(id) != 32 {
		t.Fatalf("X-Request-ID = %q", id)
	}
}

func TestReadyFailureUsesErrorEnvelope(t *testing.T) {
	h := newTestServer(context.DeadlineExceeded)
	response := ut.PerformRequest(h.Engine, consts.MethodGet, "/readyz", nil)
	if response.Code != consts.StatusServiceUnavailable {
		t.Fatalf("status = %d", response.Code)
	}
	if body := response.Body.String(); !strings.Contains(body, `"code":"NOT_READY"`) {
		t.Fatalf("body = %s", body)
	}
}

func TestNoRouteUsesErrorEnvelope(t *testing.T) {
	h := newTestServer(nil)
	response := ut.PerformRequest(h.Engine, consts.MethodGet, "/missing", nil)
	if response.Code != consts.StatusNotFound {
		t.Fatalf("status = %d", response.Code)
	}
	if body := response.Body.String(); !strings.Contains(body, `"code":"NOT_FOUND"`) {
		t.Fatalf("body = %s", body)
	}
}

func newTestServer(checkErr error) *server.Hertz {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := health.NewService(readyChecker{err: checkErr})
	return NewServer("127.0.0.1:0", time.Second, logger, service)
}
