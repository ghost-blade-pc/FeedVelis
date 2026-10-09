package redistest

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestRedisHarnessOwnershipAndProxy(t *testing.T) {
	h := New(t)
	other := New(t)
	ctx := context.Background()
	key := h.Key("owned")
	foreign := other.Key("sentinel")
	if err := h.Client.Set(ctx, key, "a", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if err := other.Client.Set(ctx, foreign, "b", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if err := h.Track(foreign); err == nil {
		t.Fatal("接受了其他命名空间")
	}
	if err := h.Clear(); err != nil {
		t.Fatal(err)
	}
	if n, _ := h.Client.Exists(ctx, key).Result(); n != 0 {
		t.Fatal("本次键未删除")
	}
	if v, _ := other.Client.Get(ctx, foreign).Result(); v != "b" {
		t.Fatal("清理影响其他命名空间")
	}
	p := NewProxy(t, h.Address)
	client := redis.NewClient(&redis.Options{Addr: p.Address(), Username: h.Client.Options().Username, Password: h.Client.Options().Password, MaxRetries: -1, ContextTimeoutEnabled: true, ReadTimeout: time.Second, WriteTimeout: time.Second, DisableIdentity: true})
	defer client.Close()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	p.Delay(100 * time.Millisecond)
	slow, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	start := time.Now()
	err := client.Ping(slow).Err()
	cancel()
	if err == nil || time.Since(start) > 300*time.Millisecond {
		t.Fatalf("慢响应未受限: %v", err)
	}
	p.Delay(0)
	p.Disconnect(true)
	if err := client.Ping(ctx).Err(); err == nil {
		t.Fatal("断连未生效")
	}
	p.Disconnect(false)
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
}
