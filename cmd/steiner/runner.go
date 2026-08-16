package main

import (
	"context"
	"os"
	"os/signal"
	"strings"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/oneshot"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

type cliRunner struct {
	runtime                  cliRuntime
	approver                 tool.ApprovalResponder
	maxTurns                 int
	runMode                  string
	streamingPreferred       bool
	currentModel             func() config.ModelConfig
	currentAlias             func() string
	currentReasoningOverride func() provider.ReasoningOverride
	promptCacheKey           string
	modeGetterFunc           func() config.ExecutionMode
	phasePrompt              string
	projectAgentsPath        string
	workflowMode             prompt.WorkflowMode
}

type runResult = oneshot.RunResult

func (r cliRunner) Run(ctx context.Context, conversation []agent.Message, skillNames []string, drainSteers func() []agent.SteerMessage) (runResult, error) {
	runCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	return r.run(runCtx, conversation, skillNames, drainSteers)
}

// RunPhase executes a single phase run without installing a signal handler.
// The caller owns cancellation and interrupt handling for phase orchestration.
func (r cliRunner) RunPhase(ctx context.Context, conversation []agent.Message, skillNames []string, drainSteers func() []agent.SteerMessage) (runResult, error) {
	resetFallbackModelWarnings()
	return r.run(ctx, conversation, skillNames, drainSteers)
}

func (r cliRunner) run(ctx context.Context, conversation []agent.Message, skillNames []string, drainSteers func() []agent.SteerMessage) (runResult, error) {
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
	// The MCP exposure projection is derived at composition time from the
	// completed registered set: in interactive mode the session runner has
	// already WaitInit'ed and re-registered late MCP defs before this run, so
	// the projection always sees the final tool list.
	exposure := BuildMCPExposure(r.runtime.registry.Definitions(), r.runtime.cfg.MCP.Servers)
	activeRegistry, err := delegation.BuildDelegateRegistry(delegation.DelegateDeps{
		BaseRegistry:          r.runtime.registry,
		SubAgentCfg:           r.runtime.cfg.SubAgent,
		AdvisorCfg:            r.runtime.cfg.Advisor,
		Provider:              setup.provider,
		Events:                events,
		WorkDir:               r.runtime.workDir,
		HomeDir:               r.runtime.homeDir,
		ResolvedModel:         setup.resolvedModel,
		MaxTokens:             setup.resolvedModel.EffectiveLimits.MaxOutputTokens,
		StreamingPreferred:    r.streamingPreferred,
		TraceLogger:           r.runtime.delegationLogger,
		Config:                r.runtime.cfg,
		ProviderFactory:       r.runtime.providerFactory,
		HTTPClient:            r.runtime.httpClient,
		Searcher:              searcher,
		UsageRecorder:         r.runtime.usageRecorder,
		SessionStore:          r.runtime.delegationSessionStore,
		ImageStore:            r.runtime.imageStore,
		ExtraAllowedTools:     exposure,
		CacheKeyStore:         r.runtime.delegationCacheKeyStore,
		SandboxEnabled:        r.runtime.sandbox != nil && r.runtime.sandbox.Enabled(),
		SandboxWritableMounts: sandboxWritableMounts(r.runtime.cfg.Sandbox),
	})
	if err != nil {
		return runResult{}, err
	}
	runner := agent.NewRunner()
	state, err := runner.Run(ctx, buildRunRequest(r, setup, activeRegistry, events, drainSteers))
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
