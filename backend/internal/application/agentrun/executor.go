package agentrun

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
	conversation "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentconversation"
	run "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
)

const claimInterval = 500 * time.Millisecond
const maintenanceInterval = time.Second
const renewalInterval = 2 * time.Second

type ExecutorConfig struct {
	Enabled                                    bool
	Concurrency                                int
	Budget                                     run.BudgetLimits
	MaxAnswerChars, ProtocolBytes              int
	ProfileVersion, PromptVersion, ToolVersion string
}
type Supervisor struct {
	repo           Repository
	tx             ports.TxManager
	service        *Service
	guard          *Guard
	builder        ContextBuilder
	runner         Runner
	observer       Observer
	config         ExecutorConfig
	ctx            context.Context
	cancel         context.CancelFunc
	stopping       atomic.Bool
	reconciled     atomic.Bool
	once           sync.Once
	loopDone, done chan struct{}
	tasks          sync.WaitGroup
	slots          chan struct{}
}

func NewSupervisor(repo Repository, tx ports.TxManager, service *Service, guard *Guard, builder ContextBuilder, runner Runner, cfg ExecutorConfig) *Supervisor {
	return &Supervisor{repo: repo, tx: tx, service: service, guard: guard, builder: builder, runner: runner, config: cfg, loopDone: make(chan struct{}), done: make(chan struct{})}
}
func (s *Supervisor) WithObserver(observer Observer) *Supervisor { s.observer = observer; return s }

func (s *Supervisor) Start(ctx context.Context) error {
	if s.ctx != nil || s.repo == nil || s.tx == nil || s.service == nil || s.config.Concurrency < 1 || s.config.Concurrency > 16 {
		return run.ErrInvalidInput
	}
	if s.config.Enabled && (s.guard == nil || s.builder == nil || s.runner == nil) {
		return &run.Error{Code: run.CodeGenerationUnavailable}
	}
	s.ctx, s.cancel = context.WithCancel(context.WithoutCancel(ctx))
	s.slots = make(chan struct{}, s.config.Concurrency)
	// 重启先收敛过期事实，再允许领取；故障时不会启动新的模型。
	if err := s.maintain(); err == nil {
		s.reconciled.Store(true)
	}
	go s.loop()
	go func() {
		select {
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			_ = s.Stop(shutdown)
		case <-s.done:
		}
	}()
	return nil
}
func (s *Supervisor) maintain() error {
	ctx, cancel := context.WithTimeout(s.ctx, 2*time.Second)
	defer cancel()
	if err := s.tx.WithinTransaction(ctx, func(ctx context.Context) error { _, err := s.repo.Maintain(ctx, 100); return err }); err != nil {
		return err
	}
	return s.tx.WithinTransaction(ctx, func(ctx context.Context) error { _, err := s.repo.Cleanup(ctx, 100); return err })
}
func (s *Supervisor) loop() {
	defer close(s.loopDone)
	claims := time.NewTicker(claimInterval)
	maintenance := time.NewTicker(maintenanceInterval)
	defer claims.Stop()
	defer maintenance.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-maintenance.C:
			if err := s.maintain(); err == nil {
				s.reconciled.Store(true)
			}
		case <-claims.C:
			if s.config.Enabled && !s.stopping.Load() && s.reconciled.Load() {
				s.claim()
			}
		}
	}
}
func (s *Supervisor) claim() {
	available := cap(s.slots) - len(s.slots)
	if available == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, 2*time.Second)
	defer cancel()
	candidates, err := s.repo.Candidates(ctx, available)
	if err != nil {
		return
	}
	for _, candidate := range candidates {
		if s.stopping.Load() {
			return
		}
		owner, err := account.NewUUID()
		if err != nil {
			return
		}
		var value run.Run
		err = s.tx.WithinTransaction(ctx, func(ctx context.Context) error {
			var err error
			value, err = s.repo.Claim(ctx, candidate.Scope, owner.String())
			return err
		})
		if err != nil {
			continue
		}
		s.slots <- struct{}{}
		s.tasks.Add(1)
		go func(v run.Run) { defer s.tasks.Done(); defer func() { <-s.slots }(); s.execute(v) }(value)
	}
}
func (s *Supervisor) Stop(ctx context.Context) error {
	if s.ctx == nil {
		return nil
	}
	s.once.Do(func() {
		s.stopping.Store(true)
		s.service.StopAccepting()
		s.cancel()
		go func() { <-s.loopDone; s.tasks.Wait(); close(s.done) }()
	})
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type executionSink struct {
	supervisor *Supervisor
	execution  Execution
	started    time.Time
	first      time.Duration
}

func (s *executionSink) SubmitBlock(ctx context.Context, commit BlockCommit) error {
	err := s.supervisor.tx.WithinTransaction(ctx, func(ctx context.Context) error { return s.supervisor.repo.AppendBlock(ctx, s.execution, commit) })
	if err == nil && s.first == 0 {
		s.first = time.Since(s.started)
	}
	return err
}
func (s *executionSink) ToolProgress(ctx context.Context, name, status string, code run.Code) error {
	return s.supervisor.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		return s.supervisor.repo.AppendToolStatus(ctx, s.execution, name, status, code)
	})
}
func (s *executionSink) UpdateUsage(ctx context.Context, usage run.Usage) error {
	return s.supervisor.tx.WithinTransaction(ctx, func(ctx context.Context) error { return s.supervisor.repo.SaveUsage(ctx, s.execution, usage) })
}

