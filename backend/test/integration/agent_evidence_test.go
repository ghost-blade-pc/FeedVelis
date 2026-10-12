package integration

import (
	"encoding/json"
	"testing"

	conversationApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentconversation"
	agent "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentrun"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentevidence"
	run "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

func TestAgentEvidenceCurrentFactsAndImmutableHistory(t *testing.T) {
	env := newTestEnv(t)
	seedAgentSessions(t, env)
	seedFeedbackArticle(t, env)
	c, m := agentQuestion(t, env, 1)
	s := newRunService(env, 200, true)
	accepted, err := s.Accept(t.Context(), agentCaller, c.ID, agentKey(1), agent.Command{MessageID: m.ID})
	if err != nil {
		t.Fatal(err)
	}
	markRunSucceeded(t, env, accepted.Snapshot.ID, c.ID)
	a := agentevidence.Answer{Version: 1, Kind: agentevidence.AnswerArticle, Blocks: []agentevidence.Block{{ID: "b", Text: "正文解释", EvidenceIDs: []string{agentTestEvidence}}}, References: []agentevidence.Reference{{EvidenceID: agentTestEvidence, Number: 1, Kind: agentevidence.Article, ArticleID: 7201, RevisionID: 7211, ContentKind: agentevidence.Body}}, Cards: []agentevidence.Card{{Number: 1, ArticleID: 7201, RevisionID: 7211, Title: "测试文章", Path: "/articles/7201"}}}
	raw, _ := json.Marshal(a)
	_, err = env.pool.Exec(t.Context(), `UPDATE velis.agent_runs SET answer=$2 WHERE id=$1`, accepted.Snapshot.ID, raw)
	if err != nil {
		t.Fatal(err)
	}
	repo := postgres.NewAgentRunRepository(env.pool)
	facts := postgres.NewAgentEvidenceFacts(env.pool)
	p := agent.NewProjector(facts).WithHistorySource(repo)
	s.WithProjector(p)
	history := newAgentService(env, conversationApp.DefaultLimits()).WithHistoryProjector(p)
	items, more, err := history.History(t.Context(), agentTestUser, c.ID, 0, 1)
	if err != nil || !more || len(items) != 1 || items[0].Content != "正文解释" || items[0].RunID == nil || items[0].ContentRedacted == nil || *items[0].ContentRedacted {
		t.Fatal(items, more, err)
	}
	original := items[0]
	current, err := facts.Current(t.Context(), []agent.ArticleIdentity{{ArticleID: 7201, RevisionID: 7211}})
	if err != nil || len(current) != 1 || !current[0].Public || current[0].Card.Path != "/articles/7201" {
		t.Fatal(current, err)
	}
	trusted := map[string]agentevidence.Card{agentTestEvidence: a.Cards[0]}
	if _, err := agent.ValidateEvidence(t.Context(), facts, a, run.Constraints{}, trusted); err != nil {
		t.Fatal(err)
	}
	_, err = env.pool.Exec(t.Context(), `UPDATE velis.articles SET status='offline',offline_reason='author',offline_at=now() WHERE id=7201`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agent.ValidateEvidence(t.Context(), facts, a, run.Constraints{}, trusted); agent.SafeCode(err) != run.CodeEvidenceUnavailable {
		t.Fatal("发送或提交前未检查下架", err)
	}
	detail, err := s.Detail(t.Context(), agentCaller, c.ID, accepted.Snapshot.ID)
	if err != nil || detail.Answer == nil || !detail.Answer.Redacted || detail.Answer.Blocks[0].Text != agentevidence.UnavailableText || len(detail.Answer.Cards) != 0 || len(detail.Answer.References) != 0 {
		t.Fatal(detail, err)
	}
	items, more, err = history.History(t.Context(), agentTestUser, c.ID, 0, 1)
	if err != nil || !more || items[0].ID != original.ID || items[0].Sequence != original.Sequence || !items[0].CreatedAt.Equal(original.CreatedAt) || items[0].ContentRedacted == nil || !*items[0].ContentRedacted || items[0].Content != agentevidence.UnavailableText {
		t.Fatal(items, err)
	}
	older, _, err := history.History(t.Context(), agentTestUser, c.ID, items[0].Sequence, 1)
	if err != nil || older[0].ID != m.ID || older[0].RunID != nil || older[0].Content != m.Content {
		t.Fatal("旧消息兼容或分页边界改变", older, err)
	}
	var persisted, body string
	err = env.pool.QueryRow(t.Context(), `SELECT r.answer::text,m.content FROM velis.agent_runs r JOIN velis.agent_messages m ON m.id=r.assistant_message_id WHERE r.id=$1`, accepted.Snapshot.ID).Scan(&persisted, &body)
	var stored agentevidence.Answer
	if err != nil || json.Unmarshal([]byte(persisted), &stored) != nil || stored.Blocks[0].Text != "正文解释" || body != "成功助手不可变正文" {
		t.Fatal("改写持久化正文", err)
	}
	_, err = env.pool.Exec(t.Context(), `INSERT INTO velis.article_versions(id,article_id,revision_no,title,plain_text,excerpt,language,content_hash,sanitizer_version,created_at) OVERRIDING SYSTEM VALUE VALUES(7212,7201,2,'新修订','新正文','新摘要','zh-CN',repeat('b',64),1,now()); UPDATE velis.articles SET status='published',offline_reason=NULL,offline_at=NULL,current_revision_id=7212 WHERE id=7201`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agent.ValidateEvidence(t.Context(), facts, a, run.Constraints{}, trusted); agent.SafeCode(err) != run.CodeEvidenceStale {
		t.Fatal("旧修订通过", err)
	}
	detail, err = s.Detail(t.Context(), agentCaller, c.ID, accepted.Snapshot.ID)
	if err != nil || !detail.Answer.Redacted {
		t.Fatal(detail, err)
	}
	appended, err := history.Append(t.Context(), agentTestUser, c.ID, agentKey(2), "失败问题不应进入上下文")
	if err != nil {
		t.Fatal(err)
	}
	var nextMessage struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(appended.Snapshot, &nextMessage) != nil {
		t.Fatal("追加快照")
	}
	failed, err := s.Accept(t.Context(), agentCaller, c.ID, agentKey(2), agent.Command{MessageID: nextMessage.ID})
	if err != nil {
		t.Fatal(err)
	}
	populateRunWork(t, env, failed.Snapshot.ID)
	failAgentRun(t, env, failed.Snapshot.ID)
	turns, err := repo.RecentSuccessful(t.Context(), agentTestUser, c.ID, 6)
	if err != nil || len(turns) != 1 || turns[0].RunID != accepted.Snapshot.ID || turns[0].Question.ID != m.ID {
		t.Fatal("上下文成功历史", turns, err)
	}
	foreign, err := repo.RecentSuccessful(t.Context(), agentTestOther, c.ID, 6)
	if err != nil || len(foreign) != 0 {
		t.Fatal("上下文跨账户", foreign, err)
	}
	projected, err := p.Turns(t.Context(), turns)
	if err != nil || !projected[0].Answer.Redacted {
		t.Fatal("上下文未投影", projected, err)
	}
}
