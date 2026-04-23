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
	Tools       []provider.ToolSpec
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
	conversation := fromProviderMessages(req.Prompt.Conversation)
	state := RunState{
		Conversation: conversation,
		Lineage:      newConversationLineage(conversation),
		Context:      fromPromptContext(req.Prompt.ContextState),
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

		turn := state.TurnCount + 1
		assembly, err := prompt.Assemble(ctx, assemblyOptions(basePrompt, state))
		if err != nil {
			if cancelled, ok := contextCancellationState(ctx, state); ok {
				emitStop(req.Events, cancelled, nil)
				return cancelled, nil
			}
			state.StopReason = StopReasonError
			emitStop(req.Events, state, err)
			return state, err
		}
		emitAssemblyDiagnostics(req.Events, req.Prompt, turn, assembly)
		state.Context = updateContextState(state.Context, assembly.Blocks, turn)

		emitEvent(req.Events, output.NewTurnStartedEvent(turn, req.Model, len(assembly.Messages)))
		emitEvent(req.Events, output.NewModelCallStartedEvent(turn, req.Model, len(assembly.Messages)))

		response, err := completeModelCall(ctx, req, turn, provider.ChatRequest{
			Model:       req.Model,
			Messages:    assembly.Messages,
			Tools:       cloneProviderTools(req.Tools),
			Temperature: req.Temperature,
			MaxTokens:   req.MaxTokens,
		})
		if err != nil {
			if cancelled, ok := contextCancellationState(ctx, state); ok {
				emitEvent(req.Events, output.NewModelCallFinishedEvent(turn, req.Model, "", 0, 0, nil))
				emitEvent(req.Events, output.NewTurnFinishedEvent(turn, 0, "", "", nil))
				emitStop(req.Events, cancelled, nil)
				return cancelled, nil
			}
			state.StopReason = StopReasonError
			emitEvent(req.Events, output.NewModelCallFinishedEvent(turn, req.Model, "", 0, 0, err))
			emitEvent(req.Events, output.NewTurnFinishedEvent(turn, 0, "", "", err))
			emitStop(req.Events, state, err)
			return state, err
		}

		state.TurnCount = turn
		if response.Usage != nil {
			state.TokenCount += response.Usage.TotalTokens
		}
		emitEvent(req.Events, output.NewModelCallFinishedEvent(turn, req.Model, response.FinishReason, len(response.Message.ToolCalls), tokenCount(response.Usage), nil))
		if content := strings.TrimSpace(response.Message.Content); content != "" || len(response.Message.ToolCalls) > 0 {
			emitEvent(req.Events, output.NewAssistantMessageEvent(turn, string(response.Message.Role), response.Message.Content))
		}

		assistant := fromProviderMessage(response.Message)
		state.Conversation = append(state.Conversation, assistant)
		state.Lineage = state.Lineage.WithAppendedMessages([]Message{assistant})

		if len(response.Message.ToolCalls) == 0 {
			emitEvent(req.Events, output.NewTurnFinishedEvent(turn, 0, response.FinishReason, response.Message.Content, nil))
			state.StopReason = StopReasonComplete
			emitStop(req.Events, state, nil)
			return state, nil
		}
		for _, call := range response.Message.ToolCalls {
			emitEvent(req.Events, output.NewToolCallStartedEvent(turn, call.Name, call.ID, cloneInput(call.Arguments)))

			result, err := req.Executor.Execute(ctx, call.Name, cloneInput(call.Arguments))
			if err != nil {
				if cancelled, ok := contextCancellationState(ctx, state); ok {
					emitEvent(req.Events, output.NewToolCallFinishedEvent(turn, call.Name, call.ID, "", nil))
					emitStop(req.Events, cancelled, nil)
					return cancelled, nil
				}
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
			state.Lineage = state.Lineage.WithAppendedMessages([]Message{{
				Role:       MessageRoleTool,
				Content:    normalizedResult.Content,
				ToolCallID: call.ID,
				Name:       call.Name,
			}})
		}

		emitEvent(req.Events, output.NewTurnFinishedEvent(turn, len(response.Message.ToolCalls), response.FinishReason, response.Message.Content, nil))
		state.Conversation = state.Lineage.FullMessages()
	}
}

func completeModelCall(ctx context.Context, req RunRequest, turn int, chatRequest provider.ChatRequest) (provider.ChatResponse, error) {
	emitEvent(req.Events, output.NewAPIRequestEvent(chatRequest.Model, chatRequest.Messages, chatRequest.Tools))

	stream, err := req.Provider.StreamChatCompletion(ctx, chatRequest)
	if err == nil {
		response, streamErr := consumeModelStream(ctx, req.Events, turn, stream)
		if streamErr != nil {
			emitEvent(req.Events, output.NewAPIResponseEvent(nil, nil, "", streamErr))
			return provider.ChatResponse{}, streamErr
		}
		emitEvent(req.Events, output.NewAPIResponseEvent(response.Message, response.Usage, response.FinishReason, nil))
		return response, nil
	}

	response, chatErr := req.Provider.ChatCompletion(ctx, chatRequest)
	emitEvent(req.Events, output.NewAPIResponseEvent(response.Message, response.Usage, response.FinishReason, chatErr))
	if chatErr != nil {
		return provider.ChatResponse{}, chatErr
	}
	return response, nil
}

