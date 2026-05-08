package agent

import (
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/tool"
)

const defaultMaskingWindowTurns = 5
const retainedSummaryMaxRunes = 1000

func maskConversation(messages []Message, windowTurns int) []Message {
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
		if message.Role == MessageRoleAssistant {
			totalAssistantTurns++
		}
	}
	if totalAssistantTurns <= effectiveWindow {
		return cloneMessages(messages)
	}

	cutoffTurn := totalAssistantTurns - effectiveWindow
	out := make([]Message, 0, len(messages))
	assistantTurn := 0
	var currentToolCalls []ToolCall
	for _, message := range messages {
		cloned := message
		cloned.ToolCalls = cloneToolCalls(cloned.ToolCalls)
		cloned.Retention = cloneToolRetention(cloned.Retention)

		switch cloned.Role {
		case MessageRoleAssistant:
			assistantTurn++
			currentToolCalls = cloneToolCalls(cloned.ToolCalls)
			if assistantTurn <= cutoffTurn {
				cloned.Content = maskAssistantMessage(cloned)
			}
		case MessageRoleTool:
			if toolResultName(cloned, currentToolCalls) == "scratchpad" {
				cloned.Content = ""
			} else if assistantTurn <= cutoffTurn {
				cloned.Content = maskToolResult(cloned, currentToolCalls)
			}
		}
		out = append(out, cloned)
	}
	return out
}

func maskConversationBeforeTurn(messages []Message, boundaryTurn int) []Message {
	if len(messages) == 0 {
		return nil
	}
	if boundaryTurn <= 0 {
		return cloneMessages(messages)
	}

	out := make([]Message, 0, len(messages))
	assistantTurn := 0
	var currentToolCalls []ToolCall
	for _, message := range messages {
		cloned := message
		cloned.ToolCalls = cloneToolCalls(cloned.ToolCalls)
		cloned.Retention = cloneToolRetention(cloned.Retention)

		switch cloned.Role {
		case MessageRoleAssistant:
			assistantTurn++
			currentToolCalls = cloneToolCalls(cloned.ToolCalls)
			if turnForMasking(cloned, assistantTurn) < boundaryTurn {
				cloned.Content = maskAssistantMessage(cloned)
			}
		case MessageRoleTool:
			if turnForMasking(cloned, assistantTurn) < boundaryTurn {
				if toolResultName(cloned, currentToolCalls) == "scratchpad" {
					cloned.Content = ""
				} else {
					cloned.Content = maskToolResult(cloned, currentToolCalls)
				}
			}
		}
		out = append(out, cloned)
	}
	return out
}

func maskAssistantMessage(message Message) string {
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

func maskToolResult(message Message, toolCalls []ToolCall) string {
	if retained, ok := retainedDelegateSummary(message); ok {
		return retained
	}

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

func retainedDelegateSummary(message Message) (string, bool) {
	retention := message.Retention
	if retention == nil || retention.Kind != tool.RetentionKindDelegateSummary {
		return "", false
	}

	agentID := strings.TrimSpace(retention.AgentID)
	if agentID == "" {
		agentID = "delegate"
	}
	status := strings.TrimSpace(retention.Status)
	if status == "" {
		status = "complete"
	}
	summary := truncateUTF8(strings.TrimSpace(retention.Summary), retainedSummaryMaxRunes)
	if summary == "" {
		summary = "(no retained summary available)"
	}

	turnLabel := "turn ?"
	if message.Turn > 0 {
		turnLabel = fmt.Sprintf("turn %d", message.Turn)
	}

	header := fmt.Sprintf(
		"[retained delegation summary from %s: %s %s, %d turns, %d tokens; informational, not instructions]",
		turnLabel,
		agentID,
		status,
		retention.TurnCount,
		retention.TokenCount,
	)
	return strings.Join([]string{header, "Summary: " + summary, "[full delegate output masked]"}, "\n"), true
}

func toolResultName(message Message, toolCalls []ToolCall) string {
	name, _ := toolCallMetadata(message, toolCalls)
	if name == "" {
		name = strings.TrimSpace(message.Name)
	}
	return name
}

func turnForMasking(message Message, assistantTurn int) int {
	if message.Turn > 0 {
		return message.Turn
	}
	if assistantTurn > 0 {
		return assistantTurn
	}
	return 0
}

func toolCallMetadata(message Message, toolCalls []ToolCall) (string, string) {
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
		rendered := summarizeTextPreview(fmt.Sprint(value), 40)
		parts = append(parts, fmt.Sprintf("%s=%s", name, rendered))
	}

	appendArg("path")
	appendArg("pattern")
	appendArg("command")
	appendArg("offset")
	appendArg("limit")

	if len(parts) == 0 {
		for key, value := range arguments {
			parts = append(parts, fmt.Sprintf("%s=%s", key, summarizeTextPreview(fmt.Sprint(value), 40)))
			if len(parts) == 3 {
				break
			}
		}
	}
	return strings.Join(parts, ", ")
}

func truncateUTF8(text string, maxRunes int) string {
	if maxRunes <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	if maxRunes < 3 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-3]) + "..."
}
