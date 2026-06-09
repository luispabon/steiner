package agent

import (
	"encoding/json"

	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

func cloneMessages(messages []Message) []Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]Message, len(messages))
	copy(out, messages)
	for i := range out {
		out[i].ToolCalls = cloneToolCalls(out[i].ToolCalls)
		out[i].Retention = cloneMessageRetention(out[i].Retention)
		out[i].ProviderMetadata = cloneMessageProviderMetadata(out[i].ProviderMetadata)
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

func cloneProviderMessages(messages []provider.Message) []provider.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]provider.Message, len(messages))
	copy(out, messages)
	for i := range out {
		out[i].ToolCalls = cloneProviderToolCalls(out[i].ToolCalls)
		out[i].ProviderMetadata = cloneProviderMessageMetadata(out[i].ProviderMetadata)
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

func messageRetentionFromToolRetention(retention *tool.ToolRetention) *MessageRetention {
	if retention == nil {
		return nil
	}
	return &MessageRetention{
		Kind:       retention.Kind,
		Summary:    retention.Summary,
		AgentID:    retention.AgentID,
		Status:     retention.Status,
		TurnCount:  retention.TurnCount,
		TokenCount: retention.TokenCount,
	}
}

func cloneMessageRetention(retention *MessageRetention) *MessageRetention {
	if retention == nil {
		return nil
	}
	cloned := *retention
	return &cloned
}

func cloneMessageProviderMetadata(metadata *MessageProviderMetadata) *MessageProviderMetadata {
	if metadata == nil {
		return nil
	}
	cloned := &MessageProviderMetadata{}
	if metadata.Anthropic != nil {
		anthropic := *metadata.Anthropic
		cloned.Anthropic = &anthropic
	}
	return cloned
}

func cloneProviderMessageMetadata(metadata *provider.MessageProviderMetadata) *provider.MessageProviderMetadata {
	if metadata == nil {
		return nil
	}
	cloned := &provider.MessageProviderMetadata{}
	if metadata.Anthropic != nil {
		anthropic := *metadata.Anthropic
		cloned.Anthropic = &anthropic
	}
	return cloned
}