func consumeModelStream(ctx context.Context, sink output.EventSink, turn int, chunks <-chan provider.ChatChunk) (provider.ChatResponse, error) {
	response := provider.ChatResponse{}
	message := provider.Message{Role: provider.MessageRoleAssistant}
	sawFinal := false

	for chunk := range chunks {
		if errText := strings.TrimSpace(chunk.Error); errText != "" {
			return provider.ChatResponse{}, fmt.Errorf("%s", errText)
		}
		if role := chunk.Delta.Role; role != "" {
			message.Role = role
		}
		if !chunk.Done {
			if content := chunk.Delta.Content; content != "" {
				message.Content += content
				emitEvent(sink, output.NewAssistantChunkEvent(turn, content))
			}
			if len(chunk.Delta.ToolCalls) > 0 {
				message.ToolCalls = cloneProviderToolCalls(chunk.Delta.ToolCalls)
			}
			continue
		}
		sawFinal = true
		response.Usage = chunk.Usage
		response.FinishReason = chunk.FinishReason
		if content := chunk.Delta.Content; content != "" {
			switch {
			case message.Content == "":
				message.Content = content
				emitEvent(sink, output.NewAssistantChunkEvent(turn, content))
			case strings.HasPrefix(content, message.Content):
				message.Content = content
			case strings.HasPrefix(message.Content, content):
				// Final chunk already represented by prior deltas.
			default:
				message.Content += content
				emitEvent(sink, output.NewAssistantChunkEvent(turn, content))
			}
		}
		if len(chunk.Delta.ToolCalls) > 0 {
			message.ToolCalls = cloneProviderToolCalls(chunk.Delta.ToolCalls)
		}
	}

	if !sawFinal {
		return provider.ChatResponse{}, fmt.Errorf("stream completed without a final chunk")
	}

	response.Message = message
	return response, nil
}

func contextCancellationState(ctx context.Context, state RunState) (RunState, bool) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		state.StopReason = StopReasonCancelled
		return state, true
	}
	return RunState{}, false
}

type eventingApprover struct {
	inner tool.ApprovalResponder
	sink  output.EventSink
}

func NewEventingApprover(sink output.EventSink, inner tool.ApprovalResponder) tool.ApprovalResponder {
	if sink == nil {
		sink = output.NoopSink{}
	}
	return eventingApprover{inner: inner, sink: sink}
}

func (a eventingApprover) RequestApproval(ctx context.Context, req tool.ApprovalRequest) error {
	if req.Response == nil {
		return fmt.Errorf("approval response channel is required")
	}
	preview := output.CompactJSON(req.Input)
	if preview == "" && req.Preview.Summary() != "" {
		preview = req.Preview.Summary()
	}
	emitEvent(a.sink, output.NewApprovalRequestedEvent(0, req.Tool.Name, string(req.Mode), preview))
	if a.inner == nil {
		response := tool.ApprovalResponse{Allow: false, Message: "approval is required"}
		req.Response <- response
		emitEvent(a.sink, output.NewApprovalDeniedEvent(0, req.Tool.Name, string(req.Mode), preview, response.Message))
		return nil
	}
	bridge := make(chan tool.ApprovalResponse, 1)
	innerReq := req
	innerReq.Response = bridge
	if err := a.inner.RequestApproval(ctx, innerReq); err != nil {
		response := tool.ApprovalResponse{Allow: false, Message: err.Error()}
		req.Response <- response
		emitEvent(a.sink, output.NewApprovalDeniedEvent(0, req.Tool.Name, string(req.Mode), preview, response.Message))
		return err
	}
	go func() {
		var response tool.ApprovalResponse
		select {
		case response = <-bridge:
		case <-ctx.Done():
			response = tool.ApprovalResponse{Allow: false, Message: ctx.Err().Error()}
		}
		req.Response <- response
		if response.Allow {
			emitEvent(a.sink, output.NewApprovalAcceptedEvent(0, req.Tool.Name, string(req.Mode), preview, response.Message))
			return
		}
		message := strings.TrimSpace(response.Message)
		if message == "" {
			message = "tool execution denied"
		}
		emitEvent(a.sink, output.NewApprovalDeniedEvent(0, req.Tool.Name, string(req.Mode), preview, message))
	}()
	return nil
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

func cloneProviderTools(tools []provider.ToolSpec) []provider.ToolSpec {
	if len(tools) == 0 {
		return nil
	}
	out := make([]provider.ToolSpec, len(tools))
	copy(out, tools)
	for i := range out {
		out[i].Function.Parameters = cloneInput(out[i].Function.Parameters)
	}
	return out
}

func cloneProviderToolCalls(calls []provider.ToolCall) []provider.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]provider.ToolCall, len(calls))
	copy(out, calls)
	for i := range out {
		out[i].Arguments = cloneInput(out[i].Arguments)
	}
	return out
}

