package enrichment

import (
	"context"
	"strings"
	"testing"
)

func TestBuildExtractiveFallbackUsesBoundedCurrentText(t *testing.T) {
	content, ok := BuildExtractiveFallback(RevisionInput{Title: "  Go 摘要  ", PlainText: strings.Repeat("内", 120) + "\x01"}, OutputLimits{
		SummaryChars: 40, KeywordCount: 12, TopicCount: 5, LabelChars: 4,
	})
	if !ok || len([]rune(content.Summary)) != 40 || strings.ContainsRune(content.Summary, '\x01') || content.Keywords[0] != "Go 摘" || content.Topics[0] != "Go 摘" {
		t.Fatalf("摘录边界错误: %+v ok=%t", content, ok)
	}
	if _, ok := BuildExtractiveFallback(RevisionInput{Title: "标题"}, OutputLimits{SummaryChars: 40, KeywordCount: 1, TopicCount: 1, LabelChars: 4}); ok {
		t.Fatal("无正文不应伪造摘录")
	}
}

func TestExecutorFallsBackOnlyAfterInvalidOutputAttemptsExhausted(t *testing.T) {
	gen, embed := profiles()
	task := ClaimedTask{ID: "task", LeaseToken: "lease", Generation: 1, Stage: "generation", Attempt: gen.MaxAttempts,
		ArticleID: 1, RevisionID: 2, Revision: RevisionInput{ArticleID: 1, RevisionID: 2, Title: "文章标题", PlainText: "这是当前修订的真实正文。"},
		GenerationProfile: gen, EmbeddingProfile: embed}
	store := &executionStoreFake{claims: []ClaimedTask{task}}
	generator := &generatorFake{err: NewError(ErrorInvalidOutput, true, "模型输出为空", nil)}
	executor := NewExecutor(store, generator, nil, nil, gen, embed, policy(), nil, nil)
	if processed, err := executor.ProcessOne(context.Background(), "worker"); !processed || err != nil {
		t.Fatalf("processed=%t err=%v", processed, err)
	}
	if store.savedGeneration != 1 || len(store.failures) != 0 || store.generation.Method != "extractive" ||
		store.generation.Content.Summary != task.Revision.PlainText || !store.needsEmbedding[0] {
		t.Fatalf("最终非法输出未保存可识别摘录: result=%+v failed=%+v", store.generation, store.failures)
	}

	task.Attempt = 1
	store = &executionStoreFake{claims: []ClaimedTask{task}}
	executor = NewExecutor(store, generator, nil, nil, gen, embed, policy(), nil, nil)
	if _, err := executor.ProcessOne(context.Background(), "worker"); err != nil || store.savedGeneration != 0 || len(store.failures) != 1 || !store.failures[0].Retry {
		t.Fatalf("尚有重试额度时过早降级: saved=%d failures=%+v err=%v", store.savedGeneration, store.failures, err)
	}

	task.Attempt = gen.MaxAttempts
	store = &executionStoreFake{claims: []ClaimedTask{task}}
	generator = &generatorFake{err: NewError(ErrorAuthentication, false, "鉴权失败", nil)}
	executor = NewExecutor(store, generator, nil, nil, gen, embed, policy(), nil, nil)
	if _, err := executor.ProcessOne(context.Background(), "worker"); err != nil || store.savedGeneration != 0 || len(store.failures) != 1 {
		t.Fatalf("鉴权错误不应降级: saved=%d failures=%+v err=%v", store.savedGeneration, store.failures, err)
	}
}
