package agent

import "strings"

func (s *ContextStateManager) resetTaskStateIfNeeded(state *RunState) {
	if state == nil {
		return
	}
	message, ok := latestUserMessage(state.Lineage.FullMessages())
	if !ok && len(state.Conversation) > 0 {
		message, ok = latestUserMessage(state.Conversation)
	}
	if !ok || !shouldResetTaskState(message.Content) {
		return
	}
	state.Context.FileTrackerSummary = nil
	state.Context.RecentToolCalls = nil
}

func latestUserMessage(messages []Message) (Message, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == MessageRoleUser {
			return messages[i], true
		}
	}
	return Message{}, false
}

func shouldResetTaskState(content string) bool {
	switch normalizeDirectiveText(content) {
	case "commit changes", "run tests", "review this", "stop":
		return true
	default:
		return false
	}
}

func normalizeDirectiveText(content string) string {
	content = strings.ToLower(strings.TrimSpace(content))
	content = strings.TrimRight(content, ".!?")
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "please ")
	return strings.TrimSpace(content)
}
