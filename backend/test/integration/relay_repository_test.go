package integration

import (
	"context"
	"testing"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

func TestRelayLeaseCompetitionRecoveryAndFencing(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	// 复用迁移测试的最小合法信封，Relay 只依赖 Outbox。
	_, err := env.pool.Exec(context.Background(), `INSERT INTO velis.outbox_events(event_id,event_type,aggregate_type,aggregate_id,aggregate_version,envelope,occurred_at,next_attempt_at)
VALUES('01993a42-8e80-7a11-87dd-1dd92b6fb0c1','article.deleted.v1','article','42',1,'{"event_id":"01993a42-8e80-7a11-87dd-1dd92b6fb0c1","event_type":"article.deleted.v1","occurred_at":"2026-09-23T00:00:00Z","producer":"velis.content","trace_id":"0123456789abcdef0123456789abcdef","aggregate":{"type":"article","id":"42","version":1},"payload":{"article_id":42,"revision_id":1,"status":"deleted"}}',now(),now())`)
	if err != nil {
		t.Fatal(err)
	}
	repo := postgres.NewRelayRepository(env.pool)
	first, err := repo.Claim(context.Background(), "worker-a", 1, 20*time.Millisecond)
	if err != nil || len(first) != 1 {
		t.Fatalf("首次认领=%d err=%v", len(first), err)
	}
	second, err := repo.Claim(context.Background(), "worker-b", 1, time.Second)
	if err != nil || len(second) != 0 {
		t.Fatalf("租约内重复认领=%d err=%v", len(second), err)
	}
	time.Sleep(30 * time.Millisecond)
	second, err = repo.Claim(context.Background(), "worker-b", 1, time.Second)
	if err != nil || len(second) != 1 {
		t.Fatalf("过期恢复=%d err=%v", len(second), err)
	}
	if ok, err := repo.Confirm(context.Background(), first[0].Event.EventID, first[0].LeaseToken); err != nil || ok {
		t.Fatalf("旧 token 回写不应成功: ok=%t err=%v", ok, err)
	}
	if ok, err := repo.Confirm(context.Background(), second[0].Event.EventID, second[0].LeaseToken); err != nil || !ok {
		t.Fatalf("当前 token 回写失败: ok=%t err=%v", ok, err)
	}
}
