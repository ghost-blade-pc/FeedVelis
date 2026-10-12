package integration

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	orch "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentorchestration"
	agent "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentrun"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
	conversation "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentconversation"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentevidence"
	run "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

func claimedRun(t *testing.T, env *testEnv) (*agent.Service, *postgres.AgentRunRepository, agent.Execution) {
	t.Helper()
	seedAgentSessions(t, env)
	seedFeedbackArticle(t, env)
	c, m := agentQuestion(t, env, 1)
	s := newRunService(env, 200, true).WithProjector(agent.NewProjector(postgres.NewAgentEvidenceFacts(env.pool)))
	accepted, err := s.Accept(t.Context(), agentCaller, c.ID, agentKey(1), agent.Command{MessageID: m.ID})
	if err != nil {
		t.Fatal(err)
	}
	repo := postgres.NewAgentRunRepository(env.pool)
	var value run.Run
	err = postgres.NewTxManager(env.pool).WithinTransaction(t.Context(), func(ctx context.Context) error {
		var err error
		value, err = repo.Claim(ctx, agent.Scope{UserID: agentTestUser, ConversationID: c.ID, RunID: accepted.Snapshot.ID}, agentKey(700))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, repo, agent.Execution{Scope: agent.Scope{UserID: agentTestUser, ConversationID: c.ID, RunID: value.ID}, SessionID: agentTestSession, OwnerToken: value.OwnerToken}
}

func TestAgentRunLeaseLossDeadlineAndNoAutomaticRerun(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(fmt.Sprint("timeout=", timeout), func(t *testing.T) {
			env := newTestEnv(t)
			s, repo, e := claimedRun(t, env)
			old, _, _ := repo.ReadExecution(t.Context(), e.Scope)
			var err error
			if timeout {
				_, err = env.pool.Exec(t.Context(), `UPDATE velis.agent_runs SET created_at=clock_timestamp()-interval '61 seconds',deadline=clock_timestamp()-interval '1 second',lease_until=clock_timestamp()-interval '2 seconds' WHERE id=$1`, e.RunID)
			} else {
				_, err = env.pool.Exec(t.Context(), `UPDATE velis.agent_runs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, e.RunID)
			}
			if err != nil {
				t.Fatal(err)
			}
			runner := &executionRunner{}
			_, _ = startExecutor(t, env, runner, true)
			state, code := run.Failed, run.CodeExecutorLost
			if timeout {
				state, code = run.TimedOut, run.CodeTimeout
			}
			record := awaitRun(t, repo, e.Scope, state, 4*time.Second)
			if record.Run.ErrorCode != code || record.Run.AssistantReserved || runner.calls.Load() != 0 || (!timeout && !record.Run.Deadline.Equal(old.Deadline)) {
				t.Fatal(record, runner.calls.Load())
			}
			if _, err := s.Detail(t.Context(), agentCaller, e.ConversationID, e.RunID); err != nil {
				t.Fatal("崩溃后仍可查询", err)
			}
			if err := postgres.NewTxManager(env.pool).WithinTransaction(t.Context(), func(ctx context.Context) error { return repo.Renew(ctx, e) }); !errors.Is(err, run.ErrExecutionLost) {
				t.Fatal("过期 owner 复活", err)
			}
		})
	}
}

func TestAgentRunFinalFactsAndLoweredQuotaReject(t *testing.T) {
	for _, mode := range []string{"offline", "quota"} {
		t.Run(mode, func(t *testing.T) {
			env := newTestEnv(t)
			_, repo, e := claimedRun(t, env)
			appendBlock(t, env, repo, e)
			commit := successCommit(t, env, repo, e)
			expected := run.CodeEvidenceUnavailable
			if mode == "offline" {
				if _, err := env.pool.Exec(t.Context(), `UPDATE velis.articles SET status='offline',offline_reason='author',offline_at=now() WHERE id=7201`); err != nil {
					t.Fatal(err)
				}
			} else {
				commit.MaxMessages = 1
				expected = run.CodeMessageLimit
			}
			err := postgres.NewTxManager(env.pool).WithinTransaction(t.Context(), func(ctx context.Context) error { return repo.Complete(ctx, e, commit) })
			if agent.SafeCode(err) != expected {
				t.Fatal("最终复核未拒绝", err)
			}
			record, _ := repo.Find(t.Context(), e.Scope)
			if record.Run.State != run.Running || !record.Run.AssistantReserved {
				t.Fatal(record)
			}
		})
	}
}

func TestAgentRunDatabaseUnavailableOnStopUsesLeaseFallback(t *testing.T) {
	env := newTestEnv(t)
	seedAgentSessions(t, env)
	c, m := agentQuestion(t, env, 1)
	runner := &executionRunner{started: make(chan struct{}, 1), hold: true}
	supervisor, s := startExecutor(t, env, runner, true)
	a, err := s.Accept(t.Context(), agentCaller, c.ID, agentKey(1), agent.Command{MessageID: m.ID})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.started:
	case <-time.After(3 * time.Second):
		t.Fatal("未启动")
	}
	// 关闭该 API 实例的数据库连接池，模拟依赖先停；其他本地服务不受影响。
	env.pool.Close()
	stop, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := supervisor.Stop(stop); err != nil {
		t.Fatal(err)
	}
	recovered, err := pgxpool.New(t.Context(), env.databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(recovered.Close)
	repo := postgres.NewAgentRunRepository(recovered)
	scope := agent.Scope{UserID: agentTestUser, ConversationID: c.ID, RunID: a.Snapshot.ID}
	record, err := repo.Find(t.Context(), scope)
	if err != nil || record.Run.State != run.Running {
		t.Fatal("落库失败时不应伪造关闭终态", record, err)
	}
	if _, err := recovered.Exec(t.Context(), `UPDATE velis.agent_runs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, a.Snapshot.ID); err != nil {
		t.Fatal(err)
	}
	if err := postgres.NewTxManager(recovered).WithinTransaction(t.Context(), func(ctx context.Context) error { _, err := repo.Maintain(ctx, 100); return err }); err != nil {
		t.Fatal(err)
	}
	record, err = repo.Find(t.Context(), scope)
	if err != nil || record.Run.State != run.Failed || record.Run.ErrorCode != run.CodeExecutorLost || record.Run.AssistantReserved {
		t.Fatal(record, err)
	}
}

func TestAgentRunFinalCommitResponseLossDoesNotRegenerate(t *testing.T) {
	env := newTestEnv(t)
	seedAgentSessions(t, env)
	c, m := agentQuestion(t, env, 1)
	repo := postgres.NewAgentRunRepository(env.pool)
	tx := postgres.NewTxManager(env.pool)
	lost := &lostAcceptResponse{TxManager: tx, once: true}
	service := agent.NewService(repo, repo.AgentConversationRepository, lost, tx, repo, postgres.NewIdempotencyRepository(env.pool), agent.Config{Accepting: true, Timeout: time.Minute, MaxMessages: 200})
	a, err := service.Accept(t.Context(), agentCaller, c.ID, agentKey(1), agent.Command{MessageID: m.ID})
	if err != nil {
		t.Fatal(err)
	}
	lost.once = false
	runner := &executionRunner{}
	guard := agent.NewGuard(repo, repo, tx)
	builder := orch.NewContextBuilder(repo, agent.EmptyPreferences{}, agent.NewProjector(postgres.NewAgentEvidenceFacts(env.pool)), 49152)
	supervisor := agent.NewSupervisor(repo, tx, service, guard, builder, runner, agent.ExecutorConfig{Enabled: true, Concurrency: 1, Budget: run.DefaultBudgetLimits(), MaxAnswerChars: 8000})
	if err := supervisor.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = supervisor.Stop(ctx)
	}()
	record := awaitRun(t, repo, agent.Scope{UserID: agentTestUser, ConversationID: c.ID, RunID: a.Snapshot.ID}, run.Succeeded, 4*time.Second)
	if runner.calls.Load() != 1 || record.Run.AssistantReserved {
		t.Fatal(record)
	}
	var assistants, terminals int
	_ = env.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM velis.agent_messages WHERE role='assistant'),(SELECT count(*) FROM velis.agent_run_events WHERE kind='run.terminal')`).Scan(&assistants, &terminals)
	if assistants != 1 || terminals != 1 {
		t.Fatal(assistants, terminals)
	}
}
func blockCommit(t *testing.T, env *testEnv, e agent.Execution) agent.BlockCommit {
	t.Helper()
	facts, err := postgres.NewAgentEvidenceFacts(env.pool).Current(t.Context(), []agent.ArticleIdentity{{ArticleID: 7201, RevisionID: 7211}})
	if err != nil || len(facts) != 1 {
		t.Fatal(facts, err)
	}
	card := facts[0].Card
	card.Number = 1
	return agent.BlockCommit{Block: agentevidence.Block{ID: "b1", Text: "正文解释", EvidenceIDs: []string{agentTestEvidence}, Quotes: []agentevidence.Quote{{EvidenceID: agentTestEvidence, Text: "正文"}}}, Evidence: []agentevidence.Evidence{{Version: 1, ID: agentTestEvidence, RunID: e.RunID, Kind: agentevidence.Article, ArticleID: 7201, RevisionID: 7211, ObtainedAt: time.Now(), ContentKind: agentevidence.Body, Content: "正文"}}, References: []agentevidence.Reference{{EvidenceID: agentTestEvidence, Number: 1, Kind: agentevidence.Article, ArticleID: 7201, RevisionID: 7211, ContentKind: agentevidence.Body}}, Cards: []agentevidence.Card{card}, RequireBody: true, MaxAnswerChars: 8000}
}
func appendBlock(t *testing.T, env *testEnv, repo *postgres.AgentRunRepository, e agent.Execution) agent.BlockCommit {
	t.Helper()
	b := blockCommit(t, env, e)
	if err := postgres.NewTxManager(env.pool).WithinTransaction(t.Context(), func(ctx context.Context) error { return repo.AppendBlock(ctx, e, b) }); err != nil {
		t.Fatal(err)
	}
	return b
}
func successCommit(t *testing.T, env *testEnv, repo *postgres.AgentRunRepository, e agent.Execution) agent.SuccessCommit {
	t.Helper()
	r, err := repo.Find(t.Context(), e.Scope)
	if err != nil || r.Answer == nil {
		t.Fatal(r, err)
	}
	m, err := conversation.NewMessage(agentKey(800), e.ConversationID, 2, conversation.RoleAssistant, agent.AnswerText(*r.Answer), 8064, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return agent.SuccessCommit{Answer: *r.Answer, Assistant: m, MaxMessages: 200, MaxAnswerChars: 8000}
}

func TestAgentRunClaimRenewBlocksCompleteAndEvents(t *testing.T) {
	env := newTestEnv(t)
	s, repo, e := claimedRun(t, env)
	tx := postgres.NewTxManager(env.pool)
	before, dbNow, err := repo.ReadExecution(t.Context(), e.Scope)
	if err != nil || before.State != run.Running || before.LeaseUntil.Sub(dbNow) > 10*time.Second {
		t.Fatal(before, err)
	}
	if err := tx.WithinTransaction(t.Context(), func(ctx context.Context) error { return repo.Renew(ctx, e) }); err != nil {
		t.Fatal(err)
	}
	after, _, _ := repo.ReadExecution(t.Context(), e.Scope)
	if !after.Deadline.Equal(before.Deadline) || after.LeaseUntil.Before(before.LeaseUntil) {
		t.Fatal("续约延长总期限或缩短租约")
	}
	appendBlock(t, env, repo, e)
	partial, err := s.Detail(t.Context(), agentCaller, e.ConversationID, e.RunID)
	if err != nil || !partial.Partial || partial.Answer == nil || partial.Answer.Blocks[0].Text != "正文解释" {
		t.Fatal(partial, err)
	}
	events, err := repo.ReadEvents(t.Context(), e.Scope, 0, 128)
	if err != nil || len(events.Events) != 4 || events.Events[2].Kind != agent.AnswerSources || events.Events[3].Kind != agent.AnswerBlock || events.Events[3].Sequence != 4 {
		t.Fatal(events, err)
	}
	commit := successCommit(t, env, repo, e)
	if err := tx.WithinTransaction(t.Context(), func(ctx context.Context) error { return repo.Complete(ctx, e, commit) }); err != nil {
		t.Fatal(err)
	}
	detail, err := s.Detail(t.Context(), agentCaller, e.ConversationID, e.RunID)
	if err != nil || detail.Status != run.Succeeded || detail.Partial || detail.AssistantMessageID == nil {
		t.Fatal(detail, err)
	}
	var count, reserved, content int
	err = env.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM velis.agent_messages WHERE conversation_id=$1),(SELECT count(*) FROM velis.agent_runs WHERE id=$2 AND assistant_reserved),(SELECT count(*) FROM velis.agent_run_evidence WHERE run_id=$2 AND content IS NOT NULL)`, e.ConversationID, e.RunID).Scan(&count, &reserved, &content)
	if err != nil || count != 2 || reserved != 0 || content != 0 {
		t.Fatal(count, reserved, content, err)
	}
	if err := tx.WithinTransaction(t.Context(), func(ctx context.Context) error { return repo.Complete(ctx, e, commit) }); !errors.Is(err, run.ErrExecutionLost) {
		t.Fatal("重复成功写入", err)
	}
	canceled, err := s.Cancel(t.Context(), agentCaller, e.ConversationID, e.RunID)
	if err != nil || canceled.Status != run.Succeeded {
		t.Fatal("成功先提交未保留", canceled, err)
	}
	batch, err := repo.ReadEvents(t.Context(), e.Scope, 4, 128)
	if err != nil || len(batch.Events) != 1 || batch.Events[0].Kind != agent.RunTerminal || batch.Events[0].State != run.Succeeded {
		t.Fatal(batch, err)
	}
}

func TestAgentRunEventBudgetsReserveTerminalAndConcurrentOrder(t *testing.T) {
	for _, mode := range []string{"count", "bytes"} {
		t.Run(mode, func(t *testing.T) {
			env := newTestEnv(t)
			_, repo, e := claimedRun(t, env)
			tx := postgres.NewTxManager(env.pool)
			if mode == "count" {
				// 并发进度写入仍按同一账户、会话和回合锁顺序分配唯一序号。
				results := make(chan error, 125)
				var group sync.WaitGroup
				for i := 0; i < 125; i++ {
					group.Add(1)
					go func() {
						defer group.Done()
						results <- tx.WithinTransaction(t.Context(), func(ctx context.Context) error { return repo.AppendToolStatus(ctx, e, "get_article", "started", "") })
					}()
				}
				group.Wait()
				close(results)
				for err := range results {
					if err != nil {
						t.Fatal(err)
					}
				}
				var count, bytes, actual int
				if err := env.pool.QueryRow(t.Context(), `SELECT event_count,event_bytes,(SELECT sum(payload_bytes) FROM velis.agent_run_events WHERE run_id=$1) FROM velis.agent_runs WHERE id=$1`, e.RunID).Scan(&count, &bytes, &actual); err != nil || count != 127 || bytes != actual {
					t.Fatal(count, bytes, actual, err)
				}
			} else {
				if _, err := env.pool.Exec(t.Context(), `UPDATE velis.agent_runs SET event_bytes=512*1024-4096-5 WHERE id=$1`, e.RunID); err != nil {
					t.Fatal(err)
				}
			}
			err := tx.WithinTransaction(t.Context(), func(ctx context.Context) error { return repo.AppendToolStatus(ctx, e, "get_article", "started", "") })
			if !errors.Is(err, run.ErrBudget) {
				t.Fatal("事件额度未拒绝", err)
			}
			if err := tx.WithinTransaction(t.Context(), func(ctx context.Context) error {
				_, err := repo.Terminate(ctx, e.Scope, run.Canceled, run.CodeCanceled)
				return err
			}); err != nil {
				t.Fatal("终态预留失效", err)
			}
			batch, err := repo.ReadEvents(t.Context(), e.Scope, 0, 128)
			if err != nil || len(batch.Events) == 0 || batch.Events[len(batch.Events)-1].Kind != agent.RunTerminal {
				t.Fatal(batch, err)
			}
			for i, event := range batch.Events {
				if event.Sequence != int64(i+1) {
					t.Fatal("并发序号不连续", event)
				}
			}
			record, err := repo.Find(t.Context(), e.Scope)
			if err != nil || record.LastEventSequence > 128 || record.EventBytes > 512*1024 {
				t.Fatal(record, err)
			}
		})
	}
}

func TestAgentRunAtomicBlockAndSuccessFaults(t *testing.T) {
	for _, table := range []string{"agent_run_evidence", "agent_run_blocks", "agent_run_block_evidence", "agent_run_events"} {
		t.Run("block_"+table, func(t *testing.T) {
			env := newTestEnv(t)
			_, repo, e := claimedRun(t, env)
			tx := postgres.NewTxManager(env.pool)
			_, err := env.pool.Exec(t.Context(), fmt.Sprintf(`CREATE FUNCTION velis.agent_fault() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION '注入块故障'; END$$; CREATE TRIGGER agent_fault BEFORE INSERT ON velis.%s FOR EACH ROW EXECUTE FUNCTION velis.agent_fault()`, table))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_, _ = env.pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS velis.agent_fault() CASCADE`)
			})
			err = tx.WithinTransaction(t.Context(), func(ctx context.Context) error { return repo.AppendBlock(ctx, e, blockCommit(t, env, e)) })
			if err == nil {
				t.Fatal("未触发故障")
			}
			var work, events int
			_ = env.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM velis.agent_run_blocks)+(SELECT count(*) FROM velis.agent_run_evidence)+(SELECT count(*) FROM velis.agent_run_block_evidence),(SELECT count(*) FROM velis.agent_run_events)`).Scan(&work, &events)
			if work != 0 || events != 2 {
				t.Fatal("块未原子回滚", work, events)
			}
		})
	}
	for _, stage := range []string{"assistant", "answer", "terminal"} {
		t.Run("complete_"+stage, func(t *testing.T) {
			env := newTestEnv(t)
			_, repo, e := claimedRun(t, env)
			appendBlock(t, env, repo, e)
			commit := successCommit(t, env, repo, e)
			tx := postgres.NewTxManager(env.pool)
			table, operation, condition := "agent_messages", "INSERT", "NEW.role='assistant'"
			if stage == "answer" {
				table, operation, condition = "agent_runs", "UPDATE", "NEW.status='succeeded'"
			}
			if stage == "terminal" {
				table, operation, condition = "agent_run_events", "INSERT", "NEW.kind='run.terminal'"
			}
			_, err := env.pool.Exec(t.Context(), fmt.Sprintf(`CREATE FUNCTION velis.agent_fault() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION '注入成功提交故障'; END$$; CREATE TRIGGER agent_fault BEFORE %s ON velis.%s FOR EACH ROW WHEN (%s) EXECUTE FUNCTION velis.agent_fault()`, operation, table, condition))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_, _ = env.pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS velis.agent_fault() CASCADE`)
			})
			err = tx.WithinTransaction(t.Context(), func(ctx context.Context) error { return repo.Complete(ctx, e, commit) })
			if err == nil {
				t.Fatal("未触发成功故障")
			}
			record, err := repo.Find(t.Context(), e.Scope)
			if err != nil || record.Run.State != run.Running || !record.Run.AssistantReserved || record.LastEventSequence != 4 {
				t.Fatal("成功事实未回滚", record, err)
			}
			var messages, material int
			_ = env.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM velis.agent_messages),(SELECT count(*) FROM velis.agent_run_evidence WHERE content IS NOT NULL)`).Scan(&messages, &material)
			if messages != 1 || material != 1 {
				t.Fatal("半成功消息或提前清除材料", messages, material)
			}
			_, err = env.pool.Exec(t.Context(), fmt.Sprintf(`DROP TRIGGER agent_fault ON velis.%s; DROP FUNCTION velis.agent_fault()`, table))
			if err != nil {
				t.Fatal(err)
			}
			if err := tx.WithinTransaction(t.Context(), func(ctx context.Context) error { return repo.Complete(ctx, e, commit) }); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAgentRunCancellationAndLateOwner(t *testing.T) {
	env := newTestEnv(t)
	s, repo, e := claimedRun(t, env)
	appendBlock(t, env, repo, e)
	commit := successCommit(t, env, repo, e)
	tx := postgres.NewTxManager(env.pool)
	if _, err := s.Cancel(t.Context(), agentOtherCaller, e.ConversationID, e.RunID); !errors.Is(err, conversation.ErrNotFound) {
		t.Fatal("跨账户取消", err)
	}
	for n := 0; n < 2; n++ {
		summary, err := s.Cancel(t.Context(), agentCaller, e.ConversationID, e.RunID)
		if err != nil || summary.Status != run.Canceled || !summary.Partial {
			t.Fatal(summary, err)
		}
	}
	if err := tx.WithinTransaction(t.Context(), func(ctx context.Context) error { return repo.Complete(ctx, e, commit) }); !errors.Is(err, run.ErrExecutionLost) {
		t.Fatal("取消先提交仍写成功", err)
	}
	if err := tx.WithinTransaction(t.Context(), func(ctx context.Context) error { return repo.AppendBlock(ctx, e, blockCommit(t, env, e)) }); !errors.Is(err, run.ErrExecutionLost) {
		t.Fatal("迟到块写入", err)
	}
	c, m := agentQuestion(t, env, 2)
	accepted, err := s.Accept(t.Context(), agentCaller, c.ID, agentKey(2), agent.Command{MessageID: m.ID})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := s.Cancel(t.Context(), agentCaller, c.ID, accepted.Snapshot.ID)
	if err != nil || summary.Status != run.Canceled {
		t.Fatal("accepted 取消", summary, err)
	}
}

type executionRunner struct {
	calls   atomic.Int32
	started chan struct{}
	hold    bool
}

func (r *executionRunner) Execute(ctx context.Context, request agent.ExecutionRequest, _ agent.OutputSink) (agent.ExecutionResult, error) {
	r.calls.Add(1)
	if r.started != nil {
		select {
		case r.started <- struct{}{}:
		default:
		}
	}
	if r.hold {
		<-ctx.Done()
		return agent.ExecutionResult{}, ctx.Err()
	}
	answer, err := orch.Template(orch.DecisionFacts{Clarification: orch.MissingTask})
	return agent.ExecutionResult{Answer: answer, Usage: request.Budget.Usage(), Constraints: request.Context.Constraints}, err
}
func startExecutor(t *testing.T, env *testEnv, runner agent.Runner, enabled bool) (*agent.Supervisor, *agent.Service) {
	t.Helper()
	repo := postgres.NewAgentRunRepository(env.pool)
	tx := postgres.NewTxManager(env.pool)
	s := newRunService(env, 200, enabled).WithProjector(agent.NewProjector(postgres.NewAgentEvidenceFacts(env.pool)))
	guard := agent.NewGuard(repo, repo, tx)
	builder := orch.NewContextBuilder(repo, agent.EmptyPreferences{}, agent.NewProjector(postgres.NewAgentEvidenceFacts(env.pool)), 49152)
	supervisor := agent.NewSupervisor(repo, tx, s, guard, builder, runner, agent.ExecutorConfig{Enabled: enabled, Concurrency: 4, Budget: run.DefaultBudgetLimits(), MaxAnswerChars: 8000})
	if err := supervisor.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := supervisor.Stop(ctx); err != nil {
			t.Error(err)
		}
	})
	return supervisor, s
}
func awaitRun(t *testing.T, repo *postgres.AgentRunRepository, scope agent.Scope, state run.State, timeout time.Duration) agent.Record {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		record, err := repo.Find(t.Context(), scope)
		if err == nil && record.Run.State == state {
			return record
		}
		time.Sleep(20 * time.Millisecond)
	}
	record, err := repo.Find(t.Context(), scope)
	t.Fatalf("等待 %s 超时: %+v %v", state, record.Run, err)
	return record
}

func TestAgentRunTwoExecutorsOneOwnerAndGracefulStop(t *testing.T) {
	env := newTestEnv(t)
	seedAgentSessions(t, env)
	c, m := agentQuestion(t, env, 1)
	s := newRunService(env, 200, true)
	accepted, err := s.Accept(t.Context(), agentCaller, c.ID, agentKey(1), agent.Command{MessageID: m.ID})
	if err != nil {
		t.Fatal(err)
	}
	runner := &executionRunner{}
	_, _ = startExecutor(t, env, runner, true)
	_, _ = startExecutor(t, env, runner, true)
	scope := agent.Scope{UserID: agentTestUser, ConversationID: c.ID, RunID: accepted.Snapshot.ID}
	repo := postgres.NewAgentRunRepository(env.pool)
	record := awaitRun(t, repo, scope, run.Succeeded, 4*time.Second)
	if runner.calls.Load() != 1 || record.Run.AssistantReserved || record.Run.AssistantMessageID == "" {
		t.Fatal(record, runner.calls.Load())
	}
}

func TestAgentRunExecutorCancelAuthStopAndDisabledMaintenance(t *testing.T) {
	for _, mode := range []string{"cancel", "logout", "disabled_account", "stop"} {
		t.Run(mode, func(t *testing.T) {
			env := newTestEnv(t)
			seedAgentSessions(t, env)
			c, m := agentQuestion(t, env, 1)
			runner := &executionRunner{started: make(chan struct{}, 1), hold: true}
			supervisor, s := startExecutor(t, env, runner, true)
			accepted, err := s.Accept(t.Context(), agentCaller, c.ID, agentKey(1), agent.Command{MessageID: m.ID})
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-runner.started:
			case <-time.After(3 * time.Second):
				t.Fatal("执行未启动")
			}
			state, code := run.Canceled, run.CodeCanceled
			switch mode {
			case "cancel":
				_, err = s.Cancel(t.Context(), agentCaller, c.ID, accepted.Snapshot.ID)
			case "logout":
				session, _ := account.ParseUUID(agentTestSession)
				err = postgres.NewSessionRepository(env.pool).Revoke(t.Context(), session, account.RevokeReasonLogout, time.Now())
				code = run.CodeAuthInvalid
			case "disabled_account":
				_, err = env.pool.Exec(t.Context(), `UPDATE velis.users SET status='disabled' WHERE id=$1`, agentTestUser)
				code = run.CodeAuthInvalid
			case "stop":
				ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
				defer cancel()
				err = supervisor.Stop(ctx)
				state, code = run.Failed, run.CodeExecutorStopped
			}
			if err != nil {
				t.Fatal(err)
			}
			repo := postgres.NewAgentRunRepository(env.pool)
			record := awaitRun(t, repo, agent.Scope{UserID: agentTestUser, ConversationID: c.ID, RunID: accepted.Snapshot.ID}, state, 3*time.Second)
			if record.Run.ErrorCode != code || record.Run.AssistantReserved || runner.calls.Load() != 1 {
				t.Fatal(record)
			}
			if mode == "stop" {
				replayed, err := s.Accept(t.Context(), agentCaller, c.ID, agentKey(1), agent.Command{MessageID: m.ID})
				if err != nil || !replayed.Replayed {
					t.Fatal("停止后接受重放", err)
				}
			}
		})
	}
	t.Run("disabled_maintenance", func(t *testing.T) {
		env := newTestEnv(t)
		seedAgentSessions(t, env)
		c, m := agentQuestion(t, env, 1)
		s := newRunService(env, 200, true)
		a, err := s.Accept(t.Context(), agentCaller, c.ID, agentKey(1), agent.Command{MessageID: m.ID})
		if err != nil {
			t.Fatal(err)
		}
		_, err = env.pool.Exec(t.Context(), `UPDATE velis.agent_runs SET created_at=clock_timestamp()-interval '61 seconds',deadline=clock_timestamp()-interval '1 second' WHERE id=$1`, a.Snapshot.ID)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = startExecutor(t, env, nil, false)
		record := awaitRun(t, postgres.NewAgentRunRepository(env.pool), agent.Scope{UserID: agentTestUser, ConversationID: c.ID, RunID: a.Snapshot.ID}, run.TimedOut, time.Second)
		if record.Run.AssistantReserved || record.Run.ErrorCode != run.CodeTimeout {
			t.Fatal(record)
		}
	})
}

