package enrichment

import (
	"context"
	"errors"
	"strings"
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
	reserveCalls                    int
	reserveResult                   bool
	events                          []string
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
	executor := NewExecutor(store, generator, nil, nil, gen, nil, policy(), nil, nil)
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
	s.events = append(s.events, "record")
	return nil
}
func (s *executionStoreFake) ReserveGenerationRepair(context.Context, ClaimedTask, time.Time) (bool, error) {
	s.reserveCalls++
	s.events = append(s.events, "reserve")
	return s.reserveResult, nil
}

type generatorFake struct {
	calls    int
	response GenerationResponse
	err      error
	deadline time.Time
}

func (g *generatorFake) Generate(ctx context.Context, _ GenerationRequest) (GenerationResponse, error) {
	g.calls++
	g.deadline, _ = ctx.Deadline()
	return g.response, g.err
}

type repairerFake struct {
	calls    int
	response GenerationResponse
	err      error
	deadline time.Time
}

func (r *repairerFake) Repair(ctx context.Context, _ RepairRequest) (GenerationResponse, error) {
	r.calls++
	r.deadline, _ = ctx.Deadline()
	return r.response, r.err
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
	return &ActiveProfile{Provider: "stub", Model: "chat", ProfileVersion: "g1", WorkflowVersion: "w1", PromptVersion: "p1", StructuredOutput: "prompt", MaxAttempts: 3, AuditTokenBudget: 100, StageBudget: time.Second},
		&ActiveProfile{Provider: "stub", Model: "embed", ProfileVersion: "e1", InputVersion: "i1", Dimensions: 2, MaxAttempts: 3, AuditTokenBudget: 100, StageBudget: time.Second}
}
func policy() ExecutorPolicy {
	return ExecutorPolicy{Lease: time.Minute, GenerationBackoffMin: time.Second, GenerationBackoffMax: time.Minute, EmbeddingBackoffMin: time.Second, EmbeddingBackoffMax: time.Minute, ChunkChars: 10, MaxChunks: 8, SingleInputChars: 20, MaxCalls: 9, Concurrency: 2, MapSummaryChars: 80, RepairInputChars: 1000, MaxOutputTokens: 100, OutputLimits: OutputLimits{SummaryChars: 100, KeywordCount: 12, TopicCount: 5, LabelChars: 64}, EmbeddingBodyChars: 20}
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
	executor := NewExecutor(store, generator, nil, embedder, gen, embed, policy(), func() time.Time { return time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC) }, nil)
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
	executor := NewExecutor(store, &generatorFake{}, nil, embedder, gen, embed, policy(), func() time.Time { return now }, func(d time.Duration) time.Duration { return d })
	if ok, err := executor.ProcessOne(context.Background(), "worker"); !ok || err != nil {
		t.Fatalf("ok=%t err=%v", ok, err)
	}
	if len(store.failures) != 1 || !store.failures[0].Retry || store.failures[0].NextAttemptAt.Sub(now) != 2*time.Second || store.savedGeneration != 0 {
		t.Fatalf("failure=%+v", store.failures)
	}
}

func TestExecutorInvalidEmbeddingDoesNotRegenerateOrReplaceGeneration(t *testing.T) {
	gen, embed := profiles()
	task := ClaimedTask{ID: "task", LeaseToken: "lease", Generation: 1, Stage: "embedding", Attempt: 1, ArticleID: 1, RevisionID: 2, Revision: RevisionInput{Title: "标题", PlainText: "正文"}, GenerationProfile: gen, EmbeddingProfile: embed}
	current := &GenerationResult{ID: "generation", Content: GeneratedContent{Summary: "摘要", Keywords: []string{"词"}, Topics: []string{"主题"}}}
	store := &executionStoreFake{claims: []ClaimedTask{task}, generation: current}
	generator := &generatorFake{}
	embedErr := NewErrorWithReason(ErrorInvalidOutput, true, ReasonVectorDimensions, "Embedding 向量维度不符", nil)
	embedder := &embedderFake{response: EmbeddingResponse{Call: CallRecord{Kind: "embedding", Status: "failed", ErrorCode: ErrorInvalidOutput, ErrorReason: ReasonVectorDimensions}}, err: embedErr}
	executor := NewExecutor(store, generator, nil, embedder, gen, embed, policy(), nil, nil)
	if processed, err := executor.ProcessOne(context.Background(), "worker"); err != nil || !processed {
		t.Fatalf("processed=%t err=%v", processed, err)
	}
	if generator.calls != 0 || store.generation != current || store.savedGeneration != 0 || store.savedEmbedding != 0 || len(store.failures) != 1 || store.failures[0].Code != ErrorInvalidOutput {
		t.Fatalf("Embedding 非法输出破坏 generation: generator=%d saved=%d/%d generation=%p failures=%+v", generator.calls, store.savedGeneration, store.savedEmbedding, store.generation, store.failures)
	}
}

