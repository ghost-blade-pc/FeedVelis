package postgres

import (
	"context"
	"encoding/json"

	agent "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentrun"
	conversation "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentconversation"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentevidence"
	run "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
)

func (r *AgentRunRepository) workAnswer(ctx context.Context, runID string) (agentevidence.Answer, []agentevidence.Evidence, error) {
	answer := agentevidence.Answer{Version: 1, Kind: agentevidence.AnswerArticle, Blocks: []agentevidence.Block{}, References: []agentevidence.Reference{}, Cards: []agentevidence.Card{}}
	q := querier(ctx, r.pool)
	rows, err := q.Query(ctx, `SELECT b.block_id,b.text,b.quotes,COALESCE(array_agg(be.evidence_id::text ORDER BY be.position) FILTER(WHERE be.evidence_id IS NOT NULL),'{}'::text[])
FROM velis.agent_run_blocks b LEFT JOIN velis.agent_run_block_evidence be ON be.run_id=b.run_id AND be.block_id=b.block_id WHERE b.run_id=$1 GROUP BY b.run_id,b.block_id,b.position ORDER BY b.position`, runID)
	if err != nil {
		return answer, nil, err
	}
	for rows.Next() {
		var b agentevidence.Block
		var raw []byte
		if err := rows.Scan(&b.ID, &b.Text, &raw, &b.EvidenceIDs); err != nil {
			rows.Close()
			return answer, nil, err
		}
		if json.Unmarshal(raw, &b.Quotes) != nil {
			rows.Close()
			return answer, nil, agentevidence.ErrOutput
		}
		answer.Blocks = append(answer.Blocks, b)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return answer, nil, err
	}
	rows, err = q.Query(ctx, `SELECT evidence_id::text,version,kind,article_id,revision_id,obtained_at,content_kind,truncated,COALESCE(content,''),reference_number,card FROM velis.agent_run_evidence WHERE run_id=$1`, runID)
	if err != nil {
		return answer, nil, err
	}
	evidence := []agentevidence.Evidence{}
	references := map[string]agentevidence.Reference{}
	cards := map[int]agentevidence.Card{}
	for rows.Next() {
		e := agentevidence.Evidence{RunID: runID}
		var number int
		var raw []byte
		if err := rows.Scan(&e.ID, &e.Version, &e.Kind, &e.ArticleID, &e.RevisionID, &e.ObtainedAt, &e.ContentKind, &e.Truncated, &e.Content, &number, &raw); err != nil {
			rows.Close()
			return answer, nil, err
		}
		var card agentevidence.Card
		if json.Unmarshal(raw, &card) != nil {
			rows.Close()
			return answer, nil, agentevidence.ErrOutput
		}
		card.Number = number
		card.ArticleID = e.ArticleID
		card.RevisionID = e.RevisionID
		if old, ok := cards[number]; ok {
			card.Truncated = card.Truncated || old.Truncated
		}
		cards[number] = card
		references[e.ID] = agentevidence.Reference{EvidenceID: e.ID, Number: number, Kind: e.Kind, ArticleID: e.ArticleID, RevisionID: e.RevisionID, ContentKind: e.ContentKind, Truncated: e.Truncated}
		evidence = append(evidence, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return answer, nil, err
	}
	seen := map[string]bool{}
	numbered := map[int]bool{}
	for _, b := range answer.Blocks {
		for _, id := range b.EvidenceIDs {
			ref, ok := references[id]
			if !ok {
				return answer, nil, agentevidence.ErrCitation
			}
			if !seen[id] {
				answer.References = append(answer.References, ref)
				seen[id] = true
			}
			if !numbered[ref.Number] {
				answer.Cards = append(answer.Cards, cards[ref.Number])
				numbered[ref.Number] = true
			}
		}
	}
	return answer, evidence, nil
}

func (r *AgentRunRepository) Complete(ctx context.Context, e agent.Execution, commit agent.SuccessCommit) error {
	c, record, now, err := r.owned(ctx, e)
	if err != nil {
		return err
	}
	maxMessages := commit.MaxMessages
	if maxMessages == 0 {
		maxMessages = 200
	}
	reservations, err := r.ConversationActive(ctx, c.ID)
	if err != nil {
		return err
	}
	if err := run.CheckAssistantCommit(c.MessageCount, reservations, maxMessages); err != nil {
		return err
	}
	if err := r.lockConstraints(ctx, record, commit.Constraints); err != nil {
		return err
	}
	a := commit.Answer
	if a.Version != 1 || !a.Kind.Valid() || a.Redacted {
		return agentevidence.ErrOutput
	}
	maxChars := commit.MaxAnswerChars
	if maxChars == 0 {
		maxChars = 8000
	}
	if a.Kind == agentevidence.AnswerArticle {
		work, evidence, err := r.workAnswer(ctx, e.RunID)
		if err != nil {
			return err
		}
		set, _ := agentevidence.NewSet(e.RunID)
		for _, item := range evidence {
			if err := set.Add(item); err != nil {
				return err
			}
		}
		assembler, _ := agentevidence.NewAssembler(set, maxChars)
		for _, b := range work.Blocks {
			if err := assembler.Add(b, false); err != nil {
				return err
			}
		}
		if err := assembler.Finish(); err != nil {
			return err
		}
		left, _ := json.Marshal(a.Blocks)
		right, _ := json.Marshal(work.Blocks)
		if string(left) != string(right) {
			return agentevidence.ErrOutput
		}
		left, _ = json.Marshal(a.References)
		right, _ = json.Marshal(work.References)
		if string(left) != string(right) {
			return agentevidence.ErrCitation
		}
		trusted := map[string]agentevidence.Card{}
		for _, ref := range work.References {
			for _, card := range work.Cards {
				if ref.Number == card.Number {
					trusted[ref.EvidenceID] = card
				}
			}
		}
		a, err = agent.ValidateEvidence(ctx, NewAgentEvidenceFacts(r.pool), a, commit.Constraints, trusted)
		if err != nil {
			return err
		}
	} else {
		if len(a.Blocks) != 1 || len(a.References) != 0 || len(a.Cards) != 0 || len(a.Blocks[0].EvidenceIDs) != 0 || len(a.Blocks[0].Quotes) != 0 || a.Blocks[0].Redacted {
			return agentevidence.ErrOutput
		}
		work, _, err := r.workAnswer(ctx, e.RunID)
		if err != nil {
			return err
		}
		if len(work.Blocks) != 0 {
			return agentevidence.ErrOutput
		}
	}
	m := commit.Assistant
	if m.ConversationID != c.ID || m.Sequence != c.NextSequence || m.Role != conversation.RoleAssistant || m.Content != agent.AnswerText(a) {
		return agentevidence.ErrOutput
	}
	if _, err := conversation.NormalizeContent(m.Content, maxChars+2*agentevidence.MaxBlocks); err != nil {
		return agentevidence.ErrOutput
	}
	if err := record.Run.Complete(e.OwnerToken, m.ID, now); err != nil {
		return err
	}
	if err := r.AppendMessage(ctx, e.UserID, m); err != nil {
		return err
	}
	encoded, err := json.Marshal(a)
	if err != nil {
		return err
	}
	usage, err := json.Marshal(commit.Usage)
	if err != nil {
		return err
	}
	tag, err := querier(ctx, r.pool).Exec(ctx, `UPDATE velis.agent_runs SET status='succeeded',assistant_message_id=$3,answer=$4,usage=$5,finished_at=clock_timestamp(),assistant_reserved=false,owner_token=NULL,lease_until=NULL
WHERE id=$1 AND status='running' AND owner_token=$2 AND lease_until>clock_timestamp() AND deadline>clock_timestamp()`, e.RunID, e.OwnerToken, m.ID, encoded, usage)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return run.ErrExecutionLost
	}
	if _, err := querier(ctx, r.pool).Exec(ctx, `UPDATE velis.agent_run_evidence SET content=NULL WHERE run_id=$1`, e.RunID); err != nil {
		return err
	}
	return r.appendEvents(ctx, &record, []agent.Event{{Kind: agent.RunTerminal, State: run.Succeeded}}, true)
}

