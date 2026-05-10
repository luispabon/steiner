package main

import (
	"context"
	"fmt"
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

	selected, err := selectedModelConfig(r.runtime.cfg)
	if err != nil {
		return runResult{}, err
	}
	if r.currentModel != nil {
		selected = r.currentModel()
	}

	prov := r.runtime.provider
	if r.runtime.providerFactory != nil {
		prov, err = r.runtime.providerFactory(selected)
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
		ScratchpadEnabled:         r.runtime.cfg.ContextManagement.ScratchpadMode == config.ScratchpadModeHybrid,
		DelegationEnabled:         r.runtime.cfg.SubAgent.Enabled,
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
	activeRegistry := buildActiveRegistry(r.runtime.registry, r.runtime.cfg.SubAgent, prov, events, r.runtime.workDir, selected.ExtraParams, selected.Thinking)
	executor := tool.NewExecutor(activeRegistry, r.runtime.cfg, r.approver, r.runtime.workDir)
	runner := agent.NewRunner()
	maxTokens := selected.MaxCompletionTokens
	ctxManager := agent.NewContextManager(string(r.runtime.cfg.ContextManagement.Mode), r.runtime.cfg.ContextManagement)
	state, err := runner.Run(runCtx, agent.RunRequest{
		Provider:    prov,
		Executor:    executor,
		Tools:       activeRegistry.ToProviderSpecs(),
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
		Thinking:           selected.Thinking,
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
func buildActiveRegistry(base *tool.Registry, subAgentCfg config.SubAgentConfig, prov provider.Provider, events output.EventSink, workDir string, extraParams map[string]any, thinking config.ThinkingConfig) *tool.Registry {
	if !subAgentCfg.Enabled {
		return base
	}
	cloned := base.Clone()
	handler := delegation.NewDelegateHandler(delegation.DelegateHandlerDeps{
		Provider:    prov,
		ParentReg:   base,
		SubAgentCfg: subAgentCfg,
		Events:      events,
		Runner:      agent.NewRunner(),
		WorkDir:     workDir,
		ExtraParams: extraParams,
		Thinking:    thinking,
	})
	cloned.Register(delegation.DelegateToolDef(handler))
	return cloned
}
