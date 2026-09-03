package main

import (
	"net/http"

	"github.com/deepnoodle-ai/wonton/web"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

//nolint:unparam // prov is always stubProvider{} in test call sites.
func buildActiveRegistry(base *tool.Registry, subAgentCfg config.SubAgentConfig, advisorCfg config.AdvisorConfig, prov provider.Provider, events output.EventSink, workDir, homeDir string, rm provider.ResolvedModel, maxTokens int, streamingPreferred bool, traceLogger *delegation.TraceLogger, cfg config.Config, providerFactory func(provider.ResolvedModel, string) (provider.Provider, error), httpClient *http.Client, searcher web.Searcher) (*tool.Registry, error) {
	return delegation.BuildDelegateRegistry(delegation.DelegateDeps{
		BaseRegistry:       base,
		SubAgentCfg:        subAgentCfg,
		AdvisorCfg:         advisorCfg,
		Provider:           prov,
		Events:             events,
		WorkDir:            workDir,
		HomeDir:            homeDir,
		ResolvedModel:      rm,
		MaxTokens:          maxTokens,
		StreamingPreferred: streamingPreferred,
		TraceLogger:        traceLogger,
		Config:             cfg,
		ProviderFactory:    providerFactory,
		HTTPClient:         httpClient,
		Searcher:           searcher,
	})
}
