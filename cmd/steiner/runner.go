package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"

	"github.com/deepnoodle-ai/wonton/web"

	"github.com/luispabon/steiner/internal/advisor"
	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/oneshot"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

type cliRunner struct {
	runtime            cliRuntime
	approver           tool.ApprovalResponder
	maxTurns           int
	runMode            string
	streamingPreferred bool
	currentModel       func() config.ModelConfig
	currentAlias       func() string
}

type runResult = oneshot.RunResult

func (r cliRunner) Run(ctx context.Context, conversation []agent.Message, skillNames []string, steerCh <-chan string) (runResult, error) {
	runCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	return r.run(runCtx, conversation, skillNames, steerCh)
}

// RunPhase executes a single phase run without installing a signal handler.
// The caller owns cancellation and interrupt handling for phase orchestration.
func (r cliRunner) RunPhase(ctx context.Context, conversation []agent.Message, skillNames []string, steerCh <-chan string) (runResult, error) {
	resetFallbackModelWarnings()
	return r.run(ctx, conversation, skillNames, steerCh)
}

func (r cliRunner) run(ctx context.Context, conversation []agent.Message, skillNames []string, steerCh <-chan string) (runResult, error) {
	setup, err := r.prepareRun(conversation, skillNames)
	if err != nil {
		return runResult{}, err
	}
	r.runtime.events.Emit(output.NewRunStartedEvent(
		setup.runMode,
		setup.resolvedModel.BackendModelID,
		lastUserPrompt(conversation),
		r.maxTurns,
		r.runtime.cfg.Limits.MaxTokens,
	))

	events, diagnostics := retainDiagnosticEvents(r.runtime.events)
	searcher, _ := builtin.NewSearchBackend(r.runtime.cfg.Search)
	activeRegistry, err := buildActiveRegistry(r.runtime.registry, r.runtime.cfg.SubAgent, r.runtime.cfg.Advisor, setup.provider, events, r.runtime.workDir, r.runtime.homeDir, setup.resolvedModel, setup.resolvedModel.EffectiveLimits.MaxOutputTokens, r.streamingPreferred, r.runtime.delegationLogger, r.runtime.cfg, r.runtime.providerFactory, r.runtime.httpClient, searcher)
	if err != nil {
		return runResult{}, err
	}
	runner := agent.NewRunner()
	state, err := runner.Run(ctx, buildRunRequest(r, setup, activeRegistry, events, steerCh))
	reason := string(state.StopReason)
	if reason == "" && err != nil {
		reason = string(agent.StopReasonError)
	}
	r.runtime.events.Emit(output.NewRunFinishedEvent(
		state.TurnCount,
		reason,
		lastAssistantReply(state.Conversation),
		"",
		err,
	))
	if err != nil {
		return runResult{Conversation: state.Conversation}, err
	}

	return runResult{
		Conversation:    state.Conversation,
		Reply:           lastAssistantReply(state.Conversation),
		Diagnostics:     cloneEvents(*diagnostics),
		WorkflowHandoff: state.WorkflowHandoff,
		Lineage:         state.Lineage,
	}, nil
}

func lastUserPrompt(messages []agent.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == agent.MessageRoleUser {
			return strings.TrimSpace(messages[i].Content)
		}
	}
	return ""
}

func toProviderConversation(messages []agent.Message) []provider.Message {
	return agent.ToReplaySafeProviderMessages(messages)
}

func cloneEvents(events []output.Event) []output.Event {
	if len(events) == 0 {
		return nil
	}
	out := make([]output.Event, len(events))
	for i, event := range events {
		out[i] = cloneEvent(event)
	}
	return out
}

func cloneEvent(event output.Event) output.Event {
	cloned := event
	switch payload := event.Payload.(type) {
	case output.ContextDiagnosticsEvent:
		payload.Notes = append([]string(nil), payload.Notes...)
		cloned.Payload = payload
	case output.StopReasonEvent:
		cloned.Payload = payload
	}
	return cloned
}

func isRetainedDiagnosticEvent(event output.Event) bool {
	switch event.Type {
	case output.EventTypeContextDiagnostics, output.EventTypeStopReason:
		return true
	default:
		return false
	}
}

type loggingProvider struct {
	inner provider.Provider
	sink  output.EventSink
}

func (p loggingProvider) ChatCompletion(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	resp, err := p.inner.ChatCompletion(ctx, req)
	if p.sink != nil {
		p.sink.Emit(output.NewAPIResponseEvent(resp.Message, resp.Usage, resp.FinishReason, err))
	}
	return resp, err
}

