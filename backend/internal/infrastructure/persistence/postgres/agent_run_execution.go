package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	agent "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentrun"
	conversation "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentconversation"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentevidence"
	run "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
	"github.com/jackc/pgx/v5"
)

const maxRunEvents = 128
const maxRunEventBytes = 512 * 1024
const terminalEventReserve = 4096

func (r *AgentRunRepository) Candidates(ctx context.Context, limit int) ([]agent.Candidate, error) {
	if limit < 1 || limit > 100 {
		return nil, run.ErrInvalidInput
	}
	rows, err := querier(ctx, r.pool).Query(ctx, `SELECT user_id::text,conversation_id::text,id::text FROM velis.agent_runs WHERE status='accepted' AND deadline>clock_timestamp() ORDER BY created_at,id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []agent.Candidate{}
	for rows.Next() {
		var c agent.Candidate
		if err := rows.Scan(&c.UserID, &c.ConversationID, &c.RunID); err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

func (r *AgentRunRepository) lockExecution(ctx context.Context, scope agent.Scope) (conversation.Conversation, agent.Record, time.Time, error) {
	if err := requireAgentTransaction(ctx); err != nil {
		return conversation.Conversation{}, agent.Record{}, time.Time{}, err
	}
	if _, err := r.LockUser(ctx, scope.UserID); err != nil {
		return conversation.Conversation{}, agent.Record{}, time.Time{}, err
	}
	c, err := r.LockConversation(ctx, scope.UserID, scope.ConversationID)
	if err != nil {
		return c, agent.Record{}, time.Time{}, err
	}
	record, err := r.LockRun(ctx, scope)
	if err != nil {
		return c, record, time.Time{}, err
	}
	now, err := r.DatabaseTime(ctx)
	return c, record, now, err
}
func (r *AgentRunRepository) owned(ctx context.Context, e agent.Execution) (conversation.Conversation, agent.Record, time.Time, error) {
	c, record, now, err := r.lockExecution(ctx, e.Scope)
	if err != nil {
		return c, record, now, err
	}
	if record.Run.SessionID != e.SessionID {
		return c, record, now, run.ErrExecutionLost
	}
	if err := r.CheckSession(ctx, e.UserID, e.SessionID); err != nil {
		return c, record, now, err
	}
	err = record.Run.CheckOwner(e.OwnerToken, now)
	return c, record, now, err
}
func (r *AgentRunRepository) Claim(ctx context.Context, scope agent.Scope, owner string) (run.Run, error) {
	_, record, now, err := r.lockExecution(ctx, scope)
	if err != nil {
		return run.Run{}, err
	}
	v := record.Run
	if err := r.CheckSession(ctx, v.UserID, v.SessionID); err != nil {
		return run.Run{}, err
	}
	if err := v.Claim(owner, now); err != nil {
		return run.Run{}, err
	}
	tag, err := querier(ctx, r.pool).Exec(ctx, `UPDATE velis.agent_runs SET status='running',owner_token=$4,lease_until=$5 WHERE user_id=$1 AND conversation_id=$2 AND id=$3 AND status='accepted' AND deadline>clock_timestamp()`, scope.UserID, scope.ConversationID, scope.RunID, owner, v.LeaseUntil)
	if err != nil {
		return run.Run{}, err
	}
	if tag.RowsAffected() != 1 {
		return run.Run{}, run.ErrExecutionLost
	}
	if err := r.appendEvents(ctx, &record, []agent.Event{{Kind: agent.RunStatus, State: run.Running}}, false); err != nil {
		return run.Run{}, err
	}
	return v, nil
}
func (r *AgentRunRepository) ReadExecution(ctx context.Context, scope agent.Scope) (run.Run, time.Time, error) {
	record, err := r.find(ctx, scope, false)
	if err != nil {
		return run.Run{}, time.Time{}, err
	}
	now, err := r.DatabaseTime(ctx)
	return record.Run, now, err
}
func (r *AgentRunRepository) Renew(ctx context.Context, e agent.Execution) error {
	_, record, now, err := r.owned(ctx, e)
	if err != nil {
		return err
	}
	v := record.Run
	if err := v.Renew(e.OwnerToken, now); err != nil {
		return err
	}
	tag, err := querier(ctx, r.pool).Exec(ctx, `UPDATE velis.agent_runs SET lease_until=$3 WHERE id=$1 AND owner_token=$2 AND status='running' AND lease_until>clock_timestamp() AND deadline>clock_timestamp()`, e.RunID, e.OwnerToken, v.LeaseUntil)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return run.ErrExecutionLost
	}
	return nil
}

func (r *AgentRunRepository) SaveUsage(ctx context.Context, e agent.Execution, usage run.Usage) error {
	if _, _, _, err := r.owned(ctx, e); err != nil {
		return err
	}
	raw, err := json.Marshal(usage)
	if err != nil {
		return err
	}
	tag, err := querier(ctx, r.pool).Exec(ctx, `UPDATE velis.agent_runs SET usage=$3 WHERE id=$1 AND owner_token=$2 AND status='running' AND lease_until>clock_timestamp() AND deadline>clock_timestamp()`, e.RunID, e.OwnerToken, raw)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return run.ErrExecutionLost
	}
	return nil
}
func (r *AgentRunRepository) SaveExecutionMetadata(ctx context.Context, e agent.Execution, metadata agent.ExecutionMetadata) error {
	if _, _, _, err := r.owned(ctx, e); err != nil {
		return err
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	if _, err := querier(ctx, r.pool).Exec(ctx, `UPDATE velis.agent_runs SET execution_metadata=execution_metadata||$2::jsonb WHERE id=$1`, e.RunID, raw); err != nil {
		return err
	}
	return r.ensureOwner(ctx, e)
}

// appendEvents 只在回合锁之后执行。JSONB 的实际序列化字节参与累计预算，终态预留不可挪用。
func (r *AgentRunRepository) appendEvents(ctx context.Context, record *agent.Record, events []agent.Event, terminal bool) error {
	for _, e := range events {
		countLimit, byteLimit := maxRunEvents-1, maxRunEventBytes-terminalEventReserve
		if terminal {
			countLimit = maxRunEvents
			byteLimit = maxRunEventBytes
		}
		if record.LastEventSequence >= int64(countLimit) || record.EventBytes >= byteLimit {
			return run.ErrBudget
		}
		e.Version = 1
		e.RunID = record.Run.ID
		e.Sequence = record.LastEventSequence + 1
		if !e.Kind.Valid() {
			return run.ErrInvalidInput
		}
		raw, err := json.Marshal(e)
		if err != nil {
			return err
		}
		allowed := byteLimit - record.EventBytes
		if terminal {
			allowed = min(allowed, terminalEventReserve)
		}
		var size int
		err = querier(ctx, r.pool).QueryRow(ctx, `INSERT INTO velis.agent_run_events(run_id,sequence,version,kind,payload,created_at)
SELECT $1,$2,1,$3,$4::jsonb,clock_timestamp() WHERE octet_length($4::jsonb::text)<=$5 RETURNING payload_bytes`, e.RunID, e.Sequence, e.Kind, raw, allowed).Scan(&size)
		if errors.Is(err, pgx.ErrNoRows) {
			return run.ErrBudget
		}
		if err != nil {
			return err
		}
		record.LastEventSequence++
		record.EventBytes += size
	}
	_, err := querier(ctx, r.pool).Exec(ctx, `UPDATE velis.agent_runs SET event_count=$2,event_bytes=$3 WHERE id=$1`, record.Run.ID, record.LastEventSequence, record.EventBytes)
	return err
}

func (r *AgentRunRepository) AppendToolStatus(ctx context.Context, e agent.Execution, name, status string, code run.Code) error {
	if name != "search_articles" && name != "recommend_articles" && name != "get_article" {
		return &run.Error{Code: run.CodeToolRejected}
	}
	if status != "started" && status != "succeeded" && status != "failed" {
		return run.ErrInvalidInput
	}
	if code != "" && !code.Valid() {
		return run.ErrInvalidInput
	}
	_, record, _, err := r.owned(ctx, e)
	if err != nil {
		return err
	}
	if err := r.appendEvents(ctx, &record, []agent.Event{{Kind: agent.ToolStatus, ToolName: name, ToolState: status, ErrorCode: code}}, false); err != nil {
		return err
	}
	return r.ensureOwner(ctx, e)
}

func (r *AgentRunRepository) ensureOwner(ctx context.Context, e agent.Execution) error {
	var valid bool
	if err := querier(ctx, r.pool).QueryRow(ctx, `SELECT status='running' AND owner_token=$2 AND lease_until>clock_timestamp() AND deadline>clock_timestamp() FROM velis.agent_runs WHERE id=$1`, e.RunID, e.OwnerToken).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return run.ErrExecutionLost
	}
	return nil
}

func fixedConstraints(explicit, candidate run.Constraints) bool {
	return (explicit.Keyword == "" || explicit.Keyword == candidate.Keyword) && (explicit.Topic == "" || explicit.Topic == candidate.Topic) && (explicit.SourceID == 0 || explicit.SourceID == candidate.SourceID)
}
func (r *AgentRunRepository) lockConstraints(ctx context.Context, record agent.Record, candidate run.Constraints) error {
	if !fixedConstraints(record.Run.Input.Constraints, candidate) {
		return agentevidence.ErrCitation
	}
	var existing []byte
	if err := querier(ctx, r.pool).QueryRow(ctx, `SELECT execution_metadata->'locked_constraints' FROM velis.agent_runs WHERE id=$1`, record.Run.ID).Scan(&existing); err != nil {
		return err
	}
	if len(existing) > 0 {
		var locked run.Constraints
		if json.Unmarshal(existing, &locked) != nil || locked != candidate {
			return agentevidence.ErrCitation
		}
		return nil
	}
	raw, _ := json.Marshal(candidate)
	_, err := querier(ctx, r.pool).Exec(ctx, `UPDATE velis.agent_runs SET execution_metadata=jsonb_set(execution_metadata,'{locked_constraints}',$2::jsonb) WHERE id=$1`, record.Run.ID, raw)
	return err
}

func (r *AgentRunRepository) AppendBlock(ctx context.Context, e agent.Execution, commit agent.BlockCommit) error {
	_, record, _, err := r.owned(ctx, e)
	if err != nil {
		return err
	}
	if err := r.lockConstraints(ctx, record, commit.Constraints); err != nil {
		return err
	}
	set, _ := agentevidence.NewSet(e.RunID)
	for _, item := range commit.Evidence {
		if err := set.Add(item); err != nil {
			return err
		}
	}
	if err := set.ValidateBlock(commit.Block, commit.RequireBody); err != nil {
		return err
	}
	trusted := map[string]agentevidence.Card{}
	for _, ref := range commit.References {
		for _, card := range commit.Cards {
			if card.Number == ref.Number {
				trusted[ref.EvidenceID] = card
			}
		}
	}
	answer := agentevidence.Answer{Version: 1, Kind: agentevidence.AnswerArticle, Blocks: []agentevidence.Block{commit.Block}, References: commit.References}
	validated, err := agent.ValidateEvidence(ctx, NewAgentEvidenceFacts(r.pool), answer, commit.Constraints, trusted)
	if err != nil {
		return err
	}
	q := querier(ctx, r.pool)
	existing := map[string]bool{}
	existingNumbers := map[int]bool{}
	rows, err := q.Query(ctx, `SELECT evidence_id::text,reference_number FROM velis.agent_run_evidence WHERE run_id=$1`, e.RunID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		var number int
		if err := rows.Scan(&id, &number); err != nil {
			rows.Close()
			return err
		}
		existing[id] = true
		existingNumbers[number] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	newRefs := []agentevidence.Reference{}
	newCards := []agentevidence.Card{}
	newNumbers := map[int]bool{}
	quotes, _ := json.Marshal(commit.Block.Quotes)
	if string(quotes) == "null" {
		quotes = []byte("[]")
	}
	for _, item := range commit.Evidence {
		var ref agentevidence.Reference
		found := false
		for _, candidate := range validated.References {
			if candidate.EvidenceID == item.ID {
				ref = candidate
				found = true
				break
			}
		}
		if !found || ref.ArticleID != item.ArticleID || ref.RevisionID != item.RevisionID || ref.ContentKind != item.ContentKind || ref.Truncated != item.Truncated {
			return agentevidence.ErrCitation
		}
		card := validated.Cards[ref.Number-1]
		encoded, _ := json.Marshal(card)
		tag, err := q.Exec(ctx, `INSERT INTO velis.agent_run_evidence(run_id,evidence_id,version,kind,article_id,revision_id,obtained_at,content_kind,truncated,content,reference_number,card,quote_checks)
VALUES($1,$2,1,'article',$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT(run_id,evidence_id) DO UPDATE SET quote_checks=velis.agent_run_evidence.quote_checks||EXCLUDED.quote_checks
WHERE velis.agent_run_evidence.article_id=EXCLUDED.article_id AND velis.agent_run_evidence.revision_id=EXCLUDED.revision_id AND velis.agent_run_evidence.content_kind=EXCLUDED.content_kind AND velis.agent_run_evidence.content=EXCLUDED.content`, e.RunID, item.ID, item.ArticleID, item.RevisionID, item.ObtainedAt, item.ContentKind, item.Truncated, item.Content, ref.Number, encoded, quotes)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return agentevidence.ErrCitation
		}
		if !existing[item.ID] {
			newRefs = append(newRefs, ref)
			if !newNumbers[ref.Number] && !existingNumbers[ref.Number] {
				newNumbers[ref.Number] = true
				newCards = append(newCards, card)
			}
		}
	}
	maxChars := commit.MaxAnswerChars
	if maxChars == 0 {
		maxChars = 8000
	}
	if maxChars < 1 || maxChars > 16000 {
		return run.ErrInvalidInput
	}
	var chars int
	if err := q.QueryRow(ctx, `SELECT COALESCE(sum(char_length(text)),0) FROM velis.agent_run_blocks WHERE run_id=$1`, e.RunID).Scan(&chars); err != nil {
		return err
	}
	if len([]rune(commit.Block.Text)) > maxChars-chars {
		return agentevidence.ErrOutput
	}
	_, err = q.Exec(ctx, `INSERT INTO velis.agent_run_blocks(run_id,block_id,position,text,quotes) VALUES($1,$2,(SELECT count(*)+1 FROM velis.agent_run_blocks WHERE run_id=$1),$3,$4)`, e.RunID, commit.Block.ID, commit.Block.Text, quotes)
	if err != nil {
		return err
	}
	for n, id := range commit.Block.EvidenceIDs {
		if _, err := q.Exec(ctx, `INSERT INTO velis.agent_run_block_evidence(run_id,block_id,evidence_id,position) VALUES($1,$2,$3,$4)`, e.RunID, commit.Block.ID, id, n+1); err != nil {
			return err
		}
	}
	events := []agent.Event{}
	if len(newRefs) > 0 {
		events = append(events, agent.Event{Kind: agent.AnswerSources, References: newRefs, Cards: newCards, Partial: true})
	}
	// 独立块事件保存完整引用身份，任意位置续读都可当前投影。
	events = append(events, agent.Event{Kind: agent.AnswerBlock, Block: &commit.Block, References: validated.References, Partial: true})
	if err := r.appendEvents(ctx, &record, events, false); err != nil {
		return err
	}
	tag, err := q.Exec(ctx, `UPDATE velis.agent_runs SET usage=usage WHERE id=$1 AND status='running' AND owner_token=$2 AND lease_until>clock_timestamp() AND deadline>clock_timestamp()`, e.RunID, e.OwnerToken)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return run.ErrExecutionLost
	}
	return nil
}

func (r *AgentRunRepository) Terminate(ctx context.Context, scope agent.Scope, state run.State, code run.Code) (agent.Record, error) {
	_, record, now, err := r.lockExecution(ctx, scope)
	if err != nil {
		return record, err
	}
	if record.Run.State.Terminal() {
		return record, nil
	}
	if err := record.Run.Finish(state, code, "", now); err != nil {
		return record, err
	}
	_, err = querier(ctx, r.pool).Exec(ctx, `UPDATE velis.agent_runs SET status=$2,error_code=$3,finished_at=$4,assistant_reserved=false,owner_token=NULL,lease_until=NULL WHERE id=$1 AND status IN ('accepted','running')`, scope.RunID, state, code, now)
	if err != nil {
		return record, err
	}
	if err := r.appendEvents(ctx, &record, []agent.Event{{Kind: agent.RunTerminal, State: state, ErrorCode: code, Partial: record.Partial}}, true); err != nil {
		return record, err
	}
	return record, nil
}

func (r *AgentRunRepository) Maintain(ctx context.Context, limit int) (int, error) {
	if err := requireAgentTransaction(ctx); err != nil {
		return 0, err
	}
	if limit < 1 || limit > 100 {
		return 0, run.ErrInvalidInput
	}
	rows, err := querier(ctx, r.pool).Query(ctx, `SELECT r.user_id::text,r.conversation_id::text,r.id::text FROM velis.agent_runs r WHERE r.status IN ('accepted','running')
AND (r.deadline<=clock_timestamp() OR (r.status='running' AND r.lease_until<=clock_timestamp()) OR NOT EXISTS(SELECT 1 FROM velis.users u JOIN velis.auth_sessions s ON s.user_id=u.id WHERE u.id=r.user_id AND s.id=r.auth_session_id AND u.status='active' AND s.revoked_at IS NULL AND s.expires_at>clock_timestamp())) ORDER BY r.user_id,r.conversation_id,r.id LIMIT $1`, limit)
	if err != nil {
		return 0, err
	}
	scopes := []agent.Scope{}
	for rows.Next() {
		var scope agent.Scope
		if err := rows.Scan(&scope.UserID, &scope.ConversationID, &scope.RunID); err != nil {
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
	count := 0
	for _, scope := range scopes {
		_, record, now, err := r.lockExecution(ctx, scope)
		if errors.Is(err, conversation.ErrNotFound) || errors.Is(err, run.ErrNotFound) {
			continue
		}
		if err != nil {
			return count, err
		}
		if !record.Run.State.Active() {
			continue
		}
		state, code := run.State(""), run.Code("")
		if record.Run.Expired(now) {
			state, code = run.TimedOut, run.CodeTimeout
		} else if record.Run.State == run.Running && !now.Before(record.Run.LeaseUntil) {
			state, code = run.Failed, run.CodeExecutorLost
		} else if err := r.CheckSession(ctx, scope.UserID, record.Run.SessionID); err != nil {
			if agent.SafeCode(err) != run.CodeAuthInvalid {
				return count, err
			}
			state, code = run.Canceled, run.CodeAuthInvalid
		}
		if state != "" {
			if _, err := r.Terminate(ctx, scope, state, code); err != nil {
				return count, err
			}
			count++
		}
	}
	return count, nil
}

// TerminateExecution 是执行者的有界收尾；不会覆盖另一 owner 或已经提交的终态。
// 过期时使用维护分类，只允许保存计数和安全终态，不能保存迟到正文。
func (r *AgentRunRepository) TerminateExecution(ctx context.Context, e agent.Execution, state run.State, code run.Code, usage run.Usage) (agent.Record, error) {
	_, record, now, err := r.lockExecution(ctx, e.Scope)
	if err != nil {
		return record, err
	}
	if record.Run.State.Terminal() {
		return record, nil
	}
	if record.Run.State != run.Running || record.Run.OwnerToken != e.OwnerToken || record.Run.SessionID != e.SessionID {
		return record, run.ErrExecutionLost
	}
	if record.Run.Expired(now) {
		state, code = run.TimedOut, run.CodeTimeout
	} else if !now.Before(record.Run.LeaseUntil) {
		state, code = run.Failed, run.CodeExecutorLost
	}
	raw, err := json.Marshal(usage)
	if err != nil {
		return record, err
	}
	if _, err := querier(ctx, r.pool).Exec(ctx, `UPDATE velis.agent_runs SET usage=$2 WHERE id=$1`, e.RunID, raw); err != nil {
		return record, err
	}
	return r.Terminate(ctx, e.Scope, state, code)
}
