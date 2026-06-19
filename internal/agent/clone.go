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
	providerMessages := provider.CloneMessages(ToProviderMessages(messages))
	out := fromProviderMessages(providerMessages)
	for i := range out {
		if messages[i].Role == MessageRoleSummary {
			out[i].Role = MessageRoleSummary
		}
		out[i].Retention = cloneMessageRetention(messages[i].Retention)
	}
	return out
}

func cloneToolCalls(calls []ToolCall) []ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]ToolCall, len(calls))
	for i := range calls {
		out[i] = ToolCall{
			ID:           calls[i].ID,
			Name:         calls[i].Name,
			Arguments:    cloneInput(calls[i].Arguments),
			RawArguments: calls[i].RawArguments,
		}
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
