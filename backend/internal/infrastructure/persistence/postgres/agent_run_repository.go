package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	agent "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentrun"
	conversation "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentconversation"
	run "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AgentRunRepository struct {
	*AgentConversationRepository
	pool *pgxpool.Pool
}

func NewAgentRunRepository(pool *pgxpool.Pool) *AgentRunRepository {
	return &AgentRunRepository{AgentConversationRepository: NewAgentConversationRepository(pool), pool: pool}
}

var _ agent.AcceptanceRepository = (*AgentRunRepository)(nil)
var _ agent.CurrentAuthorization = (*AgentRunRepository)(nil)

func (r *AgentRunRepository) DatabaseTime(ctx context.Context) (time.Time, error) {
	var now time.Time
	err := querier(ctx, r.pool).QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now)
	return now.UTC(), err
}

func (r *AgentRunRepository) CheckSession(ctx context.Context, userID, sessionID string) error {
	var active bool
	err := querier(ctx, r.pool).QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM velis.users u JOIN velis.auth_sessions s ON s.user_id=u.id
WHERE u.id=$1 AND s.id=$2 AND u.status='active' AND s.revoked_at IS NULL AND s.expires_at>clock_timestamp())`, userID, sessionID).Scan(&active)
	if err != nil {
		return err
	}
	if !active {
		return &run.Error{Code: run.CodeAuthInvalid}
	}
	return nil
}

func (r *AgentRunRepository) LockConversation(ctx context.Context, userID, id string) (conversation.Conversation, error) {
	return r.AgentConversationRepository.Find(ctx, userID, id, true)
}

func (r *AgentRunRepository) Message(ctx context.Context, conversationID, id string) (conversation.Message, error) {
	var m conversation.Message
	err := querier(ctx, r.pool).QueryRow(ctx, `SELECT id::text,conversation_id::text,sequence,role,content,created_at FROM velis.agent_messages WHERE conversation_id=$1 AND id=$2`, conversationID, id).
		Scan(&m.ID, &m.ConversationID, &m.Sequence, &m.Role, &m.Content, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, run.ErrMessageNotFound
	}
	m.CreatedAt = m.CreatedAt.UTC()
	return m, err
}

func (r *AgentRunRepository) Attempts(ctx context.Context, conversationID, messageID string) ([]run.Attempt, error) {
	rows, err := querier(ctx, r.pool).Query(ctx, `SELECT id::text,status,COALESCE(context_article_id,0),constraints,COALESCE(retry_of::text,'')
FROM velis.agent_runs WHERE conversation_id=$1 AND user_message_id=$2 ORDER BY attempt`, conversationID, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []run.Attempt{}
	for rows.Next() {
		var item run.Attempt
		var constraints []byte
		item.Input.MessageID = messageID
		if err := rows.Scan(&item.ID, &item.State, &item.Input.ContextArticleID, &constraints, &item.Input.RetryOf); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(constraints, &item.Input.Constraints); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func (r *AgentRunRepository) AccountActive(ctx context.Context, userID string) (int, error) {
	var count int
	err := querier(ctx, r.pool).QueryRow(ctx, `SELECT count(*) FROM velis.agent_runs WHERE user_id=$1 AND status IN ('accepted','running')`, userID).Scan(&count)
	return count, err
}
func (r *AgentRunRepository) ConversationActive(ctx context.Context, id string) (int, error) {
	return r.ActiveRuns(ctx, id)
}

func (r *AgentRunRepository) InsertAccepted(ctx context.Context, value run.Run) error {
	if err := requireAgentTransaction(ctx); err != nil {
		return err
	}
	if value.State != run.Accepted || !value.AssistantReserved {
		return run.ErrInvalidInput
	}
	constraints, err := json.Marshal(value.Input.Constraints)
	if err != nil {
		return err
	}
	_, err = querier(ctx, r.pool).Exec(ctx, `INSERT INTO velis.agent_runs
