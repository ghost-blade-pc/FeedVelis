package postgres

import (
	"context"
	"encoding/json"

	agent "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentrun"
	conversation "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentconversation"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentevidence"
)

func (r *AgentRunRepository) RecentSuccessful(ctx context.Context, userID, conversationID string, limit int) ([]agent.CompletedTurn, error) {
	if limit < 1 || limit > 6 {
		return nil, conversation.ErrInvalidInput
	}
	rows, err := querier(ctx, r.pool).Query(ctx, `SELECT r.id::text,m.id::text,m.conversation_id::text,m.sequence,m.content,m.created_at,r.answer
FROM velis.agent_runs r JOIN velis.agent_messages m ON m.id=r.user_message_id AND m.conversation_id=r.conversation_id
WHERE r.user_id=$1 AND r.conversation_id=$2 AND r.status='succeeded' ORDER BY r.created_at DESC,r.id DESC LIMIT $3`, userID, conversationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	turns := []agent.CompletedTurn{}
	for rows.Next() {
		var turn agent.CompletedTurn
		var raw []byte
		turn.Question.Role = conversation.RoleUser
		if err := rows.Scan(&turn.RunID, &turn.Question.ID, &turn.Question.ConversationID, &turn.Question.Sequence, &turn.Question.Content, &turn.Question.CreatedAt, &raw); err != nil {
			return nil, err
		}
		if json.Unmarshal(raw, &turn.Answer) != nil || turn.Answer.Version != 1 || !turn.Answer.Kind.Valid() {
			return nil, agentevidence.ErrOutput
		}
		turns = append(turns, turn)
	}
	return turns, rows.Err()
}
