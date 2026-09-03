package delegation

import (
	"fmt"
	"net/http"
	"strings"

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
	// SessionID identifies the parent session or oneshot run, used to group this
	// run's tool-call trace files under .steiner/traces/<SessionID>/. Empty means
	// no real session ID is available; the trace writer falls back to a random
	// process-scoped stand-in.
	SessionID string
	// ResolvedModel is the parent's config-resolved model metadata for the current
	// run, used as the fallback model for sub-agents without their own per-type
	// model alias. It must not carry session-time runtime overrides such as
	// reasoning-effort choices (issue #543).
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
	ProviderFactory func(provider.ResolvedModel, string) (provider.Provider, error)
	// HTTPClient is used for model discovery when resolving child models.
	HTTPClient *http.Client
	// Searcher provides the web search backend when available.
	Searcher web.Searcher
	// SandboxTmpDir is the path to the sandbox temporary directory inside the
	// project root. When non-empty, child executors inherit it so that /tmp
	// path rewriting works for tools like mutate. Derived from the parent's
	// runtime sandbox at the composition root in cmd/steiner.
	SandboxTmpDir string
	// SandboxEnabled reports whether the parent sandbox is active. Forwarded as
	// a plain value into child prompts so their system preamble renders the same
	// sandbox section as the parent. Derived from the parent's runtime sandbox
	// at the composition root in cmd/steiner.
	SandboxEnabled bool
	// SandboxWritableMounts lists host paths mounted writable in the sandbox,
	// forwarded to child prompts so their system preamble renders the same
	// sandbox section as the parent. Derived from the parent's config at the
	// composition root in cmd/steiner.
	SandboxWritableMounts []string
	// Sandbox is the parent's sandbox wrapper, threaded to child executors so
	// they sandbox commands identically to the parent. Derived from the
	// parent's runtime sandbox at the composition root in cmd/steiner; must
	// not be nil (tool.Unsandboxed{} when sandboxing is off).
	Sandbox tool.SandboxWrapper
	// ModeGetter returns the current execution mode. When non-nil, threaded to
	// child executors so they inherit the parent's live execution mode for
	// plan-mode restrictions (path policy and sandbox readOnlyProject).
	ModeGetter func() config.ExecutionMode
	// UsageRecorder is the singleton recorder shared across the process for cache-hit-rate tracking.
	UsageRecorder *usagestats.Recorder
	// SessionStore holds child sessions across turns for follow_up resumption.
	// When nil, BuildDelegateRegistry creates a fresh per-call store (backward-compatible).
	// Callers that need cross-turn follow_up support should provide a long-lived store.
	SessionStore *SessionStore
	// ActiveController tracks and cancels active child delegations.
	ActiveController *ActiveController
	// ExtraAllowedTools provides per-agent-type extra tool names that should be
	// included in child registries beyond the built-in allowlists. Keys are agent
	// types; values are sorted, deduplicated registered tool names. Nil or empty
	// map grants no extra tools.
	ExtraAllowedTools map[AgentType][]string
	// ImageStore provides image lookup for the vision sub-agent tool.
	// When nil or when no vision model is configured, the vision tool is not registered.
	ImageStore *agent.ImageStore
	// CacheKeyStore is the singleton store shared across the process for
	// cache-key reuse. Nil means no reuse: each delegation mints a fresh key.
	CacheKeyStore *CacheKeyStore
	// AdvisorState is the singleton advisor use-counter shared across the
	// process, so advisor.Config.MaxUsesPerRun is enforced for the whole
	// session instead of resetting every time BuildDelegateRegistry runs
	// (once per turn). Nil means no reuse: each call gets a private counter,
	// so the cap is enforced per BuildDelegateRegistry call only.
	AdvisorState *advisor.SharedState
	// AdvisorBudgetStore provides per-child advisor budgets, keyed by agent ID.
	// When non-nil and advisor is enabled, each code/review/evaluate child gets
	// its own SharedState from the store, surviving follow_up resumptions under
	// the same agent ID. Nil means no child advisor access.
	AdvisorBudgetStore *AdvisorBudgetStore
}

