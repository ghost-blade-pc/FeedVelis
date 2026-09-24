package enrichment

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
)

type ActiveProfile struct {
	Provider, Model, ProfileVersion string
	WorkflowVersion, PromptVersion  string
	InputVersion                    string
	Dimensions                      int
	MaxAttempts, AuditTokenBudget   int
	Timeout, StageBudget            time.Duration
}

type ClaimRequest struct {
	Owner      string
	Lease      time.Duration
	Generation *ActiveProfile
	Embedding  *ActiveProfile
	Now        time.Time
}

type ClaimedTask struct {
	ID, LeaseToken                      string
	Generation                          int64
	Stage                               string
	Attempt                             int
	ArticleID, RevisionID               int64
	Revision                            RevisionInput
	GenerationProfile, EmbeddingProfile *ActiveProfile
	LeaseExpiresAt                      time.Time
}

type GenerationResult struct {
	ID                    string
	ArticleID, RevisionID int64
	Profile               ActiveProfile
	InputHash             string
	InputTruncated        bool
	Content               GeneratedContent
	GeneratedAt           time.Time
}

type EmbeddingResult struct {
	ID, GenerationResultID string
	ArticleID, RevisionID  int64
	Profile                ActiveProfile
	InputHash              string
	Vector                 []float64
	GeneratedAt            time.Time
}

type FailureUpdate struct {
	Task          ClaimedTask
	Code          ErrorCode
	Message       string
	Retry         bool
	NextAttemptAt time.Time
	Now           time.Time
}

type ExecutionStore interface {
	Claim(context.Context, ClaimRequest) (*ClaimedTask, error)
	CurrentGeneration(context.Context, ClaimedTask) (*GenerationResult, error)
	SaveGeneration(context.Context, ClaimedTask, GenerationResult, bool, time.Time) (bool, error)
	SaveEmbedding(context.Context, ClaimedTask, EmbeddingResult, time.Time) (bool, error)
	Fail(context.Context, FailureUpdate) (bool, error)
	RecordCalls(context.Context, ClaimedTask, ActiveProfile, []CallRecord, time.Time) error
}

type ExecutorPolicy struct {
	Lease                                                          time.Duration
	GenerationBackoffMin, GenerationBackoffMax                     time.Duration
	EmbeddingBackoffMin, EmbeddingBackoffMax                       time.Duration
	ChunkChars, MaxChunks, SingleInputChars, MaxCalls, Concurrency int
	MaxOutputTokens                                                int
	OutputLimits                                                   OutputLimits
	EmbeddingBodyChars                                             int
}

type Executor struct {
	store                 ExecutionStore
	generator             Generator
	embedder              Embedder
	generation, embedding *ActiveProfile
	policy                ExecutorPolicy
	now                   func() time.Time
	jitter                func(time.Duration) time.Duration
	observer              Observer
}

type Observer interface {
	Claimed(task ClaimedTask)
	Calls(task ClaimedTask, calls []CallRecord)
	Finished(task ClaimedTask, result string, code ErrorCode)
}

func (e *Executor) WithObserver(observer Observer) *Executor { e.observer = observer; return e }

func NewExecutor(store ExecutionStore, generator Generator, embedder Embedder, generation, embedding *ActiveProfile, policy ExecutorPolicy, now func() time.Time, jitter func(time.Duration) time.Duration) *Executor {
	if now == nil {
		now = time.Now
	}
	if jitter == nil {
		jitter = func(d time.Duration) time.Duration { return d }
	}
	return &Executor{store: store, generator: generator, embedder: embedder, generation: generation, embedding: embedding, policy: policy, now: now, jitter: jitter}
}

func (e *Executor) ProcessOne(ctx context.Context, owner string) (bool, error) {
	now := e.now().UTC()
	task, err := e.store.Claim(ctx, ClaimRequest{Owner: owner, Lease: e.policy.Lease, Generation: e.generation, Embedding: e.embedding, Now: now})
	if err != nil || task == nil {
		return false, err
	}
	if e.observer != nil {
		e.observer.Claimed(*task)
	}
	if task.Stage == "generation" {
		return true, e.runGeneration(ctx, *task)
	}
	if task.Stage == "embedding" {
		return true, e.runEmbedding(ctx, *task)
	}
	return true, fmt.Errorf("未知增强阶段 %q", task.Stage)
}

