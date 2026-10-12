package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	conversationApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentconversation"
	agent "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentrun"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/idempotency"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	account "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
	conversation "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentconversation"
	run "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

const agentTestSession = "83000000-0000-0000-0000-000000000010"
const agentOtherSession = "83000000-0000-0000-0000-000000000011"

var agentCaller = agent.Caller{UserID: agentTestUser, SessionID: agentTestSession}
var agentOtherCaller = agent.Caller{UserID: agentTestOther, SessionID: agentOtherSession}

func seedAgentSessions(t *testing.T, env *testEnv) {
	t.Helper()
	seedAgentUsers(t, env)
	for _, caller := range []agent.Caller{agentCaller, agentOtherCaller} {
		user, _ := account.ParseUUID(caller.UserID)
		session, _ := account.ParseUUID(caller.SessionID)
		if err := postgres.NewSessionRepository(env.pool).Create(t.Context(), account.NewSession(session, user, time.Now(), time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
}

func newRunService(env *testEnv, maxMessages int, accepting bool) *agent.Service {
	tx := postgres.NewTxManager(env.pool)
	repo := postgres.NewAgentRunRepository(env.pool)
	codec, _ := conversationApp.NewCursorCodec(bytes.Repeat([]byte{4}, 32))
	return agent.NewService(repo, repo.AgentConversationRepository, tx, tx, repo, postgres.NewIdempotencyRepository(env.pool), agent.Config{Accepting: accepting, Timeout: time.Minute, MaxMessages: maxMessages}).WithPaging(codec, time.Hour).WithProjector(agent.NewProjector(postgres.NewAgentEvidenceFacts(env.pool)))
}
func agentQuestion(t *testing.T, env *testEnv, key int) (conversation.Conversation, conversation.Message) {
	t.Helper()
	service := newAgentService(env, conversationApp.DefaultLimits())
	_, c := agentCreate(t, service, key)
	result, err := service.Append(t.Context(), agentTestUser, c.ID, agentKey(key), "需要解释的私密问题")
	if err != nil {
		t.Fatal(err)
	}
	var message conversation.Message
	if err = json.Unmarshal(result.Snapshot, &message); err != nil {
		t.Fatal(err)
	}
	return c, message
}
func failAgentRun(t *testing.T, env *testEnv, id string) {
	t.Helper()
	_, err := env.pool.Exec(t.Context(), `UPDATE velis.agent_runs SET status='failed',assistant_reserved=false,finished_at=clock_timestamp(),error_code='AGENT_TOOL_FAILED',owner_token=NULL,lease_until=NULL WHERE id=$1`, id)
	if err != nil {
		t.Fatal(err)
	}
}

func TestAgentRunAcceptOwnershipLatestAndRetry(t *testing.T) {
	env := newTestEnv(t)
	seedAgentSessions(t, env)
	c, message := agentQuestion(t, env, 1)
	s := newRunService(env, 200, true)
	ctx := t.Context()
	command := agent.Command{MessageID: message.ID}
	if _, err := s.Accept(ctx, agentOtherCaller, c.ID, agentKey(1), command); !errors.Is(err, conversation.ErrNotFound) {
		t.Fatalf("管理员豁免: %v", err)
	}
	if _, err := s.Accept(ctx, agentCaller, c.ID, agentKey(1), agent.Command{MessageID: agentKey(999)}); !errors.Is(err, run.ErrMessageNotFound) {
		t.Fatalf("消息不存在: %v", err)
	}
	otherC, otherMessage := agentQuestion(t, env, 2)
	if _, err := s.Accept(ctx, agentCaller, c.ID, agentKey(1), agent.Command{MessageID: otherMessage.ID}); !errors.Is(err, run.ErrMessageNotFound) {
		t.Fatalf("跨会话消息: %v", err)
	}
	_ = otherC
	badRetry := agentKey(9)
	invalid := command
	invalid.RetryOf = &badRetry
	if _, err := s.Accept(ctx, agentCaller, c.ID, agentKey(1), invalid); !errors.Is(err, run.ErrInputConflict) {
		t.Fatal("首次 retry_of", err)
	}
	keyword, topic := "  Go  ", " 系统设计 "
	source, article := int64(8), int64(42)
	command.Constraints = &agent.ConstraintsInput{Keyword: &keyword, Topic: &topic, SourceID: &source}
	command.ContextArticleID = &article
	accepted, err := s.Accept(ctx, agentCaller, c.ID, agentKey(1), command)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Replayed || accepted.Snapshot.Status != run.Accepted || accepted.Snapshot.UserMessageID != message.ID {
		t.Fatalf("接受快照: %+v", accepted)
	}
	current, err := postgres.NewAgentRunRepository(env.pool).Find(ctx, agent.Scope{UserID: agentTestUser, ConversationID: c.ID, RunID: accepted.Snapshot.ID})
	if err != nil || current.Run.Input.Constraints != (run.Constraints{Keyword: "Go", Topic: "系统设计", SourceID: 8}) || current.Run.Input.ContextArticleID != 42 {
		t.Fatalf("锁定条件: %+v %v", current, err)
	}
	trimmedKeyword, trimmedTopic := "Go", "系统设计"
	normalized := command
	normalized.Constraints = &agent.ConstraintsInput{Keyword: &trimmedKeyword, Topic: &trimmedTopic, SourceID: &source}
	if replay, err := s.Accept(ctx, agentCaller, c.ID, agentKey(1), normalized); err != nil || !replay.Replayed || replay.Snapshot != accepted.Snapshot {
		t.Fatal("规范化约束同键重放", err)
	}
	if _, err := s.Accept(ctx, agentCaller, c.ID, agentKey(3), command); !errors.Is(err, run.ErrInputConflict) {
		t.Fatal("换键自动生成", err)
	}
	failAgentRun(t, env, accepted.Snapshot.ID)
	if _, err := s.Accept(ctx, agentCaller, c.ID, agentKey(3), command); !errors.Is(err, run.ErrInputConflict) {
		t.Fatal("未显式重试", err)
	}
	command.RetryOf = &accepted.Snapshot.ID
	changed := command
	newArticle := int64(43)
	changed.ContextArticleID = &newArticle
	if _, err := s.Accept(ctx, agentCaller, c.ID, agentKey(3), changed); !errors.Is(err, run.ErrInputConflict) {
		t.Fatal("重试改变上下文", err)
	}
	newKeyword := "Rust"
	changed = command
	changed.Constraints = &agent.ConstraintsInput{Keyword: &newKeyword, Topic: &topic, SourceID: &source}
	if _, err := s.Accept(ctx, agentCaller, c.ID, agentKey(3), changed); !errors.Is(err, run.ErrInputConflict) {
		t.Fatal("重试改变约束", err)
	}
	second, err := s.Accept(ctx, agentCaller, c.ID, agentKey(3), command)
	if err != nil {
		t.Fatal(err)
	}
	failAgentRun(t, env, second.Snapshot.ID)
	if _, err := s.Accept(ctx, agentCaller, c.ID, agentKey(4), command); !errors.Is(err, run.ErrInputConflict) {
		t.Fatal("重试没有指向最新回合", err)
	}
	command.RetryOf = &second.Snapshot.ID
	third, err := s.Accept(ctx, agentCaller, c.ID, agentKey(4), command)
	if err != nil {
		t.Fatal(err)
	}
	failAgentRun(t, env, third.Snapshot.ID)
	command.RetryOf = &third.Snapshot.ID
	if _, err := s.Accept(ctx, agentCaller, c.ID, agentKey(5), command); !errors.Is(err, run.ErrAttemptLimit) {
		t.Fatal("超过三次", err)
	}
	// 24h 去重窗口失效不能清除同消息的接受事实。
	_, err = env.pool.Exec(ctx, `UPDATE velis.idempotency_operations SET created_at=now()-interval '3 days',expires_at=now()-interval '2 days' WHERE operation='agent.run.accept'`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Accept(ctx, agentCaller, c.ID, agentKey(5), command); !errors.Is(err, run.ErrAttemptLimit) {
		t.Fatal("到期绕过尝试上限", err)
	}
}

func TestAgentRunConcurrentSameKeyAndCurrentAuthorization(t *testing.T) {
	env := newTestEnv(t)
	seedAgentSessions(t, env)
	c, m := agentQuestion(t, env, 1)
	services := []*agent.Service{newRunService(env, 2, true), newRunService(env, 2, true)}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	results := make(chan agent.Acceptance, 12)
	failures := make(chan error, 12)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Go(func() {
			result, err := services[i%2].Accept(ctx, agentCaller, c.ID, agentKey(1), agent.Command{MessageID: m.ID})
			results <- result
			failures <- err
		})
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	fresh := 0
	var first agent.AcceptedSnapshot
	for result := range results {
		if !result.Replayed {
			fresh++
		}
		if first.ID == "" {
			first = result.Snapshot
		}
		if first != result.Snapshot {
			t.Fatal("同键并发返回不同快照")
		}
	}
	if fresh != 1 {
		t.Fatalf("同键竞争首次接受数: %d", fresh)
	}
	sessionID, _ := account.ParseUUID(agentTestSession)
	err := postgres.NewSessionRepository(env.pool).Revoke(ctx, sessionID, account.RevokeReasonLogout, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := services[0].Accept(ctx, agentCaller, c.ID, agentKey(1), agent.Command{MessageID: m.ID}); agent.SafeCode(err) != run.CodeAuthInvalid {
		t.Fatal("注销后仍重放私密接受快照", err)
	}
	if _, err := services[0].Detail(ctx, agentCaller, c.ID, first.ID); agent.SafeCode(err) != run.CodeAuthInvalid {
		t.Fatal("注销后仍读详情", err)
	}
	var n int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.agent_runs`).Scan(&n); err != nil || n != 1 {
		t.Fatal("认证失败改变接受事实", err)
	}
}

func TestAgentRunRejectsOldAndAssistantMessages(t *testing.T) {
	env := newTestEnv(t)
	seedAgentSessions(t, env)
	c, message := agentQuestion(t, env, 1)
	s := newRunService(env, 200, true)
	conv := newAgentService(env, conversationApp.DefaultLimits())
	if _, err := conv.Append(t.Context(), agentTestUser, c.ID, agentKey(2), "更新的问题"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Accept(t.Context(), agentCaller, c.ID, agentKey(1), agent.Command{MessageID: message.ID}); !errors.Is(err, run.ErrInputConflict) {
		t.Fatal("旧消息", err)
	}
	_, err := env.pool.Exec(t.Context(), `UPDATE velis.agent_messages SET role='assistant' WHERE conversation_id=$1 AND sequence=2`, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	history, _, err := conv.History(t.Context(), agentTestUser, c.ID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Accept(t.Context(), agentCaller, c.ID, agentKey(1), agent.Command{MessageID: history[1].ID}); !errors.Is(err, run.ErrInputConflict) {
		t.Fatal("助手消息", err)
	}
	var n int
	if err := env.pool.QueryRow(t.Context(), `SELECT count(*) FROM velis.agent_runs`).Scan(&n); err != nil || n != 0 {
		t.Fatal("拒绝前已建立回合")
	}
}

type lostAcceptResponse struct {
	ports.TxManager
	once  bool
	depth int
}

func (tx *lostAcceptResponse) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	tx.depth++
	err := tx.TxManager.WithinTransaction(ctx, fn)
	tx.depth--
	if err == nil && !tx.once && tx.depth == 0 {
		tx.once = true
		return errors.New("注入提交响应丢失")
	}
	return err
}

func TestAgentRunAcceptanceReplayRollbackAndLostResponse(t *testing.T) {
	env := newTestEnv(t)
	seedAgentSessions(t, env)
	c, message := agentQuestion(t, env, 1)
	s := newRunService(env, 200, true)
	ctx := t.Context()
	command := agent.Command{MessageID: message.ID}
	// 在接受快照结算步骤失败，回合、助手预留和幂等占位一起回滚。
	_, err := env.pool.Exec(ctx, `CREATE FUNCTION velis.agent_run_test_fail_settle() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN IF NEW.operation='agent.run.accept' AND NEW.status='succeeded' THEN RAISE EXCEPTION '注入结算失败'; END IF; RETURN NEW; END $$;
CREATE TRIGGER agent_run_test_fail_settle BEFORE UPDATE ON velis.idempotency_operations FOR EACH ROW EXECUTE FUNCTION velis.agent_run_test_fail_settle()`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = env.pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS agent_run_test_fail_settle ON velis.idempotency_operations;DROP FUNCTION IF EXISTS velis.agent_run_test_fail_settle()`)
	})
	if _, err := s.Accept(ctx, agentCaller, c.ID, agentKey(1), command); err == nil {
		t.Fatal("注入未生效")
	}
	var runs, keys int
	if err := env.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM velis.agent_runs),(SELECT count(*) FROM velis.idempotency_operations WHERE operation='agent.run.accept')`).Scan(&runs, &keys); err != nil || runs != 0 || keys != 0 {
		t.Fatalf("回滚遗留: %d %d %v", runs, keys, err)
	}
	_, err = env.pool.Exec(ctx, `DROP TRIGGER agent_run_test_fail_settle ON velis.idempotency_operations;DROP FUNCTION velis.agent_run_test_fail_settle()`)
	if err != nil {
		t.Fatal(err)
	}
	tx := postgres.NewTxManager(env.pool)
	repo := postgres.NewAgentRunRepository(env.pool)
	lost := &lostAcceptResponse{TxManager: tx}
	lossService := agent.NewService(repo, repo.AgentConversationRepository, lost, tx, repo, postgres.NewIdempotencyRepository(env.pool), agent.Config{Accepting: true, Timeout: time.Minute, MaxMessages: 200})
	if _, err := lossService.Accept(ctx, agentCaller, c.ID, agentKey(1), command); err == nil {
		t.Fatal("提交丢失未生效")
	}
	accepted, err := s.Accept(ctx, agentCaller, c.ID, agentKey(1), command)
	if err != nil || !accepted.Replayed {
		t.Fatalf("提交后重放: %+v %v", accepted, err)
	}
	failAgentRun(t, env, accepted.Snapshot.ID)
	replay, err := newRunService(env, 1, false).Accept(ctx, agentCaller, c.ID, agentKey(1), command)
	if err != nil || !replay.Replayed || replay.Snapshot != accepted.Snapshot || replay.Snapshot.Status != run.Accepted {
		t.Fatal("终态/关闭后接受快照改变", err)
	}
	changed := command
	article := int64(42)
	changed.ContextArticleID = &article
	if _, err := s.Accept(ctx, agentCaller, c.ID, agentKey(1), changed); !errors.Is(err, idempotency.ErrKeyReused) {
		t.Fatal("同键异命令", err)
	}
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.agent_runs`).Scan(&runs); err != nil || runs != 1 {
		t.Fatalf("重放重新生成: %d %v", runs, err)
	}
	var window float64
	if err := env.pool.QueryRow(ctx, `SELECT extract(epoch FROM expires_at-created_at) FROM velis.idempotency_operations WHERE operation='agent.run.accept'`).Scan(&window); err != nil || window != 86400 {
		t.Fatalf("窗口: %f %v", window, err)
	}
}

