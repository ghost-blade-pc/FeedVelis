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

// 抓取周期边界：调度密度直接决定上游压力，因此上下界是领域规则而不是配置自由项。
const (
	MinFetchInterval     = 5 * time.Minute
	MaxFetchInterval     = 24 * time.Hour
	DefaultFetchInterval = 30 * time.Minute
)

type Status string

const (
	StatusActive   Status = "active"
	StatusPaused   Status = "paused"
	StatusDegraded Status = "degraded"
)

var (
	ErrInvalidURL      = errors.New("来源 URL 无效")
	ErrInvalidStatus   = errors.New("来源状态无效")
	ErrInvalidInterval = errors.New("抓取周期无效")
	ErrVersionConflict = errors.New("来源版本冲突")
	ErrNotFound        = errors.New("来源不存在")
	ErrLeaseHeld       = errors.New("来源正在被其他抓取任务处理")
	ErrLeaseLost       = errors.New("来源抓取租约已失效")
)

// Source 是系统级 Feed 来源。FeedURL 与 NormalizedFeedURL 创建后不可修改：
// 更换地址必须新增来源并暂停旧来源，避免历史文章与去重身份被悄悄改写。
type Source struct {
	ID                  int64
	FeedURL             string
	NormalizedFeedURL   string
	SiteURL             *string
	Title               string
	Status              Status
	FetchInterval       time.Duration
	ETag                *string
	LastModified        *string
	NextFetchAt         time.Time
	LastCheckedAt       *time.Time
	LastSuccessAt       *time.Time
	ConsecutiveFailures int
	LastErrorCode       *string
	LeaseOwner          *string
	LeaseExpiresAt      *time.Time
	// LeaseGeneration 每次认领递增，是抓取完成写入的 fencing 依据。
	LeaseGeneration int64
	LockVersion     int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// FetchIntervalOr 返回来源自身的抓取周期；未设置时使用默认值。
func (s Source) FetchIntervalOr() time.Duration {
	if s.FetchInterval <= 0 {
		return DefaultFetchInterval
	}
	return s.FetchInterval
}

// ValidateFetchInterval 校验抓取周期：必须落在 5 分钟至 24 小时之间且是整秒，
// 后者是为了不把亚秒精度悄悄截断进数据库的秒级列。
func ValidateFetchInterval(value time.Duration) error {
	if value < MinFetchInterval || value > MaxFetchInterval || value%time.Second != 0 {
		return ErrInvalidInterval
	}
	return nil
}

// CheckVersion 校验调用者持有的聚合版本；暂停、恢复与周期修改都必须先通过它。
func CheckVersion(current, expected int64) error {
	if expected <= 0 || current != expected {
		return ErrVersionConflict
	}
	return nil
}

type Metadata struct {
	Title        string
	SiteURL      *string
	ETag         *string
	LastModified *string
}

// Lease 是一次认领返回的 fencing generation。Owner 与 ExpiresAt 必须原样参与完成写入。
type Lease struct {
	Owner     string
	ExpiresAt time.Time
}

type FailureUpdate struct {
	Status              Status
	ConsecutiveFailures int
	LastErrorCode       string
	LastCheckedAt       time.Time
	NextFetchAt         time.Time
}

type Repository interface {
	// Add 以规范化 URL 去重；已存在时返回既有来源且 inserted 为 false。
	Add(context.Context, string, string, string, time.Duration, time.Time) (Source, bool, error)
	List(context.Context) ([]Source, error)
	Get(context.Context, int64) (Source, error)
	ClaimByID(context.Context, int64, string, time.Time, time.Time) (Source, error)
	// Pause、Resume 与 SetFetchInterval 都要求当前 lock_version；版本不符返回 ErrVersionConflict。
	Pause(context.Context, int64, int64, time.Time) (Source, error)
	Resume(context.Context, int64, int64, time.Time) (Source, error)
	SetFetchInterval(context.Context, int64, int64, time.Duration, time.Time) (Source, error)
	ClaimDue(context.Context, string, time.Time, time.Time, int) ([]Source, error)
	MarkNotModified(context.Context, int64, Lease, *string, *string, time.Time, time.Time) error
	MarkSuccess(context.Context, int64, Lease, Metadata, time.Time, time.Time) error
	MarkFailure(context.Context, int64, Lease, FailureUpdate) error
}

func (s Source) CurrentLease() (Lease, error) {
	if s.LeaseOwner == nil || *s.LeaseOwner == "" || s.LeaseExpiresAt == nil || s.LeaseExpiresAt.IsZero() {
		return Lease{}, ErrLeaseLost
	}
	return Lease{Owner: *s.LeaseOwner, ExpiresAt: *s.LeaseExpiresAt}, nil
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
