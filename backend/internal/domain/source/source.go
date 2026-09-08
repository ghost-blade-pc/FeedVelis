// Package source 定义系统级 Feed 来源及抓取状态规则。
package source

import (
	"context"
	"errors"
	"math"
	"net/url"
	"strings"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/shared"
)

const (
	MaxFeedURLBytes = 4096
	MaxTitleRunes   = 500
)

type Status string

const (
	StatusActive   Status = "active"
	StatusPaused   Status = "paused"
	StatusDegraded Status = "degraded"
)

var (
	ErrInvalidURL    = errors.New("来源 URL 无效")
	ErrInvalidStatus = errors.New("来源状态无效")
	ErrNotFound      = errors.New("来源不存在")
	ErrLeaseHeld     = errors.New("来源正在被其他抓取任务处理")
)

type Source struct {
	ID                  int64
	FeedURL             string
	NormalizedFeedURL   string
	SiteURL             *string
	Title               string
	Status              Status
	ETag                *string
	LastModified        *string
	NextFetchAt         time.Time
	LastCheckedAt       *time.Time
	LastSuccessAt       *time.Time
	ConsecutiveFailures int
	LastErrorCode       *string
	LeaseOwner          *string
	LeaseExpiresAt      *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type Metadata struct {
	Title        string
	SiteURL      *string
	ETag         *string
	LastModified *string
}

type FailureUpdate struct {
	Status              Status
	ConsecutiveFailures int
	LastErrorCode       string
	LastCheckedAt       time.Time
	NextFetchAt         time.Time
}

type Repository interface {
	Add(context.Context, string, string, string, time.Time) (Source, bool, error)
	List(context.Context) ([]Source, error)
	Get(context.Context, int64) (Source, error)
	ClaimByID(context.Context, int64, string, time.Time, time.Time) (Source, error)
	Pause(context.Context, int64, time.Time) error
	Resume(context.Context, int64, time.Time) error
	ClaimDue(context.Context, string, time.Time, time.Time, int) ([]Source, error)
	MarkNotModified(context.Context, int64, *string, *string, time.Time, time.Time) error
	MarkSuccess(context.Context, int64, Metadata, time.Time, time.Time) error
	MarkFailure(context.Context, int64, FailureUpdate) error
}

func NormalizeFeedURL(raw string) (string, error) {
	value, err := shared.NormalizeHTTPURL(raw, MaxFeedURLBytes)
	if err != nil {
		return "", ErrInvalidURL
	}
	return value, nil
}

func DefaultTitle(normalizedURL string) string {
	u, err := url.Parse(normalizedURL)
	if err != nil || u.Hostname() == "" {
		return "未知来源"
	}
	return u.Hostname()
}

func TruncateRunes(value string, limit int) string {
	value = strings.ReplaceAll(strings.ToValidUTF8(value, "�"), "\x00", "�")
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > limit {
		runes = runes[:limit]
	}
	return string(runes)
}

func NextFailure(now time.Time, failures int, errorCode string, jitter float64) FailureUpdate {
	if failures < 0 {
		failures = 0
	}
	failures++
	if jitter < -0.2 {
		jitter = -0.2
	}
	if jitter > 0.2 {
		jitter = 0.2
	}
	exponent := math.Min(float64(failures-1), 9)
	delay := time.Duration(float64(time.Minute*time.Duration(1<<int(exponent))) * (1 + jitter))
	if delay > 6*time.Hour {
		delay = 6 * time.Hour
	}
	status := StatusActive
	if failures >= 5 {
		status = StatusDegraded
	}
	return FailureUpdate{
		Status:              status,
		ConsecutiveFailures: failures,
		LastErrorCode:       errorCode,
		LastCheckedAt:       now.UTC(),
		NextFetchAt:         now.UTC().Add(delay),
	}
}
