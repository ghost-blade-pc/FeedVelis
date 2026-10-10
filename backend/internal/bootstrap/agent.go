package bootstrap

import (
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agenttools"
	articleApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/recommendation"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/config"
)

// buildAgentTools 是后续受控编排的内部装配点，不注册HTTP、CLI或模型调用。
func buildAgentTools(cfg config.Config, search articlesearch.Searcher, recommend *recommendation.Service, text articleApp.PublicArticleTextReader) *agenttools.Service {
	once, ok := search.(agenttools.Searcher)
	if !ok {
		once = articlesearch.UnavailableService{}
	}
	return agenttools.NewService(once, recommend, text, agenttools.Config{Timeout: cfg.Agent.Tools.Timeout, MaxOutputBytes: cfg.Agent.Tools.MaxOutputBytes})
}
