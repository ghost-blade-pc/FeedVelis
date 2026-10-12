package integration

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

const agentTestRun = "83000000-0000-0000-0000-000000000001"
const agentTestEvidence = "83000000-0000-0000-0000-000000000002"

func seedAgentRun(t *testing.T, env *testEnv) {
	t.Helper()
	seedAgentUsers(t, env)
	seedAgentConversation(t, env)
	_, err := env.pool.Exec(context.Background(), `INSERT INTO velis.agent_messages(id,conversation_id,sequence,role,content,created_at)
VALUES($1,$2,1,'user','私密问题',now());`, agentTestMessage, agentTestConversation)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = env.pool.Exec(context.Background(), `UPDATE velis.agent_conversations SET message_count=1,next_sequence=2 WHERE id=$1`, agentTestConversation); err != nil {
		t.Fatal(err)
	}
	_, err = env.pool.Exec(context.Background(), `INSERT INTO velis.agent_runs
(id,conversation_id,user_id,auth_session_id,user_message_id,attempt,status,assistant_reserved,created_at,deadline)
VALUES($1,$2,$3,$3,$4,1,'accepted',true,now(),now()+interval '60 seconds')`, agentTestRun, agentTestConversation, agentTestUser, agentTestMessage)
	if err != nil {
		t.Fatal(err)
	}
}

func TestAgentRunMigrationUniqueSuccessAndRetryAssociation(t *testing.T) {
	env := newTestEnv(t)
	seedAgentRun(t, env)
	ctx := t.Context()
	markRunSucceeded(t, env, agentTestRun, agentTestConversation)
	_, err := env.pool.Exec(ctx, `INSERT INTO velis.agent_messages VALUES($1,$2,3,'assistant','另一个助手夹具',now())`, agentKey(991), agentTestConversation)
	if err != nil {
		t.Fatal(err)
	}
	_, err = env.pool.Exec(ctx, `INSERT INTO velis.agent_runs(id,conversation_id,user_id,auth_session_id,user_message_id,attempt,retry_of,status,assistant_reserved,created_at,deadline,finished_at,assistant_message_id,answer)
SELECT gen_random_uuid(),conversation_id,user_id,auth_session_id,user_message_id,2,id,'succeeded',false,created_at,deadline,now(),$2,answer FROM velis.agent_runs WHERE id=$1`, agentTestRun, agentKey(991))
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatal("同消息重复成功没有唯一保护", err)
	}
	_, err = env.pool.Exec(ctx, `INSERT INTO velis.agent_runs(id,conversation_id,user_id,auth_session_id,user_message_id,attempt,retry_of,status,assistant_reserved,created_at,deadline,finished_at,error_code)
SELECT gen_random_uuid(),conversation_id,user_id,auth_session_id,user_message_id,2,gen_random_uuid(),'failed',false,created_at,deadline,now(),'AGENT_TOOL_FAILED' FROM velis.agent_runs WHERE id=$1`, agentTestRun)
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		t.Fatal("不存在的 retry_of 没有关联保护", err)
	}
	if _, err = env.pool.Exec(ctx, `DELETE FROM velis.agent_conversations WHERE id=$1`, agentTestConversation); err != nil {
		t.Fatal("成功助手关联阻止整会话删除", err)
	}
	var n int
	if err = env.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM velis.agent_runs)+(SELECT count(*) FROM velis.agent_messages)`).Scan(&n); err != nil || n != 0 {
		t.Fatal("成功关联级联不完整", err)
	}
}

func TestAgentRunMigrationConstraints(t *testing.T) {
	env := newTestEnv(t)
	seedAgentRun(t, env)
	ctx := context.Background()
	_, err := env.pool.Exec(ctx, `INSERT INTO velis.agent_run_evidence
(run_id,evidence_id,version,kind,article_id,revision_id,obtained_at,content_kind,truncated,content)
VALUES($1,$2,1,'article',1,2,now(),'body',false,'私密工作证据')`, agentTestRun, agentTestEvidence)
	if err != nil {
		t.Fatal(err)
	}
	_, err = env.pool.Exec(ctx, `INSERT INTO velis.agent_run_blocks VALUES($1,'b1',1,'私密片段','[]');