(id,conversation_id,user_id,auth_session_id,user_message_id,attempt,retry_of,context_article_id,constraints,status,assistant_reserved,created_at,deadline)
VALUES($1,$2,$3,$4,$5,(SELECT COALESCE(max(attempt),0)+1 FROM velis.agent_runs WHERE user_message_id=$5),NULLIF($6,'')::uuid,NULLIF($7::bigint,0),$8,'accepted',true,$9,$10)`,
		value.ID, value.ConversationID, value.UserID, value.SessionID, value.Input.MessageID, value.Input.RetryOf, value.Input.ContextArticleID, constraints, value.CreatedAt, value.Deadline)
	if err != nil {
		return err
	}
	return r.appendEvents(ctx, &agent.Record{Run: value}, []agent.Event{{Kind: agent.RunStatus, State: run.Accepted}}, false)
}

const agentRunColumns = `r.id::text,r.user_id::text,r.auth_session_id::text,r.conversation_id::text,r.user_message_id::text,
COALESCE(r.retry_of::text,''),COALESCE(r.context_article_id,0),r.constraints,r.status,r.created_at,r.deadline,r.finished_at,
COALESCE(r.owner_token::text,''),r.lease_until,r.assistant_reserved,COALESCE(r.assistant_message_id::text,''),COALESCE(r.error_code,''),
r.answer,r.usage,r.event_count,r.event_bytes,r.events_expired,
EXISTS(SELECT 1 FROM velis.agent_run_blocks b WHERE b.run_id=r.id) AND r.status<>'succeeded'`

func scanAgentRun(row pgx.Row) (agent.Record, error) {
	var record agent.Record
	v := &record.Run
	var constraints, answer, usage []byte
	var finished, lease *time.Time
	err := row.Scan(&v.ID, &v.UserID, &v.SessionID, &v.ConversationID, &v.Input.MessageID, &v.Input.RetryOf, &v.Input.ContextArticleID, &constraints,
		&v.State, &v.CreatedAt, &v.Deadline, &finished, &v.OwnerToken, &lease, &v.AssistantReserved, &v.AssistantMessageID, &v.ErrorCode,
		&answer, &usage, &record.LastEventSequence, &record.EventBytes, &record.EventsExpired, &record.Partial)
	if errors.Is(err, pgx.ErrNoRows) {
		return record, run.ErrNotFound
	}
	if err != nil {
		return record, err
	}
	if err = json.Unmarshal(constraints, &v.Input.Constraints); err != nil {
		return record, err
	}
	if len(answer) > 0 {
		if err = json.Unmarshal(answer, &record.Answer); err != nil {
			return record, err
		}
	}
	if err = json.Unmarshal(usage, &record.Usage); err != nil {
		return record, err
	}
	v.CreatedAt = v.CreatedAt.UTC()
	v.Deadline = v.Deadline.UTC()
	if finished != nil {
		v.FinishedAt = finished.UTC()
	}
	if lease != nil {
		v.LeaseUntil = lease.UTC()
	}
	return record, nil
}

func (r *AgentRunRepository) find(ctx context.Context, scope agent.Scope, lock bool) (agent.Record, error) {
	sql := `SELECT ` + agentRunColumns + ` FROM velis.agent_runs r WHERE r.user_id=$1 AND r.conversation_id=$2 AND r.id=$3`
	if lock {
		if err := requireAgentTransaction(ctx); err != nil {
			return agent.Record{}, err
		}
		sql += ` FOR UPDATE OF r`
	}
	return scanAgentRun(querier(ctx, r.pool).QueryRow(ctx, sql, scope.UserID, scope.ConversationID, scope.RunID))
}
func (r *AgentRunRepository) Find(ctx context.Context, scope agent.Scope) (agent.Record, error) {
	record, err := r.find(ctx, scope, false)
	if err != nil {
		return record, err
	}
	if record.Partial {
		a, _, err := r.workAnswer(ctx, scope.RunID)
		if err != nil {
			return record, err
		}
		record.Answer = &a
	}
	return record, nil
}
func (r *AgentRunRepository) LockRun(ctx context.Context, scope agent.Scope) (agent.Record, error) {
	return r.find(ctx, scope, true)
}

func (r *AgentRunRepository) List(ctx context.Context, userID, conversationID string, boundary *agent.Boundary, limit int) ([]agent.Record, error) {
	sql := `SELECT ` + agentRunColumns + ` FROM velis.agent_runs r WHERE r.user_id=$1 AND r.conversation_id=$2`
	args := []any{userID, conversationID, limit}
	if boundary != nil {
		sql += ` AND (r.created_at,r.id)<($4,$5::uuid)`
		args = append(args, boundary.CreatedAt, boundary.ID)
	}
	rows, err := querier(ctx, r.pool).Query(ctx, sql+` ORDER BY r.created_at DESC,r.id DESC LIMIT $3`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []agent.Record{}
	for rows.Next() {
		item, err := scanAgentRun(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// Cleanup 由外层事务包围整个固定有界批次；候选读取不锁回合，逐项保持账户→会话→回合顺序。
func (r *AgentRunRepository) Cleanup(ctx context.Context, limit int) (int, error) {
	if err := requireAgentTransaction(ctx); err != nil {
		return 0, err
	}
	if limit < 1 || limit > 100 {
		return 0, run.ErrInvalidInput
	}
	rows, err := querier(ctx, r.pool).Query(ctx, `SELECT user_id::text,conversation_id::text,id::text FROM velis.agent_runs
