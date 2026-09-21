package asset

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const (
	ownerID = "51000000-0000-0000-0000-000000000001"
	otherID = "51000000-0000-0000-0000-000000000002"
	assetID = "6f1c6b1e-2e4e-4f1b-9c7a-3f2d5b8a0c11"
)

var testLimits = Limits{MaxFileBytes: 10 << 20, MaxWidth: 8192, MaxHeight: 8192, MaxPixels: 40000000}

func testNow() time.Time { return time.Date(2026, 9, 21, 4, 0, 0, 0, time.UTC) }

func newPendingAsset(t *testing.T) Asset {
	t.Helper()
	pending, err := NewPending(assetID, ownerID, testNow())
	if err != nil {
		t.Fatal(err)
	}
	return pending
}

func confirmedMedia(t *testing.T) Media {
	t.Helper()
	media, err := NewMedia(ContentTypePNG, 1024, 800, 600, "sha256:abc", testLimits)
	if err != nil {
		t.Fatal(err)
	}
	return media
}

func TestObjectKeyIsDerivedFromOwnerAndAssetID(t *testing.T) {
	key, err := ObjectKey(ownerID, assetID)
	if err != nil {
		t.Fatal(err)
	}
	if key != "article-assets/"+ownerID+"/"+assetID+"/original" {
		t.Fatalf("对象键 = %q", key)
	}
	// 大小写不同的同一 UUID 必须得到同一对象键，否则会为同一资产产生两个对象。
	upper, err := ObjectKey(strings.ToUpper(ownerID), strings.ToUpper(assetID))
	if err != nil || upper != key {
		t.Fatalf("大小写变体 = %q err=%v", upper, err)
	}
	if _, err := ObjectKey("not-a-uuid", assetID); !errors.Is(err, ErrInvalidOwner) {
		t.Fatalf("非法所有者 err = %v", err)
	}
	if _, err := ObjectKey(ownerID, "short"); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("非法资产 ID err = %v", err)
	}
}

func TestNewMediaEnforcesEveryLimit(t *testing.T) {
	cases := map[string]struct {
		contentType string
		size        int64
		width       int
		height      int
		checksum    string
	}{
		"类型不支持": {"image/gif", 1024, 10, 10, ""},
		"空类型":   {"", 1024, 10, 10, ""},
		"大小为零":  {ContentTypePNG, 0, 10, 10, ""},
		"超过单文件": {ContentTypePNG, testLimits.MaxFileBytes + 1, 10, 10, ""},
		"宽度超限":  {ContentTypePNG, 1024, testLimits.MaxWidth + 1, 10, ""},
		"高度超限":  {ContentTypePNG, 1024, 10, testLimits.MaxHeight + 1, ""},
		"像素超限":  {ContentTypeJPEG, 1024, 8000, 8000, ""},
		"校验值过长": {ContentTypeWebP, 1024, 10, 10, strings.Repeat("a", 257)},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewMedia(testCase.contentType, testCase.size, testCase.width, testCase.height, testCase.checksum, testLimits); !errors.Is(err, ErrInvalidMedia) {
				t.Fatalf("err = %v", err)
			}
		})
	}
	// 边界（含）应通过：单文件大小、宽高与总像素都取上限值。
	// 8192×8192 会超过 4000 万像素上限，因此像素维度单独取恰好等于上限的组合。
	if _, err := NewMedia(ContentTypeWebP, testLimits.MaxFileBytes, 8000, 5000, "", testLimits); err != nil {
		t.Fatalf("边界值应通过: %v", err)
	}
	if _, err := NewMedia(ContentTypePNG, 1, testLimits.MaxWidth, testLimits.MaxHeight, "", testLimits); !errors.Is(err, ErrInvalidMedia) {
		t.Fatalf("8192×8192 应因像素上限被拒: %v", err)
	}
}

func TestConfirmRequiresOwnershipAndPendingState(t *testing.T) {
	pending := newPendingAsset(t)
	if pending.QuotaCountedAt() != nil || pending.ConfirmedAt() != nil {
		t.Fatal("新建资产不得带有确认或计费时间")
	}
	if err := pending.Confirm(otherID, confirmedMedia(t), testNow()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("他人确认 err = %v", err)
	}
	if pending.Status() != StatusPending {
		t.Fatal("被拒绝的确认不得改变状态")
	}
	if err := pending.Confirm(ownerID, confirmedMedia(t), testNow()); err != nil {
		t.Fatal(err)
	}
	if pending.Status() != StatusReady || pending.ConfirmedAt() == nil || pending.Media() == nil {
		t.Fatalf("确认结果 = %+v", pending)
	}
	// 重复确认保持幂等，且不覆盖既有媒体信息。
	if err := pending.Confirm(ownerID, confirmedMedia(t), testNow().Add(time.Hour)); err != nil {
		t.Fatalf("重复确认 err = %v", err)
	}
	if !pending.ConfirmedAt().Equal(testNow()) {
		t.Fatalf("重复确认不得改写确认时间: %v", pending.ConfirmedAt())
	}
}