`, agentTestRun)
	if err != nil {
		t.Fatal(err)
	}
	_, err = env.pool.Exec(ctx, `INSERT INTO velis.agent_run_block_evidence VALUES($1,'b1',$2,1)`, agentTestRun, agentTestEvidence)
	if err != nil {
		t.Fatal(err)
	}
	_, err = env.pool.Exec(ctx, `INSERT INTO velis.agent_run_events(run_id,sequence,version,kind,payload,created_at) VALUES($1,1,1,'run.status','{}',now())`, agentTestRun)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct{ name, sql, code string }{
		{"归属关联", `UPDATE velis.agent_runs SET user_id='` + agentTestOther + `'`, "23503"},
		{"用户消息角色", `UPDATE velis.agent_runs SET user_message_role='assistant'`, "23514"},
		{"跨会话消息", `UPDATE velis.agent_runs SET user_message_id=gen_random_uuid()`, "23503"},
		{"非法状态", `UPDATE velis.agent_runs SET status='unknown'`, "23514"},
		{"活动预留", `UPDATE velis.agent_runs SET assistant_reserved=false`, "23514"},
		{"活动终态时间", `UPDATE velis.agent_runs SET finished_at=now()`, "23514"},
		{"执行者与租约", `UPDATE velis.agent_runs SET status='running'`, "23514"},
		{"租约超过期限", `UPDATE velis.agent_runs SET status='running',owner_token=gen_random_uuid(),lease_until=deadline+interval '1 second'`, "23514"},
		{"期限", `UPDATE velis.agent_runs SET deadline=created_at`, "23514"},
		{"超过尝试上限", `UPDATE velis.agent_runs SET attempt=4`, "23514"},
		{"首次指向重试", `UPDATE velis.agent_runs SET retry_of=id`, "23514"},
		{"活动唯一", `INSERT INTO velis.agent_runs SELECT gen_random_uuid(),conversation_id,user_id,auth_session_id,user_message_id,user_message_role,assistant_message_id,assistant_message_role,2,id,context_article_id,constraints,status,assistant_reserved,created_at,deadline,finished_at,owner_token,lease_until,error_code,answer,usage,execution_metadata,event_count,event_bytes,events_expired FROM velis.agent_runs`, "23505"},
		{"来源类型", `UPDATE velis.agent_run_evidence SET kind='web'`, "23514"},
		{"证据版本", `UPDATE velis.agent_run_evidence SET version=2`, "23514"},
		{"跨回合证据", `UPDATE velis.agent_run_block_evidence SET evidence_id=gen_random_uuid()`, "23503"},
		{"片段序号", `UPDATE velis.agent_run_blocks SET position=33`, "23514"},
		{"事件位置", `UPDATE velis.agent_run_events SET sequence=0`, "23514"},
		{"事件唯一", `INSERT INTO velis.agent_run_events(run_id,sequence,version,kind,payload,created_at) SELECT run_id,sequence,version,kind,payload,created_at FROM velis.agent_run_events`, "23505"},
		{"内部事件", `UPDATE velis.agent_run_events SET kind='model.delta'`, "23514"},
		{"瞬态事件", `UPDATE velis.agent_run_events SET kind='stream.error'`, "23514"},
		{"事件总额", `UPDATE velis.agent_runs SET event_bytes=524289`, "23514"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := env.pool.Exec(ctx, tt.sql)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != tt.code {
				t.Fatalf("期望 %s: %v", tt.code, err)
			}
		})
	}
	if _, err := env.pool.Exec(ctx, `DELETE FROM velis.agent_conversations WHERE id=$1`, agentTestConversation); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"agent_runs", "agent_run_evidence", "agent_run_blocks", "agent_run_block_evidence", "agent_run_events"} {
		var n int
		if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.`+table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("级联删除 %s: %d %v", table, n, err)
		}
	}
}

func TestAgentRunMigrationSafeDown(t *testing.T) {
	env := newTestEnv(t)
	seedAgentUsers(t, env)
	ctx := context.Background()
	down, err := os.ReadFile("../../migrations/000013_create_agent_runs.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, err := os.ReadFile("../../migrations/000013_create_agent_runs.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []string{"空事实", "已有回合", "过期接受幂等"} {
		t.Run(fixture, func(t *testing.T) {
			tx, err := env.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if fixture == "已有回合" {
				_, err = tx.Exec(ctx, `INSERT INTO velis.agent_conversations(id,user_id,title,created_at,last_activity_at) VALUES($1,$2,'保留的会话',now(),now())`, agentTestConversation, agentTestUser)
				if err == nil {
					_, err = tx.Exec(ctx, `INSERT INTO velis.agent_messages VALUES($1,$2,1,'user','保留的问题',now())`, agentTestMessage, agentTestConversation)
				}
				if err == nil {
					_, err = tx.Exec(ctx, `INSERT INTO velis.agent_runs(id,conversation_id,user_id,auth_session_id,user_message_id,attempt,status,assistant_reserved,created_at,deadline) VALUES($1,$2,$3,$3,$4,1,'accepted',true,now(),now()+interval '60 seconds')`, agentTestRun, agentTestConversation, agentTestUser, agentTestMessage)
				}
			}
			if fixture == "过期接受幂等" {
				_, err = tx.Exec(ctx, `INSERT INTO velis.idempotency_operations(actor_user_id,operation,idempotency_key,request_digest,status,created_at,expires_at) VALUES($1,'agent.run.accept',$2,decode(repeat('00',32),'hex'),'pending',now()-interval '2 days',now()-interval '1 day')`, agentTestUser, agentTestKey)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(ctx, `SAVEPOINT before_run_down`); err != nil {
				t.Fatal(err)
			}
			_, err = tx.Exec(ctx, string(down))
			if fixture == "空事实" {
				if err != nil {
					t.Fatal(err)
				}
				if _, err = tx.Exec(ctx, string(up)); err != nil {
					t.Fatalf("空事实 down/up: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "拒绝回滚 Agent 回合迁移") {
				t.Fatalf("有事实回退未被拒绝: %v", err)
			}
			if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT before_run_down`); err != nil {
				t.Fatal(err)
			}
			var n int
			table := "agent_runs"
			if fixture == "过期接受幂等" {
				table = "idempotency_operations"
			}
			if err = tx.QueryRow(ctx, `SELECT count(*) FROM velis.`+table).Scan(&n); err != nil || n != 1 {
				t.Fatalf("拒绝回退损坏事实: %d %v", n, err)
			}
		})
	}
}
