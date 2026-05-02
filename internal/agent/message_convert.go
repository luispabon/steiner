package agent

import (
	"fmt"
	"strings"

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

	if state.TurnCount > 0 || state.CompactionCount > 0 {
		parts = append(parts, fmt.Sprintf("session: turn=%d compactions=%d", state.TurnCount, state.CompactionCount))
	}

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

	if len(state.FileTrackerSummary) > 0 {
		parts = append(parts, "tracked files:\n- "+strings.Join(state.FileTrackerSummary, "\n- "))
	}

	if len(state.RecentToolCalls) > 0 {
		parts = append(parts, "recent tool calls:\n- "+strings.Join(state.RecentToolCalls, "\n- "))
	}

	scratchpad := strings.TrimSpace(state.Scratchpad)
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
