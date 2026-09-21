package integration

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	idempotencyApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/idempotency"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

func TestIdempotencyServiceConcurrentReplayAndExpiry(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx := context.Background()
	now := fixedNow()
	const actorID = "50000000-0000-0000-0000-000000000001"
	if _, err := env.pool.Exec(ctx, `INSERT INTO velis.users
(id,username,nickname,password_hash,role,status,created_at,updated_at)
VALUES ($1,'idempotent_user','幂等用户','hash','user','active',$2,$2)`, actorID, now); err != nil {
		t.Fatal(err)
	}

	service := idempotencyApp.NewService(postgres.NewIdempotencyRepository(env.pool), postgres.NewTxManager(env.pool), 24*time.Hour)
	identity := idempotencyApp.Identity{ActorUserID: actorID, Operation: "article.create", Key: "50000000-0000-0000-0000-000000000002"}
	command := idempotencyApp.Command{Identity: identity, Payload: map[string]any{"title": "同一请求", "publish": true}, Now: now}

	var executions atomic.Int32
	type concurrentResult struct {
		outcome idempotencyApp.Outcome
		err     error
	}
	results := make(chan concurrentResult, 4)
	var group sync.WaitGroup
	for range 4 {
		group.Add(1)
		go func() {
			defer group.Done()
			outcome, err := service.Execute(ctx, command, func(context.Context) (any, string, string, error) {
				executions.Add(1)
				time.Sleep(25 * time.Millisecond)
				return map[string]any{"articleId": "article-1", "lockVersion": 1}, "article", "article-1", nil
			})
			results <- concurrentResult{outcome: outcome, err: err}
		}()
	}
	group.Wait()
	close(results)

	var fresh, replayed int
	for result := range results {
		if result.err != nil {
			t.Fatalf("并发执行失败: %v", result.err)
		}
		var payload struct {
			ArticleID   string `json:"articleId"`
			LockVersion int    `json:"lockVersion"`
		}
		if err := json.Unmarshal(result.outcome.Result, &payload); err != nil || payload.ArticleID != "article-1" || payload.LockVersion != 1 {
			t.Fatalf("重放结果 = %s, err=%v", result.outcome.Result, err)
		}
		if result.outcome.Replayed {
			replayed++
		} else {
			fresh++
		}
	}
	if executions.Load() != 1 || fresh != 1 || replayed != 3 {
		t.Fatalf("execute=%d fresh=%d replayed=%d", executions.Load(), fresh, replayed)
	}

	_, err := service.Execute(ctx, idempotencyApp.Command{Identity: identity, Payload: map[string]any{"title": "不同请求"}, Now: now.Add(time.Hour)},
		func(context.Context) (any, string, string, error) { return nil, "", "", errors.New("不应执行") })
	if !errors.Is(err, idempotencyApp.ErrKeyReused) {
		t.Fatalf("同键异请求错误 = %v", err)
	}

	expiredIdentity := idempotencyApp.Identity{ActorUserID: actorID, Operation: "article.update", Key: "50000000-0000-0000-0000-000000000003"}
	oldDigest, err := idempotencyApp.Digest(map[string]any{"title": "旧请求"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(ctx, `INSERT INTO velis.idempotency_operations
(actor_user_id,operation,idempotency_key,request_digest,status,result_version,result_payload,created_at,completed_at,expires_at)
VALUES ($1,$2,$3,$4,'succeeded',1,'{"old":true}',$5,$6,$6)`, actorID, expiredIdentity.Operation,
		expiredIdentity.Key, oldDigest[:], now.Add(-25*time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	expiredOutcome, err := service.Execute(ctx, idempotencyApp.Command{Identity: expiredIdentity,
		Payload: map[string]any{"title": "新请求"}, Now: now}, func(context.Context) (any, string, string, error) {
		return map[string]bool{"new": true}, "article", "article-2", nil
	})
	if err != nil || expiredOutcome.Replayed || string(expiredOutcome.Result) != `{"new":true}` {
		t.Fatalf("过期键复用 outcome=%+v err=%v", expiredOutcome, err)
	}
}

func TestIdempotencyServiceRollsBackFailedCommand(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx := context.Background()
	now := fixedNow()
	const actorID = "50000000-0000-0000-0000-000000000011"
	if _, err := env.pool.Exec(ctx, `INSERT INTO velis.users
(id,username,nickname,password_hash,role,status,created_at,updated_at)
VALUES ($1,'rollback_user','回滚用户','hash','user','active',$2,$2)`, actorID, now); err != nil {
		t.Fatal(err)
	}
	service := idempotencyApp.NewService(postgres.NewIdempotencyRepository(env.pool), postgres.NewTxManager(env.pool), 24*time.Hour)
	identity := idempotencyApp.Identity{ActorUserID: actorID, Operation: "article.delete", Key: "50000000-0000-0000-0000-000000000012"}
	want := errors.New("业务失败")
	_, err := service.Execute(ctx, idempotencyApp.Command{Identity: identity, Payload: map[string]any{"id": "article-1"}, Now: now},
		func(context.Context) (any, string, string, error) { return nil, "", "", want })
	if !errors.Is(err, want) {
		t.Fatalf("错误 = %v", err)
	}
	var count int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.idempotency_operations
WHERE actor_user_id=$1 AND operation=$2 AND idempotency_key=$3`, actorID, identity.Operation, identity.Key).Scan(&count); err != nil || count != 0 {
		t.Fatalf("失败命令不应留下 pending：count=%d err=%v", count, err)
	}
}
