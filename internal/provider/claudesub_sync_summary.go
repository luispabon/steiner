package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// markInterrupted records that the CLI was interrupted (Esc or tool
// cancellation) so the next request tolerates the resulting clean-up edits
// (D22). Only the assistant produced by the interrupted call may become
// interruptible: the last assistant entry, provided no user message was sent
// after it. Once a follow-up user was written, that assistant belongs to a
// completed call and is left immutable.
func (s *claudeSubSync) markInterrupted() {
	s.interrupted = true
	s.interruptible = -1
	lastAssistant, lastUser := -1, -1
	for i, e := range s.entries {
		switch e.Role {
		case MessageRoleAssistant:
			lastAssistant = i
		case MessageRoleUser:
			lastUser = i
		}
	}
	if lastAssistant >= 0 && lastAssistant > lastUser {
		s.interruptible = lastAssistant
	}
}

// planAdvisor returns the messages an advisor request adds to its CLI
// transcript: the parent-snapshot delta since the last advisor call (rendered
// as one transcript message on the first call) followed by the files+question
// message. The advisor process is append-only per parent session (D19).
func (s *claudeSubSync) planAdvisor(req ChatRequest) (claudeSubDelta, error) {
	if len(req.Messages) == 0 || req.Messages[len(req.Messages)-1].Role != MessageRoleUser {
		return claudeSubDelta{}, fmt.Errorf("claude_subscription advisor: request must end with a user message")
	}
	msgs := claudeSubMessages(req.Messages)
	if len(msgs) == 0 {
		return claudeSubDelta{}, fmt.Errorf("claude_subscription advisor: request has no messages")
	}
	snapshot := msgs[:len(msgs)-1]
	final := msgs[len(msgs)-1]
	// Compare the whole already-sent prefix, not just its length: any rewrite of
	// an earlier snapshot message, even at equal length, is a history change.
	if len(snapshot) < len(s.advisorEntries) {
		return claudeSubDelta{}, errClaudeSubHistoryChanged
	}
	for i, entry := range s.advisorEntries {
		if !claudeSubEntrySnapshotEqual(entry, snapshot[i]) {
			return claudeSubDelta{}, errClaudeSubHistoryChanged
		}
	}
	from := len(s.advisorEntries)
	if !s.started {
		from = 0
	}
	var user []Message
	if part := snapshot[from:]; len(part) > 0 {
		user = append(user, Message{Role: MessageRoleUser, Content: claudeSubTranscript(part)})
	}
	user = append(user, final)
	return claudeSubDelta{User: user}, nil
}

// commitAdvisor records that an advisor request delivered the given parent
// snapshot, so the next call sends only the parent-conversation delta.
func (s *claudeSubSync) commitAdvisor(snapshot []Message) {
	s.advisorEntries = make([]claudeSubEntry, 0, len(snapshot))
	for _, m := range snapshot {
		s.advisorEntries = append(s.advisorEntries, claudeSubEntryFromMessage(m))
	}
	s.started = true
}

// errClaudeSubSummaryNoPlace reports that an in-place summary cannot be
// appended to the live transcript. The caller falls back to a reseed.
var errClaudeSubSummaryNoPlace = errors.New("claude_subscription summary request does not extend the live transcript")

// taint marks the record as no longer describing the CLI transcript, after an
// in-place summary was sent. A tainted record plans and checkpoints nothing, so
// its process is never reused and returns no backend state.
func (s *claudeSubSync) taint() {
	s.tainted = true
}

// sourceTailUUID returns the CLI uuid of the entry that ends the first source
// request messages. It reports false unless the record starts with exactly those
// messages and that entry is an assistant message with a uuid.
func (s *claudeSubSync) sourceTailUUID(msgs []Message, source int) (string, bool) {
	if source <= 0 || source > len(msgs) || source > len(s.entries) {
		return "", false
	}
	if !s.sourcePrefixMatches(msgs, source) {
		return "", false
	}
	last := s.entries[source-1]
	if last.Role != MessageRoleAssistant || last.UUID == "" {
		return "", false
	}
	return last.UUID, true
}

