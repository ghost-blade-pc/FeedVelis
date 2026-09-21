package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	sourceDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/source"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

func TestSourceIntervalDefaultsAndBoundaries(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	ctx := context.Background()
	now := fixedNow()
	sources := postgres.NewSourceRepository(env.pool)

	// 未指定周期的零值走默认 30 分钟。
	created, inserted, err := sources.Add(ctx, "https://interval.example/feed", "https://interval.example/feed",
		"默认周期", 0, now)
	if err != nil || !inserted {
		t.Fatalf("新增来源 inserted=%t err=%v", inserted, err)
	}
	if created.FetchInterval != sourceDomain.DefaultFetchInterval || created.LockVersion != 1 {
		t.Fatalf("默认来源 = %+v", created)
	}

	// 边界值可以落库并原样读回。
	minimum, _, err := sources.Add(ctx, "https://min.example/feed", "https://min.example/feed",
		"最短周期", sourceDomain.MinFetchInterval, now)
	if err != nil || minimum.FetchInterval != sourceDomain.MinFetchInterval {
		t.Fatalf("最短周期 = %+v err=%v", minimum, err)
	}
	maximum, _, err := sources.Add(ctx, "https://max.example/feed", "https://max.example/feed",
		"最长周期", sourceDomain.MaxFetchInterval, now)
	if err != nil || maximum.FetchInterval != sourceDomain.MaxFetchInterval {
		t.Fatalf("最长周期 = %+v err=%v", maximum, err)
	}

	// 修改周期只动周期与版本，Feed URL 保持不变。
	updated, err := sources.SetFetchInterval(ctx, created.ID, created.LockVersion, 2*time.Hour, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("修改周期失败: %v", err)
	}
	if updated.FetchInterval != 2*time.Hour || updated.LockVersion != created.LockVersion+1 {
		t.Fatalf("修改后的来源 = %+v", updated)
	}
	if updated.FeedURL != created.FeedURL || updated.NormalizedFeedURL != created.NormalizedFeedURL {
		t.Fatalf("Feed URL 不得改变: %q -> %q", created.FeedURL, updated.FeedURL)
	}
	// 使用旧版本再次修改必须冲突，且不覆盖较新的状态。
	if _, err := sources.SetFetchInterval(ctx, created.ID, created.LockVersion, 5*time.Minute, now.Add(2*time.Minute)); !errors.Is(err, sourceDomain.ErrVersionConflict) {
		t.Fatalf("旧版本修改 err = %v", err)
	}
	reloaded, err := sources.Get(ctx, created.ID)
	if err != nil || reloaded.FetchInterval != 2*time.Hour || reloaded.LockVersion != updated.LockVersion {
		t.Fatalf("冲突后来源 = %+v err=%v", reloaded, err)
	}
}

func TestSourcePauseResumeWithVersionGuard(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	ctx := context.Background()
	now := fixedNow()
	sources := postgres.NewSourceRepository(env.pool)

	created, _, err := sources.Add(ctx, "https://pause.example/feed", "https://pause.example/feed",
		"暂停恢复", sourceDomain.DefaultFetchInterval, now)
	if err != nil {
		t.Fatal(err)
	}
	// 暂停前先认领租约，验证暂停会清除它并让旧抓取的完成写入失效。
	claimed, err := sources.ClaimByID(ctx, created.ID, "worker-old", now, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	lease, err := claimed.CurrentLease()
	if err != nil {
		t.Fatal(err)
	}
	paused, err := sources.Pause(ctx, created.ID, claimed.LockVersion, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if paused.Status != sourceDomain.StatusPaused || paused.LeaseOwner != nil || paused.LeaseExpiresAt != nil {
		t.Fatalf("暂停结果 = %+v", paused)
	}
	// 暂停后旧租约的完成写入被 fencing 拒绝。
	if err := sources.MarkSuccess(ctx, created.ID, lease, sourceDomain.Metadata{Title: "过期写入"},
		now.Add(2*time.Minute), now.Add(32*time.Minute)); !errors.Is(err, sourceDomain.ErrLeaseLost) {
		t.Fatalf("旧租约写入 err = %v", err)
	}
	// 旧版本暂停同样冲突。
	if _, err := sources.Pause(ctx, created.ID, claimed.LockVersion, now.Add(2*time.Minute)); !errors.Is(err, sourceDomain.ErrVersionConflict) {
		t.Fatalf("旧版本暂停 err = %v", err)
	}

	resumed, err := sources.Resume(ctx, created.ID, paused.LockVersion, now.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Status != sourceDomain.StatusActive || resumed.LockVersion != paused.LockVersion+1 {
		t.Fatalf("恢复结果 = %+v", resumed)
	}
	// 恢复重新安排抓取，并保留既有文章与历史（这里以周期不变为证）。
	if !resumed.NextFetchAt.Equal(now.Add(3*time.Minute)) || resumed.FetchInterval != sourceDomain.DefaultFetchInterval {
		t.Fatalf("恢复后的调度 = %+v", resumed)
	}
	// 恢复后可以重新认领；暂停期间不可被调度。
	renewed, err := sources.ClaimByID(ctx, created.ID, "worker-new", now.Add(4*time.Minute), now.Add(6*time.Minute))
	if err != nil || renewed.LeaseOwner == nil || *renewed.LeaseOwner != "worker-new" {
		t.Fatalf("恢复后认领 = %+v err=%v", renewed, err)
	}
}

func TestSourceMutationsReportMissingSource(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	ctx := context.Background()
	now := fixedNow()
	sources := postgres.NewSourceRepository(env.pool)

	// 不存在与版本冲突必须可区分：前者 404，后者 409。
	if _, err := sources.Pause(ctx, 999999, 1, now); !errors.Is(err, sourceDomain.ErrNotFound) {
		t.Fatalf("不存在的来源 err = %v", err)
	}
	if _, err := sources.SetFetchInterval(ctx, 999999, 1, time.Hour, now); !errors.Is(err, sourceDomain.ErrNotFound) {
		t.Fatalf("不存在的来源修改周期 err = %v", err)
	}
}
