package provider

import "encoding/json"

// CloneMessages deep-copies provider messages for safe reuse across packages.
func CloneMessages(messages []Message) []Message {
	if len(messages) == 0 {
		return nil
	}
	cloned := make([]Message, len(messages))
	copy(cloned, messages)
	for i := range cloned {
		cloned[i].ToolCalls = CloneToolCalls(cloned[i].ToolCalls)
		cloned[i].ProviderMetadata = CloneMessageMetadata(cloned[i].ProviderMetadata)
	}
	return cloned
}

// CloneTools deep-copies provider tool definitions for safe reuse across packages.
func CloneTools(tools []ToolSpec) []ToolSpec {
	if len(tools) == 0 {
		return nil
	}
	cloned := make([]ToolSpec, len(tools))
	copy(cloned, tools)
	for i := range cloned {
		cloned[i].Function.Parameters = CloneToolArguments(cloned[i].Function.Parameters)
	}
	return cloned
}

// CloneToolCalls deep-copies provider tool calls, including nested arguments.
func CloneToolCalls(calls []ToolCall) []ToolCall {
	if len(calls) == 0 {
		return nil
	}
	cloned := make([]ToolCall, len(calls))
	for i := range calls {
		cloned[i] = ToolCall{
			ID:           calls[i].ID,
			Name:         calls[i].Name,
			Arguments:    CloneToolArguments(calls[i].Arguments),
			RawArguments: calls[i].RawArguments,
		}
	}
	return cloned
}

// CloneToolArguments deep-copies provider tool arguments recursively.
func CloneToolArguments(arguments map[string]any) map[string]any {
	if arguments == nil {
		return nil
	}
	cloned := make(map[string]any, len(arguments))
	for key, value := range arguments {
		cloned[key] = cloneArgumentValue(value)
	}
	return cloned
}

// CloneMessageMetadata deep-copies provider-native message metadata.
func CloneMessageMetadata(metadata *MessageProviderMetadata) *MessageProviderMetadata {
	if metadata == nil {
		return nil
	}
	cloned := &MessageProviderMetadata{}
	if metadata.Anthropic != nil {
		anthropic := *metadata.Anthropic
		cloned.Anthropic = &anthropic
	}
	if metadata.Codex != nil {
		codex := *metadata.Codex
		cloned.Codex = &codex
	}
	return cloned
}

func cloneArgumentValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return CloneToolArguments(typed)
	case []any:
		cloned := make([]any, len(typed))
		for i := range typed {
			cloned[i] = cloneArgumentValue(typed[i])
		}
		return cloned
	case json.RawMessage:
		return append(json.RawMessage(nil), typed...)
	default:
		return value
	}
}
