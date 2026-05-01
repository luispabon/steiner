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

	totalAssistantTurns := 0
	for _, message := range messages {
		if message.Role == provider.MessageRoleAssistant {
			totalAssistantTurns++
		}
	}
	if totalAssistantTurns <= windowTurns {
		return cloneProviderMessages(messages)
	}

	cutoffTurn := totalAssistantTurns - windowTurns
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
				cloned.Content = maskAssistantContent(cloned.Content)
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

func maskAssistantContent(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	line := content
	if idx := strings.IndexByte(content, '\n'); idx >= 0 {
		line = content[:idx]
	}
	return strings.TrimSpace(line)
}

func maskToolResult(message provider.Message, toolCalls []provider.ToolCall) string {
	name, args := toolCallMetadata(message, toolCalls)
	if name == "" {
		name = strings.TrimSpace(message.Name)
	}
	if name == "" {
		name = "tool"
	}

	parts := []string{fmt.Sprintf("[older tool result masked: %s", name)}
	if args != "" {
		parts = append(parts, "args="+args)
	}
	parts = append(parts, "]")
	return strings.Join(parts, " ")
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
		out[key] = value
	}
	return out
}
