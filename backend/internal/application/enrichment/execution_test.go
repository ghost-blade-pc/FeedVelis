package enrichment

import (
	"context"
	"errors"
	"testing"
	"time"
)

type executionStoreFake struct {
	claims                          []ClaimedTask
	generation                      *GenerationResult
	savedGeneration, savedEmbedding int
	failures                        []FailureUpdate
	calls                           int
	needsEmbedding                  []bool
}

func (s *executionStoreFake) Claim(context.Context, ClaimRequest) (*ClaimedTask, error) {
	if len(s.claims) == 0 {
		return nil, nil
	}
	task := s.claims[0]
	s.claims = s.claims[1:]
	return &task, nil
}
func (s *executionStoreFake) CurrentGeneration(context.Context, ClaimedTask) (*GenerationResult, error) {
	return s.generation, nil
}
func (s *executionStoreFake) SaveGeneration(_ context.Context, _ ClaimedTask, result GenerationResult, needsEmbedding bool, _ time.Time) (bool, error) {
	s.savedGeneration++
	s.needsEmbedding = append(s.needsEmbedding, needsEmbedding)
	s.generation = &result
	return true, nil
}

func TestExecutorGenerationSucceedsWithoutEmbeddingProfile(t *testing.T) {
	gen, _ := profiles()
	task := ClaimedTask{ID: "task", LeaseToken: "lease", Generation: 1, Stage: "generation", Attempt: 1, ArticleID: 1, RevisionID: 2, Revision: RevisionInput{ArticleID: 1, RevisionID: 2, Title: "标题", PlainText: "正文"}, GenerationProfile: gen}
	store := &executionStoreFake{claims: []ClaimedTask{task}}
	generator := &generatorFake{response: GenerationResponse{Content: GeneratedContent{Summary: "摘要", Keywords: []string{"词"}, Topics: []string{"主题"}}, Calls: []CallRecord{{Kind: "generation_single", Status: "succeeded"}}}}
	executor := NewExecutor(store, generator, nil, gen, nil, policy(), nil, nil)
	if processed, err := executor.ProcessOne(context.Background(), "worker"); err != nil || !processed {
		t.Fatalf("processed=%t err=%v", processed, err)
	}
	if store.savedGeneration != 1 || len(store.needsEmbedding) != 1 || store.needsEmbedding[0] {
		t.Fatalf("仅 generation profile 不应等待 Embedding: %+v", store.needsEmbedding)
	}
}
func (s *executionStoreFake) SaveEmbedding(context.Context, ClaimedTask, EmbeddingResult, time.Time) (bool, error) {
	s.savedEmbedding++
	return true, nil
}
func (s *executionStoreFake) Fail(_ context.Context, update FailureUpdate) (bool, error) {
	s.failures = append(s.failures, update)
	return true, nil
}
func (s *executionStoreFake) RecordCalls(_ context.Context, _ ClaimedTask, _ ActiveProfile, calls []CallRecord, _ time.Time) error {
	s.calls += len(calls)
	return nil
}

type generatorFake struct {
	calls    int
	response GenerationResponse
	err      error
}

func (g *generatorFake) Generate(context.Context, GenerationRequest) (GenerationResponse, error) {
	g.calls++
	return g.response, g.err
}

type embedderFake struct {
	calls    int
	response EmbeddingResponse
	err      error
}

func (e *embedderFake) Embed(context.Context, EmbeddingRequest) (EmbeddingResponse, error) {
	e.calls++
	return e.response, e.err
}

func profiles() (*ActiveProfile, *ActiveProfile) {
	return &ActiveProfile{Provider: "stub", Model: "chat", ProfileVersion: "g1", WorkflowVersion: "w1", PromptVersion: "p1", MaxAttempts: 3, AuditTokenBudget: 100, StageBudget: time.Second},
		&ActiveProfile{Provider: "stub", Model: "embed", ProfileVersion: "e1", InputVersion: "i1", Dimensions: 2, MaxAttempts: 3, AuditTokenBudget: 100, StageBudget: time.Second}
}
func policy() ExecutorPolicy {
	return ExecutorPolicy{Lease: time.Minute, GenerationBackoffMin: time.Second, GenerationBackoffMax: time.Minute, EmbeddingBackoffMin: time.Second, EmbeddingBackoffMax: time.Minute, ChunkChars: 10, MaxChunks: 8, SingleInputChars: 20, MaxCalls: 9, Concurrency: 2, MaxOutputTokens: 100, OutputLimits: OutputLimits{SummaryChars: 100, KeywordCount: 12, TopicCount: 5, LabelChars: 64}, EmbeddingBodyChars: 20}
}