func TestAgentRunConcurrentAccountAndConversationQuota(t *testing.T) {
	env := newTestEnv(t)
	seedAgentSessions(t, env)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	services := []*agent.Service{newRunService(env, 200, true), newRunService(env, 200, true)}
	type question struct {
		c conversation.Conversation
		m conversation.Message
	}
	questions := []question{}
	for i := 0; i < 6; i++ {
		c, m := agentQuestion(t, env, 100+i)
		questions = append(questions, question{c, m})
	}
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for i, q := range questions {
		wg.Go(func() {
			_, err := services[i%2].Accept(ctx, agentCaller, q.c.ID, agentKey(200+i), agent.Command{MessageID: q.m.ID})
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	success := 0
	for err := range errs {
		if err == nil {
			success++
		} else if !errors.Is(err, run.ErrInProgress) {
			t.Fatal(err)
		}
	}
	if success != 2 {
		t.Fatalf("跨实例账户名额: %d", success)
	}
	_, err := env.pool.Exec(ctx, `UPDATE velis.agent_runs SET status='failed',assistant_reserved=false,finished_at=now(),error_code='AGENT_TOOL_FAILED'`)
	if err != nil {
		t.Fatal(err)
	}
	c, m := agentQuestion(t, env, 300)
	services = []*agent.Service{newRunService(env, 2, true), newRunService(env, 2, true)}
	errs = make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Go(func() {
			_, err := services[i%2].Accept(ctx, agentCaller, c.ID, agentKey(400+i), agent.Command{MessageID: m.ID})
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	success = 0
	for err := range errs {
		if err == nil {
			success++
		} else if !errors.Is(err, run.ErrInputConflict) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("跨实例会话名额: %d", success)
	}
}

func TestAgentRunMessageReservationAndAppendCompatibility(t *testing.T) {
	env := newTestEnv(t)
	seedAgentSessions(t, env)
	c, m := agentQuestion(t, env, 1)
	s := newRunService(env, 2, true)
	ctx := t.Context()
	accepted, err := s.Accept(ctx, agentCaller, c.ID, agentKey(1), agent.Command{MessageID: m.ID})
	if err != nil {
		t.Fatal(err)
	}
	conv := newAgentService(env, conversationApp.Limits{MaxConversations: 50, MaxMessages: 2, MaxMessageChars: 4000})
	current, err := conv.Detail(ctx, agentTestUser, c.ID)
	if err != nil || current.MessageCount != 1 || current.NextSequence != 2 {
		t.Fatalf("预留变成消息: %+v %v", current, err)
	}
	if _, err := conv.Append(ctx, agentTestUser, c.ID, agentKey(1), "需要解释的私密问题"); err != nil {
		t.Fatal("活动期间原消息重放", err)
	}
	if _, err := conv.Append(ctx, agentTestUser, c.ID, agentKey(2), "新问题"); !errors.Is(err, run.ErrInProgress) {
		t.Fatal("活动追加", err)
	}
	if _, err := conv.Rename(ctx, agentTestUser, c.ID, agentKey(3), "可改名", 1); err != nil {
		t.Fatal(err)
	}
	// 后续终态事务必须使用当前配额再次校验，不能凭原预留突破降低后的配置。
	_, err = env.pool.Exec(ctx, `UPDATE velis.agent_runs SET status='running',owner_token=$2,lease_until=LEAST(clock_timestamp()+interval '10 seconds',deadline) WHERE id=$1`, accepted.Snapshot.ID, agentKey(88))
	if err != nil {
		t.Fatal(err)
	}
	execution := agent.Execution{Scope: agent.Scope{UserID: agentTestUser, ConversationID: c.ID, RunID: accepted.Snapshot.ID}, SessionID: agentTestSession, OwnerToken: agentKey(88)}
	called := false
	if err := newRunService(env, 1, true).WithAssistantReservation(ctx, execution, func(context.Context, conversation.Conversation, agent.Record) error { called = true; return nil }); !errors.Is(err, run.ErrMessageLimit) || called {
		t.Fatal("降低配额后的最终拒绝", err)
	}
	if err := s.WithAssistantReservation(ctx, execution, func(_ context.Context, c conversation.Conversation, _ agent.Record) error {
		called = true
		if c.NextSequence != 2 {
			return errors.New("预留占用序号")
		}
		return nil
	}); err != nil || !called {
		t.Fatal("合法预留没有进入保存事务", err)
	}
	failAgentRun(t, env, accepted.Snapshot.ID)
	if _, err := conv.Append(ctx, agentTestUser, c.ID, agentKey(2), "新问题"); err != nil {
		t.Fatal("失败释放后同键可追加", err)
	}
	history, _, err := conv.History(ctx, agentTestUser, c.ID, 0, 20)
	if err != nil || len(history) != 2 || history[1].Sequence != 2 {
		t.Fatal("失败预留占用消息序号", err)
	}
	if _, err := s.Accept(ctx, agentCaller, c.ID, agentKey(2), agent.Command{MessageID: history[1].ID}); !errors.Is(err, run.ErrMessageLimit) {
		t.Fatal("最后名额只够 user", err)
	}
	if _, err := newRunService(env, 200, false).Accept(ctx, agentCaller, c.ID, agentKey(4), agent.Command{MessageID: history[1].ID}); agent.SafeCode(err) != run.CodeGenerationUnavailable {
		t.Fatal("关闭生成接受", err)
	}
}
