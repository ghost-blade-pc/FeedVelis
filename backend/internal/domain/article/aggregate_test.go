package article

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestOriginRequiresExactlyOneCompleteIdentity(t *testing.T) {
	validRSS := &RSSOrigin{SourceID: 1, DedupeKey: strings.Repeat("a", 64), CanonicalURL: "https://example.com/a"}
	validUser := &UserOrigin{AuthorUserID: "user-1"}
	for _, origin := range []Origin{
		{}, {RSS: validRSS, User: validUser}, {RSS: &RSSOrigin{}}, {User: &UserOrigin{}},
	} {
		if !errors.Is(origin.Validate(), ErrInvalidOrigin) {
			t.Fatalf("非法来源应被拒绝: %+v", origin)
		}
	}
	if err := (Origin{RSS: validRSS}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Origin{User: validUser}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestUserLifecycleAndFixedPublishedAt(t *testing.T) {
	now := time.Date(2026, 9, 21, 1, 0, 0, 0, time.UTC)
	revision := mustRevision(t, 1, 1, "a", now)
	aggregate, err := NewUserDraft(1, "author", revision, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := aggregate.PublishByAuthor("author", 1, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	publishedAt := *aggregate.PublishedAt()
	if aggregate.Status() != StatusPublished || aggregate.LockVersion() != 2 {
		t.Fatalf("首次发布状态 = %s/%d", aggregate.Status(), aggregate.LockVersion())
	}
	if err := aggregate.OfflineByAuthor("author", 2, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := aggregate.PublishByAuthor("author", 3, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if !aggregate.PublishedAt().Equal(publishedAt) || aggregate.CurrentRevision().Number() != 1 {
		t.Fatal("重新发布不得改变发布时间或制造修订")
	}
	if err := aggregate.DeleteByAuthor("author", 4, now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := aggregate.PublishByAuthor("author", 5, now.Add(5*time.Minute)); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("删除终态必须拒绝发布: %v", err)
	}
}

func TestAdministratorOfflineHasPriority(t *testing.T) {
	now := time.Now().UTC()
	aggregate, err := NewUserDraft(1, "author", mustRevision(t, 1, 1, "a", now), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := aggregate.PublishByAuthor("author", 1, now); err != nil {
		t.Fatal(err)
	}
	if err := aggregate.OfflineByAdministrator("admin", 2, now); err != nil {
		t.Fatal(err)
	}
	if err := aggregate.PublishByAuthor("author", 3, now); !errors.Is(err, ErrAdminOffline) {
		t.Fatalf("作者不得覆盖管理员下架: %v", err)
	}
	changed, err := aggregate.EditByAuthor("author", mustRevision(t, 2, 1, "b", now), 3, now)
	if err != nil || !changed || aggregate.Status() != StatusOffline {
		t.Fatalf("管理员下架期间应允许编辑但保持离线: changed=%t status=%s err=%v", changed, aggregate.Status(), err)
	}
	if err := aggregate.RestoreByAdministrator("admin", 4, now); err != nil {
		t.Fatal(err)
	}
	if aggregate.Status() != StatusPublished || aggregate.CurrentRevision().Number() != 2 {
		t.Fatal("管理员恢复应公开最新修订")
	}
}

func TestRevisionAndLockVersionChangeIndependently(t *testing.T) {
	now := time.Now().UTC()
	aggregate, err := NewRSSPublished(9, RSSOrigin{SourceID: 1, DedupeKey: strings.Repeat("d", 64), CanonicalURL: "https://example.com/9"}, mustRevision(t, 1, 9, "a", now), now, now)
	if err != nil {
		t.Fatal(err)
	}
	unchanged := mustRevision(t, 2, 9, "a", now)
	changed, err := aggregate.UpdateRSS(unchanged, 1, now)
	if err != nil || changed || aggregate.LockVersion() != 1 || aggregate.CurrentRevision().Number() != 1 {
		t.Fatalf("无变化更新错误: changed=%t lock=%d revision=%d err=%v", changed, aggregate.LockVersion(), aggregate.CurrentRevision().Number(), err)
	}
	changed, err = aggregate.UpdateRSS(mustRevision(t, 2, 9, "b", now), 1, now)
	if err != nil || !changed || aggregate.LockVersion() != 2 || aggregate.CurrentRevision().Number() != 2 {
		t.Fatalf("内容更新错误: changed=%t lock=%d revision=%d err=%v", changed, aggregate.LockVersion(), aggregate.CurrentRevision().Number(), err)
	}
	if err := aggregate.OfflineByAdministrator("admin", 2, now); err != nil {
		t.Fatal(err)
	}
	if aggregate.LockVersion() != 3 || aggregate.CurrentRevision().Number() != 2 {
		t.Fatal("状态变化只能增加 lock_version")
	}
	if changed, err := aggregate.UpdateRSS(mustRevision(t, 3, 9, "c", now), 2, now); !errors.Is(err, ErrVersionConflict) || changed {
		t.Fatalf("旧版本写入应冲突: changed=%t err=%v", changed, err)
	}
}

func TestAuthorPermissionsAndRevisionImmutability(t *testing.T) {
	now := time.Now().UTC()
	markdown := "初稿"
	revision, err := NewRevision(1, 1, 1, "标题", &markdown, strings.Repeat("a", 64), now)
	if err != nil {
		t.Fatal(err)
	}
	aggregate, err := NewUserDraft(1, "author", revision, now)
	if err != nil {
		t.Fatal(err)
	}
	markdown = "外部修改"
	if *aggregate.CurrentRevision().Markdown() != "初稿" {
		t.Fatal("修订必须复制输入而不暴露可变引用")
	}
	if _, err := aggregate.EditByAuthor("other", mustRevision(t, 2, 1, "b", now), 1, now); !errors.Is(err, ErrForbidden) {
		t.Fatalf("非作者编辑应被拒绝: %v", err)
	}
}

func mustRevision(t *testing.T, number int, articleID int64, hashSeed string, now time.Time) Revision {
	t.Helper()
	revision, err := NewRevision(int64(number), articleID, number, "标题", nil, strings.Repeat(hashSeed, 64), now)
	if err != nil {
		t.Fatal(err)
	}
	return revision
}
