package agent

import "strings"

// ReplaySafeConversation returns a cloned conversation that is safe to replay
// to OpenAI-compatible providers.
//
// Valid assistant/tool exchanges are preserved verbatim. Orphan tool messages
// are dropped. Assistant messages with tool calls that do not have the
// complete matching sequence of tool results immediately following them have
// their ToolCalls cleared so the transcript remains provider-valid.
func ReplaySafeConversation(conversation []Message) []Message {
	if len(conversation) == 0 {
		return nil
	}

	out := make([]Message, 0, len(conversation))
	for i := 0; i < len(conversation); i++ {
		// Tool results are cloned only once their assistant's exchange is
		// validated, so a dropped or unpaired result costs no copy.
		if conversation[i].Role == MessageRoleTool {
			continue
		}
		msg := cloneMessage(conversation[i])
		if msg.Role != MessageRoleAssistant || len(msg.ToolCalls) == 0 {
			if replayDropsEmptyAssistant(msg) {
				continue
			}
			out = append(out, msg)
			continue
		}

		nextIndex, ok := replayPairedToolEnd(conversation, i, len(msg.ToolCalls))
		if !ok {
			msg.ToolCalls = nil
			if replayDropsEmptyAssistant(msg) {
				continue
			}
			out = append(out, msg)
			continue
		}

		out = append(out, msg)
		for toolIndex := i + 1; toolIndex < nextIndex; toolIndex++ {
			out = append(out, cloneMessage(conversation[toolIndex]))
		}
		i = nextIndex - 1
	}

	return out
}

// replayPairedToolEnd reports the index just past the complete run of tool
// results that follows the assistant message at assistantIndex. It clones
// nothing, so callers copy the run straight into their output slice.
func replayPairedToolEnd(conversation []Message, assistantIndex, toolCallCount int) (int, bool) {
	if assistantIndex < 0 || assistantIndex >= len(conversation) {
		return assistantIndex, false
	}

	assistant := conversation[assistantIndex]
	nextIndex := assistantIndex + 1

	for toolIndex := 0; toolIndex < toolCallCount; toolIndex++ {
		if nextIndex >= len(conversation) {
			return nextIndex, false
		}
		toolMsg := conversation[nextIndex]
		if toolMsg.Role != MessageRoleTool {
			return nextIndex, false
		}
		expectedID := assistant.ToolCalls[toolIndex].ID
		if expectedID != "" && toolMsg.ToolCallID != expectedID {
			return nextIndex, false
		}
		nextIndex++
	}

	return nextIndex, true
}

func replaySafeRunState(state RunState) RunState {
	conversation := ReplaySafeConversation(state.Conversation)
	state.Conversation = conversation
	if state.Lineage.Empty() {
		state.Lineage = newConversationLineage(conversation)
		return state
	}
	state.Lineage = state.Lineage.WithCurrentMessages(conversation)
	return state
}

func replayDropsEmptyAssistant(msg Message) bool {
	return msg.Role == MessageRoleAssistant && len(msg.ToolCalls) == 0 && strings.TrimSpace(msg.Content) == ""
}
