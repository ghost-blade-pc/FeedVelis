package postgres

import (
	"context"
	"errors"
	"time"

	agent "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentconversation"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/idempotency"
	conversation "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentconversation"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AgentConversationRepository struct{ pool *pgxpool.Pool }

func NewAgentConversationRepository(pool *pgxpool.Pool) *AgentConversationRepository {
	return &AgentConversationRepository{pool: pool}
}

var _ agent.Repository = (*AgentConversationRepository)(nil)

func requireAgentTransaction(ctx context.Context) error {
	state, ok := ctx.Value(txContextKey{}).(*transactionState)
	if !ok || state.readOnly {
		return errors.New("会话写入必须位于写事务中")
	}
	return nil
}

func (r *AgentConversationRepository) LockUser(ctx context.Context, userID string) (agent.UserState, error) {
	if err := requireAgentTransaction(ctx); err != nil {
		return agent.UserState{}, err
	}
	q := querier(ctx, r.pool)
	if _, err := q.Exec(ctx, `INSERT INTO velis.agent_user_state(user_id) VALUES($1) ON CONFLICT DO NOTHING`, userID); err != nil {
		return agent.UserState{}, err
	}
	var state agent.UserState
	err := q.QueryRow(ctx, `SELECT conversation_count,clock_timestamp() FROM velis.agent_user_state WHERE user_id=$1 FOR UPDATE`, userID).Scan(&state.ConversationCount, &state.Now)
	state.Now = state.Now.UTC()
	return state, err
}

const agentConversationColumns = `id::text,user_id::text,title,title_version,message_count,next_sequence,created_at,last_activity_at`

func scanAgentConversation(row pgx.Row) (conversation.Conversation, error) {
	var c conversation.Conversation
	err := row.Scan(&c.ID, &c.UserID, &c.Title, &c.TitleVersion, &c.MessageCount, &c.NextSequence, &c.CreatedAt, &c.LastActivityAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return conversation.Conversation{}, conversation.ErrNotFound
	}
	c.CreatedAt = c.CreatedAt.UTC()
	c.LastActivityAt = c.LastActivityAt.UTC()
	return c, err
}

func (r *AgentConversationRepository) Find(ctx context.Context, userID, id string, lock bool) (conversation.Conversation, error) {
	sql := `SELECT ` + agentConversationColumns + ` FROM velis.agent_conversations WHERE user_id=$1 AND id=$2`
	if lock {
		if err := requireAgentTransaction(ctx); err != nil {
			return conversation.Conversation{}, err
		}
		sql += ` FOR UPDATE`
	}
	return scanAgentConversation(querier(ctx, r.pool).QueryRow(ctx, sql, userID, id))
}

func (r *AgentConversationRepository) WasDeleted(ctx context.Context, userID, id string) (bool, error) {
	var found bool
	err := querier(ctx, r.pool).QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM velis.agent_conversation_deletions WHERE user_id=$1 AND conversation_id=$2)`, userID, id).Scan(&found)
	return found, err
}

func (r *AgentConversationRepository) CreateResource(ctx context.Context, identity idempotency.Identity, now time.Time) (string, error) {
	if err := requireAgentTransaction(ctx); err != nil {
		return "", err
	}
	var id string
	err := querier(ctx, r.pool).QueryRow(ctx, `SELECT resource_id FROM velis.idempotency_operations
WHERE actor_user_id=$1 AND operation=$2 AND idempotency_key=$3 AND status='succeeded'
AND resource_type='agent_conversation' AND expires_at>$4`, identity.ActorUserID, agent.CreateOperation, identity.Key, now).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, err
}

func (r *AgentConversationRepository) InsertConversation(ctx context.Context, c conversation.Conversation) error {
	if err := requireAgentTransaction(ctx); err != nil {
		return err
	}
	q := querier(ctx, r.pool)
	_, err := q.Exec(ctx, `INSERT INTO velis.agent_conversations
(id,user_id,title,title_version,message_count,next_sequence,created_at,last_activity_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, c.ID, c.UserID, c.Title, c.TitleVersion, c.MessageCount, c.NextSequence, c.CreatedAt, c.LastActivityAt)
	if err != nil {
		return err
	}
	tag, err := q.Exec(ctx, `UPDATE velis.agent_user_state SET conversation_count=conversation_count+1 WHERE user_id=$1`, c.UserID)
	if err == nil && tag.RowsAffected() != 1 {
		return errors.New("会话用户状态缺失")
	}
	return err
}