func assemblyOptions(base prompt.AssemblyOptions, state RunState) prompt.AssemblyOptions {
	conversation := state.Lineage.FullMessages()
	if len(conversation) == 0 {
		conversation = state.Conversation
	}
	base.Conversation = toProviderMessages(conversation)
	base.ToolResults = nil
	base.ContextState = toPromptContext(state.Context)
	return base
}

func updateContextState(current ContextState, blocks []prompt.ContextBlock, turn int) ContextState {
	summary, ok := latestConversationSummary(blocks)
	if !ok {
		return current
	}

	text := summary.Content
	title := summary.Path
	var envelope struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(summary.Content), &envelope); err == nil {
		if strings.TrimSpace(envelope.Title) != "" {
			title = envelope.Title
		}
		if strings.TrimSpace(envelope.Content) != "" {
			text = envelope.Content
		}
	}

	next := current.Clone()
	next.RetainedSummaries = appendRetainedSummary(next.RetainedSummaries, RetainedSummary{
		Title:  title,
		Text:   text,
		Source: string(summary.Source),
		Turn:   turn,
	})
	return next
}

func appendRetainedSummary(existing []RetainedSummary, summary RetainedSummary) []RetainedSummary {
	if strings.TrimSpace(summary.Text) == "" {
		return cloneRetainedSummaries(existing)
	}
	next := cloneRetainedSummaries(existing)
	if len(next) > 0 {
		last := next[len(next)-1]
		if retainedSummariesEqual(last, summary) {
			next[len(next)-1] = summary
			return next
		}
	}
	return append(next, summary)
}

func retainedSummariesEqual(a, b RetainedSummary) bool {
	return a.Title == b.Title &&
		a.Text == b.Text &&
		a.Source == b.Source &&
		a.Turn == b.Turn
}

func latestConversationSummary(blocks []prompt.ContextBlock) (prompt.ContextBlock, bool) {
	for _, block := range blocks {
		if block.Source == prompt.ContextSourceConversationSummary && block.Path == "compacted conversation history" {
			return block, true
		}
	}
	return prompt.ContextBlock{}, false
}

func toPromptContext(state ContextState) prompt.DurableContextState {
	out := prompt.DurableContextState{
		ActiveConstraints: make([]prompt.DurableContextEntry, 0, len(state.ActiveConstraints)),
		UnresolvedWork:    make([]prompt.DurableContextEntry, 0, len(state.UnresolvedWork)),
		RetainedSummaries: make([]prompt.DurableSummaryEntry, 0, len(state.RetainedSummaries)),
	}
	for _, item := range state.ActiveConstraints {
		out.ActiveConstraints = append(out.ActiveConstraints, prompt.DurableContextEntry{
			Text:   item.Text,
			Source: item.Source,
			Turn:   item.Turn,
		})
	}
	for _, item := range state.UnresolvedWork {
		out.UnresolvedWork = append(out.UnresolvedWork, prompt.DurableContextEntry{
			Text:   item.Text,
			Source: item.Source,
			Turn:   item.Turn,
		})
	}
	if state.ActiveFocus != nil {
		out.ActiveFocus = &prompt.DurableContextEntry{
			Text:   state.ActiveFocus.Text,
			Source: state.ActiveFocus.Source,
			Turn:   state.ActiveFocus.Turn,
		}
	}
	for _, item := range state.RetainedSummaries {
		out.RetainedSummaries = append(out.RetainedSummaries, prompt.DurableSummaryEntry{
			Title:  item.Title,
			Text:   item.Text,
			Source: item.Source,
			Turn:   item.Turn,
		})
	}
	return out
}

