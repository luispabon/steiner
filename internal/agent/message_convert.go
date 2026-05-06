package agent

import (
	"strings"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

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
	role := provider.MessageRole(message.Role)
	if message.Role == MessageRoleSummary {
		role = provider.MessageRoleSystem
	}
	out := provider.Message{
		Role:       role,
		Content:    message.Content,
		Name:       message.Name,
		ToolCallID: message.ToolCallID,
		Turn:       message.Turn,
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
		Turn:       message.Turn,
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
	providerMsgs := toProviderMessages(conversation)

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
	system := prompt.SystemPreamble(req.Prompt.PromptOverrides.System, false).Content
	user := scaffoldInferenceUserPrompt(scaffoldState, assistantContent)
	chatReq := provider.ChatRequest{
		Model:       req.Model,
		Messages:    []provider.Message{{Role: provider.MessageRoleSystem, Content: system}, {Role: provider.MessageRoleUser, Content: user}},
		ExtraParams: req.ExtraParams,
		MaxTokens:   scaffoldInferenceMaxTokens(req.ModelBudget),
	}
	cfg := req.Thinking
	cfg.Enabled = cfg.Enabled && cfg.EnabledScaffoldInference
	return applyThinking(cfg, chatReq)
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
		state.ActiveFocus != nil ||
		len(state.FileTrackerSummary) > 0 ||
		len(state.RecentToolCalls) > 0

	if !scratchpadEnabled && !hasSubstantiveContent {
		return provider.Message{}, false
	}

	hasContent := hasSubstantiveContent || state.TurnCount > 0
	if !hasContent {
		return provider.Message{}, false
	}

	var parts []string
	parts = append(parts, "[Current task state]")

	if len(state.ActiveConstraints) > 0 {
		lines := []string{"active constraints:"}
		for _, c := range state.ActiveConstraints {
			lines = append(lines, "- "+c.Text)
		}
		parts = append(parts, strings.Join(lines, "\n"))
	}

	if len(state.UnresolvedWork) > 0 {
		lines := []string{"unresolved work:"}
		for _, w := range state.UnresolvedWork {
			lines = append(lines, "- "+w.Text)
		}
		parts = append(parts, strings.Join(lines, "\n"))
	}

	if state.ActiveFocus != nil && strings.TrimSpace(state.ActiveFocus.Text) != "" {
		parts = append(parts, "active focus:\n- "+state.ActiveFocus.Text)
	}

	if contextState := strings.TrimSpace(state.Render()); contextState != "" {
		parts = append(parts, contextState)
	}

	scratchpad := strings.TrimSpace(strings.Join(scratchpadFieldLines(state.Scratchpad), "\n"))
	if scratchpad != "" {
		parts = append(parts, scratchpad)
	} else {
		parts = append(parts, "intent: \ndecisions: \nopen: \nnext: ")
	}

	return provider.Message{
		Role:    provider.MessageRoleUser,
		Content: strings.Join(parts, "\n\n"),
	}, true
}

func scratchpadFieldLines(rendered string) []string {
	rendered = strings.TrimSpace(rendered)
	if rendered == "" {
		return nil
	}

	lines := strings.Split(rendered, "\n")
	out := make([]string, 0, len(lines))
	skippingTrackedFiles := false
	skippingRecentToolCalls := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed == "[Current task state]" {
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, "session state:"):
			continue
		case trimmed == "tracked files:":
			skippingTrackedFiles = true
			skippingRecentToolCalls = false
			continue
		case trimmed == "recent tool calls:":
			skippingRecentToolCalls = true
			skippingTrackedFiles = false
			continue
		}
		if skippingTrackedFiles && strings.HasPrefix(trimmed, "- ") {
			continue
		}
		if skippingRecentToolCalls && strings.HasPrefix(trimmed, "- ") {
			continue
		}
		skippingTrackedFiles = false
		skippingRecentToolCalls = false
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

// hasThinkingMarker reports whether the last user message contains marker.
func hasThinkingMarker(messages []provider.Message, marker string) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != provider.MessageRoleUser {
			continue
		}
		return strings.Contains(messages[i].Content, marker)
	}
	return false
}

// appendThinkingMarker returns a copy of messages with marker appended to the
// last user message. If there is no user message, messages is returned as-is.
func appendThinkingMarker(messages []provider.Message, marker string) []provider.Message {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != provider.MessageRoleUser {
			continue
		}
		out := make([]provider.Message, len(messages))
		copy(out, messages)
		out[i].Content = out[i].Content + " " + marker
		return out
	}
	return messages
}

// mergeThinkingParams returns a new map with base merged first, then params on
// top (params wins on collision).
func mergeThinkingParams(base, params map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(params))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range params {
		out[k] = v
	}
	return out
}

// applyThinking returns req with thinking params injected or suppressed
// according to cfg. When thinking is disabled and a disable marker is
// configured, the marker is appended to the last user message so the model
// knows not to think.
func applyThinking(cfg config.ThinkingConfig, req provider.ChatRequest) provider.ChatRequest {
	if cfg.DisableMarker != "" {
		markerPresent := hasThinkingMarker(req.Messages, cfg.DisableMarker)
		if !cfg.Enabled {
			if !markerPresent {
				req.Messages = appendThinkingMarker(req.Messages, cfg.DisableMarker)
			}
			return req
		}
		if markerPresent {
			return req
		}
	} else if !cfg.Enabled {
		return req
	}
	if len(cfg.Params) > 0 {
		req.ExtraParams = mergeThinkingParams(req.ExtraParams, cfg.Params)
	}
	return req
}
