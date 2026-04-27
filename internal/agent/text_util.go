package agent

import (
	"fmt"
	"strings"
)

func summarizeConversationMessages(messages []Message, maxMessages int) string {
	if len(messages) == 0 {
		return "none recorded"
	}
	if maxMessages <= 0 || maxMessages > len(messages) {
		maxMessages = len(messages)
	}
	start := len(messages) - maxMessages
	parts := make([]string, 0, maxMessages)
	for i := start; i < len(messages); i++ {
		message := messages[i]
		parts = append(parts, fmt.Sprintf("%s: %s", message.Role, summarizeTextPreview(message.Content, 80)))
	}
	return strings.Join(parts, " | ")
}

func firstMessageContentByRole(messages []Message, role MessageRole) string {
	for _, message := range messages {
		if message.Role == role && strings.TrimSpace(message.Content) != "" {
			return message.Content
		}
	}
	return ""
}

func summarizeTextPreview(text string, limit int) string {
	text = strings.TrimSpace(strings.Join(strings.Fields(text), " "))
	if len(text) <= limit {
		return text
	}
	if limit <= 3 {
		return text[:limit]
	}
	return text[:limit-3] + "..."
}

func countTurns(messages []Message) int {
	if len(messages) == 0 {
		return 0
	}
	turns := 0
	for _, message := range messages {
		if message.Role == MessageRoleUser {
			turns++
		}
	}
	if turns == 0 {
		return 1
	}
	return turns
}
