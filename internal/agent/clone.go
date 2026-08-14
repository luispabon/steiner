package agent

import (
	"encoding/json"

	"github.com/luispabon/steiner/internal/tool"
)

func cloneMessages(messages []Message) []Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]Message, len(messages))
	for i := range messages {
		out[i] = cloneMessage(messages[i])
	}
	return out
}

func cloneMessage(message Message) Message {
	cloned := message
	if len(message.ToolCalls) > 0 {
		cloned.ToolCalls = make([]ToolCall, len(message.ToolCalls))
		for i := range message.ToolCalls {
			cloned.ToolCalls[i] = ToolCall{
				ID:           message.ToolCalls[i].ID,
				Name:         message.ToolCalls[i].Name,
				Arguments:    cloneInput(message.ToolCalls[i].Arguments),
				RawArguments: message.ToolCalls[i].RawArguments,
			}
		}
	}
	// ImageBlock holds only value fields, so copying the slice is a deep copy.
	// Unlike the old provider round-trip, this preserves images with empty Data.
	cloned.Images = append([]ImageBlock(nil), message.Images...)
	cloned.Retention = cloneMessageRetention(message.Retention)
	cloned.ProviderMetadata = cloneMessageProviderMetadata(message.ProviderMetadata)
	return cloned
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
	if metadata.Codex != nil {
		codex := *metadata.Codex
		cloned.Codex = &codex
	}
	return cloned
}