func TestExecutorGenerationThenEmbeddingWithoutRegeneration(t *testing.T) {
	gen, embed := profiles()
	task := ClaimedTask{ID: "task", LeaseToken: "lease", Generation: 1, Stage: "generation", Attempt: 1, ArticleID: 1, RevisionID: 2, Revision: RevisionInput{ArticleID: 1, RevisionID: 2, Title: "标题", PlainText: "正文"}, GenerationProfile: gen, EmbeddingProfile: embed}
	embedTask := task
	embedTask.Stage = "embedding"
	embedTask.Attempt = 1
	store := &executionStoreFake{claims: []ClaimedTask{task, embedTask}}
	generator := &generatorFake{response: GenerationResponse{Content: GeneratedContent{Summary: "摘要", Keywords: []string{"词"}, Topics: []string{"主题"}}, Calls: []CallRecord{{Kind: "generation_single", Status: "succeeded"}}}}
	embedder := &embedderFake{response: EmbeddingResponse{Vector: []float64{1, 2}, Call: CallRecord{Kind: "embedding", Status: "succeeded"}}}
	executor := NewExecutor(store, generator, embedder, gen, embed, policy(), func() time.Time { return time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC) }, nil)
	if ok, err := executor.ProcessOne(context.Background(), "worker"); !ok || err != nil {
		t.Fatalf("generation ok=%t err=%v", ok, err)
	}
	if ok, err := executor.ProcessOne(context.Background(), "worker"); !ok || err != nil {
		t.Fatalf("embedding ok=%t err=%v", ok, err)
	}
	if generator.calls != 1 || embedder.calls != 1 || store.savedGeneration != 1 || store.savedEmbedding != 1 {
		t.Fatalf("calls generation=%d embedding=%d saved=%d/%d", generator.calls, embedder.calls, store.savedGeneration, store.savedEmbedding)
	}
}

func TestExecutorEmbeddingFailureKeepsGenerationAndBacksOff(t *testing.T) {
	gen, embed := profiles()
	task := ClaimedTask{ID: "task", LeaseToken: "lease", Generation: 1, Stage: "embedding", Attempt: 2, ArticleID: 1, RevisionID: 2, Revision: RevisionInput{Title: "标题", PlainText: "正文"}, GenerationProfile: gen, EmbeddingProfile: embed}
	store := &executionStoreFake{claims: []ClaimedTask{task}, generation: &GenerationResult{ID: "generation", Content: GeneratedContent{Summary: "摘要", Keywords: []string{"词"}, Topics: []string{"主题"}}}}
	embedder := &embedderFake{response: EmbeddingResponse{Call: CallRecord{Kind: "embedding", Status: "failed", ErrorCode: ErrorRateLimited}}, err: NewError(ErrorRateLimited, true, "限流", errors.New("429"))}
	now := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	executor := NewExecutor(store, &generatorFake{}, embedder, gen, embed, policy(), func() time.Time { return now }, func(d time.Duration) time.Duration { return d })
	if ok, err := executor.ProcessOne(context.Background(), "worker"); !ok || err != nil {
		t.Fatalf("ok=%t err=%v", ok, err)
	}
	if len(store.failures) != 1 || !store.failures[0].Retry || store.failures[0].NextAttemptAt.Sub(now) != 2*time.Second || store.savedGeneration != 0 {
		t.Fatalf("failure=%+v", store.failures)
	}
}

func TestExecutorStopsOnActualUsageBudgetAndAttemptExhaustion(t *testing.T) {
	gen, _ := profiles()
	gen.AuditTokenBudget = 10
	task := ClaimedTask{ID: "task", LeaseToken: "lease", Generation: 1, Stage: "generation", Attempt: 3, ArticleID: 1, RevisionID: 2, Revision: RevisionInput{Title: "标题", PlainText: "正文"}, GenerationProfile: gen}
	tokens := 11
	store := &executionStoreFake{claims: []ClaimedTask{task}}
	generator := &generatorFake{response: GenerationResponse{Content: GeneratedContent{Summary: "摘要", Keywords: []string{"词"}, Topics: []string{"主题"}}, Calls: []CallRecord{{Kind: "generation_single", Status: "succeeded", Usage: Usage{TotalTokens: &tokens}}}}}
	executor := NewExecutor(store, generator, nil, gen, nil, policy(), nil, nil)
	_, err := executor.ProcessOne(context.Background(), "worker")
	if err != nil || len(store.failures) != 1 || store.failures[0].Retry || store.failures[0].Code != ErrorBudgetExceeded || store.savedGeneration != 0 {
		t.Fatalf("failures=%+v err=%v", store.failures, err)
	}
}

func TestExecutorErrorPolicyIsFinite(t *testing.T) {
	gen, _ := profiles()
	cases := []struct {
		name      string
		attempt   int
		err       error
		code      ErrorCode
		wantRetry bool
	}{
		{"非法输出有限重试", 1, NewError(ErrorInvalidOutput, true, "非法输出", nil), ErrorInvalidOutput, true},
		{"鉴权错误永久失败", 1, NewError(ErrorAuthentication, false, "鉴权失败", nil), ErrorAuthentication, false},
		{"暂时错误尝试耗尽", 3, NewError(ErrorTimeout, true, "超时", context.DeadlineExceeded), ErrorTimeout, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			task := ClaimedTask{ID: "task", LeaseToken: "lease", Generation: 1, Stage: "generation", Attempt: test.attempt, ArticleID: 1, RevisionID: 2, Revision: RevisionInput{Title: "标题", PlainText: "正文"}, GenerationProfile: gen}
			store := &executionStoreFake{claims: []ClaimedTask{task}}
			generator := &generatorFake{err: test.err}
			executor := NewExecutor(store, generator, nil, gen, nil, policy(), nil, func(d time.Duration) time.Duration { return d })
			if _, err := executor.ProcessOne(context.Background(), "worker"); err != nil {
				t.Fatal(err)
			}
			if len(store.failures) != 1 || store.failures[0].Code != test.code || store.failures[0].Retry != test.wantRetry {
				t.Fatalf("failure=%+v", store.failures)
			}
		})
	}
}