func (e *Executor) runGeneration(ctx context.Context, task ClaimedTask) error {
	if e.generator == nil || task.GenerationProfile == nil {
		return e.finishFailure(ctx, task, ErrorConfiguration, false, "generation profile 未配置")
	}
	prepared := PrepareGenerationInput(task.Revision, e.policy.ChunkChars, e.policy.MaxChunks)
	chunks := prepared.Chunks
	truncated := prepared.Truncated
	if prepared.RuneCount <= e.policy.SingleInputChars {
		chunks = []string{prepared.Normalized.PlainText}
		truncated = false
	}
	request := GenerationRequest{Revision: prepared.Normalized, InputHash: prepared.Hash, Chunks: chunks, InputTruncated: truncated,
		PromptVersion: task.GenerationProfile.PromptVersion, WorkflowVersion: task.GenerationProfile.WorkflowVersion,
		MaxOutputTokens: e.policy.MaxOutputTokens, MaxCalls: e.policy.MaxCalls, Concurrency: e.policy.Concurrency,
		TotalTimeout: task.GenerationProfile.StageBudget, AuditTokenBudget: task.GenerationProfile.AuditTokenBudget, Limits: e.policy.OutputLimits}
	response, callErr := e.generator.Generate(ctx, request)
	if err := e.store.RecordCalls(ctx, task, *task.GenerationProfile, response.Calls, e.now().UTC()); err != nil {
		return err
	}
	if e.observer != nil {
		e.observer.Calls(task, response.Calls)
	}
	if callErr != nil {
		code, retryable := ErrorClassification(callErr)
		return e.finishFailure(ctx, task, code, retryable, safeFailureMessage(code))
	}
	if usageExceeds(response.Calls, task.GenerationProfile.AuditTokenBudget) {
		return e.finishFailure(ctx, task, ErrorBudgetExceeded, false, "generation Token 审计预算已超出")
	}
	result := GenerationResult{ID: uuid.NewString(), ArticleID: task.ArticleID, RevisionID: task.RevisionID, Profile: *task.GenerationProfile,
		InputHash: prepared.Hash, InputTruncated: prepared.Truncated, Content: response.Content, GeneratedAt: e.now().UTC()}
	ok, err := e.store.SaveGeneration(ctx, task, result, task.EmbeddingProfile != nil, e.now().UTC())
	if e.observer != nil && err == nil {
		if ok {
			e.observer.Finished(task, "success", "")
		} else {
			e.observer.Finished(task, "stale", "")
		}
	}
	return err
}

func (e *Executor) runEmbedding(ctx context.Context, task ClaimedTask) error {
	if e.embedder == nil || task.EmbeddingProfile == nil {
		return e.finishFailure(ctx, task, ErrorConfiguration, false, "embedding profile 未配置")
	}
	generation, err := e.store.CurrentGeneration(ctx, task)
	if err != nil {
		return err
	}
	if generation == nil {
		return e.finishFailure(ctx, task, ErrorConfiguration, false, "当前修订缺少 generation 结果")
	}
	document := BuildRetrievalDocument(task.EmbeddingProfile.InputVersion, task.Revision, generation.Content, e.policy.EmbeddingBodyChars)
	callCtx := ctx
	var cancel context.CancelFunc
	if task.EmbeddingProfile.StageBudget > 0 {
		callCtx, cancel = context.WithTimeout(ctx, task.EmbeddingProfile.StageBudget)
		defer cancel()
	}
	response, callErr := e.embedder.Embed(callCtx, EmbeddingRequest{Document: document.Document, InputHash: document.Hash, InputVersion: document.Version})
	if err := e.store.RecordCalls(ctx, task, *task.EmbeddingProfile, []CallRecord{response.Call}, e.now().UTC()); err != nil {
		return err
	}
	if e.observer != nil {
		e.observer.Calls(task, []CallRecord{response.Call})
	}
	if callErr != nil {
		code, retryable := ErrorClassification(callErr)
		return e.finishFailure(ctx, task, code, retryable, safeFailureMessage(code))
	}
	if usageExceeds([]CallRecord{response.Call}, task.EmbeddingProfile.AuditTokenBudget) {
		return e.finishFailure(ctx, task, ErrorBudgetExceeded, false, "Embedding Token 审计预算已超出")
	}
	result := EmbeddingResult{ID: uuid.NewString(), GenerationResultID: generation.ID, ArticleID: task.ArticleID, RevisionID: task.RevisionID,
		Profile: *task.EmbeddingProfile, InputHash: document.Hash, Vector: response.Vector, GeneratedAt: e.now().UTC()}
	ok, err := e.store.SaveEmbedding(ctx, task, result, e.now().UTC())
	if e.observer != nil && err == nil {
		if ok {
			e.observer.Finished(task, "success", "")
		} else {
			e.observer.Finished(task, "stale", "")
		}
	}
	return err
}

func (e *Executor) finishFailure(ctx context.Context, task ClaimedTask, code ErrorCode, retryable bool, message string) error {
	profile, minBackoff, maxBackoff := task.GenerationProfile, e.policy.GenerationBackoffMin, e.policy.GenerationBackoffMax
	if task.Stage == "embedding" {
		profile, minBackoff, maxBackoff = task.EmbeddingProfile, e.policy.EmbeddingBackoffMin, e.policy.EmbeddingBackoffMax
	}
	maxAttempts := 1
	if profile != nil {
		maxAttempts = profile.MaxAttempts
	}
	retry := retryable && task.Attempt < maxAttempts
	delay := minBackoff
	if task.Attempt > 1 {
		delay = time.Duration(float64(minBackoff) * math.Pow(2, float64(task.Attempt-1)))
	}
	if delay > maxBackoff {
		delay = maxBackoff
	}
	ok, err := e.store.Fail(ctx, FailureUpdate{Task: task, Code: code, Message: message, Retry: retry, NextAttemptAt: e.now().UTC().Add(e.jitter(delay)), Now: e.now().UTC()})
	if e.observer != nil && err == nil {
		result := "failed"
		if retry {
			result = "retry"
		}
		if !ok {
			result = "stale"
		}
		e.observer.Finished(task, result, code)
	}
	return err
}

func usageExceeds(calls []CallRecord, budget int) bool {
	total := 0
	for _, call := range calls {
		if call.Usage.TotalTokens != nil {
			total += *call.Usage.TotalTokens
		}
	}
	return total > budget
}

func safeFailureMessage(code ErrorCode) string { return "模型阶段失败: " + string(code) }
