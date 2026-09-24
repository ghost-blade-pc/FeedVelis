package integration

import (
	"context"
	"strings"
	"testing"
)

func TestReliableAsyncMigrationUpSafeDownAndI2Preservation(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	runner := newMigrationRunner(t, env.databaseURL)
	ctx := context.Background()
	// 本测试专门验证 v6；从当前 v8 显式退回 v6。
	if err := runner.Steps(-2); err != nil {
		t.Fatalf("退回可靠异步迁移: %v", err)
	}

	for _, table := range []string{"outbox_events", "consumed_events", "async_tasks"} {
		var exists bool
		if err := env.pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, "velis."+table).Scan(&exists); err != nil || !exists {
			t.Fatalf("迁移未创建 %s: exists=%t err=%v", table, exists, err)
		}
	}

	var i2Tables int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
WHERE n.nspname='velis' AND c.relname IN ('articles','article_versions','sources')`).Scan(&i2Tables); err != nil {
		t.Fatal(err)
	}
	if i2Tables != 3 {
		t.Fatalf("I2 表缺失: %d", i2Tables)
	}

	if _, err := env.pool.Exec(ctx, `INSERT INTO velis.outbox_events
(event_id,event_type,aggregate_type,aggregate_id,aggregate_version,envelope,occurred_at)
VALUES ('01993a42-8e80-7a11-87dd-1dd92b6fb0c1','article.published.v1','article','42',7,
'{"event_id":"01993a42-8e80-7a11-87dd-1dd92b6fb0c1","event_type":"article.published.v1","aggregate":{"type":"article","id":"42","version":7}}'::jsonb,now())`); err != nil {
		t.Fatal(err)
	}
	if err := runner.Steps(-1); err == nil || !strings.Contains(err.Error(), "拒绝删除可靠异步表") {
		t.Fatalf("有数据时 down 应被拒绝，实际: %v", err)
	}
	// golang-migrate 会把失败的 down 标为 dirty；确认拒绝行为后恢复到仍已应用的 v6。
	if err := runner.Force(6); err != nil {
		t.Fatalf("恢复失败迁移版本: %v", err)
	}
	if _, err := env.pool.Exec(ctx, `DELETE FROM velis.outbox_events`); err != nil {
		t.Fatal(err)
	}
	if err := runner.Steps(-1); err != nil {
		t.Fatalf("空表 down: %v", err)
	}
	t.Cleanup(func() {
		if err := runner.Steps(3); err != nil {
			t.Errorf("恢复迁移: %v", err)
		}
	})

	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
WHERE n.nspname='velis' AND c.relname IN ('articles','article_versions','sources')`).Scan(&i2Tables); err != nil || i2Tables != 3 {
		t.Fatalf("down 损坏 I2 表: count=%d err=%v", i2Tables, err)
	}
}
