package output

import "github.com/luispabon/steiner/internal/provider"

func cloneProviderMessages(messages []provider.Message) []provider.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]provider.Message, len(messages))
	for i, message := range messages {
		out[i] = message
		if len(message.ToolCalls) > 0 {
			out[i].ToolCalls = make([]provider.ToolCall, len(message.ToolCalls))
			for j, call := range message.ToolCalls {
				out[i].ToolCalls[j] = call
				if len(call.Arguments) > 0 {
					out[i].ToolCalls[j].Arguments = cloneMap(call.Arguments)
				}
			}
		}
	}
	return out
}

func cloneProviderTools(tools []provider.ToolSpec) []provider.ToolSpec {
	if len(tools) == 0 {
		return nil
	}
	out := make([]provider.ToolSpec, len(tools))
	for i, tool := range tools {
		out[i] = tool
		if len(tool.Function.Parameters) > 0 {
			out[i].Function.Parameters = cloneMap(tool.Function.Parameters)
		}
	}
	return out
}

func cloneMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = cloneAny(value)
	}
	return out
}

func cloneAny(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return cloneMap(v)
	case []any:
		out := make([]any, len(v))
		for i := range v {
			out[i] = cloneAny(v[i])
		}
		return out
	default:
		return value
	}
}

func cloneOptionalInt(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