func (p loggingProvider) StreamChatCompletion(ctx context.Context, req provider.ChatRequest) (<-chan provider.ChatChunk, error) {
	return p.inner.StreamChatCompletion(ctx, req)
}

func (p loggingProvider) SupportsUsageStats() bool {
	return p.inner.SupportsUsageStats()
}

// buildActiveRegistry returns the registry to use for a run. When sub-agent
// delegation is enabled the base registry is cloned and the delegate tool is
// registered into the clone so that the base registry stays clean.
//
// An extended base registry is also built that includes real tools (web_search
// when a searcher is available, fetch_url always via Builtins) so child
// registries can filter them in via their per-type allowlists.
func buildActiveRegistry(base *tool.Registry, subAgentCfg config.SubAgentConfig, advisorCfg config.AdvisorConfig, prov provider.Provider, events output.EventSink, workDir, homeDir string, rm provider.ResolvedModel, maxTokens int, streamingPreferred bool, traceLogger *delegation.TraceLogger, cfg config.Config, providerFactory func(provider.ResolvedModel) (provider.Provider, error), httpClient *http.Client, searcher web.Searcher) (*tool.Registry, error) {
	if !subAgentCfg.Enabled && !advisorCfg.Enabled {
		return base, nil
	}
	cloned := base.Clone()

	if advisorCfg.Enabled {
		advisorResolved, err := provider.ResolveWithDiscovery(cfg, advisorCfg.Model, httpClient)
		if err != nil {
			return nil, fmt.Errorf("resolve advisor model %q: %w", advisorCfg.Model, err)
		}
		advisorProvider, err := resolveToolProvider(prov, rm, advisorResolved, providerFactory)
		if err != nil {
			return nil, fmt.Errorf("build advisor provider for %q: %w", advisorCfg.Model, err)
		}
		cloned.Register(advisor.ToolDef(advisor.NewHandler(advisor.HandlerDeps{
			Provider: advisorProvider,
			Model:    advisorResolved,
			Events:   events,
			Config: advisor.Config{
				MaxUsesPerRun: advisorCfg.MaxUsesPerRun,
				MaxTokens:     advisorCfg.MaxTokens,
			},
		})))
	}

	if !subAgentCfg.Enabled {
		return cloned, nil
	}
	mt := maxTokens
	store := delegation.NewSessionStore()

	// extendedBase is used as ParentReg for child agents.
	// fetch_url is always present (via Builtins). Conditionally add web_search.
	extendedBase := base.Clone()
	if searcher != nil {
		extendedBase.Register(builtin.NewWebSearchTool(searcher))
	}

	delegateDeps := delegation.DelegateHandlerDeps{
		Provider:             prov,
		ParentReg:            extendedBase,
		SubAgentCfg:          subAgentCfg,
		Events:               events,
		Runner:               agent.NewRunner(),
		WorkDir:              workDir,
		HomeDir:              homeDir,
		ProjectContextConfig: cfg.ProjectContext,
		ResolvedModel:        rm,
		MaxTokens:            &mt,
		StreamingPreferred:   streamingPreferred,
		CaveHuman:            cfg.CaveHuman,
		TraceLogger:          traceLogger,
		SessionStore:         store,
	}

	// Register the generic delegate tool.
	handler := delegation.NewDelegateHandler(delegateDeps)
	cloned.Register(delegation.DelegateToolDef(handler))
	cloned.Register(delegation.FollowUpToolDef(delegation.NewFollowUpHandler(delegateDeps)))

	// Conditionally expose web_search to the parent model.
	if searcher != nil {
		cloned.Register(builtin.NewWebSearchTool(searcher))
	}

	// Build a model resolver for specialized tools to use per-type model aliases.
	modelResolver := func(alias string) (provider.Provider, provider.ResolvedModel, error) {
		resolved, err := provider.ResolveWithDiscovery(cfg, alias, httpClient)
		if err != nil {
			return nil, provider.ResolvedModel{}, err
		}
		if providerFactory == nil {
			return prov, resolved, nil
		}
		p, err := providerFactory(resolved)
		if err != nil {
			return nil, provider.ResolvedModel{}, err
		}
		return p, resolved, nil
	}

	// Register a specialized tool for each agent type.
	// Skip research agent when no search backend is configured.
	specializedDeps := delegation.SpecializedToolDeps{
		DelegateHandlerDeps: delegateDeps,
		ModelResolver:       modelResolver,
	}
	var excludeTypes []delegation.AgentType
	if searcher == nil {
		excludeTypes = []delegation.AgentType{delegation.AgentTypeResearch}
	}
	for _, def := range delegation.AllSpecializedToolDefs(specializedDeps, excludeTypes) {
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
