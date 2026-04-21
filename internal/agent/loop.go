package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

type ToolExecutor interface {
	Execute(ctx context.Context, toolName string, input map[string]any) (any, error)
}

type RunRequest struct {
	Provider    provider.Provider
	Executor    ToolExecutor
	Prompt      prompt.AssemblyOptions
	Model       string
	Temperature *float64
	MaxTokens   *int
	Limits      Limits
	Events      output.EventSink
}

type Runner struct{}

func NewRunner() *Runner {
	return &Runner{}
}

func (r *Runner) Run(ctx context.Context, req RunRequest) (RunState, error) {
	state := RunState{
		Conversation: fromProviderMessages(req.Prompt.Conversation),
	}
	if req.Provider == nil {
		state.StopReason = StopReasonError
		return state, fmt.Errorf("provider is required")
	}
	if req.Executor == nil {
		state.StopReason = StopReasonError
		return state, fmt.Errorf("tool executor is required")
	}
	if err := ctx.Err(); err != nil {
		state.StopReason = StopReasonCancelled
		emitStop(req.Events, state, nil)
		return state, nil
	}

	basePrompt := req.Prompt
	basePrompt.Conversation = nil

	for {
		if err := ctx.Err(); err != nil {
			state.StopReason = StopReasonCancelled
			emitStop(req.Events, state, nil)
			return state, nil
		}

		if req.Limits.MaxTurns > 0 && state.TurnCount >= req.Limits.MaxTurns {
			state.StopReason = StopReasonMaxTurns
			emitStop(req.Events, state, nil)
			return state, nil
		}
		if req.Limits.MaxTokens > 0 && state.TokenCount >= req.Limits.MaxTokens {
			state.StopReason = StopReasonMaxTokens
			emitStop(req.Events, state, nil)
			return state, nil
		}

		assembly, err := prompt.Assemble(ctx, assemblyOptions(basePrompt, state.Conversation))
		if err != nil {
			state.StopReason = StopReasonError
			emitStop(req.Events, state, err)
			return state, err
		}

		turn := state.TurnCount + 1
		emitEvent(req.Events, output.NewModelCallStartedEvent(turn, req.Model, len(assembly.Messages)))

		response, err := req.Provider.ChatCompletion(ctx, provider.ChatRequest{
			Model:       req.Model,
			Messages:    assembly.Messages,
			Temperature: req.Temperature,
			MaxTokens:   req.MaxTokens,
		})
		if err != nil {
			state.StopReason = StopReasonError
			emitEvent(req.Events, output.NewModelCallFinishedEvent(turn, req.Model, "", 0, 0, err))
			emitStop(req.Events, state, err)
			return state, err
		}

		state.TurnCount = turn
		if response.Usage != nil {
			state.TokenCount += response.Usage.TotalTokens
		}
		emitEvent(req.Events, output.NewModelCallFinishedEvent(turn, req.Model, response.FinishReason, len(response.Message.ToolCalls), tokenCount(response.Usage), nil))

		assistant := fromProviderMessage(response.Message)
		state.Conversation = append(state.Conversation, assistant)

		if len(response.Message.ToolCalls) == 0 {
			state.StopReason = StopReasonComplete
			emitStop(req.Events, state, nil)
			return state, nil
		}
		if len(response.Message.ToolCalls) > 1 {
			err := fmt.Errorf("model returned %d tool calls; stage 1 supports only one", len(response.Message.ToolCalls))
			state.StopReason = StopReasonError
			emitStop(req.Events, state, err)
			return state, err
		}

		call := response.Message.ToolCalls[0]
		emitEvent(req.Events, output.NewToolCallStartedEvent(turn, call.Name, call.ID, cloneInput(call.Arguments)))

		result, err := req.Executor.Execute(ctx, call.Name, cloneInput(call.Arguments))
		if err != nil {
			emitEvent(req.Events, output.NewToolCallFinishedEvent(turn, call.Name, call.ID, "", err))
			state.StopReason = StopReasonError
			emitStop(req.Events, state, err)
			return state, err
		}

		normalizedResult := normalizeToolResult(result)
		emitEvent(req.Events, output.NewToolCallFinishedEvent(turn, call.Name, call.ID, normalizedResult.Content, nil))
		state.Conversation = append(state.Conversation, Message{
			Role:       MessageRoleTool,
			Content:    normalizedResult.Content,
			ToolCallID: call.ID,
			Name:       call.Name,
		})
	}
}

type eventingApprover struct {
	inner tool.Approver
	sink  output.EventSink
}