WHERE finished_at<=clock_timestamp()-interval '24 hours' AND NOT events_expired ORDER BY user_id,conversation_id,id LIMIT $1`, limit)
	if err != nil {
		return 0, err
	}
	scopes := []agent.Scope{}
	for rows.Next() {
		var scope agent.Scope
		if err = rows.Scan(&scope.UserID, &scope.ConversationID, &scope.RunID); err != nil {
			rows.Close()
			return 0, err
		}
		scopes = append(scopes, scope)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	cleaned := 0
	for _, scope := range scopes {
		if _, err = r.LockUser(ctx, scope.UserID); err != nil {
			return 0, err
		}
		if _, err = r.LockConversation(ctx, scope.UserID, scope.ConversationID); errors.Is(err, conversation.ErrNotFound) {
			continue
		} else if err != nil {
			return 0, err
		}
		record, err := r.LockRun(ctx, scope)
		if errors.Is(err, run.ErrNotFound) {
			continue
		}
		if err != nil {
			return 0, err
		}
		var expired bool
		if err = querier(ctx, r.pool).QueryRow(ctx, `SELECT finished_at<=clock_timestamp()-interval '24 hours' AND NOT events_expired FROM velis.agent_runs WHERE id=$1`, scope.RunID).Scan(&expired); err != nil {
			return 0, err
		}
		if !expired {
			continue
		}
		if _, err = querier(ctx, r.pool).Exec(ctx, `DELETE FROM velis.agent_run_events WHERE run_id=$1`, scope.RunID); err != nil {
			return 0, err
		}
		if record.Run.State != run.Succeeded {
			if _, err = querier(ctx, r.pool).Exec(ctx, `DELETE FROM velis.agent_run_blocks WHERE run_id=$1`, scope.RunID); err != nil {
				return 0, err
			}
			if _, err = querier(ctx, r.pool).Exec(ctx, `DELETE FROM velis.agent_run_evidence WHERE run_id=$1`, scope.RunID); err != nil {
				return 0, err
			}
		}
		if _, err = querier(ctx, r.pool).Exec(ctx, `UPDATE velis.agent_runs SET events_expired=true WHERE id=$1`, scope.RunID); err != nil {
			return 0, err
		}
		cleaned++
	}
	return cleaned, nil
}
