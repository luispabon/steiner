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
	"github.com/luispabon/steiner/internal/usagestats"
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
	// UsageRecorder is the singleton recorder shared across the process for cache-hit-rate tracking.
	UsageRecorder *usagestats.Recorder
	// SessionStore holds child sessions across turns for follow_up resumption.
	// When nil, BuildDelegateRegistry creates a fresh per-call store (backward-compatible).
	// Callers that need cross-turn follow_up support should provide a long-lived store.
	SessionStore *SessionStore
	// ExtraAllowedTools provides per-agent-type extra tool names that should be
	// included in child registries beyond the built-in allowlists. Keys are agent
	// types; values are sorted, deduplicated registered tool names. Nil or empty
	// map grants no extra tools.
	ExtraAllowedTools map[AgentType][]string
	// ImageStore provides image lookup for the vision sub-agent tool.
	// When nil or when no vision model is configured, the vision tool is not registered.
	ImageStore *agent.ImageStore
}

// BuildDelegateRegistry assembles the active registry for a run, cloning the base registry
// and registering advisor, delegation, and specialized sub-agent tools when enabled.
func BuildDelegateRegistry(deps DelegateDeps) (*tool.Registry, error) {
	if !deps.SubAgentCfg.Enabled && !deps.AdvisorCfg.Enabled {
		return deps.BaseRegistry, nil
	}

	cloned := deps.BaseRegistry.Clone()

	if deps.AdvisorCfg.Enabled {
		advisorResolved, err := provider.ResolveWithDiscovery(deps.Config, deps.Config.Models.Advisor, deps.HTTPClient)
		if err != nil {
			return nil, fmt.Errorf("resolve advisor model %q: %w", deps.Config.Models.Advisor, err)
		}
		if deps.AdvisorCfg.Timeout != nil {
			advisorResolved.ProviderConfig.Timeout = *deps.AdvisorCfg.Timeout
		}
		advisorProvider, err := resolveToolProvider(deps.Provider, deps.ResolvedModel, advisorResolved, deps.ProviderFactory)
		if err != nil {
			return nil, fmt.Errorf("build advisor provider for %q: %w", deps.Config.Models.Advisor, err)
		}
		advisorPolicy := tool.NewPathPolicy(deps.WorkDir, deps.Config.Paths)
		cloned.Register(advisor.ToolDef(advisor.NewHandler(advisor.HandlerDeps{
			Provider: advisorProvider,
			Model:    advisorResolved,
			Events:   deps.Events,
			Config: advisor.Config{
				MaxUsesPerRun: deps.AdvisorCfg.MaxUsesPerRun,
				MaxTokens:     deps.AdvisorCfg.MaxTokens,
			},
			UsageRecorder: deps.UsageRecorder,
			WorkDir:       deps.WorkDir,
			PathPolicy:    &advisorPolicy,
		})))
	}

	if !deps.SubAgentCfg.Enabled {
		return cloned, nil
	}

	mt := deps.MaxTokens
	store := deps.SessionStore
	if store == nil {
		store = NewSessionStore()
	}

	// extendedBase is used as ParentReg for child agents.
	// fetch_url is always present (via Builtins). Conditionally add web_search.
	extendedBase := deps.BaseRegistry.Clone()
	if deps.Searcher != nil {
		extendedBase.Register(builtin.NewWebSearchTool(deps.Searcher))
	}

	subAgentDeps := SubAgentHandlerDeps{
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
		ExtraAllowedTools:    deps.ExtraAllowedTools,
		UsageRecorder:        deps.UsageRecorder,
	}

	// Register the follow_up tool.
	cloned.Register(FollowUpToolDef(NewFollowUpHandler(subAgentDeps)))

	// Conditionally expose web_search to the parent model.
	if deps.Searcher != nil {
		cloned.Register(builtin.NewWebSearchTool(deps.Searcher))
	}

	// Build a model resolver for specialized tools to use per-type model aliases.
	modelResolver := buildModelResolver(deps)

	// Register a specialized tool for each agent type.
	// Skip research agent when no search backend is configured.
	// Skip vision agent when no vision model is configured.
	specializedDeps := SpecializedToolDeps{
		SubAgentHandlerDeps: subAgentDeps,
		ModelResolver:       modelResolver,
		ImageStore:          deps.ImageStore,
		AgentModels:         deps.Config.Models.SubAgents,
	}
	var excludeTypes []AgentType
	if deps.Searcher == nil {
		excludeTypes = append(excludeTypes, AgentTypeResearch)
	}
	if deps.Config.Models.SubAgents[string(AgentTypeVision)] == "" || deps.ImageStore == nil {
		excludeTypes = append(excludeTypes, AgentTypeVision)
	}
	for _, def := range AllSpecializedToolDefs(specializedDeps, excludeTypes) {
		cloned.Register(def)
	}

	return cloned, nil
}

// buildModelResolver returns a function that resolves a model alias to its provider and model metadata.
func buildModelResolver(deps DelegateDeps) func(string) (provider.Provider, provider.ResolvedModel, error) {
	return func(alias string) (provider.Provider, provider.ResolvedModel, error) {
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
