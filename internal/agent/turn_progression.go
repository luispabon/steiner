package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// executeModelCall runs the model-call phase of the turn lifecycle and applies
// the assistant response to the conversation state. It owns:
//   - TurnStarted / ModelCallStarted event emission
//   - completeModelCall invocation
//   - cancellation and error handling
//   - token accounting and ModelCallFinished event
//   - AssistantMessage event
//   - assistant transcript and state mutation
//   - TurnFinished / StopReason event emission for assistant-only turns
//
// When the response contains tool calls, it returns them via outcome.Response
// so the caller (Runner.runTurn) can delegate to executeToolCalls.
func (p *turnProgressor) executeModelCall(ctx context.Context, in turnInput, assembly prompt.Assembly, chatRequest provider.ChatRequest) turnOutcome {
	turn := in.State.TurnCount + 1

	emitEvent(in.Request.Events, output.NewTurnStartedEvent(turn, in.Request.Model, len(assembly.Messages)))
	emitEvent(in.Request.Events, output.NewModelCallStartedEvent(turn, in.Request.Model, len(assembly.Messages)))

	response, err := completeModelCall(ctx, in.Request, turn, chatRequest, assembly.Blocks, in.Request.ModelBudget)
	if err != nil {
		if cancelled, ok := contextCancellationState(ctx, in.State); ok {
			emitEvent(in.Request.Events, output.NewModelCallFinishedEvent(turn, in.Request.Model, "", 0, 0, nil))
			emitEvent(in.Request.Events, output.NewTurnFinishedEvent(turn, 0, "", "", nil))
			emitStop(in.Request.Events, cancelled, nil)
			return turnOutcome{State: cancelled, Stop: true}
		}
		state := in.State
		state.StopReason = StopReasonError
		emitEvent(in.Request.Events, output.NewModelCallFinishedEvent(turn, in.Request.Model, "", 0, 0, err))
		emitEvent(in.Request.Events, output.NewTurnFinishedEvent(turn, 0, "", "", err))
		emitStop(in.Request.Events, state, err)
		return turnOutcome{State: state, Stop: true, Error: err}
	}

	if response.Message.Role == "" {
		response.Message.Role = provider.MessageRoleAssistant
	}
	state := in.State
	state.TurnCount = turn
	turnTokens, err := tokenCount(ctx, chatRequest, response.Usage)
	if err != nil {
		emitEvent(in.Request.Events, output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
			Kind:     "session_health",
			Severity: "warning",
			Notes:    []string{err.Error()},
		}))
	}
	state.TokenCount += turnTokens
	emitEvent(in.Request.Events, output.NewModelCallFinishedEvent(turn, in.Request.Model, response.FinishReason, len(response.Message.ToolCalls), turnTokens, nil))
	if content := strings.TrimSpace(response.Message.Content); content != "" || len(response.Message.ToolCalls) > 0 {
		emitEvent(in.Request.Events, output.NewAssistantMessageEvent(turn, string(response.Message.Role), response.Message.Content))
	}

	assistant := fromProviderMessage(response.Message)
	state.Conversation = append(state.Conversation, assistant)
	state.Lineage = state.Lineage.WithAppendedMessages([]Message{assistant})

	if len(response.Message.ToolCalls) == 0 {
		emitEvent(in.Request.Events, output.NewTurnFinishedEvent(turn, 0, response.FinishReason, response.Message.Content, nil))
		state.StopReason = StopReasonComplete
		emitStop(in.Request.Events, state, nil)
		return turnOutcome{State: state, Stop: true}
	}

	return turnOutcome{State: state, Response: &response}
}

// turnProgressor owns the per-turn progression lifecycle.
type turnProgressor struct {
	runner *Runner
}

func newTurnProgressor(runner *Runner) *turnProgressor {
	return &turnProgressor{runner: runner}
}

// advance handles the compaction path of the turn lifecycle.
// It emits the compaction event, runs compaction, and returns the outcome.
func (p *turnProgressor) advance(ctx context.Context, in turnInput, fit prompt.RequestTokenBudget) turnOutcome {
	return p.handleCompaction(ctx, in, fit)
}

// handleCompaction coordinates compaction when the request does not fit
// the model token budget. It returns a retry outcome on success (the caller
// should re-run the turn with the compacted state) or an error outcome on
// failure.
func (p *turnProgressor) handleCompaction(ctx context.Context, in turnInput, fit prompt.RequestTokenBudget) turnOutcome {
	turn := in.State.TurnCount + 1
	emitCompactionStartedEvent(in.Request.Events, turn)
	state := in.State
	compacted, err := p.runner.compactConversationForBudget(ctx, in.Request, &state, turn, in.CompactionHistory, in.CompactionCount)
	if err != nil {
		return turnOutcome{State: state, Error: err, Stop: true}
	}
	if compacted {
		return turnOutcome{State: state, Retry: true}
	}
	return turnOutcome{
		State: state,
		Error: fmt.Errorf("request exceeds context window: %s", fit.String()),
		Stop:  true,
	}
}

// prepareTurn assembles the prompt, constructs the chat request, and fits it
// against the model token budget. Diagnostics are emitted through the request
// event sink.
func prepareTurn(ctx context.Context, in turnInput) (prompt.Assembly, provider.ChatRequest, prompt.RequestTokenBudget, error) {
	turn := in.State.TurnCount + 1
	assembly, err := prompt.Assemble(ctx, assemblyOptions(in.BasePrompt, in.State))
	if err != nil {
		return prompt.Assembly{}, provider.ChatRequest{}, prompt.RequestTokenBudget{}, err
	}
	emitAssemblyDiagnostics(in.Request.Events, in.Request.Prompt, turn, assembly)

	chatRequest := provider.ChatRequest{
		Model:       in.Request.Model,
		Messages:    assembly.Messages,
		Tools:       cloneProviderTools(in.Request.Tools),
		ExtraParams: in.Request.ExtraParams,
		MaxTokens:   in.Request.MaxTokens,
	}

	fit, err := in.Request.ModelBudget.FitRequest(ctx, chatRequest)
	if err != nil {
		return prompt.Assembly{}, provider.ChatRequest{}, prompt.RequestTokenBudget{}, err
	}
	emitRequestTokenDiagnostic(in.Request.Events, turn, fit, !fit.Fits)
	return assembly, chatRequest, fit, nil
}