// planSummary returns the delta of an in-place summary request. The source
// messages must be a prefix of the record, and the instruction after them is
// appended after the whole record, retained tail included. It never changes the
// record, and it reports errClaudeSubSummaryNoPlace when the record cannot take
// the request.
func (s *claudeSubSync) planSummary(msgs []Message, source int) (claudeSubDelta, error) {
	if s.tainted || !s.started || s.interrupted || source <= 0 || len(msgs) != source+1 || len(s.entries) < source || msgs[source].Role != MessageRoleUser {
		return claudeSubDelta{}, errClaudeSubSummaryNoPlace
	}
	if !s.sourcePrefixMatches(msgs, source) {
		return claudeSubDelta{}, errClaudeSubSummaryNoPlace
	}
	instruction := claudeSubMarkSummary(msgs[source], source, len(s.entries)-source)
	return claudeSubDelta{
		User:              []Message{instruction},
		keep:              append([]claudeSubEntry(nil), s.entries...),
		keepInterruptible: s.interruptible,
	}, nil
}

// sourcePrefixMatches reports whether the record's first source entries match
// the first source request messages. The caller bounds source against both.
func (s *claudeSubSync) sourcePrefixMatches(msgs []Message, source int) bool {
	for i := range source {
		if !claudeSubEntryMatches(s.entries[i], msgs[i]) {
			return false
		}
	}
	return true
}

// claudeSubMarkSummary marks a summary instruction sent in place. It names the
// older source the summary covers and the retained tail that already sits in
// the transcript.
func claudeSubMarkSummary(instruction Message, source, tail int) Message {
	instruction.Content = fmt.Sprintf("[steiner compaction summary request: summarize the %d older source messages earlier in this transcript. The %d retained tail messages after them are already part of this transcript.]\n\n%s", source, tail, instruction.Content)
	return instruction
}

// claudeSubTranscript renders snapshot messages as one labelled transcript the
// advisor reads as a single user message.
func claudeSubTranscript(msgs []Message) string {
	var b strings.Builder
	b.WriteString("Conversation so far:\n\n")
	for _, m := range msgs {
		switch m.Role {
		case MessageRoleUser:
			b.WriteString("[user]\n")
		case MessageRoleTool:
			b.WriteString("[tool output]\n")
		default:
			b.WriteString("[assistant]\n")
		}
		b.WriteString(m.Content)
		for _, c := range m.ToolCalls {
			fmt.Fprintf(&b, "\n[tool call] %s %s", c.Name, claudeSubToolArguments(c))
		}
		b.WriteString("\n\n")
	}
	return b.String()
}

// claudeSubToolArguments returns a tool call's arguments as JSON text, using
// the raw arguments the model produced when present.
func claudeSubToolArguments(c ToolCall) string {
	if c.RawArguments != "" {
		return c.RawArguments
	}
	if c.Arguments == nil {
		return "{}"
	}
	data, err := json.Marshal(c.Arguments)
	if err != nil {
		// Arguments come from decoded JSON, so this should not happen. Keep the
		// call visible in the transcript rather than dropping it.
		return fmt.Sprintf("%v", c.Arguments)
	}
	return string(data)
}

// claudeSubUserBlocks renders user messages as claude CLI content blocks: a text
// block per non-empty message followed by one base64 image block per carried
// image. Images without data are skipped (steiner drops FilePath-only images
// before provider calls).
func claudeSubUserBlocks(msgs []Message) []map[string]any {
	blocks := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		if m.Content != "" {
			blocks = append(blocks, map[string]any{"type": "text", "text": m.Content})
		}
		for _, img := range m.Images {
			if img.Data == "" {
				continue
			}
			blocks = append(blocks, map[string]any{
				"type": "image",
				"source": map[string]any{
					"type":       "base64",
					"media_type": img.MediaType,
					"data":       img.Data,
				},
			})
		}
	}
	return blocks
}
