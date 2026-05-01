package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/luispabon/steiner/internal/agent"
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
}

type runResult struct {
	Conversation []agent.Message
	Reply        string
	Diagnostics  []output.Event
}

func (r cliRunner) Run(ctx context.Context, conversation []agent.Message, skillNames []string) (runResult, error) {
	runCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	selected, err := selectedModelConfig(r.runtime.cfg)
	if err != nil {
		return runResult{}, err
	}

	prov := r.runtime.provider
	if r.runtime.providerFactory != nil {
		prov, err = r.runtime.providerFactory(r.runtime.cfg.Model)
		if err != nil {
			return runResult{}, err
		}
	}
	if prov == nil {
		return runResult{}, fmt.Errorf("provider is required")
	}
	prov = loggingProvider{
		inner: prov,
		sink:  r.runtime.events,
	}

	modelBudget := prompt.ModelTokenBudget{
		ContextSize:         selected.ContextSize,
		MaxCompletionTokens: selected.MaxCompletionTokens,
		SafetyMarginTokens:  selected.Compaction.SafetyMarginTokens,
		SummaryMaxTokens:    selected.Compaction.SummaryMaxTokens,
	}

	assembly := prompt.AssemblyOptions{
		HomeDir:                   r.runtime.homeDir,
		ProjectRoot:               r.runtime.workDir,
		SkillsRoot:                prompt.DefaultSkillsRoot(r.runtime.homeDir),
		SkillNames:                append([]string(nil), skillNames...),
		ModelBudget:               modelBudget,
		PromptOverrides:           selected.Prompts,
		ProjectContextBudgetBytes: r.runtime.cfg.ProjectContext.MaxTokens,
		ProjectContextExtraFiles:  append([]string(nil), r.runtime.cfg.ProjectContext.ExtraFiles...),
		ProjectContextIgnoreFiles: append([]string(nil), r.runtime.cfg.ProjectContext.IgnoreFiles...),
		Conversation:              toProviderConversation(conversation),
	}

	runMode := strings.TrimSpace(r.runMode)
	if runMode == "" {
		runMode = "exec"
	}
	r.runtime.events.Emit(output.NewRunStartedEvent(
		runMode,
		selected.Model,
		lastUserPrompt(conversation),
		r.maxTurns,
		r.runtime.cfg.Limits.MaxTokens,
	))

	var diagnostics []output.Event
	events := output.NewMultiSink(
		r.runtime.events,
		output.SinkFunc(func(event output.Event) {
			if isRetainedDiagnosticEvent(event) {
				diagnostics = append(diagnostics, event)
			}
		}),
	)
	executor := tool.NewExecutor(r.runtime.registry, r.runtime.cfg, r.approver, r.runtime.workDir)
	runner := agent.NewRunner()
	maxTokens := selected.MaxCompletionTokens
	ctxManager := agent.NewContextManager(string(r.runtime.cfg.ContextManagement.Mode))
	state, err := runner.Run(runCtx, agent.RunRequest{
		Provider:    prov,
		Executor:    executor,
		Tools:       r.runtime.registry.ToProviderSpecs(),
		Prompt:      assembly,
		ModelBudget: modelBudget,
		Model:       selected.Model,
		ExtraParams: selected.ExtraParams,
		MaxTokens:   &maxTokens,
		Limits: agent.Limits{
			MaxTurns:  r.maxTurns,
			MaxTokens: r.runtime.cfg.Limits.MaxTokens,
		},
		Events:             events,
		ContextManager:     ctxManager,
		StreamingPreferred: r.streamingPreferred,
	})
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
		Diagnostics:  cloneEvents(diagnostics),
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
