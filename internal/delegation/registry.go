package delegation

import (
	"fmt"
	"net/http"

	"github.com/deepnoodle-ai/wonton/web"

	"github.com/luispabon/steiner/internal/advisor"
	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

// DelegateDeps holds the dependencies needed to assemble the active delegate registry.
type DelegateDeps struct {
	// BaseRegistry is the starting registry to clone before registering delegation tools.
	BaseRegistry *tool.Registry
	// SubAgentCfg controls delegate and specialized sub-agent registration.
	SubAgentCfg config.SubAgentConfig
	// AdvisorCfg controls advisor tool registration.
	AdvisorCfg config.AdvisorConfig
	// Provider is the active provider used for child delegation runs.
	Provider provider.Provider
	// Events receives run and tool events emitted while building delegation handlers.
	Events output.EventSink
	// WorkDir is the working directory passed to delegated child agents.
	WorkDir string
	// HomeDir is the home directory passed to delegated child agents.
	HomeDir string
	// ResolvedModel is the active model metadata for the current run.
	ResolvedModel provider.ResolvedModel
	// MaxTokens is the maximum completion token budget for delegated child runs.
	MaxTokens int
	// StreamingPreferred reports whether child runs should prefer streaming.
	StreamingPreferred bool
	// TraceLogger receives delegation trace output.
	TraceLogger *TraceLogger
	// Config is the full runtime configuration used for child prompt and model resolution.
	Config config.Config
	// ProviderFactory builds providers for resolved child models when one is required.
	ProviderFactory func(provider.ResolvedModel) (provider.Provider, error)
	// HTTPClient is used for model discovery when resolving child models.
	HTTPClient *http.Client
	// Searcher provides the web search backend when available.
	Searcher web.Searcher
}

// BuildDelegateRegistry assembles the active registry for a run, cloning the base registry
// and registering advisor, delegation, and specialized sub-agent tools when enabled.
func BuildDelegateRegistry(deps DelegateDeps) (*tool.Registry, error) {
	if !deps.SubAgentCfg.Enabled && !deps.AdvisorCfg.Enabled {
		return deps.BaseRegistry, nil
	}

	cloned := deps.BaseRegistry.Clone()

	if deps.AdvisorCfg.Enabled {
		advisorResolved, err := provider.ResolveWithDiscovery(deps.Config, deps.AdvisorCfg.Model, deps.HTTPClient)
		if err != nil {
			return nil, fmt.Errorf("resolve advisor model %q: %w", deps.AdvisorCfg.Model, err)
		}
		advisorProvider, err := resolveToolProvider(deps.Provider, deps.ResolvedModel, advisorResolved, deps.ProviderFactory)
		if err != nil {
			return nil, fmt.Errorf("build advisor provider for %q: %w", deps.AdvisorCfg.Model, err)
		}
		cloned.Register(advisor.ToolDef(advisor.NewHandler(advisor.HandlerDeps{
			Provider: advisorProvider,
			Model:    advisorResolved,
			Events:   deps.Events,
			Config: advisor.Config{
				MaxUsesPerRun: deps.AdvisorCfg.MaxUsesPerRun,
				MaxTokens:     deps.AdvisorCfg.MaxTokens,
			},
		})))
	}

	if !deps.SubAgentCfg.Enabled {
		return cloned, nil
	}

	mt := deps.MaxTokens
	store := NewSessionStore()

	// extendedBase is used as ParentReg for child agents.
	// fetch_url is always present (via Builtins). Conditionally add web_search.
	extendedBase := deps.BaseRegistry.Clone()
	if deps.Searcher != nil {
		extendedBase.Register(builtin.NewWebSearchTool(deps.Searcher))
	}

	delegateDeps := DelegateHandlerDeps{
		Provider:             deps.Provider,
		ParentReg:            extendedBase,
		SubAgentCfg:          deps.SubAgentCfg,
		Events:               deps.Events,
		Runner:               agent.NewRunner(),
		WorkDir:              deps.WorkDir,
		HomeDir:              deps.HomeDir,
		ProjectContextConfig: deps.Config.ProjectContext,
		ResolvedModel:        deps.ResolvedModel,
		MaxTokens:            &mt,
		StreamingPreferred:   deps.StreamingPreferred,
		CaveHuman:            deps.Config.CaveHuman,
		TraceLogger:          deps.TraceLogger,
		SessionStore:         store,
	}

	// Register the generic delegate tool.
	handler := NewDelegateHandler(delegateDeps)
	cloned.Register(DelegateToolDef(handler))
	cloned.Register(FollowUpToolDef(NewFollowUpHandler(delegateDeps)))

	// Conditionally expose web_search to the parent model.
	if deps.Searcher != nil {
		cloned.Register(builtin.NewWebSearchTool(deps.Searcher))
	}

	// Build a model resolver for specialized tools to use per-type model aliases.
	modelResolver := func(alias string) (provider.Provider, provider.ResolvedModel, error) {
		resolved, err := provider.ResolveWithDiscovery(deps.Config, alias, deps.HTTPClient)
		if err != nil {
			return nil, provider.ResolvedModel{}, err
		}
		if deps.ProviderFactory == nil {
			return deps.Provider, resolved, nil
		}
		p, err := deps.ProviderFactory(resolved)
		if err != nil {
			return nil, provider.ResolvedModel{}, err
		}
		return p, resolved, nil
	}

	// Register a specialized tool for each agent type.
	// Skip research agent when no search backend is configured.
	specializedDeps := SpecializedToolDeps{
		DelegateHandlerDeps: delegateDeps,
		ModelResolver:       modelResolver,
	}
	var excludeTypes []AgentType
	if deps.Searcher == nil {
		excludeTypes = []AgentType{AgentTypeResearch}
	}
	for _, def := range AllSpecializedToolDefs(specializedDeps, excludeTypes) {
		cloned.Register(def)
	}

	return cloned, nil
}

func resolveToolProvider(current provider.Provider, currentModel provider.ResolvedModel, target provider.ResolvedModel, providerFactory func(provider.ResolvedModel) (provider.Provider, error)) (provider.Provider, error) {
	if providerFactory != nil {
		return providerFactory(target)
	}
	if current != nil && currentModel.ProviderAlias == target.ProviderAlias && currentModel.EffectiveProviderType == target.EffectiveProviderType {
		return current, nil
	}
	return nil, fmt.Errorf("provider factory is required for advisor model %q", target.Alias)
}
