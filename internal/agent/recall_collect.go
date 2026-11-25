package agent

import (
	"context"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
)

// collectRecallEntries returns the recall entries for every result body in
// msgs, in message order: one per tool message and one per sub-agent result
// envelope in a user message. An envelope entry holds the whole parsed inner
// text, preamble included, so the archived body is byte-identical to what the
// model read. Bodies are kept verbatim, including text shaped like recall
// markers. It writes nothing.
func collectRecallEntries(msgs []Message) []RecallEntry {
	var entries []RecallEntry
	for _, msg := range msgs {
		switch msg.Role {
		case MessageRoleTool:
			entries = append(entries, RecallEntry{
				Kind:   "tool",
				Tool:   msg.Name,
				CallID: msg.ToolCallID,
				Body:   msg.Content,
			})
		case MessageRoleUser:
			for _, raw := range prompt.SplitMessageBlocks(msg.Content).ResultEnvelopes {
				parsed, ok := ParseSubAgentResultEnvelope(raw)
				if !ok {
					// SplitMessageBlocks returns only envelopes it parsed, so this
					// is unreachable unless the two parsers disagree.
					continue
				}
				entries = append(entries, RecallEntry{
					Kind:   "agent",
					Tool:   parsed.AgentType,
					CallID: parsed.CallID,
					Body:   parsed.Inner,
				})
			}
		}
	}
	return entries
}

// archiveRecallEntries writes one recall entry per result body in removed to
// archive, so a body compaction drops can be read back later. A nil archive
// does nothing. A failed Write emits a compact warning and collection
// continues with the next entry.
func archiveRecallEntries(ctx context.Context, archive RecallArchive, events output.EventSink, removed []Message) {
	if archive == nil {
		return
	}
	for _, entry := range collectRecallEntries(removed) {
		if _, err := archive.Write(ctx, entry); err != nil {
			emitEvent(events, recallArchiveWarning(err))
		}
	}
}

// recallArchiveWarning reports a failed recall write as a compact warning.
func recallArchiveWarning(err error) output.Event {
	return output.NewConversationWarningEvent(output.ConversationWarningKindCompact, "archive recall body: "+err.Error())
}
