// Package idempotency 提供写命令的规范摘要、事务执行和成功结果重放。
package idempotency

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
)

var (
	ErrKeyReused = errors.New("幂等键已被不同请求使用")
	ErrPending   = errors.New("幂等操作仍在执行")
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusSucceeded Status = "succeeded"
)

type Identity struct {
	ActorUserID string
	Operation   string
	Key         string
}

type Record struct {
	Identity
	Digest        [32]byte
	Status        Status
	ResultVersion int
	Result        json.RawMessage
	CreatedAt     time.Time
	CompletedAt   *time.Time
	ExpiresAt     time.Time
}

type Repository interface {
	Begin(context.Context, Identity, [32]byte, time.Time, time.Time) (Record, bool, error)
	Succeed(context.Context, Identity, int, json.RawMessage, string, string, time.Time) error
}

type Service struct {
	repository Repository
	txManager  ports.TxManager
	retention  time.Duration
}

func NewService(repository Repository, txManager ports.TxManager, retention time.Duration) *Service {
	return &Service{repository: repository, txManager: txManager, retention: retention}
}

type Command struct {
	Identity
	Payload any
	Now     time.Time
}

type Outcome struct {
	Result   json.RawMessage
	Replayed bool
}

type ExecuteFunc func(context.Context) (result any, resourceType, resourceID string, err error)

func (s *Service) Execute(ctx context.Context, command Command, execute ExecuteFunc) (Outcome, error) {
	digest, err := Digest(command.Payload)
	if err != nil {
		return Outcome{}, err
	}
	var outcome Outcome
	err = s.txManager.WithinTransaction(ctx, func(txContext context.Context) error {
		record, acquired, err := s.repository.Begin(txContext, command.Identity, digest, command.Now, command.Now.Add(s.retention))
		if err != nil {
			return err
		}
		if !acquired {
			if record.Status != StatusSucceeded {
				return ErrPending
			}
			outcome = Outcome{Result: append(json.RawMessage(nil), record.Result...), Replayed: true}
			return nil
		}
		result, resourceType, resourceID, err := execute(txContext)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("编码幂等结果: %w", err)
		}
		if err := s.repository.Succeed(txContext, command.Identity, 1, encoded, resourceType, resourceID, command.Now); err != nil {
			return err
		}
		outcome = Outcome{Result: encoded}
		return nil
	})
	return outcome, err
}

// Reservation 是一次幂等占位的状态：调用方据此决定执行、重放还是返回在飞结果。
type Reservation struct {
	// Acquired 为真表示本次调用取得了执行权，应当继续执行业务动作。
	Acquired bool
	// Replay 非空表示该键已有成功结果，调用方必须原样重放而不是重新执行。
	Replay json.RawMessage
	// Pending 为真表示同键的首次执行仍在进行中。
	Pending bool
}

// Reserve 在独立短事务里占位，适用于不能把网络 I/O 包在单个事务里的操作。
// 与 Execute 不同，成功结果必须由调用方在自己的完成事务里通过 Settle 写回。
func (s *Service) Reserve(ctx context.Context, command Command) (Reservation, error) {
	digest, err := Digest(command.Payload)
	if err != nil {
		return Reservation{}, err
	}
	var reservation Reservation
	err = s.txManager.WithinTransaction(ctx, func(txContext context.Context) error {
		record, acquired, err := s.repository.Begin(txContext, command.Identity, digest, command.Now, command.Now.Add(s.retention))
		if err != nil {
			return err
		}
		switch {
		case acquired:
			reservation = Reservation{Acquired: true}
		case record.Status == StatusSucceeded:
			reservation = Reservation{Replay: append(json.RawMessage(nil), record.Result...)}
		default:
			reservation = Reservation{Pending: true}
		}
		return nil
	})
	return reservation, err
}

// Settle 在调用方的完成事务里写回成功结果；与业务变更同事务提交。
func (s *Service) Settle(ctx context.Context, identity Identity, result any, resourceType, resourceID string, completedAt time.Time) error {
	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("编码幂等结果: %w", err)
	}
	return s.repository.Succeed(ctx, identity, 1, encoded, resourceType, resourceID, completedAt)
}

// Digest 对已解析的语义命令做确定性 JSON 编码，不依赖原始请求字段顺序和空白。
func Digest(payload any) ([32]byte, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return [32]byte{}, fmt.Errorf("编码幂等请求摘要: %w", err)
	}
	return sha256.Sum256(encoded), nil
}