func TestExecutorStopsOnActualUsageBudgetAndAttemptExhaustion(t *testing.T) {
	gen, _ := profiles()
	gen.AuditTokenBudget = 10
	task := ClaimedTask{ID: "task", LeaseToken: "lease", Generation: 1, Stage: "generation", Attempt: 3, ArticleID: 1, RevisionID: 2, Revision: RevisionInput{Title: "标题", PlainText: "正文"}, GenerationProfile: gen}
	tokens := 11
	store := &executionStoreFake{claims: []ClaimedTask{task}}
	generator := &generatorFake{response: GenerationResponse{Content: GeneratedContent{Summary: "摘要", Keywords: []string{"词"}, Topics: []string{"主题"}}, Calls: []CallRecord{{Kind: "generation_single", Status: "succeeded", Usage: Usage{TotalTokens: &tokens}}}}}
	executor := NewExecutor(store, generator, nil, nil, gen, nil, policy(), nil, nil)
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
			executor := NewExecutor(store, generator, nil, nil, gen, nil, policy(), nil, func(d time.Duration) time.Duration { return d })
			if _, err := executor.ProcessOne(context.Background(), "worker"); err != nil {
				t.Fatal(err)
			}
			if len(store.failures) != 1 || store.failures[0].Code != test.code || store.failures[0].Retry != test.wantRetry {
				t.Fatalf("failure=%+v", store.failures)
			}
		})
	}
}

func TestExecutorRepairsInvalidFinalOutputAfterPersistentReservation(t *testing.T) {
	gen, _ := profiles()
	task := ClaimedTask{ID: "task", LeaseToken: "lease", Generation: 1, Stage: "generation", Attempt: 1, ArticleID: 1, RevisionID: 2, Revision: RevisionInput{Title: "标题", PlainText: "正文"}, GenerationProfile: gen}
	store := &executionStoreFake{claims: []ClaimedTask{task}, reserveResult: true}
	initial := &generatorFake{response: GenerationResponse{Calls: []CallRecord{{Kind: "generation_single", Status: "failed", ErrorCode: ErrorInvalidOutput}}, Candidate: &RepairCandidate{Raw: "bad-json", Reason: "json_syntax"}}, err: NewErrorWithReason(ErrorInvalidOutput, true, "json_syntax", "非法输出", nil)}
	repair := &repairerFake{response: GenerationResponse{Content: GeneratedContent{Summary: "纠正摘要", Keywords: []string{"词"}, Topics: []string{"主题"}}, Calls: []CallRecord{{Kind: "generation_repair", Status: "succeeded"}}}}
	executor := NewExecutor(store, initial, repair, nil, gen, nil, policy(), nil, nil)
	if _, err := executor.ProcessOne(context.Background(), "worker"); err != nil {
		t.Fatal(err)
	}
	if store.calls != 2 || store.reserveCalls != 1 || repair.calls != 1 || store.savedGeneration != 1 || initial.deadline.IsZero() || !initial.deadline.Equal(repair.deadline) {
		t.Fatalf("纠正编排错误: calls=%d reserve=%d repair=%d saved=%d deadlines=%v/%v", store.calls, store.reserveCalls, repair.calls, store.savedGeneration, initial.deadline, repair.deadline)
	}
	if got := strings.Join(store.events, ","); got != "record,reserve,record" {
		t.Fatalf("必须先审计首次调用、再预占、最后审计纠正调用: %s", got)
	}
}

func TestExecutorUsesAtMostOneRepairAcrossAttempts(t *testing.T) {
	gen, _ := profiles()
	first := ClaimedTask{ID: "task", LeaseToken: "lease-1", Generation: 1, Stage: "generation", Attempt: 1, ArticleID: 1, RevisionID: 2, Revision: RevisionInput{Title: "标题", PlainText: "正文"}, GenerationProfile: gen}
	second := first
	second.LeaseToken, second.Attempt, second.GenerationRepairUsed = "lease-2", 2, true
	store := &executionStoreFake{claims: []ClaimedTask{first, second}, reserveResult: true}
	initial := &generatorFake{response: GenerationResponse{Calls: []CallRecord{{Kind: "generation_single", Status: "failed", ErrorCode: ErrorInvalidOutput}}, Candidate: &RepairCandidate{Raw: "bad-json", Reason: "json_syntax"}}, err: NewError(ErrorInvalidOutput, true, "非法输出", nil)}
	repair := &repairerFake{response: GenerationResponse{Calls: []CallRecord{{Kind: "generation_repair", Status: "failed", ErrorCode: ErrorInvalidOutput}}}, err: NewError(ErrorInvalidOutput, true, "纠正后仍非法", nil)}
	executor := NewExecutor(store, initial, repair, nil, gen, nil, policy(), nil, nil)
	for range 2 {
		if _, err := executor.ProcessOne(context.Background(), "worker"); err != nil {
			t.Fatal(err)
		}
	}
	if initial.calls != 2 || repair.calls != 1 || store.reserveCalls != 1 || len(store.failures) != 2 {
		t.Fatalf("跨 attempt 重复纠正: initial=%d repair=%d reserve=%d failures=%d", initial.calls, repair.calls, store.reserveCalls, len(store.failures))
	}
}