func fromPromptContext(state prompt.DurableContextState) ContextState {
	out := ContextState{
		ActiveConstraints: make([]ActiveConstraint, 0, len(state.ActiveConstraints)),
		UnresolvedWork:    make([]UnresolvedWorkItem, 0, len(state.UnresolvedWork)),
		RetainedSummaries: make([]RetainedSummary, 0, len(state.RetainedSummaries)),
	}
	for _, item := range state.ActiveConstraints {
		out.ActiveConstraints = append(out.ActiveConstraints, ActiveConstraint{
			Text:   item.Text,
			Source: item.Source,
			Turn:   item.Turn,
		})
	}
	for _, item := range state.UnresolvedWork {
		out.UnresolvedWork = append(out.UnresolvedWork, UnresolvedWorkItem{
			Text:   item.Text,
			Source: item.Source,
			Turn:   item.Turn,
		})
	}
	if state.ActiveFocus != nil {
		out.ActiveFocus = &ActiveFocus{
			Text:   state.ActiveFocus.Text,
			Source: state.ActiveFocus.Source,
			Turn:   state.ActiveFocus.Turn,
		}
	}
	for _, item := range state.RetainedSummaries {
		out.RetainedSummaries = append(out.RetainedSummaries, RetainedSummary{
			Title:  item.Title,
			Text:   item.Text,
			Source: item.Source,
			Turn:   item.Turn,
		})
	}
	return out
}

func emitAssemblyDiagnostics(sink output.EventSink, opts prompt.AssemblyOptions, turn int, assembly prompt.Assembly) {
	if sink == nil {
		return
	}

	budgets := diagnosticBudgets(opts)
	for _, block := range assembly.Blocks {
		if !block.Truncated {
			continue
		}

		notes := make([]string, 0, 2)
		if block.Path != "" {
			notes = append(notes, "path="+block.Path)
		}
		if block.Source == prompt.ContextSourceConversationSummary {
			notes = append(notes, "compacted conversation history")
		}

		emitEvent(sink, output.NewContextBudgetEvent(
			string(block.Source),
			turn,
			block.ByteSize,
			budgetForSource(budgets, block.Source),
			true,
			notes...,
		))
	}
	for _, diagnostic := range assembly.Diagnostics {
		if !diagnostic.Truncated {
			continue
		}

		notes := make([]string, 0, 1)
		if diagnostic.Path != "" {
			notes = append(notes, "path="+diagnostic.Path)
		}

		emitEvent(sink, output.NewContextBudgetEvent(
			string(diagnostic.Source),
			turn,
			diagnostic.ByteSize,
			budgetForSource(budgets, diagnostic.Source),
			true,
			notes...,
		))
	}
}

func diagnosticBudgets(opts prompt.AssemblyOptions) prompt.SourceBudgetModel {
	defaults := prompt.DefaultAssemblyPolicy().Budgets
	budgets := opts.Policy.Budgets

	if budgets.PreambleBytes == 0 {
		budgets.PreambleBytes = defaults.PreambleBytes
	}
	if budgets.GlobalAgentsBytes == 0 {
		budgets.GlobalAgentsBytes = defaults.GlobalAgentsBytes
	}
	if budgets.ProjectAgentsBytes == 0 {
		budgets.ProjectAgentsBytes = defaults.ProjectAgentsBytes
	}
	if opts.ProjectContextBudgetBytes > 0 {
		budgets.ProjectContextBytes = opts.ProjectContextBudgetBytes
	} else if budgets.ProjectContextBytes == 0 {
		budgets.ProjectContextBytes = defaults.ProjectContextBytes
	}
	if budgets.SkillBytes == 0 {
		budgets.SkillBytes = defaults.SkillBytes
	}
	if budgets.DurableContextBytes == 0 {
		budgets.DurableContextBytes = defaults.DurableContextBytes
	}
	if budgets.ConversationBytes == 0 {
		budgets.ConversationBytes = defaults.ConversationBytes
	}
	if budgets.ConversationSummaryBytes == 0 {
		budgets.ConversationSummaryBytes = defaults.ConversationSummaryBytes
	}
	if budgets.ToolResultBytes == 0 {
		budgets.ToolResultBytes = defaults.ToolResultBytes
	}
	if budgets.ToolSummaryBytes == 0 {
		budgets.ToolSummaryBytes = defaults.ToolSummaryBytes
	}

	return budgets
}

func budgetForSource(budgets prompt.SourceBudgetModel, source prompt.ContextSource) int {
	switch source {
	case prompt.ContextSourcePreamble:
		return budgets.PreambleBytes
	case prompt.ContextSourceGlobalAgentsMD:
		return budgets.GlobalAgentsBytes
	case prompt.ContextSourceProjectAgentsMD:
		return budgets.ProjectAgentsBytes
	case prompt.ContextSourceProjectContext:
		return budgets.ProjectContextBytes
	case prompt.ContextSourceSkill:
		return budgets.SkillBytes
	case prompt.ContextSourceDurableContext:
		return budgets.DurableContextBytes
	case prompt.ContextSourceConversation:
		return budgets.ConversationBytes
	case prompt.ContextSourceConversationSummary:
		return budgets.ConversationSummaryBytes
	case prompt.ContextSourceToolResult:
		return budgets.ToolResultBytes
	case prompt.ContextSourceToolSummary, prompt.ContextSourceDelegationResult:
		return budgets.ToolSummaryBytes
	default:
		return 0
	}
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
