package agent

import (
	"strings"

	"github.com/luispabon/steiner/internal/output"
)

// commitInterruptedAssistant appends the assistant text that streamed before a
// failed model call as an incomplete assistant message. It runs once at run
// exit, so a partial survives cancellation, a usage limit, and retries the
// runner abandoned. The message and the event carry the exact streamed text;
// only the provider projection adds the note that marks it incomplete.
//
// Content is known non-blank here, so isStrictlyEmptyAssistant cannot apply.
func commitInterruptedAssistant(req RunRequest, state RunState) RunState {
	pending := state.PendingAssistant
	state.PendingAssistant = PendingAssistant{}
	if strings.TrimSpace(pending.Content) == "" {
		return state
	}
	partial := Message{
		Role:       MessageRoleAssistant,
		Content:    pending.Content,
		Turn:       pending.Turn,
		Incomplete: true,
	}
	partial = stampMessageTimestamp(partial)
	state.Conversation = append(state.Conversation, partial)
	if !state.Lineage.Empty() {
		state.Lineage = state.Lineage.WithAppendedMessages([]Message{partial})
	}
	if pending.Turn > state.TurnCount {
		state.TurnCount = pending.Turn
	}
	emitEvent(req.Events, output.NewAssistantMessageEvent(pending.Turn, string(MessageRoleAssistant), pending.Content))
	return state
}
