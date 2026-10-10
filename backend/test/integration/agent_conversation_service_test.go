package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	agent "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentconversation"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/idempotency"
	conversation "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentconversation"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

func newAgentService(env *testEnv, limits agent.Limits) *agent.Service {
	tx := postgres.NewTxManager(env.pool)
	return agent.NewService(postgres.NewAgentConversationRepository(env.pool), tx, tx, postgres.NewIdempotencyRepository(env.pool), limits)
}

func agentKey(n int) string { return fmt.Sprintf("82000000-0000-0000-0000-%012d", n) }

func agentCreate(t *testing.T, service *agent.Service, key int) (agent.Result, conversation.Conversation) {
	t.Helper()
	result, err := service.Create(context.Background(), agentTestUser, agentKey(key), nil)
	if err != nil {
		t.Fatal(err)
	}
	var c conversation.Conversation
	if err := json.Unmarshal(result.Snapshot, &c); err != nil {
		t.Fatal(err)
	}
	return result, c
}

func TestAgentConversationRepositoryTransactions(t *testing.T) {
	env := newTestEnv(t)
	seedAgentUsers(t, env)
	repo := postgres.NewAgentConversationRepository(env.pool)
	tx := postgres.NewTxManager(env.pool)
	ctx := context.Background()
	if _, err := repo.LockUser(ctx, agentTestUser); err == nil {
		t.Fatal("写入不得脱离事务")
	}
	rollback := errors.New("注入回滚")
	err := tx.WithinTransaction(ctx, func(ctx context.Context) error {
		state, err := repo.LockUser(ctx, agentTestUser)
		if err != nil {
			return err
		}
		c, err := conversation.New(agentTestConversation, agentTestUser, nil, state.Now)
		if err != nil {
			return err
		}
		if err := repo.InsertConversation(ctx, c); err != nil {
			return err
		}
		m, err := conversation.NewMessage(agentTestMessage, c.ID, 1, conversation.RoleAssistant, "助手消息", 100, state.Now)
		if err != nil {
			return err
		}
		if err := repo.AppendMessage(ctx, agentTestUser, m); err != nil {
			return err
		}
		current, err := repo.Find(ctx, agentTestUser, c.ID, true)
		if err != nil || current.MessageCount != 1 || current.NextSequence != 2 {
			t.Fatalf("事务计数: %+v %v", current, err)
		}
		if _, err := repo.Find(ctx, agentTestOther, c.ID, false); !errors.Is(err, conversation.ErrNotFound) {
			t.Fatal("仓储归属泄露")
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	for _, table := range []string{"agent_user_state", "agent_conversations", "agent_messages"} {
		var count int
		if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.`+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s未回滚: %d %v", table, count, err)
		}
	}
}

func TestAgentConversationServiceOwnershipReplayDelete(t *testing.T) {
	env := newTestEnv(t)
	seedAgentUsers(t, env)
	service := newAgentService(env, agent.Limits{MaxConversations: 1, MaxMessages: 1, MaxMessageChars: 4000})
	ctx := context.Background()
	created, c := agentCreate(t, service, 1)
	if created.Status != 201 || c.Title != "新对话" || c.MessageCount != 0 {
		t.Fatal("创建状态错误")
	}
	if _, err := service.Create(ctx, agentTestUser, agentKey(2), nil); !errors.Is(err, conversation.ErrConversationLimit) {
		t.Fatal("会话配额未拒绝")
	}
	replay, err := service.Create(ctx, agentTestUser, agentKey(1), nil)
	if err != nil || !replay.Replayed || string(replay.Snapshot) != string(created.Snapshot) {
		t.Fatalf("满配额创建重放: %+v %v", replay, err)
	}
	title := "改名"
	if _, err := service.Create(ctx, agentTestUser, agentKey(1), &title); !errors.Is(err, idempotency.ErrKeyReused) {
		t.Fatal("摘要冲突未拒绝")
	}
	message, err := service.Append(ctx, agentTestUser, c.ID, agentKey(1), "  **正文**\r\n")
	if err != nil || message.Status != 201 {
		t.Fatal(err)
	}
	messageReplay, err := service.Append(ctx, agentTestUser, c.ID, agentKey(1), "  **正文**\n")
	if err != nil || !messageReplay.Replayed || string(messageReplay.Snapshot) != string(message.Snapshot) {
		t.Fatalf("正文规范化重放: %v", err)
	}
	if _, err := service.Append(ctx, agentTestUser, c.ID, agentKey(3), "新消息"); !errors.Is(err, conversation.ErrMessageLimit) {
		t.Fatal("消息配额未拒绝")
	}
	rename, err := service.Rename(ctx, agentTestUser, c.ID, agentKey(1), title, 1)
	if err != nil || rename.Status != 200 {
		t.Fatal(err)
	}
	if _, err := service.Rename(ctx, agentTestUser, c.ID, agentKey(4), title, 1); !errors.Is(err, conversation.ErrTitleVersion) {
		t.Fatal("无变化旧版本未冲突")
	}
	renameReplay, err := service.Rename(ctx, agentTestUser, c.ID, agentKey(1), title, 1)
	if err != nil || !renameReplay.Replayed || string(renameReplay.Snapshot) != string(rename.Snapshot) {
		t.Fatal("重放应先于版本")
	}
	current, err := service.Detail(ctx, agentTestUser, c.ID)
	if err != nil || current.TitleVersion != 2 || current.MessageCount != 1 {
		t.Fatalf("当前事实: %+v %v", current, err)
	}
	unchanged, err := service.Rename(ctx, agentTestUser, c.ID, agentKey(5), " 改名 ", 2)
	if err != nil {
		t.Fatal(err)
	}
	var same conversation.Conversation
	if err := json.Unmarshal(unchanged.Snapshot, &same); err != nil || !same.LastActivityAt.Equal(current.LastActivityAt) || same.TitleVersion != 2 {
		t.Fatal("无变化改名改变活跃时间")
	}
	history, more, err := service.History(ctx, agentTestUser, c.ID, 0, 20)
	if err != nil || more || len(history) != 1 || history[0].Sequence != 1 || history[0].Role != conversation.RoleUser || history[0].Content != "  **正文**\n" {
		t.Fatalf("无自动回复/历史: %+v %v", history, err)
	}
	// 管理员也必须通过同一所有权判断；旧版本和配额不得优先泄露。
	for _, call := range []func() error{
		func() error { _, e := service.Detail(ctx, agentTestOther, c.ID); return e },
		func() error { _, _, e := service.History(ctx, agentTestOther, c.ID, 0, 20); return e },
		func() error { _, e := service.Append(ctx, agentTestOther, c.ID, agentKey(1), "正文"); return e },
		func() error { _, e := service.Rename(ctx, agentTestOther, c.ID, agentKey(1), title, 1); return e },
		func() error { return service.Delete(ctx, agentTestOther, c.ID) },
	} {
		if err := call(); !errors.Is(err, conversation.ErrNotFound) {
			t.Fatalf("管理员越权: %v", err)
		}
	}
	if err := service.Delete(ctx, agentTestUser, c.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(ctx, agentTestUser, c.ID); err != nil {
		t.Fatal("本人重复删除失败", err)
	}
	for _, call := range []func() error{
		func() error { _, e := service.Create(ctx, agentTestUser, agentKey(1), nil); return e },
		func() error {
			_, e := service.Append(ctx, agentTestUser, c.ID, agentKey(1), "  **正文**\n")
			return e
		},
		func() error { _, e := service.Rename(ctx, agentTestUser, c.ID, agentKey(1), title, 1); return e },
		func() error { _, e := service.Create(ctx, agentTestUser, agentKey(1), &title); return e },
		func() error { return service.Delete(ctx, agentTestOther, c.ID) },
		func() error { return service.Delete(ctx, agentTestUser, agentKey(999)) },
	} {
		if err := call(); !errors.Is(err, conversation.ErrNotFound) {
			t.Fatalf("删除后不应泄露或复活: %v", err)
		}
	}
	rows, err := env.pool.Query(ctx, `SELECT result_payload::text,resource_id,expires_at-created_at FROM velis.idempotency_operations WHERE resource_type='agent_conversation'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var count int
	for rows.Next() {
		var payload, resource string
		var interval any
		if err := rows.Scan(&payload, &resource, &interval); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(payload, "标题") || strings.Contains(payload, "正文") || strings.Contains(payload, "改名") || strings.Contains(payload, "snapshot") || resource != c.ID {
			t.Fatalf("成功载荷未清除: %s", payload)
		}
		var fields map[string]any
		if err := json.Unmarshal([]byte(payload), &fields); err != nil || len(fields) != 3 || fields["deleted"] != true {
			t.Fatalf("终态字段过多: %s", payload)
		}
		count++
	}
	if err := rows.Err(); err != nil || count != 4 {
		t.Fatalf("关联成功记录不完整: %d %v", count, err)
	}
	rows.Close()
	var messages, conversations, quota, markerFields int
	if err := env.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM velis.agent_messages),
(SELECT count(*) FROM velis.agent_conversations),(SELECT conversation_count FROM velis.agent_user_state WHERE user_id=$1),
(SELECT count(*) FROM information_schema.columns WHERE table_schema='velis' AND table_name='agent_conversation_deletions')`, agentTestUser).Scan(&messages, &conversations, &quota, &markerFields); err != nil || messages != 0 || conversations != 0 || quota != 0 || markerFields != 3 {
		t.Fatalf("删除事实: %d %d %d %d %v", messages, conversations, quota, markerFields, err)
	}
	// 原键窗口外可生成新资源；永久标记不依赖去重记录。
	if _, err := env.pool.Exec(ctx, `UPDATE velis.idempotency_operations SET created_at=now()-interval '3 days',expires_at=now()-interval '2 days' WHERE resource_type='agent_conversation'`); err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(ctx, agentTestUser, c.ID); err != nil {
		t.Fatal(err)
	}
	_, fresh := agentCreate(t, service, 1)
	if fresh.ID == c.ID {
		t.Fatal("到期创建不得复活旧ID")
	}
}

func TestAgentConversationConcurrentQuotaAndSequences(t *testing.T) {
	env := newTestEnv(t)
	seedAgentUsers(t, env)
	service := newAgentService(env, agent.Limits{MaxConversations: 1, MaxMessages: 65, MaxMessageChars: 4000})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	results := make(chan error, 20)
	for n := 1; n <= 20; n++ {
		wg.Go(func() { _, err := service.Create(ctx, agentTestUser, agentKey(n), nil); results <- err })
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, conversation.ErrConversationLimit) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatalf("最后会话名额竞争: %d", successes)
	}
	items, _, err := service.List(ctx, agentTestUser, nil, 20)
	if err != nil || len(items) != 1 {
		t.Fatalf("列表: %+v %v", items, err)
	}
	id := items[0].ID
	results = make(chan error, 80)
	for n := 1; n <= 80; n++ {
		wg.Go(func() { _, err := service.Append(ctx, agentTestUser, id, agentKey(n), "并发消息"); results <- err })
	}
	wg.Wait()
	close(results)
	successes = 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, conversation.ErrMessageLimit) {
			t.Fatal(err)
		}
	}
	if successes != 65 {
		t.Fatalf("消息竞争成功数: %d", successes)
	}
	// 同时刻也必须依赖序号排序。
	if _, err := env.pool.Exec(ctx, `UPDATE velis.agent_messages SET created_at=$1`, fixedNow()); err != nil {
		t.Fatal(err)
	}
	before := int64(0)
	for _, bounds := range [][2]int64{{46, 65}, {26, 45}, {6, 25}, {1, 5}} {
		batch, more, err := service.History(ctx, agentTestUser, id, before, 20)
		if err != nil || len(batch) != int(bounds[1]-bounds[0]+1) || more != (bounds[0] > 1) {
			t.Fatalf("历史批次: %+v %t %v", batch, more, err)
		}
		for i, m := range batch {
			if m.Sequence != bounds[0]+int64(i) {
				t.Fatal("序号重复或跳过")
			}
		}
		before = batch[0].Sequence
	}
	current, err := service.Detail(ctx, agentTestUser, id)
	if err != nil || current.TitleVersion != 1 || current.MessageCount != 65 {
		t.Fatalf("追加不得改标题版本: %+v %v", current, err)
	}
	results = make(chan error, 2)
	for n := 101; n <= 102; n++ {
		wg.Go(func() {
			_, err := service.Rename(ctx, agentTestUser, id, agentKey(n), fmt.Sprint(n), 1)
			results <- err
		})
	}
	wg.Wait()
	close(results)
	successes = 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, conversation.ErrTitleVersion) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatal("同版本并发改名必须仅一个成功")
	}
}

func TestAgentConversationIdempotencyIsolationFailureAndExpiry(t *testing.T) {
	env := newTestEnv(t)
	seedAgentUsers(t, env)
	service := newAgentService(env, agent.Limits{MaxConversations: 1, MaxMessages: 200, MaxMessageChars: 4000})
	ctx := context.Background()
	_, first := agentCreate(t, service, 1)
	if _, err := service.Create(ctx, agentTestOther, agentKey(1), nil); err != nil {
		t.Fatal("用户间幂等键不应冲突", err)
	}
	if _, err := service.Create(ctx, agentTestUser, agentKey(2), nil); !errors.Is(err, conversation.ErrConversationLimit) {
		t.Fatal("预期配额失败", err)
	}
	if err := service.Delete(ctx, agentTestUser, first.ID); err != nil {
		t.Fatal(err)
	}
	_, second := agentCreate(t, service, 2) // 失败后同键可以重用。
	var windowSeconds float64
	if err := env.pool.QueryRow(ctx, `SELECT extract(epoch FROM expires_at-created_at) FROM velis.idempotency_operations
WHERE actor_user_id=$1 AND operation=$2 AND idempotency_key=$3`, agentTestUser, agent.CreateOperation, agentKey(2)).Scan(&windowSeconds); err != nil || windowSeconds != 86400 {
		t.Fatalf("窗口: %v %v", windowSeconds, err)
	}
	// 人工在飞记录应稳定映射，而不是当成成功或配额失败。
	payload := struct{ ConversationID, Content string }{second.ID, "正文"}
	digest, err := idempotency.Digest(payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(ctx, `INSERT INTO velis.idempotency_operations
(actor_user_id,operation,idempotency_key,request_digest,status,created_at,expires_at)
VALUES($1,$2,$3,$4,'pending',now(),now()+interval '1 day')`, agentTestUser, agent.AppendOperation, agentKey(3), digest[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Append(ctx, agentTestUser, second.ID, agentKey(3), "正文"); !errors.Is(err, idempotency.ErrPending) {
		t.Fatal("在飞错误", err)
	}
	if _, err := service.Append(ctx, agentTestUser, second.ID, agentKey(3), "不同正文"); !errors.Is(err, idempotency.ErrKeyReused) {
		t.Fatal("同键不同摘要", err)
	}
	if _, err := env.pool.Exec(ctx, `UPDATE velis.idempotency_operations SET created_at=now()-interval '2 days',expires_at=now()-interval '1 day'
WHERE actor_user_id=$1 AND operation=$2 AND idempotency_key=$3`, agentTestUser, agent.AppendOperation, agentKey(3)); err != nil {
		t.Fatal(err)
	}
	result, err := service.Append(ctx, agentTestUser, second.ID, agentKey(3), "不同正文")
	if err != nil || result.Replayed {
		t.Fatalf("到期必须重新执行: %+v %v", result, err)
	}
}

func TestAgentConversationDeleteFailureRollbackAndRaces(t *testing.T) {
	env := newTestEnv(t)
	seedAgentUsers(t, env)
	service := newAgentService(env, agent.DefaultLimits())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, c := agentCreate(t, service, 1)
	if _, err := service.Append(ctx, agentTestUser, c.ID, agentKey(1), "保留正文"); err != nil {
		t.Fatal(err)
	}
	// 在最后步骤失败，证明前面的成功快照清理、级联消息删除和标记全部回滚。
	if _, err := env.pool.Exec(ctx, `CREATE FUNCTION velis.agent_test_fail_count() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN IF NEW.conversation_count < OLD.conversation_count THEN RAISE EXCEPTION '测试注入失败'; END IF; RETURN NEW; END $$;
CREATE TRIGGER agent_test_fail_count BEFORE UPDATE ON velis.agent_user_state FOR EACH ROW EXECUTE FUNCTION velis.agent_test_fail_count()`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = env.pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS agent_test_fail_count ON velis.agent_user_state; DROP FUNCTION IF EXISTS velis.agent_test_fail_count()`)
	})
	if err := service.Delete(ctx, agentTestUser, c.ID); err == nil {
		t.Fatal("注入删除失败未触发")
	}
	current, err := service.Detail(ctx, agentTestUser, c.ID)
	if err != nil || current.MessageCount != 1 {
		t.Fatalf("删除失败事实损坏: %+v %v", current, err)
	}
	var marks, quota int
	if err := env.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM velis.agent_conversation_deletions),
