package searchprojection

import (
	"testing"

	projectionDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/searchprojection"
)

func TestErrorCodeVocabularyIsBounded(t *testing.T) {
	if len(ErrorCodes) != 10 {
		t.Fatalf("受控错误分类数量变化，需要同步核对指标标签: %d", len(ErrorCodes))
	}
	for _, code := range ErrorCodes {
		if !code.Known() {
			t.Fatalf("%s 必须在受控词表内", code)
		}
	}
	for _, code := range []ErrorCode{"", "unknown", "connection reset by peer", "mapping 错误"} {
		if code.Known() {
			t.Fatalf("%q 不得成为指标标签", code)
		}
	}
}

func TestDeliveryOutcomeNormalizeFoldsUnknownCodes(t *testing.T) {
	cases := map[string]struct {
		outcome     DeliveryOutcome
		wantCode    ErrorCode
		wantMessage string
	}{
		"未知错误码折叠为 internal": {
			outcome:     DeliveryOutcome{Result: projectionDomain.ResultRetryable, Code: "socket hang up", Message: "连接中断"},
			wantCode:    ErrorInternal,
			wantMessage: "连接中断",
		},
		"空错误码折叠为 internal": {
			outcome:     DeliveryOutcome{Result: projectionDomain.ResultRetryable},
			wantCode:    ErrorInternal,
			wantMessage: "",
		},
		"受控错误码保持不变": {
			outcome:     DeliveryOutcome{Result: projectionDomain.ResultRetryable, Code: ErrorThrottled, Message: "429"},
			wantCode:    ErrorThrottled,
			wantMessage: "429",
		},
		"已收敛结果不携带错误码": {
			outcome:     DeliveryOutcome{Result: projectionDomain.ResultNoop, Code: ErrorVersionConflict, Message: "版本冲突"},
			wantCode:    ErrorUnclassified,
			wantMessage: "",
		},
	}
	for name, item := range cases {
		normalized := item.outcome.Normalize()
		if normalized.Code != item.wantCode || normalized.Message != item.wantMessage {
			t.Fatalf("%s: 规范化结果 = %s/%q，期望 %s/%q", name, normalized.Code, normalized.Message, item.wantCode, item.wantMessage)
		}
	}
}

func TestDeliveryOutcomeStatusForClassifiesRetries(t *testing.T) {
	cases := map[string]struct {
		outcome     DeliveryOutcome
		attempt     int
		maxAttempts int
		want        projectionDomain.Status
	}{
		"写入成功":     {DeliveryOutcome{Result: projectionDomain.ResultCreated}, 1, 3, projectionDomain.StatusSucceeded},
		"过期写入视为收敛": {DeliveryOutcome{Result: projectionDomain.ResultNoop}, 1, 3, projectionDomain.StatusSucceeded},
		"限流可重试":    {DeliveryOutcome{Result: projectionDomain.ResultRetryable, Code: ErrorThrottled}, 1, 3, projectionDomain.StatusRetryWait},
		"结果未知可重试":  {DeliveryOutcome{Result: projectionDomain.ResultUnknown, Code: ErrorConnection}, 2, 3, projectionDomain.StatusRetryWait},
		"重试次数用尽":   {DeliveryOutcome{Result: projectionDomain.ResultRetryable, Code: ErrorTimeout}, 3, 3, projectionDomain.StatusFailed},
		"映射错误立即失败": {DeliveryOutcome{Result: projectionDomain.ResultPermanent, Code: ErrorMapping}, 1, 3, projectionDomain.StatusFailed},
		"文档过大立即失败": {DeliveryOutcome{Result: projectionDomain.ResultPermanent, Code: ErrorDocumentTooLarge}, 1, 3, projectionDomain.StatusFailed},
	}
	for name, item := range cases {
		if status := item.outcome.StatusFor(item.attempt, item.maxAttempts); status != item.want {
			t.Fatalf("%s: 投递状态 = %s，期望 %s", name, status, item.want)
		}
	}
}

func TestDeliveryOutcomeFailureOmitsDiagnosisForConvergedResults(t *testing.T) {
	if failure := (DeliveryOutcome{Result: projectionDomain.ResultUpdated}).Failure(); failure != nil {
		t.Fatalf("已收敛结果不应产生失败诊断: %+v", failure)
	}
	failure := (DeliveryOutcome{Result: projectionDomain.ResultPermanent, Code: "raw_provider_error", Message: "映射失败"}).Failure()
	if failure == nil || failure.Code != string(ErrorInternal) {
		t.Fatalf("未受控错误码必须先折叠为 internal: %+v", failure)
	}
}

func TestDocumentTombstoneKeepsIdentityAndDropsSearchableContent(t *testing.T) {
	sourceID := int64(9)
	document := Document{
		ArticleID: 7, Generation: 4, LockVersion: 5, RevisionID: 50, SchemaVersion: 2,
		Visible: true, OriginType: "rss", SourceID: &sourceID, SourceTitle: "来源",
		AuthorUserID: "user-1", AuthorName: "作者", Title: "标题", PlainText: "正文",
		Excerpt: "摘要", Summary: "AI 摘要", Keywords: []string{"关键词"}, Topics: []string{"主题"},
		GenerationResultID: "g-1", EmbeddingResultID: "e-1", Vector: []float64{0.1, 0.2},
	}
	tombstone := document.Tombstone("offline")
	if tombstone.Visible || tombstone.InvisibleReason != "offline" {
		t.Fatalf("tombstone 必须不可检索: %+v", tombstone)
	}
	if tombstone.Title != "" || tombstone.PlainText != "" || tombstone.Excerpt != "" ||
		tombstone.Summary != "" || len(tombstone.Keywords) != 0 || len(tombstone.Topics) != 0 || len(tombstone.Vector) != 0 {
		t.Fatalf("tombstone 不得保留正文、增强结果或向量: %+v", tombstone)
	}
	if tombstone.GenerationResultID != "" || tombstone.EmbeddingResultID != "" {
		t.Fatal("tombstone 不得保留 AI 结果引用")
	}
	// 身份与 fencing 字段必须保留，否则无法拒绝迟到的旧写入。
	if tombstone.ArticleID != 7 || tombstone.Generation != 4 || tombstone.LockVersion != 5 ||
		tombstone.RevisionID != 50 || tombstone.SchemaVersion != 2 || tombstone.OriginType != "rss" ||
		tombstone.SourceID == nil || *tombstone.SourceID != 9 || tombstone.AuthorUserID != "user-1" {
		t.Fatalf("tombstone 必须保留完整身份与版本: %+v", tombstone)
	}
}
