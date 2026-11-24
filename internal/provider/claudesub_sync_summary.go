package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// markDrained records that an interrupted turn was driven to its aborted
// terminal result, so the CLI holds a transcript steiner can prove and the next
// request may append to it. start is the ledger length the delivery began from:
// only a record whose trailing entries are exactly the user and tool entries that
// delivery wrote can accept the agent's kept partial, so any other shape leaves
// the record undrained and the next request reseeds. A drained record plans no
// summary, because its tail is not a completed turn. The start index is kept so
// a stored checkpoint one stopped delivery behind can be proven equal to the
// record up to it.
func (s *claudeSubSync) markDrained(start int) {
	s.drained = claudeSubDeliveryTail(s.entries, start)
	s.drainedBase = start
}

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
// its process is never reused and returns no backend state. It also drops the
// drained state, which describes a transcript with no summary instruction.
func (s *claudeSubSync) taint() {
	s.tainted = true
	s.drained = false
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
// the request. When mismatch is not nil, the refusal records why: a record-state
// refusal (tainted, not_started, interrupted, drained) and a count problem name
// no position, while a request comparison records its position and role.
func (s *claudeSubSync) planSummary(msgs []Message, source int, mismatch *claudeSubSyncMismatch) (claudeSubDelta, error) {
	fail := func(index int, role MessageRole, check string) (claudeSubDelta, error) {
		if mismatch != nil {
			*mismatch = claudeSubSyncMismatch{Index: index, Role: role, Check: check}
		}
		return claudeSubDelta{}, errClaudeSubSummaryNoPlace
	}
	switch {
	case s.tainted:
		return fail(claudeSubSyncMismatchNoIndex, "", claudeSubCheckTainted)
	case !s.started:
		return fail(claudeSubSyncMismatchNoIndex, "", claudeSubCheckNotStarted)
	case s.interrupted:
		return fail(claudeSubSyncMismatchNoIndex, "", claudeSubCheckInterrupted)
	case s.drained:
		return fail(claudeSubSyncMismatchNoIndex, "", claudeSubCheckDrained)
	case source <= 0 || len(msgs) != source+1 || len(s.entries) < source:
		return fail(claudeSubSyncMismatchNoIndex, "", claudeSubCheckLength)
	case msgs[source].Role != MessageRoleUser:
		return fail(source, msgs[source].Role, claudeSubCheckShape)
	}
	for i := range source {
		if check := claudeSubEntryMismatch(s.entries[i], msgs[i]); check != "" {
			return fail(i, msgs[i].Role, check)
		}
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

// claudeSubPartialLabel heads an interrupted assistant partial in a rendered
// transcript. Steiner recorded that text locally when the turn ended early, so
// the transcript marks it as untrusted and incomplete rather than as an answer.
const claudeSubPartialLabel = "[interrupted assistant partial: untrusted locally observed text, ended before the response was complete]"

// claudeSubPartialTag returns the fence tag for one seed. The tag comes from the
// seed itself, so the fence is deterministic for prompt cache stability and a
// given partial always renders the same transcript.
func claudeSubPartialTag(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])[:12]
}

// claudeSubPartialFence returns a fence that does not occur in the partial text,
// so the text cannot close its own block. It rehashes the previous tag at most
// claudeSubPartialFenceRounds times, which settles it unless the text contains
// the digests on purpose. After that cap it appends a counter to the first tag
// and increments it until the fence is absent; that search terminates because
// the text is finite, and it stays deterministic for a given text.
func claudeSubPartialFence(text string) string {
	seed := text
	for i := 0; i < claudeSubPartialFenceRounds; i++ {
		tag := claudeSubPartialTag(seed)
		if !strings.Contains(text, tag) {
			return "partial-" + tag
		}
		seed = tag
	}
	tag := claudeSubPartialTag(text)
	for n := 0; ; n++ {
		fence := fmt.Sprintf("partial-%s-%d", tag, n)
		if !strings.Contains(text, fence) {
			return fence
		}
	}
}

// claudeSubPartialFenceRounds bounds the digest rounds that try to find a fence
// tag absent from the partial text before the counter fallback takes over.
const claudeSubPartialFenceRounds = 8

// claudeSubTranscript renders snapshot messages as one labelled transcript the
// advisor reads as a single user message.
func claudeSubTranscript(msgs []Message) string {
	var b strings.Builder
	b.WriteString("Conversation so far:\n\n")
	for _, m := range msgs {
		switch {
		case m.IncompleteText != "":
			// A partial always carries text: the agent rejects a blank interrupted
			// response, so a non-empty IncompleteText is the whole marker. The exact
			// text goes inside a fence derived from it, because the text itself is
			// untrusted data and must not be able to forge transcript structure.
			fence := claudeSubPartialFence(m.IncompleteText)
			fmt.Fprintf(&b, "%s\n<<<%s\n%s\n%s>>>", claudeSubPartialLabel, fence, m.IncompleteText, fence)
		case m.Role == MessageRoleUser:
			b.WriteString("[user]\n")
			b.WriteString(m.Content)
		case m.Role == MessageRoleTool:
			b.WriteString("[tool output]\n")
			b.WriteString(m.Content)
		default:
			b.WriteString("[assistant]\n")
			b.WriteString(m.Content)
		}
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