// advisorRuntime holds the resolved advisor provider, model, and configuration.
type advisorRuntime struct {
	provider   provider.Provider
	model      provider.ResolvedModel
	events     output.EventSink
	recorder   *usagestats.Recorder
	workDir    string
	pathPolicy tool.PathPolicy
	cacheKey   string
	maxTokens  *int
}

// newAdvisorRuntime resolves the advisor model and provider, returning the runtime
// state needed to mint advisor tool definitions. It contains the resolution logic
// from registerAdvisorTool up to and including cache-key resolution.
func newAdvisorRuntime(deps DelegateDeps) (advisorRuntime, error) {
	advisorAlias := strings.TrimSpace(deps.Config.Models.Effective.Advisor)
	if advisorAlias == "" {
		advisorAlias = strings.TrimSpace(deps.Config.Models.Effective.DefaultModel)
	}
	advisorResolved, err := provider.ResolveWithDiscovery(deps.Config, advisorAlias, deps.HTTPClient)
	if err != nil {
		return advisorRuntime{}, fmt.Errorf("resolve advisor model %q: %w", advisorAlias, err)
	}
	if deps.AdvisorCfg.Timeout != nil {
		advisorResolved.ProviderConfig.Timeout = *deps.AdvisorCfg.Timeout
	}
	advisorProvider, err := resolveToolProvider(deps.Provider, deps.ResolvedModel, advisorResolved, deps.ProviderFactory, deps.SessionID)
	if err != nil {
		return advisorRuntime{}, fmt.Errorf("build advisor provider for %q: %w", advisorAlias, err)
	}
	advisorPolicy := tool.NewPathPolicy(deps.WorkDir, deps.Config.Paths)

	return advisorRuntime{
		provider:   advisorProvider,
		model:      advisorResolved,
		events:     deps.Events,
		recorder:   deps.UsageRecorder,
		workDir:    deps.WorkDir,
		pathPolicy: advisorPolicy,
		cacheKey:   resolveAdvisorCacheKey(deps.CacheKeyStore),
		maxTokens:  deps.AdvisorCfg.MaxTokens,
	}, nil
}

// toolDef mints a tool definition for the advisor bound to a specific budget and state.
func (r advisorRuntime) toolDef(maxUses int, state *advisor.SharedState) tool.ToolDef {
	return advisor.ToolDef(advisor.NewHandler(advisor.HandlerDeps{
		Provider: r.provider,
		Model:    r.model,
		Events:   r.events,
		Config: advisor.Config{
			MaxUsesPerRun: maxUses,
			MaxTokens:     r.maxTokens,
		},
		UsageRecorder: r.recorder,
		WorkDir:       r.workDir,
		PathPolicy:    &r.pathPolicy,
		CacheKey:      r.cacheKey,
		SharedState:   state,
	}))
}

// resolveAdvisorCacheKey returns the advisor's cache key from store when
// provided, falling back to a freshly minted key when store is nil or fails
// to mint one.
func resolveAdvisorCacheKey(store *CacheKeyStore) string {
	return cacheKeyOrMint(store, cacheKeyAgentTypeAdvisor)
}

// buildAdvisorTools registers parent and child advisor tools when enabled,
// returning an advisorForChild factory if child advisor access is configured.
func buildAdvisorTools(cloned *tool.Registry, deps DelegateDeps) (func(string) (tool.ToolDef, bool), error) {
	if !deps.AdvisorCfg.Enabled {
		return nil, nil
	}
	advRuntime, err := newAdvisorRuntime(deps)
	if err != nil {
		return nil, err
	}
	cloned.Register(advRuntime.toolDef(deps.AdvisorCfg.MaxUsesPerRun, deps.AdvisorState))

	if deps.AdvisorBudgetStore == nil {
		return nil, nil
	}
	advisorForChild := func(agentID string) (tool.ToolDef, bool) {
		if agentID == "" {
			return tool.ToolDef{}, false
		}
		state := deps.AdvisorBudgetStore.StateFor(agentID)
		scopedEvents := withAgentScope(agentID, "", deps.Events)
		scopedRuntime := advisorRuntime{
			provider:   advRuntime.provider,
			model:      advRuntime.model,
			events:     scopedEvents,
			recorder:   advRuntime.recorder,
			workDir:    advRuntime.workDir,
			pathPolicy: advRuntime.pathPolicy,
			cacheKey:   advRuntime.cacheKey,
			maxTokens:  advRuntime.maxTokens,
		}
		return scopedRuntime.toolDef(deps.AdvisorCfg.MaxUsesPerSubAgent, state), true
	}
	return advisorForChild, nil
}

