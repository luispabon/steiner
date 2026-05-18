package agent

import (
	"strings"

	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// ToProviderMessages converts a slice of agent Messages to provider Messages.
func ToProviderMessages(messages []Message) []provider.Message {
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
	role := provider.MessageRole(message.Role)
	if message.Role == MessageRoleSummary {
		role = provider.MessageRoleSystem
	}
	out := provider.Message{
		Role:             role,
		Content:          message.Content,
		ReasoningContent: message.ReasoningContent,
		Name:             message.Name,
		ToolCallID:       message.ToolCallID,
		Turn:             message.Turn,
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
		Role:             MessageRole(message.Role),
		Content:          message.Content,
		ReasoningContent: message.ReasoningContent,
		Name:             message.Name,
		ToolCallID:       message.ToolCallID,
		Turn:             message.Turn,
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

func assemblyOptions(base prompt.AssemblyOptions, state RunState) prompt.AssemblyOptions {
	conversation := state.Lineage.FullMessages()
	if len(conversation) == 0 {
		conversation = state.Conversation
	}

	scratchpadEnabled := base.ScratchpadEnabled || strings.TrimSpace(state.Context.Scratchpad) != ""
	providerMsgs := ToProviderMessages(conversation)

	if scratchpadMsg, ok := buildScratchpadMessage(state.Context, scratchpadEnabled); ok {
		providerMsgs = append(providerMsgs, scratchpadMsg)
	}

	base.Conversation = providerMsgs
	base.ToolResults = nil
	base.ContextState = toPromptContext(state.Context)
	base.ScratchpadEnabled = scratchpadEnabled
	return base
}

func buildScaffoldInferenceRequest(req RunRequest, scaffoldState, assistantContent string) provider.ChatRequest {
	system := prompt.SystemPreamble(req.Prompt.PromptOverrides.System, false, false).Content
	user := scaffoldInferenceUserPrompt(scaffoldState, assistantContent)
	chatReq := provider.ChatRequest{
		Model:       req.ResolvedModel.BackendModelID,
		Messages:    []provider.Message{{Role: provider.MessageRoleSystem, Content: system}, {Role: provider.MessageRoleUser, Content: user}},
		Params:      req.ResolvedModel.Params,
		ExtraParams: req.ResolvedModel.ExtraParams,
		MaxTokens:   scaffoldInferenceMaxTokens(req.ModelBudget),
	}
	return applyPromptSuffix(req.ResolvedModel.PromptSuffix, chatReq)
}

func scaffoldInferenceUserPrompt(scaffoldState, assistantContent string) string {
	parts := []string{
		"[Current scaffold state]",
		strings.TrimSpace(scaffoldState),
		"[Last assistant response]",
		truncateScaffoldInferenceText(assistantContent, 200),
		"Respond with ONLY a JSON object:",
		`{"intent":"what is being done and why","next":"planned next action"}`,
	}
	return strings.Join(filterEmptyStrings(parts), "\n\n")
}

func truncateScaffoldInferenceText(text string, limit int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "(empty)"
	}
	if limit <= 0 {
		limit = 200
	}
	words := strings.Fields(text)
	if len(words) <= limit {
		return text
	}
	return strings.Join(words[:limit], " ") + " ..."
}

func scaffoldInferenceMaxTokens(budget prompt.ModelTokenBudget) *int {
	maxTokens := 150
	if budget.MaxCompletionTokens > 0 && budget.MaxCompletionTokens < maxTokens {
		maxTokens = budget.MaxCompletionTokens
	}
	return &maxTokens
}

func filterEmptyStrings(values []string) []string {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		filtered = append(filtered, value)
	}
	return filtered
}

func buildScratchpadMessage(state ContextState, scratchpadEnabled bool) (provider.Message, bool) {
	hasSubstantiveContent := strings.TrimSpace(state.Scratchpad) != "" ||
		len(state.ActiveConstraints) > 0 ||
		len(state.UnresolvedWork) > 0 ||
		state.ActiveFocus != nil

	if !scratchpadEnabled && !hasSubstantiveContent {
		return provider.Message{}, false
	}
	if !hasSubstantiveContent && state.TurnCount == 0 {
		return provider.Message{}, false
	}

	parts := scratchpadMessageParts(state)
	return provider.Message{
		Role:    provider.MessageRoleUser,
		Content: strings.Join(parts, "\n\n"),
	}, true
}

func scratchpadMessageParts(state ContextState) []string {
	parts := []string{"[Current task state]"}
	parts = append(parts, scratchpadConstraintLines(state.ActiveConstraints)...)
	parts = append(parts, scratchpadWorkLines(state.UnresolvedWork)...)
	if state.ActiveFocus != nil && strings.TrimSpace(state.ActiveFocus.Text) != "" {
		parts = append(parts, "active focus:\n- "+state.ActiveFocus.Text)
	}
	if contextState := strings.TrimSpace(state.Render()); contextState != "" {
		parts = append(parts, contextState)
	}
	scratchpad := strings.TrimSpace(strings.Join(scratchpadFieldLines(state.Scratchpad), "\n"))
	if scratchpad == "" {
		scratchpad = "intent: \ndecisions: \nopen: \nnext: "
	}
	return append(parts, scratchpad)
}

func scratchpadConstraintLines(items []ActiveConstraint) []string {
	if len(items) == 0 {
		return nil
	}
	lines := []string{"active constraints:"}
	for _, item := range items {
		lines = append(lines, "- "+item.Text)
	}
	return []string{strings.Join(lines, "\n")}
}

func scratchpadWorkLines(items []UnresolvedWorkItem) []string {
	if len(items) == 0 {
		return nil
	}
	lines := []string{"unresolved work:"}
	for _, item := range items {
		lines = append(lines, "- "+item.Text)
	}
	return []string{strings.Join(lines, "\n")}
}

func scratchpadFieldLines(rendered string) []string {
	rendered = strings.TrimSpace(rendered)
	if rendered == "" {
		return nil
	}

	lines := strings.Split(rendered, "\n")
	out := make([]string, 0, len(lines))

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed == "[Current task state]" {
			continue
		}
		if strings.HasPrefix(trimmed, "session state:") {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

func toPromptContext(state ContextState) prompt.DurableContextState {
	out := prompt.DurableContextState{
		RetainedSummaries: make([]prompt.DurableSummaryEntry, 0, len(state.RetainedSummaries)),
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
		RetainedSummaries: make([]RetainedSummary, 0, len(state.RetainedSummaries)),
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

func stripReasoningContent(messages []provider.Message) {
	for i := range messages {
		messages[i].ReasoningContent = ""
	}
}

// LastAssistantMessage returns the last message with Role == MessageRoleAssistant.
// It iterates from the end of the slice for efficiency. The bool return indicates
// whether an assistant message was found.
func LastAssistantMessage(msgs []Message) (Message, bool) {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == MessageRoleAssistant {
			return msgs[i], true
		}
	}
	return Message{}, false
}

// hasPromptSuffix reports whether the last user message already contains suffix.
func hasPromptSuffix(messages []provider.Message, suffix string) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != provider.MessageRoleUser {
			continue
		}
		return strings.Contains(messages[i].Content, suffix)
	}
	return false
}

// appendPromptSuffix returns a copy of messages with suffix appended to the
// last user message. If there is no user message, messages is returned as-is.
func appendPromptSuffix(messages []provider.Message, suffix string) []provider.Message {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != provider.MessageRoleUser {
			continue
		}
		out := make([]provider.Message, len(messages))
		copy(out, messages)
		out[i].Content = strings.TrimSpace(out[i].Content + " " + suffix)
		return out
	}
	return messages
}

// applyPromptSuffix appends suffix to the last user message without duplicating it.
func applyPromptSuffix(suffix string, req provider.ChatRequest) provider.ChatRequest {
	suffix = strings.TrimSpace(suffix)
	if suffix == "" || hasPromptSuffix(req.Messages, suffix) {
		return req
	}
	req.Messages = appendPromptSuffix(req.Messages, suffix)
	return req
}
