package agent

import (
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

func assemblyOptions(base prompt.AssemblyOptions, state RunState) prompt.AssemblyOptions {
	conversation := state.Lineage.SummaryPrefixStrippedMessages()
	if len(conversation) == 0 {
		conversation = state.Conversation
	}
	base.Conversation = toProviderMessages(conversation)
	base.ToolResults = nil
	base.ContextState = toPromptContext(state.Context)
	base.ScratchpadEnabled = base.ScratchpadEnabled || strings.TrimSpace(state.Context.Scratchpad) != ""
	return base
}

func toPromptContext(state ContextState) prompt.DurableContextState {
	out := prompt.DurableContextState{
		ActiveConstraints:  make([]prompt.DurableContextEntry, 0, len(state.ActiveConstraints)),
		UnresolvedWork:     make([]prompt.DurableContextEntry, 0, len(state.UnresolvedWork)),
		RetainedSummaries:  make([]prompt.DurableSummaryEntry, 0, len(state.RetainedSummaries)),
		FileTrackerSummary: cloneStrings(state.FileTrackerSummary),
		RecentToolCalls:    cloneStrings(state.RecentToolCalls),
		TurnCount:          state.TurnCount,
		CompactionCount:    state.CompactionCount,
		Scratchpad:         state.Scratchpad,
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
		ActiveConstraints:  make([]ActiveConstraint, 0, len(state.ActiveConstraints)),
		UnresolvedWork:     make([]UnresolvedWorkItem, 0, len(state.UnresolvedWork)),
		RetainedSummaries:  make([]RetainedSummary, 0, len(state.RetainedSummaries)),
		FileTrackerSummary: cloneStrings(state.FileTrackerSummary),
		RecentToolCalls:    cloneStrings(state.RecentToolCalls),
		TurnCount:          state.TurnCount,
		CompactionCount:    state.CompactionCount,
		Scratchpad:         state.Scratchpad,
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