func (s *Supervisor) execute(v run.Run) {
	started := time.Now()
	e := Execution{Scope: Scope{v.UserID, v.ConversationID, v.ID}, SessionID: v.SessionID, OwnerToken: v.OwnerToken}
	ctx, cancel := context.WithDeadline(s.ctx, v.Deadline)
	defer cancel()
	budget, err := run.NewBudget(s.config.Budget)
	if err != nil {
		s.finish(e, run.Failed, run.CodeInternal, run.Usage{})
		return
	}
	reason := make(chan run.Code, 1)
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		checks := time.NewTicker(time.Second)
		renew := time.NewTicker(renewalInterval)
		defer checks.Stop()
		defer renew.Stop()
		for {
			var err error
			select {
			case <-ctx.Done():
				return
			case <-checks.C:
				checkCtx, end := context.WithTimeout(ctx, 750*time.Millisecond)
				err = s.guard.Check(checkCtx, e)
				end()
			case <-renew.C:
				renewCtx, end := context.WithTimeout(ctx, 750*time.Millisecond)
				err = s.tx.WithinTransaction(renewCtx, func(ctx context.Context) error { return s.repo.Renew(ctx, e) })
				end()
			}
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				code := SafeCode(err)
				if code == run.CodeInternal {
					code = run.CodeDependencyUnavailable
				}
				select {
				case reason <- code:
				default:
				}
				cancel()
				return
			}
		}
	}()
	sink := &executionSink{supervisor: s, execution: e, started: v.CreatedAt}
	var result ExecutionResult
	err = s.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		return s.repo.SaveExecutionMetadata(ctx, e, ExecutionMetadata{ProfileVersion: s.config.ProfileVersion, PromptVersion: s.config.PromptVersion, ToolVersion: s.config.ToolVersion, Budget: s.config.Budget})
	})
	var question conversation.Message
	if err == nil {
		question, err = s.repo.Message(ctx, v.ConversationID, v.Input.MessageID)
	}
	if err == nil {
		var contextValue Context
		contextValue, err = s.builder.Build(ctx, v.UserID, v.ConversationID, question, v.Input.ContextArticleID, v.Input.Constraints, s.config.ProtocolBytes)
		if err == nil {
			result, err = s.runner.Execute(ctx, ExecutionRequest{Execution: e, Context: contextValue, Budget: budget, Deadline: v.Deadline}, sink)
		}
	}
	// 不让定期读/续租与最后短事务相互等待；最终提交仍复核数据库执行权。
	cancel()
	<-monitorDone
	state, code := run.Failed, SafeCode(err)
	select {
	case code = <-reason:
	default:
	}
	if s.stopping.Load() || s.ctx.Err() != nil {
		code = run.CodeExecutorStopped
	} else if code == run.CodeInternal {
		if errors.Is(err, context.DeadlineExceeded) {
			code = run.CodeTimeout
		} else if errors.Is(err, context.Canceled) {
			code = run.CodeCanceled
		} else {
			code = run.CodeDependencyUnavailable
		}
	}
	if code == run.CodeAuthInvalid || code == run.CodeCanceled {
		state = run.Canceled
	}
	if code == run.CodeTimeout {
		state = run.TimedOut
	}
	if err == nil && code == "" {
		commitCtx, end := context.WithTimeout(context.WithoutCancel(s.ctx), 2*time.Second)
		err = s.commit(commitCtx, e, result)
		end()
		if err == nil {
			state, code = run.Succeeded, ""
		} else {
			code = SafeCode(err)
			if code == run.CodeInternal {
				code = run.CodeDependencyUnavailable
			}
		}
	}
	if state != run.Succeeded {
		if code == run.CodeAuthInvalid || code == run.CodeCanceled {
			state = run.Canceled
		}
		if code == run.CodeTimeout {
			state = run.TimedOut
		}
		record, finishErr := s.finish(e, state, code, budget.Usage())
		if finishErr == nil {
			state, code = record.Run.State, record.Run.ErrorCode
		}
	}
	if s.observer != nil {
		s.observer.ObserveRun(context.WithoutCancel(s.ctx), Observation{State: state, ErrorCode: code, Usage: budget.Usage(), FirstBlock: sink.first, Duration: time.Since(started)})
	}
}

func (s *Supervisor) commit(ctx context.Context, e Execution, result ExecutionResult) error {
	err := s.service.WithAssistantReservation(ctx, e, func(ctx context.Context, c conversation.Conversation, _ Record) error {
		id, err := account.NewUUID()
		if err != nil {
			return err
		}
		now, err := s.repo.DatabaseTime(ctx)
		if err != nil {
			return err
		}
		m, err := conversation.NewMessage(id.String(), c.ID, c.NextSequence, conversation.RoleAssistant, AnswerText(result.Answer), s.config.MaxAnswerChars+2*32, now)
		if err != nil {
			return err
		}
		return s.repo.Complete(ctx, e, SuccessCommit{Answer: result.Answer, Assistant: m, Usage: result.Usage, Constraints: result.Constraints, MaxMessages: s.service.config.MaxMessages, MaxAnswerChars: s.config.MaxAnswerChars})
	})
	if err == nil {
		return nil
	}
	// 提交响应不确定时只查询原回合，不重新执行模型或重做助手追加。
	check, end := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer end()
	record, readErr := s.repo.Find(check, e.Scope)
	if readErr == nil && record.Run.State == run.Succeeded {
		return nil
	}
	return err
}
func (s *Supervisor) finish(e Execution, state run.State, code run.Code, usage run.Usage) (Record, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), 2*time.Second)
	defer cancel()
	var record Record
	err := s.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		var err error
		record, err = s.repo.TerminateExecution(ctx, e, state, code, usage)
		return err
	})
	return record, err
}
