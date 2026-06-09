package prompt

import (
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/provider"
)

const defaultMaskingWindowTurns = 5

// MaskConversation returns a copy of messages with older assistant prose and
// tool results compacted while preserving recent turns verbatim.
func MaskConversation(messages []provider.Message, windowTurns int) []provider.Message {
	if len(messages) == 0 {
		return nil
	}
	if windowTurns <= 0 {
		windowTurns = defaultMaskingWindowTurns
	}

	effectiveWindow := windowTurns
	if effectiveWindow < 2 {
		effectiveWindow = 2
	}

	totalAssistantTurns := 0
	for _, message := range messages {
		if message.Role == provider.MessageRoleAssistant {
			totalAssistantTurns++
		}
	}
	if totalAssistantTurns <= effectiveWindow {
		return cloneProviderMessages(messages)
	}

	cutoffTurn := totalAssistantTurns - effectiveWindow
	out := make([]provider.Message, 0, len(messages))
	assistantTurn := 0
	var currentToolCalls []provider.ToolCall
	for _, message := range messages {
		cloned := cloneProviderMessage(message)
		switch cloned.Role {
		case provider.MessageRoleAssistant:
			assistantTurn++
			currentToolCalls = cloneProviderToolCalls(cloned.ToolCalls)
			if assistantTurn <= cutoffTurn {
				cloned.Content = maskAssistantMessage(cloned)
			}
		case provider.MessageRoleTool:
			if assistantTurn <= cutoffTurn {
				cloned.Content = maskToolResult(cloned, currentToolCalls)
			}
		}
		out = append(out, cloned)
	}
	return out
}

// MaskConversationBeforeTurn returns a copy of messages with older assistant
// prose and tool results compacted while preserving the suffix at and after the
// supplied turn boundary.
func MaskConversationBeforeTurn(messages []provider.Message, boundaryTurn int) []provider.Message {
	if len(messages) == 0 {
		return nil
	}
	if boundaryTurn <= 0 {
		return cloneProviderMessages(messages)
	}

	out := make([]provider.Message, 0, len(messages))
	assistantTurn := 0
	var currentToolCalls []provider.ToolCall
	for _, message := range messages {
		cloned := cloneProviderMessage(message)
		switch cloned.Role {
		case provider.MessageRoleAssistant:
			assistantTurn++
			currentToolCalls = cloneProviderToolCalls(cloned.ToolCalls)
			if turnForMasking(cloned, assistantTurn) < boundaryTurn {
				cloned.Content = maskAssistantMessage(cloned)
			}
		case provider.MessageRoleTool:
			if turnForMasking(cloned, assistantTurn) < boundaryTurn {
				cloned.Content = maskToolResult(cloned, currentToolCalls)
			}
		}
		out = append(out, cloned)
	}
	return out
}

func maskAssistantMessage(message provider.Message) string {
	content := strings.TrimSpace(message.Content)
	if content == "" {
		return ""
	}
	line := content
	if idx := strings.IndexByte(content, '\n'); idx >= 0 {
		line = content[:idx]
	}
	line = strings.TrimSpace(line)
	if message.Turn > 0 {
		return fmt.Sprintf("[turn %d] %s", message.Turn, line)
	}
	return line
}

func maskToolResult(message provider.Message, toolCalls []provider.ToolCall) string {
	name, args := toolCallMetadata(message, toolCalls)
	if name == "" {
		name = strings.TrimSpace(message.Name)
	}
	if name == "" {
		name = "tool"
	}

	var prefix string
	if message.Turn > 0 {
		prefix = fmt.Sprintf("[tool result from turn %d masked: %s", message.Turn, name)
	} else {
		prefix = fmt.Sprintf("[older tool result masked: %s", name)
	}

	parts := []string{prefix}
	if args != "" {
		parts = append(parts, "args="+args)
	}
	if message.Turn > 0 {
		parts = append(parts, "- re-read if needed]")
	} else {
		parts = append(parts, "]")
	}
	return strings.Join(parts, " ")
}

func turnForMasking(message provider.Message, assistantTurn int) int {
	if message.Turn > 0 {
		return message.Turn
	}
	if assistantTurn > 0 {
		return assistantTurn
	}
	return 0
}

func toolCallMetadata(message provider.Message, toolCalls []provider.ToolCall) (string, string) {
	if message.ToolCallID == "" {
		if len(toolCalls) == 1 {
			return toolCalls[0].Name, summarizeToolArguments(toolCalls[0].Arguments)
		}
		return "", ""
	}
	for _, call := range toolCalls {
		if call.ID == message.ToolCallID {
			return call.Name, summarizeToolArguments(call.Arguments)
		}
	}
	return "", ""
}

func summarizeToolArguments(arguments map[string]any) string {
	if len(arguments) == 0 {
		return ""
	}
	parts := make([]string, 0, len(arguments))
	appendArg := func(name string) {
		value, ok := arguments[name]
		if !ok {
			return
		}
		rendered := compactMessageContent(fmt.Sprint(value), 40)
		parts = append(parts, fmt.Sprintf("%s=%s", name, rendered))
	}

	appendArg("path")
	appendArg("pattern")
	appendArg("command")
	appendArg("offset")
	appendArg("limit")

	if len(parts) == 0 {
		for key, value := range arguments {
			parts = append(parts, fmt.Sprintf("%s=%s", key, compactMessageContent(fmt.Sprint(value), 40)))
			if len(parts) == 3 {
				break
			}
		}
	}
	return strings.Join(parts, ", ")
}

func cloneProviderMessages(messages []provider.Message) []provider.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]provider.Message, 0, len(messages))
	for _, message := range messages {
		out = append(out, cloneProviderMessage(message))
	}
	return out
}

func cloneProviderMessage(message provider.Message) provider.Message {
	cloned := message
	cloned.ToolCalls = cloneProviderToolCalls(message.ToolCalls)
	cloned.ProviderMetadata = cloneProviderMessageMetadata(message.ProviderMetadata)
	return cloned
}

func cloneProviderToolCalls(calls []provider.ToolCall) []provider.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]provider.ToolCall, 0, len(calls))
	for _, call := range calls {
		out = append(out, provider.ToolCall{
			ID:        call.ID,
			Name:      call.Name,
			Arguments: cloneToolArguments(call.Arguments),
		})
	}
	return out
}

func cloneToolArguments(arguments map[string]any) map[string]any {
	if len(arguments) == 0 {
		return nil
	}
	out := make(map[string]any, len(arguments))
	for key, value := range arguments {
		out[key] = cloneToolArgumentValue(value)
	}
	return out
}

func cloneToolArgumentValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneToolArguments(typed)
	case []any:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = cloneToolArgumentValue(typed[i])
		}
		return out
	default:
		return value
	}
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