func TestConfirmRejectsDeletedAsset(t *testing.T) {
	pending := newPendingAsset(t)
	if err := pending.RequestDelete(testNow()); err != nil {
		t.Fatal(err)
	}
	if err := pending.MarkDeleted(testNow()); err != nil {
		t.Fatal(err)
	}
	if err := pending.Confirm(ownerID, confirmedMedia(t), testNow()); !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("已删除资产确认 err = %v", err)
	}
}

func TestBindEnforcesSingleArticleAndReadyState(t *testing.T) {
	pending := newPendingAsset(t)
	if err := pending.Bind(7, testNow()); !errors.Is(err, ErrNotReady) {
		t.Fatalf("未确认资产绑定 err = %v", err)
	}
	if err := pending.Confirm(ownerID, confirmedMedia(t), testNow()); err != nil {
		t.Fatal(err)
	}
	if err := pending.Bind(7, testNow()); err != nil {
		t.Fatal(err)
	}
	// 同一文章重复绑定保持幂等，跨文章复用被拒绝。
	if err := pending.Bind(7, testNow()); err != nil {
		t.Fatalf("重复绑定 err = %v", err)
	}
	if err := pending.Bind(8, testNow()); !errors.Is(err, ErrBoundElsewhere) {
		t.Fatalf("跨文章绑定 err = %v", err)
	}
	if bound := pending.BoundArticleID(); bound == nil || *bound != 7 {
		t.Fatalf("绑定文章 = %v", bound)
	}
	if err := pending.Bind(0, testNow()); !errors.Is(err, ErrBoundElsewhere) {
		t.Fatalf("非法文章 ID err = %v", err)
	}
}

func TestRequestDeleteCommutesWithPublishedArticle(t *testing.T) {
	// 下架文章不触发删除：只有显式请求才进入 delete_pending。
	ready := newPendingAsset(t)
	if err := ready.Confirm(ownerID, confirmedMedia(t), testNow()); err != nil {
		t.Fatal(err)
	}
	if err := ready.RequestDelete(testNow()); err != nil {
		t.Fatal(err)
	}
	if ready.Status() != StatusDeletePending || ready.DeleteRequestedAt() == nil || ready.DeletedAt() != nil {
		t.Fatalf("请求删除 = %+v", ready)
	}
	// 对象存储删除失败后重试：重复请求与重复标记都保持幂等。
	if err := ready.RequestDelete(testNow().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := ready.MarkDeleted(testNow().Add(2 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if ready.Status() != StatusDeleted || ready.DeletedAt() == nil {
		t.Fatalf("标记删除 = %+v", ready)
	}
	if err := ready.MarkDeleted(testNow().Add(3 * time.Hour)); err != nil {
		t.Fatalf("重复标记 err = %v", err)
	}
	if !ready.DeletedAt().Equal(testNow().Add(2 * time.Hour)) {
		t.Fatalf("重复标记不得改写删除时间: %v", ready.DeletedAt())
	}
}

func TestMarkQuotaCountedIsOncePerAsset(t *testing.T) {
	ready := newPendingAsset(t)
	if !ready.MarkQuotaCounted(testNow()) {
		t.Fatal("首次计费应成功")
	}
	if ready.MarkQuotaCounted(testNow().Add(time.Hour)) {
		t.Fatal("重复计费必须被拒绝")
	}
	if !ready.QuotaCountedAt().Equal(testNow()) {
		t.Fatalf("计费时间 = %v", ready.QuotaCountedAt())
	}
}

func TestRestoreKeepsStoredState(t *testing.T) {
	confirmed := testNow()
	bound := int64(7)
	media := confirmedMedia(t)
	restored := Restore(assetID, ownerID, "article-assets/"+ownerID+"/"+assetID+"/original",
		StatusReady, &bound, &media, &confirmed, &confirmed, nil, nil, testNow(), testNow())
	if restored.Status() != StatusReady || restored.ObjectKey() == "" || restored.Media() == nil {
		t.Fatalf("重建结果 = %+v", restored)
	}
	// 返回值必须是副本：修改取出的媒体信息不得影响聚合内部状态。
	restored.Media().SizeBytes = 1
	if restored.Media().SizeBytes != 1024 {
		t.Fatal("媒体信息必须是副本")
	}
	*restored.BoundArticleID() = 9
	if *restored.BoundArticleID() != 7 {
		t.Fatal("绑定文章必须是副本")
	}
}