func NewEventingApprover(sink output.EventSink, inner tool.Approver) tool.Approver {
	if sink == nil {
		sink = output.NoopSink{}
	}
	return eventingApprover{inner: inner, sink: sink}
}

func (a eventingApprover) Approve(ctx context.Context, req tool.ApprovalRequest) (tool.ApprovalResponse, error) {
	emitEvent(a.sink, output.NewApprovalRequestedEvent(0, req.Tool.Name, string(req.Mode)))
	if a.inner == nil {
		err := fmt.Errorf("approval is required")
		emitEvent(a.sink, output.NewApprovalDeniedEvent(0, req.Tool.Name, string(req.Mode), err.Error()))
		return tool.ApprovalResponse{}, err
	}
	resp, err := a.inner.Approve(ctx, req)
	if err != nil {
		emitEvent(a.sink, output.NewApprovalDeniedEvent(0, req.Tool.Name, string(req.Mode), err.Error()))
		return resp, err
	}
	if resp.Allow {
		emitEvent(a.sink, output.NewApprovalAcceptedEvent(0, req.Tool.Name, string(req.Mode), resp.Message))
		return resp, nil
	}
	message := resp.Message
	if strings.TrimSpace(message) == "" {
		message = "tool execution denied"
	}
	emitEvent(a.sink, output.NewApprovalDeniedEvent(0, req.Tool.Name, string(req.Mode), message))
	return resp, nil
}

func cloneMessages(messages []Message) []Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]Message, len(messages))
	copy(out, messages)
	for i := range out {
		out[i].ToolCalls = cloneToolCalls(out[i].ToolCalls)
	}
	return out
}

func cloneToolCalls(calls []ToolCall) []ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]ToolCall, len(calls))
	copy(out, calls)
	for i := range out {
		out[i].Arguments = cloneInput(out[i].Arguments)
	}
	return out
}

func cloneInput(input map[string]any) map[string]any {
	if len(input) == 0 {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = cloneValue(value)
	}
	return cloned
}

func cloneValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return cloneInput(v)
	case []any:
		out := make([]any, len(v))
		for i := range v {
			out[i] = cloneValue(v[i])
		}
		return out
	case json.RawMessage:
		return append(json.RawMessage(nil), v...)
	default:
		return value
	}
}

func assemblyOptions(base prompt.AssemblyOptions, conversation []Message) prompt.AssemblyOptions {
	base.Conversation = toProviderMessages(conversation)
	base.ToolResults = nil
	return base
}

func toProviderMessages(messages []Message) []provider.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]provider.Message, 0, len(messages))
	for _, message := range messages {
		out = append(out, toProviderMessage(message))
	}
	return out
}

func fromProviderMessages(messages []provider.Message) []Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]Message, 0, len(messages))
	for _, message := range messages {
		out = append(out, fromProviderMessage(message))
	}
	return out
}

func toProviderMessage(message Message) provider.Message {
	out := provider.Message{
		Role:       provider.MessageRole(message.Role),
		Content:    message.Content,
		Name:       message.Name,
		ToolCallID: message.ToolCallID,
	}
	if len(message.ToolCalls) > 0 {
		out.ToolCalls = make([]provider.ToolCall, 0, len(message.ToolCalls))
		for _, call := range message.ToolCalls {
			out.ToolCalls = append(out.ToolCalls, provider.ToolCall{
				ID:        call.ID,
				Name:      call.Name,
				Arguments: cloneInput(call.Arguments),
			})
		}
	}
	return out
}

func fromProviderMessage(message provider.Message) Message {
	out := Message{
		Role:       MessageRole(message.Role),
		Content:    message.Content,
		Name:       message.Name,
		ToolCallID: message.ToolCallID,
	}
	if len(message.ToolCalls) > 0 {
		out.ToolCalls = make([]ToolCall, 0, len(message.ToolCalls))
		for _, call := range message.ToolCalls {
			out.ToolCalls = append(out.ToolCalls, ToolCall{
				ID:        call.ID,
				Name:      call.Name,
				Arguments: cloneInput(call.Arguments),
			})
		}
	}
	return out
}

func emitEvent(sink output.EventSink, event output.Event) {
	if sink != nil {
		sink.Emit(event)
	}
}

func emitStop(sink output.EventSink, state RunState, err error) {
	emitEvent(sink, output.NewStopReasonEvent(state.TurnCount, string(state.StopReason), err))
}

func tokenCount(usage *provider.UsageStats) int {
	if usage == nil {
		return 0
	}
	return usage.TotalTokens
}