func (r *AgentRunRepository) ReadEvents(ctx context.Context, scope agent.Scope, after int64, limit int) (agent.EventBatch, error) {
	if after < 0 || limit < 1 || limit > 128 {
		return agent.EventBatch{}, run.ErrInvalidInput
	}
	record, err := r.find(ctx, scope, false)
	if err != nil {
		return agent.EventBatch{}, err
	}
	batch := agent.EventBatch{Events: []agent.Event{}, LastSequence: record.LastEventSequence, Terminal: record.Run.State.Terminal(), Expired: record.EventsExpired}
	if batch.Expired {
		return batch, nil
	}
	rows, err := querier(ctx, r.pool).Query(ctx, `SELECT payload FROM velis.agent_run_events WHERE run_id=$1 AND sequence>$2 ORDER BY sequence LIMIT $3`, scope.RunID, after, limit)
	if err != nil {
		return batch, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var event agent.Event
		if err := rows.Scan(&raw); err != nil {
			return batch, err
		}
		if json.Unmarshal(raw, &event) != nil || event.Version != 1 || event.RunID != scope.RunID || !event.Kind.Valid() {
			return batch, agentevidence.ErrOutput
		}
		batch.Events = append(batch.Events, event)
	}
	return batch, rows.Err()
}

var _ agent.Repository = (*AgentRunRepository)(nil)
var _ agent.ExecutionReader = (*AgentRunRepository)(nil)
var _ agent.EventReader = (*AgentRunRepository)(nil)
