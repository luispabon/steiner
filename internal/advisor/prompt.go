package advisor

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/provider"
)

const (
	advisorSystemPrompt = "You are Steiner's internal advisor. Review the conversation and give concise strategic guidance for the main agent's next response. Do not call tools, do not invent tool results, and do not address the end user directly."
	advisorUserPrompt   = "Analyze the conversation above and return a short advisory note for the main agent. Focus on risks, missing reasoning, and the most useful next move."

	// maxToolArgStringLen bounds any single string value inside a flattened
	// tool call's arguments. Longer strings (e.g. mutate's whole-file
	// content) are truncated with a size-preserving elision marker so the
	// advisor payload stays bounded without depending on conversation
	// position or budget, which would break prompt caching.
	maxToolArgStringLen = 1000
)

// capToolArgStrings returns a deep copy of v with any string longer than
// maxToolArgStringLen replaced by a truncated prefix plus an elision marker
// that records the original byte length. Maps and slices are recursed into;
// all other values are returned unchanged.
func capToolArgStrings(v any) any {
	switch val := v.(type) {
	case string:
		if len(val) <= maxToolArgStringLen {
			return val
		}
		return fmt.Sprintf("%s…[elided, %d bytes total]", val[:maxToolArgStringLen], len(val))
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, elem := range val {
			out[k] = capToolArgStrings(elem)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, elem := range val {
			out[i] = capToolArgStrings(elem)
		}
		return out
	default:
		return val
	}
}

// flattenToolMessages converts tool_use/tool_result messages to plain text so
// the advisor request requires no toolConfig on the provider side.
func flattenToolMessages(messages []provider.Message) []provider.Message {
	out := make([]provider.Message, 0, len(messages))
	for _, m := range messages {
		switch m.Role {
		case provider.MessageRoleTool:
			content := m.Content
			if content == "" {
				content = "(empty)"
			}
			out = append(out, provider.Message{
				Role:    provider.MessageRoleUser,
				Content: fmt.Sprintf("[tool_result: %s]\n%s", m.Name, content),
			})
		case provider.MessageRoleAssistant:
			if len(m.ToolCalls) == 0 {
				m.ReasoningContent = ""
				m.ProviderMetadata = nil
				out = append(out, m)
				continue
			}
			var sb strings.Builder
			if m.Content != "" {
				sb.WriteString(m.Content)
				sb.WriteByte('\n')
			}
			for _, tc := range m.ToolCalls {
				argsJSON, err := json.Marshal(capToolArgStrings(tc.Arguments))
				if err != nil {
					argsJSON = []byte(fmt.Sprintf("%v", tc.Arguments))
				}
				fmt.Fprintf(&sb, "[tool_call: %s %s]", tc.Name, argsJSON)
				sb.WriteByte('\n')
			}
			out = append(out, provider.Message{
				Role:    provider.MessageRoleAssistant,
				Content: strings.TrimSuffix(sb.String(), "\n"),
			})
		default:
			m.ReasoningContent = ""
			m.ProviderMetadata = nil
			out = append(out, m)
		}
	}
	return out
}

func buildMessages(snapshot []provider.Message) []provider.Message {
	messages := make([]provider.Message, 0, len(snapshot)+2)
	messages = append(messages, provider.Message{
		Role:    provider.MessageRoleSystem,
		Content: advisorSystemPrompt,
	})
	messages = append(messages, provider.CloneMessages(flattenToolMessages(snapshot))...)
	messages = append(messages, provider.Message{
		Role:    provider.MessageRoleUser,
		Content: advisorUserPrompt,
	})
	return messages
}
