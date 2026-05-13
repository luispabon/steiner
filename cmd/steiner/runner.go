package main

import (
	"context"
	"os"
	"os/signal"
	"strings"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

type cliRunner struct {
	runtime            cliRuntime
	approver           tool.ApprovalResponder
	maxTurns           int
	runMode            string
	streamingPreferred bool
	currentModel       func() config.ModelConfig
}

type runResult struct {
	Conversation []agent.Message
	Reply        string
	Diagnostics  []output.Event
}

func (r cliRunner) Run(ctx context.Context, conversation []agent.Message, skillNames []string) (runResult, error) {
	runCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	setup, err := r.prepareRun(conversation, skillNames)
	if err != nil {
		return runResult{}, err
	}
	r.runtime.events.Emit(output.NewRunStartedEvent(
		setup.runMode,
		setup.selected.Model,
		lastUserPrompt(conversation),
		r.maxTurns,
		r.runtime.cfg.Limits.MaxTokens,
	))

	events, diagnostics := retainDiagnosticEvents(r.runtime.events)
	activeRegistry := buildActiveRegistry(r.runtime.registry, r.runtime.cfg.SubAgent, setup.provider, events, r.runtime.workDir, setup.selected.ExtraParams, setup.selected.Thinking, setup.modelBudget, setup.selected.Model, setup.selected.MaxCompletionTokens, r.streamingPreferred)
	runner := agent.NewRunner()
	state, err := runner.Run(runCtx, buildRunRequest(r, conversation, setup, activeRegistry, events))
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
		return runResult{}, err
	}

	return runResult{
		Conversation: state.Conversation,
		Reply:        lastAssistantReply(state.Conversation),
		Diagnostics:  cloneEvents(*diagnostics),
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
	if len(messages) == 0 {
		return nil
	}
	out := make([]provider.Message, 0, len(messages))
	for _, message := range messages {
		wire := provider.Message{
			Role:       provider.MessageRole(message.Role),
			Content:    message.Content,
			Name:       message.Name,
			ToolCallID: message.ToolCallID,
			Turn:       message.Turn,
		}
		if len(message.ToolCalls) > 0 {
			wire.ToolCalls = make([]provider.ToolCall, 0, len(message.ToolCalls))
			for _, call := range message.ToolCalls {
				wire.ToolCalls = append(wire.ToolCalls, provider.ToolCall{
					ID:        call.ID,
					Name:      call.Name,
					Arguments: tool.CloneJSONMap(call.Arguments),
				})
			}
		}
		out = append(out, wire)
	}
	return out
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
func buildActiveRegistry(base *tool.Registry, subAgentCfg config.SubAgentConfig, prov provider.Provider, events output.EventSink, workDir string, extraParams map[string]any, thinking config.ThinkingConfig, modelBudget prompt.ModelTokenBudget, model string, maxTokens int, streamingPreferred bool) *tool.Registry {
	if !subAgentCfg.Enabled {
		return base
	}
	cloned := base.Clone()
	mt := maxTokens
	handler := delegation.NewDelegateHandler(delegation.DelegateHandlerDeps{
		Provider:           prov,
		ParentReg:          base,
		SubAgentCfg:        subAgentCfg,
		Events:             events,
		Runner:             agent.NewRunner(),
		WorkDir:            workDir,
		ExtraParams:        extraParams,
		Thinking:           thinking,
		ModelBudget:        modelBudget,
		Model:              model,
		MaxTokens:          &mt,
		StreamingPreferred: streamingPreferred,
	})
	cloned.Register(delegation.DelegateToolDef(handler))
	return cloned
}
