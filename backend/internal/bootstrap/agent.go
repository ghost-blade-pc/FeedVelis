package bootstrap

import (
	"context"
	"time"

	orch "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentorchestration"
	agent "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentrun"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agenttools"
	articleApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/recommendation"
	run "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
	einoAdapter "github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/ai/eino"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/config"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

// buildAgentTools 是后续受控编排的内部装配点，不注册HTTP、CLI或模型调用。
func buildAgentTools(cfg config.Config, search articlesearch.Searcher, recommend *recommendation.Service, text articleApp.PublicArticleTextReader) *agenttools.Service {
	once, ok := search.(agenttools.Searcher)
	if !ok {
		once = articlesearch.UnavailableService{}
	}
	return agenttools.NewService(once, recommend, text, agenttools.Config{Timeout: cfg.Agent.Tools.Timeout, MaxOutputBytes: cfg.Agent.Tools.MaxOutputBytes})
}

type agentRuntime struct {
	runs       *agent.Service
	supervisor *agent.Supervisor
	projector  *agent.Projector
}

func buildAgentRuntime(cfg config.Config, pool *pgxpool.Pool, search articlesearch.Searcher, recommend *recommendation.Service) *agentRuntime {
	repo := postgres.NewAgentRunRepository(pool)
	tx := postgres.NewTxManager(pool)
	facts := postgres.NewAgentEvidenceFacts(pool)
	projector := agent.NewProjector(facts).WithHistorySource(repo)
	guard := agent.NewGuard(repo, repo, tx)
	enabled := cfg.Agent.GenerationAvailable(cfg.Auth.Enabled)
	service := agent.NewService(repo, repo.AgentConversationRepository, tx, tx, repo, postgres.NewIdempotencyRepository(pool), agent.Config{Accepting: enabled, Timeout: cfg.Agent.Execution.Timeout, MaxMessages: cfg.Agent.MaxMessagesPerConversation}).WithProjector(projector)
	builder := orch.NewContextBuilder(repo, agent.EmptyPreferences{}, projector, cfg.Agent.Execution.MaxInputBytes)
	var runner agent.Runner
	if enabled {
		runner = einoAdapter.NewReadingRunner(cfg.Agent, buildAgentTools(cfg, search, recommend, postgres.NewArticleRepository(pool)), guard, facts)
	}
	e := cfg.Agent.Execution
	supervisor := agent.NewSupervisor(repo, tx, service, guard, builder, runner, agent.ExecutorConfig{Enabled: enabled, Concurrency: e.Concurrency, MaxAnswerChars: e.MaxAnswerChars, ProtocolBytes: einoAdapter.ContextProtocolBytes(), Budget: run.BudgetLimits{ModelCalls: e.MaxModelCalls, ToolCalls: e.MaxToolCalls, PerCallOutput: int64(cfg.Agent.Generation.MaxOutputTokens), TotalOutput: int64(e.MaxOutputTokens), AuditTokens: int64(e.AuditTokenBudget), InputBytes: e.MaxInputBytes}, ProfileVersion: cfg.Agent.Generation.ProfileVersion, PromptVersion: cfg.Agent.Generation.PromptVersion, ToolVersion: "article-read-v1"})
	return &agentRuntime{runs: service, supervisor: supervisor, projector: projector}
}
func (r *agentRuntime) stop(parent context.Context) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 10*time.Second)
	defer cancel()
	_ = r.supervisor.Stop(ctx)
}
