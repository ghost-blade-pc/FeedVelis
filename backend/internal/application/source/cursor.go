package source

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"time"

	sourceDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/source"
)

// runCursorPayload 只携带稳定排序键：开始时间与运行 ID。
type runCursorPayload struct {
	Version   int       `json:"v"`
	StartedAt time.Time `json:"started_at"`
	ID        string    `json:"id"`
}

func encodeRunCursor(run sourceDomain.FetchRun) string {
	data, _ := json.Marshal(runCursorPayload{Version: 1, StartedAt: run.StartedAt.UTC(), ID: run.ID})
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeRunCursor(value string) (sourceDomain.FetchRunCursor, error) {
	if len(value) > 1024 {
		return sourceDomain.FetchRunCursor{}, sourceDomain.ErrInvalidRunCursor
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return sourceDomain.FetchRunCursor{}, sourceDomain.ErrInvalidRunCursor
	}
	var payload runCursorPayload
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || payload.Version != 1 || payload.ID == "" || payload.StartedAt.IsZero() {
		return sourceDomain.FetchRunCursor{}, sourceDomain.ErrInvalidRunCursor
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return sourceDomain.FetchRunCursor{}, sourceDomain.ErrInvalidRunCursor
	}
	return sourceDomain.FetchRunCursor{StartedAt: payload.StartedAt.UTC(), ID: payload.ID}, nil
}
