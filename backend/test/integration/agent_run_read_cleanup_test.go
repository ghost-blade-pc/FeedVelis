package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	conversationApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentconversation"
	agent "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentrun"
	conversation "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentconversation"
	run "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestAgentRunListDetailAndCursorIsolation(t *testing.T) {
	env := newTestEnv(t)
	seedAgentSessions(t, env)
	c, m := agentQuestion(t, env, 1)
	s := newRunService(env, 200, true)
	conv := newAgentService(env, conversationApp.DefaultLimits())
	ctx := t.Context()
	ids := []string{}
	for i := 0; i < 23; i++ {
		if i > 0 {
			result, err := conv.Append(ctx, agentTestUser, c.ID, agentKey(100+i), "下一条问题")
			if err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(result.Snapshot, &m); err != nil {
				t.Fatal(err)
			}
		}
		accepted, err := s.Accept(ctx, agentCaller, c.ID, agentKey(200+i), agent.Command{MessageID: m.ID})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, accepted.Snapshot.ID)
		failAgentRun(t, env, accepted.Snapshot.ID)
	}
	now := time.Now().UTC()
	_, err := env.pool.Exec(ctx, `UPDATE velis.agent_runs SET created_at=$1::timestamptz,deadline=$1::timestamptz+interval '60 seconds',finished_at=$1::timestamptz+interval '1 second'`, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	page, err := s.List(ctx, agentCaller, c.ID, "", 0, now)
	if err != nil || len(page.Items) != 20 || !page.HasMore || page.NextCursor == nil {
		t.Fatalf("默认分页: %+v %v", page, err)
	}
	for i, item := range page.Items {
		if item.ID != ids[i] {
			t.Fatal("同时间排序不稳定")
		}
	}
	next, err := s.List(ctx, agentCaller, c.ID, *page.NextCursor, 50, now.Add(59*time.Minute))
	if err != nil || len(next.Items) != 3 || next.HasMore || next.NextCursor != nil {
		t.Fatalf("续页: %+v %v", next, err)
	}
	for i, item := range next.Items {
		if item.ID != ids[i+20] {
			t.Fatal("跨页遗漏/重复")
		}
	}
	one, err := s.List(ctx, agentCaller, c.ID, "", 1, now)
	if err != nil || len(one.Items) != 1 || !one.HasMore {
		t.Fatal(err)
	}
	codec, _ := conversationApp.NewCursorCodec(bytes.Repeat([]byte{4}, 32))
	position, err := codec.Decode(*one.NextCursor, agentTestUser, conversationApp.RunListPurpose, c.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	continued, err := s.List(ctx, agentCaller, c.ID, *one.NextCursor, 1, now.Add(30*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	position2, err := codec.Decode(*continued.NextCursor, agentTestUser, conversationApp.RunListPurpose, c.ID, now.Add(30*time.Minute))
	if err != nil || position2.ExpiresAt != position.ExpiresAt {
		t.Fatal("续页延长游标有效期")
	}
	otherC, _ := agentQuestion(t, env, 2)
	if _, err := s.List(ctx, agentCaller, otherC.ID, *page.NextCursor, 20, now); !errors.Is(err, conversationApp.ErrInvalidCursor) {
		t.Fatal("跨会话游标", err)
	}
	if _, err := s.List(ctx, agentOtherCaller, c.ID, *page.NextCursor, 20, now); !errors.Is(err, conversation.ErrNotFound) {
		t.Fatal("游标先于所有权", err)
	}
	foreign, err := codec.Encode(conversationApp.Cursor{UserID: agentTestOther, Purpose: conversationApp.RunListPurpose, ConversationID: c.ID, ID: ids[0], ActivityAt: now.Add(-time.Hour), StartedAt: now, ExpiresAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	history, err := codec.Encode(conversationApp.Cursor{UserID: agentTestUser, Purpose: conversationApp.HistoryPurpose, ConversationID: c.ID, BeforeSequence: 2, StartedAt: now, ExpiresAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	list, err := codec.Encode(conversationApp.Cursor{UserID: agentTestUser, Purpose: conversationApp.ListPurpose, ID: c.ID, ActivityAt: now, StartedAt: now, ExpiresAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{foreign, history, list, *page.NextCursor + "!"} {
		if _, err := s.List(ctx, agentCaller, c.ID, raw, 20, now); !errors.Is(err, conversationApp.ErrInvalidCursor) {
			t.Fatalf("非法作用域被接受: %v", err)
		}
	}
	if _, err := s.List(ctx, agentCaller, c.ID, *page.NextCursor, 20, now.Add(time.Hour)); !errors.Is(err, conversationApp.ErrInvalidCursor) {
		t.Fatal("过期游标", err)
	}
	for _, limit := range []int{-1, 51} {
		if _, err := s.List(ctx, agentCaller, c.ID, "", limit, now); !errors.Is(err, run.ErrInvalidInput) {
			t.Fatal("分页范围", err)
		}
	}
	detail, err := newRunService(env, 200, false).Detail(ctx, agentCaller, c.ID, ids[0])
	if err != nil || detail.Status != run.Failed || detail.Answer != nil || detail.Partial || detail.Usage.Complete {
		t.Fatalf("安全详情: %+v %v", detail, err)
	}
	encoded, _ := json.Marshal(detail)
	for _, secret := range []string{"auth_session", "owner_token", "constraints", "deadline", "私密问题", "execution_metadata"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("详情泄露内部字段: %s", encoded)
		}
	}
	if _, err := s.Detail(ctx, agentOtherCaller, c.ID, ids[0]); !errors.Is(err, conversation.ErrNotFound) {
		t.Fatal("详情跨账户", err)
	}
	if _, err := s.Detail(ctx, agentCaller, c.ID, agentKey(999)); !errors.Is(err, run.ErrNotFound) {
		t.Fatal("未知回合", err)
	}
	var count int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.agent_runs`).Scan(&count); err != nil || count != 23 {
		t.Fatal("读取触发生成", err)
	}
}

func populateRunWork(t *testing.T, env *testEnv, id string) {
	t.Helper()
	ctx := t.Context()
	_, err := env.pool.Exec(ctx, `INSERT INTO velis.agent_run_evidence
(run_id,evidence_id,version,kind,article_id,revision_id,obtained_at,content_kind,truncated,content,reference_number,card,quote_checks)
VALUES($1,$2,1,'article',1,2,now(),'body',false,'私密工作材料',1,'{"title":"私密文章标题"}','[{"quote":"私密引文"}]')`, id, agentTestEvidence)
	if err != nil {
		t.Fatal(err)
	}
	_, err = env.pool.Exec(ctx, `INSERT INTO velis.agent_run_blocks VALUES($1,'b1',1,'私密片段','[]')`, id)
	if err != nil {
		t.Fatal(err)
	}
	_, err = env.pool.Exec(ctx, `INSERT INTO velis.agent_run_block_evidence VALUES($1,'b1',$2,1)`, id, agentTestEvidence)
	if err != nil {
		t.Fatal(err)
	}
	_, err = env.pool.Exec(ctx, `INSERT INTO velis.agent_run_events(run_id,sequence,version,kind,payload,created_at) VALUES($1,2,1,'answer.block','{"text":"私密事件片段"}',now())`, id)
	if err != nil {
		t.Fatal(err)
	}
}

// markRunSucceeded 仅构造保留期/约束夹具；生成成功原子提交由独立执行测试覆盖。
func markRunSucceeded(t *testing.T, env *testEnv, id, conversationID string) {
	t.Helper()
	ctx := t.Context()
	assistant := agentKey(990)
	_, err := env.pool.Exec(ctx, `INSERT INTO velis.agent_messages(id,conversation_id,sequence,role,content,created_at)
SELECT $1,id,next_sequence,'assistant','成功助手不可变正文',clock_timestamp() FROM velis.agent_conversations WHERE id=$2`, assistant, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = env.pool.Exec(ctx, `UPDATE velis.agent_conversations SET message_count=message_count+1,next_sequence=next_sequence+1 WHERE id=$1`, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = env.pool.Exec(ctx, `UPDATE velis.agent_runs SET status='succeeded',assistant_reserved=false,finished_at=clock_timestamp(),assistant_message_id=$2,
answer='{"version":1,"kind":"answer","blocks":[{"id":"b1","text":"成功助手不可变正文","evidence_ids":[]}],"references":[],"cards":[],"redacted":false}' WHERE id=$1`, id, assistant)
	if err != nil {
		t.Fatal(err)
	}
}

func TestAgentRunRetentionPreservesSuccessAndCleansFailedWork(t *testing.T) {
	env := newTestEnv(t)
	seedAgentSessions(t, env)
	s := newRunService(env, 200, true)
	ctx := t.Context()
	c, m := agentQuestion(t, env, 1)
	failed, err := s.Accept(ctx, agentCaller, c.ID, agentKey(1), agent.Command{MessageID: m.ID})
	if err != nil {
		t.Fatal(err)
	}
	populateRunWork(t, env, failed.Snapshot.ID)
	failAgentRun(t, env, failed.Snapshot.ID)
	before, err := s.Detail(ctx, agentCaller, c.ID, failed.Snapshot.ID)
	if err != nil || !before.Partial {
		t.Fatal("失败工作没有 partial 摘要", err)
	}
	c2, m2 := agentQuestion(t, env, 2)
	success, err := s.Accept(ctx, agentCaller, c2.ID, agentKey(2), agent.Command{MessageID: m2.ID})
	if err != nil {
		t.Fatal(err)
	}
	populateRunWork(t, env, success.Snapshot.ID)
	markRunSucceeded(t, env, success.Snapshot.ID, c2.ID)
	// 缺少投影器时仍必须关闭读取，不能旁路返回数据库原文。
	if _, err := s.WithProjector(nil).Detail(ctx, agentCaller, c2.ID, success.Snapshot.ID); agent.SafeCode(err) != run.CodeDependencyUnavailable {
		t.Fatal("未经复核的成功原文泄漏", err)
	}
	s.WithProjector(agent.NewProjector(postgres.NewAgentEvidenceFacts(env.pool)))
	c3, m3 := agentQuestion(t, env, 3)
	recent, err := s.Accept(ctx, agentCaller, c3.ID, agentKey(3), agent.Command{MessageID: m3.ID})
	if err != nil {
		t.Fatal(err)
	}
	populateRunWork(t, env, recent.Snapshot.ID)
	failAgentRun(t, env, recent.Snapshot.ID)
	_, err = env.pool.Exec(ctx, `UPDATE velis.agent_runs SET finished_at=clock_timestamp()-interval '25 hours' WHERE id=ANY($1::uuid[])`, []string{failed.Snapshot.ID, success.Snapshot.ID})
	if err != nil {
		t.Fatal(err)
	}
	count, err := s.Cleanup(ctx)
	if err != nil || count != 2 {
		t.Fatalf("24h 有界清理: %d %v", count, err)
	}
	count, err = s.Cleanup(ctx)
	if err != nil || count != 0 {
		t.Fatalf("幂等清理: %d %v", count, err)
	}
	for _, table := range []string{"agent_run_evidence", "agent_run_blocks", "agent_run_block_evidence", "agent_run_events"} {
		var old, retained, newer int
		if err := env.pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE run_id=$1),count(*) FILTER(WHERE run_id=$2),count(*) FILTER(WHERE run_id=$3) FROM velis.`+table, failed.Snapshot.ID, success.Snapshot.ID, recent.Snapshot.ID).Scan(&old, &retained, &newer); err != nil {
			t.Fatal(err)
		}
		want := 1
		if table == "agent_run_events" {
			want = 0
		}
		wantNew := 1
		if table == "agent_run_events" {
			wantNew = 2
		}
		if old != 0 || retained != want || newer != wantNew {
			t.Fatalf("保留边界 %s: %d %d %d", table, old, retained, newer)
		}
	}
	after, err := s.Detail(ctx, agentCaller, c.ID, failed.Snapshot.ID)
	if err != nil || after.Partial || after.Status != run.Failed || after.UserMessageID != m.ID {
		t.Fatalf("失败摘要: %+v %v", after, err)
	}
	var answer, content string
	if err := env.pool.QueryRow(ctx, `SELECT r.answer::text,m.content FROM velis.agent_runs r JOIN velis.agent_messages m ON m.id=r.assistant_message_id WHERE r.id=$1`, success.Snapshot.ID).Scan(&answer, &content); err != nil || !strings.Contains(answer, "成功助手不可变正文") || content != "成功助手不可变正文" {
		t.Fatal("清理误删成功回答", err)
	}
	// 成功事实阻止新键再次生成，即使已经没有事件日志。
	if _, err := s.Accept(ctx, agentCaller, c2.ID, agentKey(99), agent.Command{MessageID: m2.ID}); !errors.Is(err, run.ErrInputConflict) {
		t.Fatal("成功再次生成", err)
	}
}

func TestAgentRunCleanupFailureRollsBackBatch(t *testing.T) {
	env := newTestEnv(t)
	seedAgentSessions(t, env)
	c, m := agentQuestion(t, env, 1)
	s := newRunService(env, 200, true)
	ctx := t.Context()
	accepted, err := s.Accept(ctx, agentCaller, c.ID, agentKey(1), agent.Command{MessageID: m.ID})
	if err != nil {
		t.Fatal(err)
	}
	populateRunWork(t, env, accepted.Snapshot.ID)
	failAgentRun(t, env, accepted.Snapshot.ID)
	_, err = env.pool.Exec(ctx, `UPDATE velis.agent_runs SET finished_at=now()-interval '25 hours';
CREATE FUNCTION velis.agent_run_test_fail_cleanup() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.events_expired THEN RAISE EXCEPTION '注入清理失败';END IF;RETURN NEW;END $$;
CREATE TRIGGER agent_run_test_fail_cleanup BEFORE UPDATE ON velis.agent_runs FOR EACH ROW EXECUTE FUNCTION velis.agent_run_test_fail_cleanup()`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = env.pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS agent_run_test_fail_cleanup ON velis.agent_runs;DROP FUNCTION IF EXISTS velis.agent_run_test_fail_cleanup()`)
	})
	if n, err := s.Cleanup(ctx); err == nil || n != 0 {
		t.Fatal("失败清理未回滚", err)
	}
	for _, table := range []string{"agent_run_evidence", "agent_run_blocks", "agent_run_block_evidence", "agent_run_events"} {
		var n int
		want := 1
		if table == "agent_run_events" {
			want = 2
		}
		if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.`+table).Scan(&n); err != nil || n != want {
			t.Fatalf("清理失败丢失 %s: %d %v", table, n, err)
		}
	}
	var expired bool
	if err := env.pool.QueryRow(ctx, `SELECT events_expired FROM velis.agent_runs`).Scan(&expired); err != nil || expired {
		t.Fatal("清理标记没有回滚", err)
	}
	repo := postgres.NewAgentRunRepository(env.pool)
	if _, err := repo.Cleanup(ctx, 100); err == nil {
		t.Fatal("清理脱离事务")
	}
	tx := postgres.NewTxManager(env.pool)
	if err := tx.WithinTransaction(ctx, func(ctx context.Context) error { _, err := repo.Cleanup(ctx, 101); return err }); !errors.Is(err, run.ErrInvalidInput) {
		t.Fatal("无界清理批次", err)
	}
}

func TestAgentRunDeletePrivacyRollbackAndRaces(t *testing.T) {
	env := newTestEnv(t)
	seedAgentSessions(t, env)
	c, m := agentQuestion(t, env, 1)
	s := newRunService(env, 200, true)
	conv := newAgentService(env, conversationApp.DefaultLimits())
	ctx := t.Context()
	accepted, err := s.Accept(ctx, agentCaller, c.ID, agentKey(1), agent.Command{MessageID: m.ID})
	if err != nil {
		t.Fatal(err)
	}
	populateRunWork(t, env, accepted.Snapshot.ID)
	_, err = env.pool.Exec(ctx, `CREATE FUNCTION velis.agent_run_test_fail_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.conversation_count<OLD.conversation_count THEN RAISE EXCEPTION '注入删除失败';END IF;RETURN NEW;END $$;
CREATE TRIGGER agent_run_test_fail_delete BEFORE UPDATE ON velis.agent_user_state FOR EACH ROW EXECUTE FUNCTION velis.agent_run_test_fail_delete()`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = env.pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS agent_run_test_fail_delete ON velis.agent_user_state;DROP FUNCTION IF EXISTS velis.agent_run_test_fail_delete()`)
	})
	if err := conv.Delete(ctx, agentTestUser, c.ID); err == nil {
		t.Fatal("删除故障未触发")
	}
	for _, table := range []string{"agent_runs", "agent_run_evidence", "agent_run_blocks", "agent_run_block_evidence", "agent_run_events"} {
		var n int
		want := 1
		if table == "agent_run_events" {
			want = 2
		}
		if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.`+table).Scan(&n); err != nil || n != want {
			t.Fatalf("删除失败损坏 %s: %d %v", table, n, err)
		}
	}
	replay, err := s.Accept(ctx, agentCaller, c.ID, agentKey(1), agent.Command{MessageID: m.ID})
	if err != nil || !replay.Replayed {
		t.Fatal("删除失败损坏接受重放", err)
	}
	_, err = env.pool.Exec(ctx, `DROP TRIGGER agent_run_test_fail_delete ON velis.agent_user_state;DROP FUNCTION velis.agent_run_test_fail_delete()`)
	if err != nil {
		t.Fatal(err)
	}
	if err := conv.Delete(ctx, agentTestUser, c.ID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"agent_runs", "agent_run_evidence", "agent_run_blocks", "agent_run_block_evidence", "agent_run_events", "agent_messages"} {
		var n int
		if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.`+table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("删除私密载荷残留 %s: %d %v", table, n, err)
		}
	}
	var payload string
	if err := env.pool.QueryRow(ctx, `SELECT result_payload::text FROM velis.idempotency_operations WHERE operation='agent.run.accept'`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(payload), &fields); err != nil || len(fields) != 3 || fields["deleted"] != true || strings.Contains(payload, accepted.Snapshot.ID) {
		t.Fatalf("接受快照未转成三字段标记: %s %v", payload, err)
	}
	if _, err := s.Accept(ctx, agentCaller, c.ID, agentKey(1), agent.Command{MessageID: m.ID}); !errors.Is(err, conversation.ErrNotFound) {
		t.Fatal("删除后接受重放", err)
	}
	_, err = env.pool.Exec(ctx, `INSERT INTO velis.agent_run_blocks VALUES($1,'late',1,'迟到片段','[]')`, accepted.Snapshot.ID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		t.Fatal("删除后迟到写入未拒绝", err)
	}
	var markerFields int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema='velis' AND table_name='agent_conversation_deletions'`).Scan(&markerFields); err != nil || markerFields != 3 {
		t.Fatal("删除标记扩张", err)
	}
	// 接受/普通消息写入与删除共享账户锁，所有可能顺序最终都必须无回合或孤儿消息。
	for round := 0; round < 10; round++ {
		c, m := agentQuestion(t, env, 100+round)
		errs := make(chan error, 3)
		var wg sync.WaitGroup
		wg.Go(func() {
			_, err := s.Accept(ctx, agentCaller, c.ID, agentKey(200+round), agent.Command{MessageID: m.ID})
			errs <- err
		})
		wg.Go(func() {
			_, err := conv.Append(ctx, agentTestUser, c.ID, agentKey(300+round), "竞争写入")
			errs <- err
		})
		wg.Go(func() { errs <- conv.Delete(ctx, agentTestUser, c.ID) })
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil && !errors.Is(err, conversation.ErrNotFound) && !errors.Is(err, run.ErrInputConflict) && !errors.Is(err, run.ErrInProgress) {
				t.Fatal("删除竞争", err)
			}
		}
		var n int
		if err := env.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM velis.agent_runs)+(SELECT count(*) FROM velis.agent_messages)`).Scan(&n); err != nil || n != 0 {
			t.Fatal("删除竞争产生孤儿", err)
		}
	}
}
