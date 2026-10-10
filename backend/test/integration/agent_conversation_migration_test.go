package integration

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

const agentTestUser = "81000000-0000-0000-0000-000000000001"
const agentTestOther = "81000000-0000-0000-0000-000000000002"
const agentTestConversation = "81000000-0000-0000-0000-000000000003"
const agentTestMessage = "81000000-0000-0000-0000-000000000004"
const agentTestKey = "81000000-0000-0000-0000-000000000005"

func seedAgentUsers(t *testing.T, env *testEnv) {
	t.Helper()
	env.resetArticles(t)
	env.resetAccounts(t)
	_, err := env.pool.Exec(context.Background(), `INSERT INTO velis.users
(id,username,nickname,password_hash,role,status,created_at,updated_at)
VALUES ($1,'agent_user','对话用户','hash','user','active',now(),now()),
($2,'agent_admin','管理员','hash','admin','active',now(),now())`, agentTestUser, agentTestOther)
	if err != nil {
		t.Fatal(err)
	}
}

func seedAgentConversation(t *testing.T, env *testEnv) {
	t.Helper()
	_, err := env.pool.Exec(context.Background(), `INSERT INTO velis.agent_conversations
(id,user_id,title,created_at,last_activity_at) VALUES ($1,$2,'私有标题',now(),now())`, agentTestConversation, agentTestUser)
	if err != nil {
		t.Fatal(err)
	}
}

func TestAgentConversationMigrationConstraints(t *testing.T) {
	env := newTestEnv(t)
	seedAgentUsers(t, env)
	seedAgentConversation(t, env)
	ctx := context.Background()
	_, err := env.pool.Exec(ctx, `INSERT INTO velis.agent_messages
(id,conversation_id,sequence,role,content,created_at) VALUES ($1,$2,1,'user','私有正文',now())`, agentTestMessage, agentTestConversation)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct{ name, sql, code string }{
		{"会话归属", `INSERT INTO velis.agent_conversations (id,user_id,title,created_at,last_activity_at) VALUES (gen_random_uuid(),gen_random_uuid(),'标题',now(),now())`, "23503"},
		{"状态归属", `INSERT INTO velis.agent_user_state (user_id) VALUES (gen_random_uuid())`, "23503"},
		{"删除标记归属", `INSERT INTO velis.agent_conversation_deletions VALUES (gen_random_uuid(),gen_random_uuid(),now())`, "23503"},
		{"孤儿消息", `INSERT INTO velis.agent_messages VALUES (gen_random_uuid(),gen_random_uuid(),1,'user','正文',now())`, "23503"},
		{"非法角色", `UPDATE velis.agent_messages SET role='system'`, "23514"},
		{"非法序号", `UPDATE velis.agent_messages SET sequence=0`, "23514"},
		{"序号唯一", `INSERT INTO velis.agent_messages SELECT gen_random_uuid(),conversation_id,sequence,role,content,created_at FROM velis.agent_messages`, "23505"},
		{"消息计数", `UPDATE velis.agent_conversations SET message_count=-1`, "23514"},
		{"下个序号", `UPDATE velis.agent_conversations SET next_sequence=0`, "23514"},
		{"标题版本", `UPDATE velis.agent_conversations SET title_version=0`, "23514"},
		{"标题长度", `UPDATE velis.agent_conversations SET title=repeat('界',101)`, "23514"},
		{"会话计数", `INSERT INTO velis.agent_user_state VALUES ('` + agentTestUser + `',-1)`, "23514"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := env.pool.Exec(ctx, test.sql)
			pgErr, ok := err.(*pgconn.PgError)
			if !ok || pgErr.Code != test.code {
				t.Fatalf("期望约束 %s，实际 %v", test.code, err)
			}
		})
	}
	if _, err := env.pool.Exec(ctx, `DELETE FROM velis.agent_conversations WHERE id=$1`, agentTestConversation); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.agent_messages`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("消息未级联删除: %d %v", count, err)
	}
}

func TestAgentConversationMigrationSafeDown(t *testing.T) {
	env := newTestEnv(t)
	seedAgentUsers(t, env)
	ctx := context.Background()
	down, err := os.ReadFile("../../migrations/000012_create_agent_conversations.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, err := os.ReadFile("../../migrations/000012_create_agent_conversations.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	// 每次在同一事务中测试回退；保护失败后回滚，不触碰迁移版本表。
	for _, fixture := range []string{"空库", "空状态", "非空计数", "会话", "消息", "删除标记", "Agent幂等"} {
		t.Run(fixture, func(t *testing.T) {
			tx, err := env.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if fixture == "空状态" {
				_, err = tx.Exec(ctx, `INSERT INTO velis.agent_user_state(user_id) VALUES($1)`, agentTestUser)
			}
			if fixture == "非空计数" {
				_, err = tx.Exec(ctx, `INSERT INTO velis.agent_user_state(user_id,conversation_count) VALUES($1,1)`, agentTestUser)
			}
			if fixture == "会话" || fixture == "消息" {
				_, err = tx.Exec(ctx, `INSERT INTO velis.agent_conversations(id,user_id,title,created_at,last_activity_at)
VALUES($1,$2,'必须保留的标题',now(),now())`, agentTestConversation, agentTestUser)
			}
			if fixture == "消息" && err == nil {
				_, err = tx.Exec(ctx, `INSERT INTO velis.agent_messages VALUES($1,$2,1,'user','必须保留的正文',now())`, agentTestMessage, agentTestConversation)
			}
			if fixture == "删除标记" {
				_, err = tx.Exec(ctx, `INSERT INTO velis.agent_conversation_deletions VALUES($1,$2,now())`, agentTestConversation, agentTestUser)
			}
			if fixture == "Agent幂等" {
				_, err = tx.Exec(ctx, `INSERT INTO velis.idempotency_operations
(actor_user_id,operation,idempotency_key,request_digest,status,created_at,expires_at)
VALUES($1,'agent.conversation.create',$2,decode(repeat('00',32),'hex'),'pending',now()-interval '2 days',now()-interval '1 day')`, agentTestUser, agentTestKey)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(ctx, `SAVEPOINT before_down`); err != nil {
				t.Fatal(err)
			}
			_, err = tx.Exec(ctx, string(down))
			if fixture == "空库" || fixture == "空状态" {
				if err != nil {
					t.Fatalf("允许空事实回退: %v", err)
				}
				if _, err := tx.Exec(ctx, string(up)); err != nil {
					t.Fatalf("down/up: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "拒绝回滚 Agent") {
				t.Fatalf("非空事实回退未保护: %v", err)
			}
			if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT before_down`); err != nil {
				t.Fatal(err)
			}
			var count int
			table := map[string]string{"非空计数": "agent_user_state", "会话": "agent_conversations", "消息": "agent_messages", "删除标记": "agent_conversation_deletions", "Agent幂等": "idempotency_operations"}[fixture]
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM velis.`+table).Scan(&count); err != nil || count != 1 {
				t.Fatalf("保护失败后原事实不完整: %d %v", count, err)
			}
			if fixture == "消息" {
				var content string
				if err := tx.QueryRow(ctx, `SELECT content FROM velis.agent_messages`).Scan(&content); err != nil || content != "必须保留的正文" {
					t.Fatalf("原正文损坏: %q %v", content, err)
				}
			}
		})
	}
}