(SELECT conversation_count FROM velis.agent_user_state WHERE user_id=$1)`, agentTestUser).Scan(&marks, &quota); err != nil || marks != 0 || quota != 1 {
		t.Fatalf("标记/配额未回滚: %d %d %v", marks, quota, err)
	}
	if result, err := service.Append(ctx, agentTestUser, c.ID, agentKey(1), "保留正文"); err != nil || !result.Replayed {
		t.Fatalf("原成功快照未回滚: %+v %v", result, err)
	}
	if _, err := env.pool.Exec(ctx, `DROP TRIGGER agent_test_fail_count ON velis.agent_user_state; DROP FUNCTION velis.agent_test_fail_count()`); err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 10; round++ {
		if round > 0 {
			_, c = agentCreate(t, service, 100+round)
		}
		id := c.ID
		var wg sync.WaitGroup
		errorsCh := make(chan error, 4)
		wg.Go(func() { errorsCh <- service.Delete(ctx, agentTestUser, id) })
		wg.Go(func() {
			_, err := service.Append(ctx, agentTestUser, id, agentKey(200+round), "竞争追加")
			errorsCh <- err
		})
		wg.Go(func() {
			_, err := service.Rename(ctx, agentTestUser, id, agentKey(300+round), "竞争改名", 1)
			errorsCh <- err
		})
		wg.Go(func() {
			key := 100 + round
			if round == 0 {
				key = 1
			}
			_, err := service.Create(ctx, agentTestUser, agentKey(key), nil)
			errorsCh <- err
		})
		wg.Wait()
		close(errorsCh)
		for err := range errorsCh {
			if err != nil && !errors.Is(err, conversation.ErrNotFound) {
				t.Fatal("删除竞争异常", err)
			}
		}
		if _, err := service.Detail(ctx, agentTestUser, id); !errors.Is(err, conversation.ErrNotFound) {
			t.Fatal("删除竞争复活会话", err)
		}
		if err := service.Delete(ctx, agentTestUser, id); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAgentConversationCleanupPreservesHistoryAndDeletionMarkers(t *testing.T) {
	env := newTestEnv(t)
	seedAgentUsers(t, env)
	service := newAgentService(env, agent.DefaultLimits())
	ctx := context.Background()
	_, c := agentCreate(t, service, 1)
	if _, err := service.Append(ctx, agentTestUser, c.ID, agentKey(1), "永久历史"); err != nil {
		t.Fatal(err)
	}
	_, deleted := agentCreate(t, service, 2)
	if err := service.Delete(ctx, agentTestUser, deleted.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(ctx, `UPDATE velis.idempotency_operations SET created_at=now()-interval '3 days',expires_at=now()-interval '2 days'`); err != nil {
		t.Fatal(err)
	}
	count, err := postgres.NewCleanupRepository(env.pool).DeleteExpiredIdempotency(ctx, time.Now(), 100)
	if err != nil || count != 3 {
		t.Fatalf("仅清理到期去重: %d %v", count, err)
	}
	history, _, err := service.History(ctx, agentTestUser, c.ID, 0, 20)
	if err != nil || len(history) != 1 || history[0].Content != "永久历史" {
		t.Fatalf("清理误删历史: %+v %v", history, err)
	}
	if err := service.Delete(ctx, agentTestUser, deleted.ID); err != nil {
		t.Fatal("清理不应删除永久标记", err)
	}
}