func (r *AgentConversationRepository) SaveTitle(ctx context.Context, c conversation.Conversation) error {
	if err := requireAgentTransaction(ctx); err != nil {
		return err
	}
	tag, err := querier(ctx, r.pool).Exec(ctx, `UPDATE velis.agent_conversations SET title=$3,title_version=$4,
last_activity_at=GREATEST(last_activity_at,$5) WHERE user_id=$1 AND id=$2`, c.UserID, c.ID, c.Title, c.TitleVersion, c.LastActivityAt)
	if err == nil && tag.RowsAffected() != 1 {
		return conversation.ErrNotFound
	}
	return err
}

func (r *AgentConversationRepository) AppendMessage(ctx context.Context, userID string, m conversation.Message) error {
	if err := requireAgentTransaction(ctx); err != nil {
		return err
	}
	q := querier(ctx, r.pool)
	tag, err := q.Exec(ctx, `UPDATE velis.agent_conversations SET message_count=message_count+1,
next_sequence=$3+1,last_activity_at=GREATEST(last_activity_at,$4)
WHERE user_id=$1 AND id=$2 AND next_sequence=$3`, userID, m.ConversationID, m.Sequence, m.CreatedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return conversation.ErrNotFound
	}
	_, err = q.Exec(ctx, `INSERT INTO velis.agent_messages(id,conversation_id,sequence,role,content,created_at)
VALUES($1,$2,$3,$4,$5,$6)`, m.ID, m.ConversationID, m.Sequence, m.Role, m.Content, m.CreatedAt)
	return err
}

// DeleteConversation 在既有用户锁下清除事实和全部关联成功载荷；任一步失败由外层回滚。
func (r *AgentConversationRepository) DeleteConversation(ctx context.Context, c conversation.Conversation, now time.Time) error {
	if err := requireAgentTransaction(ctx); err != nil {
		return err
	}
	q := querier(ctx, r.pool)
	_, err := q.Exec(ctx, `UPDATE velis.idempotency_operations SET result_payload=
jsonb_build_object('version',1,'conversation_id',$2::text,'deleted',true)
WHERE actor_user_id=$1 AND resource_type='agent_conversation' AND resource_id=$2
AND status='succeeded' AND operation IN ('agent.conversation.create','agent.message.append','agent.conversation.rename')`, c.UserID, c.ID)
	if err != nil {
		return err
	}
	tag, err := q.Exec(ctx, `DELETE FROM velis.agent_conversations WHERE user_id=$1 AND id=$2`, c.UserID, c.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return conversation.ErrNotFound
	}
	if _, err = q.Exec(ctx, `INSERT INTO velis.agent_conversation_deletions(conversation_id,user_id,deleted_at) VALUES($1,$2,$3)`, c.ID, c.UserID, now); err != nil {
		return err
	}
	tag, err = q.Exec(ctx, `UPDATE velis.agent_user_state SET conversation_count=conversation_count-1 WHERE user_id=$1`, c.UserID)
	if err == nil && tag.RowsAffected() != 1 {
		return errors.New("会话用户状态缺失")
	}
	return err
}

func (r *AgentConversationRepository) List(ctx context.Context, userID string, boundary *agent.ListBoundary, limit int) ([]conversation.Conversation, error) {
	sql := `SELECT ` + agentConversationColumns + ` FROM velis.agent_conversations WHERE user_id=$1`
	args := []any{userID, limit}
	if boundary != nil {
		sql += ` AND (last_activity_at,id)<($3,$4::uuid)`
		args = append(args, boundary.ActivityAt, boundary.ID)
	}
	rows, err := querier(ctx, r.pool).Query(ctx, sql+` ORDER BY last_activity_at DESC,id DESC LIMIT $2`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]conversation.Conversation, 0)
	for rows.Next() {
		c, err := scanAgentConversation(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, c)
	}
	return items, rows.Err()
}

// Messages 返回倒序候选；应用服务裁掉多取的一条后再将当前批反转。
func (r *AgentConversationRepository) Messages(ctx context.Context, conversationID string, before int64, limit int) ([]conversation.Message, error) {
	rows, err := querier(ctx, r.pool).Query(ctx, `SELECT id::text,conversation_id::text,sequence,role,content,created_at
FROM velis.agent_messages WHERE conversation_id=$1 AND ($2::bigint=0 OR sequence<$2)
ORDER BY sequence DESC LIMIT $3`, conversationID, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]conversation.Message, 0)
	for rows.Next() {
		var m conversation.Message
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Sequence, &m.Role, &m.Content, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.CreatedAt = m.CreatedAt.UTC()
		items = append(items, m)
	}
	return items, rows.Err()
}
