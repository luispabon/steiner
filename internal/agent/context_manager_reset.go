package agent

import "strings"

// minTurnInMessages returns the smallest positive Turn value across all
// messages, or 0 if no messages have a Turn set.
func minTurnInMessages(messages []Message) int {
	minTurn := 0
	for _, m := range messages {
		if m.Turn > 0 {
			if minTurn == 0 || m.Turn < minTurn {
				minTurn = m.Turn
			}
		}
	}
	return minTurn
}

func (s *SmartContextManager) resetTaskStateIfNeeded(state *RunState) {
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
	s.scratchpad.Reset()
	state.Context.ActiveFocus = nil
	state.Context.UnresolvedWork = nil
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