func TestAgentRunConcurrentCancelSuccessFirstCommitWins(t *testing.T) {
	for n := 0; n < 8; n++ {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			env := newTestEnv(t)
			s, repo, e := claimedRun(t, env)
			appendBlock(t, env, repo, e)
			commit := successCommit(t, env, repo, e)
			tx := postgres.NewTxManager(env.pool)
			start := make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-start
				_ = tx.WithinTransaction(t.Context(), func(ctx context.Context) error { return repo.Complete(ctx, e, commit) })
			}()
			go func() { defer wg.Done(); <-start; _, _ = s.Cancel(t.Context(), agentCaller, e.ConversationID, e.RunID) }()
			close(start)
			wg.Wait()
			r, err := repo.Find(t.Context(), e.Scope)
			if err != nil || !r.Run.State.Terminal() || r.Run.AssistantReserved {
				t.Fatal(r, err)
			}
			var assistants, terminals int
			_ = env.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM velis.agent_messages WHERE role='assistant'),(SELECT count(*) FROM velis.agent_run_events WHERE kind='run.terminal')`).Scan(&assistants, &terminals)
			if terminals != 1 || (r.Run.State == run.Succeeded && assistants != 1) || (r.Run.State == run.Canceled && assistants != 0) {
				t.Fatal("竞争产生重复终态或助手", r, assistants, terminals)
			}
		})
	}
}
