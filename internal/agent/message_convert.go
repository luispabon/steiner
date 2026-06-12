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

// ToReplaySafeProviderMessages converts agent messages to provider messages
// after normalizing the transcript for provider replay.
func ToReplaySafeProviderMessages(messages []Message) []provider.Message {
	return ToProviderMessages(ReplaySafeConversation(messages))
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
		ProviderMetadata: toProviderMessageMetadata(message.ProviderMetadata),
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
	for _, img := range message.Images {
		if img.Data == "" {
			continue
		}
		out.Images = append(out.Images, provider.ImageBlock{
			MediaType: img.MediaType,
			Data:      img.Data,
			Width:     img.Width,
			Height:    img.Height,
			SizeBytes: img.SizeBytes,
		})
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
		ProviderMetadata: fromProviderMessageMetadata(message.ProviderMetadata),
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
	if len(message.Images) > 0 {
		out.Images = make([]ImageBlock, 0, len(message.Images))
		for _, img := range message.Images {
			out.Images = append(out.Images, ImageBlock{
				MediaType: img.MediaType,
				Data:      img.Data,
				Width:     img.Width,
				Height:    img.Height,
				SizeBytes: img.SizeBytes,
			})
		}
	}
	return out
}

func toProviderMessageMetadata(metadata *MessageProviderMetadata) *provider.MessageProviderMetadata {
	if metadata == nil {
		return nil
	}
	out := &provider.MessageProviderMetadata{}
	if metadata.Anthropic != nil {
		out.Anthropic = &provider.AnthropicMessageMetadata{
			ThinkingSignature: metadata.Anthropic.ThinkingSignature,
		}
	}
	return out
}

func fromProviderMessageMetadata(metadata *provider.MessageProviderMetadata) *MessageProviderMetadata {
	if metadata == nil {
		return nil
	}
	out := &MessageProviderMetadata{}
	if metadata.Anthropic != nil {
		out.Anthropic = &AnthropicMessageMetadata{
			ThinkingSignature: metadata.Anthropic.ThinkingSignature,
		}
	}
	return out
}

func assemblyOptions(base prompt.AssemblyOptions, state RunState) prompt.AssemblyOptions {
	conversation := state.Lineage.FullMessages()
	if len(conversation) == 0 {
		conversation = state.Conversation
	}

	providerMsgs := ToReplaySafeProviderMessages(conversation)

	base.Conversation = providerMsgs
	base.ToolResults = nil
	base.ContextState = toPromptContext(state.Context)
	return base
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
