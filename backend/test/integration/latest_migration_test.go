package integration

import (
	"context"
	"strings"
	"testing"
)

func TestLatestEffectiveTimeIndexMigrationCanRollbackAndRestore(t *testing.T) {
	env := newTestEnv(t)
	runner := newMigrationRunner(t, env.databaseURL)
	isRolledBack := false
	defer func() {
		if isRolledBack {
			if err := runner.Steps(1); err != nil {
				t.Errorf("清理时恢复 latest 有效时间索引: %v", err)
			}
		}
	}()

	assertLatestIndexDefinition(t, env, "COALESCE(source_published_at, published_at)")
	if err := runner.Steps(-1); err != nil {
		t.Fatalf("回滚 latest 有效时间索引: %v", err)
	}
	isRolledBack = true
	assertLatestIndexDefinition(t, env, "published_at DESC")
	assertLatestIndexDefinitionDoesNotContain(t, env, "COALESCE(source_published_at, published_at)")

	if err := runner.Steps(1); err != nil {
		t.Fatalf("重新应用 latest 有效时间索引: %v", err)
	}
	isRolledBack = false
	assertLatestIndexDefinition(t, env, "COALESCE(source_published_at, published_at)")
}

func assertLatestIndexDefinition(t *testing.T, env *testEnv, fragment string) {
	t.Helper()
	definition := latestIndexDefinition(t, env)
	if !strings.Contains(definition, fragment) {
		t.Fatalf("articles_latest_idx 定义不符合预期: %s", definition)
	}
	var partial bool
	if err := env.pool.QueryRow(context.Background(), `SELECT indpred IS NOT NULL
FROM pg_index WHERE indexrelid='velis.articles_latest_idx'::regclass`).Scan(&partial); err != nil || !partial {
		t.Fatalf("articles_latest_idx 必须保持为部分索引: partial=%t err=%v", partial, err)
	}
}

func assertLatestIndexDefinitionDoesNotContain(t *testing.T, env *testEnv, fragment string) {
	t.Helper()
	if definition := latestIndexDefinition(t, env); strings.Contains(definition, fragment) {
		t.Fatalf("articles_latest_idx 仍包含 %q: %s", fragment, definition)
	}
}

func latestIndexDefinition(t *testing.T, env *testEnv) string {
	t.Helper()
	var definition string
	if err := env.pool.QueryRow(context.Background(), `SELECT pg_get_indexdef('velis.articles_latest_idx'::regclass)`).Scan(&definition); err != nil {
		t.Fatal(err)
	}
	return definition
}