func TestExecutorDoesNotReserveRepairWhenBudgetOrInputIsInsufficient(t *testing.T) {
	gen, _ := profiles()
	base := ClaimedTask{ID: "task", LeaseToken: "lease", Generation: 1, Stage: "generation", Attempt: 1, ArticleID: 1, RevisionID: 2, Revision: RevisionInput{Title: "标题", PlainText: "正文"}, GenerationProfile: gen}
	for _, tc := range []struct {
		name   string
		mutate func(*ActiveProfile, *ExecutorPolicy, *RepairCandidate)
		code   ErrorCode
	}{
		{"Token 预算不足", func(profile *ActiveProfile, _ *ExecutorPolicy, _ *RepairCandidate) { profile.AuditTokenBudget = 99 }, ErrorBudgetExceeded},
		{"纠正输入过长", func(_ *ActiveProfile, policy *ExecutorPolicy, candidate *RepairCandidate) {
			policy.RepairInputChars = 3
			candidate.Raw = "四个字符"
		}, ErrorInvalidOutput},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profile := *gen
			task := base
			task.GenerationProfile = &profile
			candidate := &RepairCandidate{Raw: "bad", Reason: "json_syntax"}
			policy := policy()
			tc.mutate(&profile, &policy, candidate)
			store := &executionStoreFake{claims: []ClaimedTask{task}, reserveResult: true}
			initial := &generatorFake{response: GenerationResponse{Calls: []CallRecord{{Kind: "generation_single", Status: "failed"}}, Candidate: candidate}, err: NewError(ErrorInvalidOutput, true, "非法输出", nil)}
			repair := &repairerFake{}
			executor := NewExecutor(store, initial, repair, nil, &profile, nil, policy, nil, nil)
			if _, err := executor.ProcessOne(context.Background(), "worker"); err != nil {
				t.Fatal(err)
			}
			if store.reserveCalls != 0 || repair.calls != 0 || len(store.failures) != 1 || store.failures[0].Code != tc.code {
				t.Fatalf("预算不足仍预占: reserve=%d repair=%d failures=%+v", store.reserveCalls, repair.calls, store.failures)
			}
		})
	}
}

func TestExecutorRejectsLateRepairTokenOverrun(t *testing.T) {
	gen, _ := profiles()
	task := ClaimedTask{ID: "task", LeaseToken: "lease", Generation: 1, Stage: "generation", Attempt: 1, ArticleID: 1, RevisionID: 2, Revision: RevisionInput{Title: "标题", PlainText: "正文"}, GenerationProfile: gen}
	store := &executionStoreFake{claims: []ClaimedTask{task}, reserveResult: true}
	initial := &generatorFake{response: GenerationResponse{Calls: []CallRecord{{Kind: "generation_single", Status: "failed"}}, Candidate: &RepairCandidate{Raw: "bad", Reason: "json_syntax"}}, err: NewError(ErrorInvalidOutput, true, "非法输出", nil)}
	tokens := 101
	repair := &repairerFake{response: GenerationResponse{Content: GeneratedContent{Summary: "摘要", Keywords: []string{"词"}, Topics: []string{"主题"}}, Calls: []CallRecord{{Kind: "generation_repair", Status: "succeeded", Usage: Usage{TotalTokens: &tokens}}}}}
	executor := NewExecutor(store, initial, repair, nil, gen, nil, policy(), nil, nil)
	if _, err := executor.ProcessOne(context.Background(), "worker"); err != nil {
		t.Fatal(err)
	}
	if store.savedGeneration != 0 || len(store.failures) != 1 || store.failures[0].Code != ErrorBudgetExceeded {
		t.Fatalf("迟到 Token 超预算仍发布: saved=%d failures=%+v", store.savedGeneration, store.failures)
	}
}
