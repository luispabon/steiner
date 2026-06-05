package agent

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
		msg := cloneMessages([]Message{conversation[i]})[0]

		if msg.Role == MessageRoleTool {
			continue
		}
		if msg.Role != MessageRoleAssistant || len(msg.ToolCalls) == 0 {
			out = append(out, msg)
			continue
		}

		pairedTools, nextIndex, ok := replayPairedToolMessages(conversation, i, len(msg.ToolCalls))
		if !ok {
			msg.ToolCalls = nil
			out = append(out, msg)
			continue
		}

		out = append(out, msg)
		out = append(out, pairedTools...)
		i = nextIndex - 1
	}

	return out
}

func replayPairedToolMessages(conversation []Message, assistantIndex, toolCallCount int) ([]Message, int, bool) {
	if toolCallCount == 0 {
		return nil, assistantIndex + 1, true
	}
	if assistantIndex < 0 || assistantIndex >= len(conversation) {
		return nil, assistantIndex, false
	}

	assistant := conversation[assistantIndex]
	paired := make([]Message, 0, toolCallCount)
	nextIndex := assistantIndex + 1

	for toolIndex := 0; toolIndex < toolCallCount; toolIndex++ {
		if nextIndex >= len(conversation) {
			return nil, nextIndex, false
		}
		toolMsg := conversation[nextIndex]
		if toolMsg.Role != MessageRoleTool {
			return nil, nextIndex, false
		}
		expectedID := assistant.ToolCalls[toolIndex].ID
		if expectedID != "" && toolMsg.ToolCallID != expectedID {
			return nil, nextIndex, false
		}
		paired = append(paired, cloneMessages([]Message{toolMsg})...)
		nextIndex++
	}

	return paired, nextIndex, true
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
