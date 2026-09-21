package integration

import (
	"context"
	"testing"
	"time"

	idempotencyApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/idempotency"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

func TestCleanupRepositoryDeletesOnlyExpiredIdempotencyInBatches(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx := context.Background()
	now := fixedNow()
	const actorID = "54000000-0000-0000-0000-000000000001"
	if _, err := env.pool.Exec(ctx, `INSERT INTO velis.users
(id,username,nickname,password_hash,role,status,created_at,updated_at)
VALUES ($1,'cleanup_actor','清理用户','hash','user','active',$2,$2)`, actorID, now); err != nil {
		t.Fatal(err)
	}
	digest, err := idempotencyApp.Digest(map[string]bool{"ok": true})
	if err != nil {
		t.Fatal(err)
	}
	for index, expiry := range []time.Time{now.Add(-3 * time.Hour), now.Add(-2 * time.Hour), now.Add(-time.Hour), now.Add(time.Hour)} {
		key := []string{
			"54000000-0000-0000-0000-000000000011", "54000000-0000-0000-0000-000000000012",
			"54000000-0000-0000-0000-000000000013", "54000000-0000-0000-0000-000000000014",
		}[index]
		if _, err := env.pool.Exec(ctx, `INSERT INTO velis.idempotency_operations
(actor_user_id,operation,idempotency_key,request_digest,status,result_version,result_payload,created_at,completed_at,expires_at)
VALUES ($1,'cleanup.test',$2,$3,'succeeded',1,'{}',$4,$4,$5)`, actorID, key, digest[:], now.Add(-25*time.Hour), expiry); err != nil {
			t.Fatal(err)
		}
	}
	repository := postgres.NewCleanupRepository(env.pool)
	if removed, err := repository.DeleteExpiredIdempotency(ctx, now, 2); err != nil || removed != 2 {
		t.Fatalf("首批 removed=%d err=%v", removed, err)
	}
	if removed, err := repository.DeleteExpiredIdempotency(ctx, now, 2); err != nil || removed != 1 {
		t.Fatalf("第二批 removed=%d err=%v", removed, err)
	}
	if removed, err := repository.DeleteExpiredIdempotency(ctx, now, 2); err != nil || removed != 0 {
		t.Fatalf("重复执行 removed=%d err=%v", removed, err)
	}
	var remaining, retained int
	if err := env.pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE expires_at>$2)
FROM velis.idempotency_operations WHERE actor_user_id=$1`, actorID, now).Scan(&remaining, &retained); err != nil || remaining != 1 || retained != 1 {
		t.Fatalf("保留期内结果被删除 remaining=%d retained=%d err=%v", remaining, retained, err)
	}
}