// BuildDelegateRegistry assembles the active registry for a run, cloning the base registry
// and registering advisor, delegation, and specialized sub-agent tools when enabled.
func BuildDelegateRegistry(deps DelegateDeps) (*tool.Registry, error) {
	if !deps.SubAgentCfg.Enabled && !deps.AdvisorCfg.Enabled {
		return deps.BaseRegistry, nil
	}

	cloned := deps.BaseRegistry.Clone()

	advisorForChild, err := buildAdvisorTools(cloned, deps)
	if err != nil {
		return nil, err
	}

	if !deps.SubAgentCfg.Enabled {
		return cloned, nil
	}
	if deps.ActiveController == nil {
		deps.ActiveController = NewActiveController()
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
		Provider:              deps.Provider,
		ParentReg:             extendedBase,
		SubAgentCfg:           deps.SubAgentCfg,
		Events:                deps.Events,
		Runner:                agent.NewRunner(),
		WorkDir:               deps.WorkDir,
		HomeDir:               deps.HomeDir,
		SessionID:             deps.SessionID,
		ProjectContextConfig:  deps.Config.ProjectContext,
		ResolvedModel:         deps.ResolvedModel,
		MaxTokens:             &mt,
		StreamingPreferred:    deps.StreamingPreferred,
		CaveHuman:             deps.Config.CaveHuman,
		TraceLogger:           deps.TraceLogger,
		SessionStore:          store,
		ActiveController:      deps.ActiveController,
		ExtraAllowedTools:     deps.ExtraAllowedTools,
		UsageRecorder:         deps.UsageRecorder,
		SandboxTmpDir:         deps.SandboxTmpDir,
		SandboxEnabled:        deps.SandboxEnabled,
		SandboxWritableMounts: deps.SandboxWritableMounts,
		Sandbox:               deps.Sandbox,
		ModeGetter:            deps.ModeGetter,
		CacheKeyStore:         deps.CacheKeyStore,
		MaxParallelTools:      deps.Config.Limits.MaxParallelTools,
		Limits:                deps.Config.Limits,
		Paths:                 deps.Config.Paths,
		ContextManagement:     deps.Config.ContextManagement,
		AdvisorForChild:       advisorForChild,
		AdvisorSubAgentBudget: deps.AdvisorCfg.MaxUsesPerSubAgent,
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
		AgentModels:         deps.Config.Models.Effective.SubAgents,
		DefaultModel:        deps.Config.Models.Effective.DefaultModel,
	}
	var excludeTypes []AgentType
	if deps.Searcher == nil {
		excludeTypes = append(excludeTypes, AgentTypeResearch)
	}
	if deps.Config.Models.Effective.SubAgents[string(AgentTypeVision)] == "" || deps.ImageStore == nil {
		excludeTypes = append(excludeTypes, AgentTypeVision)
	}
	cloned.Register(SubAgentToolDef(specializedDeps, excludeTypes))

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
		p, err := deps.ProviderFactory(resolved, deps.SessionID)
		if err != nil {
			return nil, provider.ResolvedModel{}, err
		}
		return p, resolved, nil
	}
}

func resolveToolProvider(current provider.Provider, currentModel provider.ResolvedModel, target provider.ResolvedModel, providerFactory func(provider.ResolvedModel, string) (provider.Provider, error), sessionID string) (provider.Provider, error) {
	if providerFactory != nil {
		return providerFactory(target, sessionID)
	}
	if current != nil && currentModel.ProviderAlias == target.ProviderAlias && currentModel.EffectiveProviderType == target.EffectiveProviderType {
		return current, nil
	}
	return nil, fmt.Errorf("provider factory is required for advisor model %q", target.Alias)
}
