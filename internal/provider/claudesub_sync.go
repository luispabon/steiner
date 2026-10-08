package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// claudeSubEntry records one message already delivered to a claude CLI process.
// User and tool messages are compared by content digest (images excluded:
// steiner strips image data after consumption, the one tolerated retroactive
// edit) plus the multiset of image fingerprints actually sent. Assistant
// messages are compared by their tool-call id multiset; their text is not
// compared because steiner may normalise assistant text and reasoning.
type claudeSubEntry struct {
	Role        MessageRole
	Digest      string
	Images      []string
	ToolCallIDs []string
	ToolCallID  string
}

// claudeSubDigest returns the content identity of a message for the append-only
// record. Images are excluded on purpose: steiner strips image data after
// consumption, so a sent message may lose its images between requests.
func claudeSubDigest(m Message) string {
	sum := sha256.Sum256([]byte(string(m.Role) + "\x00" + m.Content + "\x00" + m.Name))
	return hex.EncodeToString(sum[:])
}

// claudeSubImageFingerprints returns the fingerprints of the images actually
// sent for a message: an image block is only delivered when it carries data
// (FilePath-only images are skipped by claudeSubUserBlocks).
func claudeSubImageFingerprints(m Message) []string {
	var out []string
	for _, img := range m.Images {
		if img.Data == "" {
			continue
		}
		sum := sha256.Sum256([]byte(img.MediaType + "\x00" + img.Data))
		out = append(out, hex.EncodeToString(sum[:]))
	}
	return out
}

// errClaudeSubHistoryChanged is returned when a request would rewrite history
// the claude CLI process already holds. Split 2 (issue #895) will support it.
var errClaudeSubHistoryChanged = errors.New("conversation history changed - not supported yet with the claude_subscription provider (tracked in #895); start a new session")

// claudeSubToolNote is appended to every system prompt the provider writes. The
// claude CLI always prefixes steiner's MCP tool names with mcp__steiner__, so
// the model needs the mapping even when the prompt names a tool plainly.
const claudeSubToolNote = "Your tools are provided by steiner over MCP and appear to you with the mcp__steiner__ prefix (for example mcp__steiner__read is steiner's read tool); when instructions name a tool by its plain name, use the prefixed mcp__steiner__ tool."

// claudeSubDelta is the set of messages a request adds to the CLI transcript:
// tool results (delivered to the CLI's pending tool calls) and user messages.
type claudeSubDelta struct {
	ToolResults []Message
	User        []Message
}

// claudeSubSync is the per-process append-only record of what has been sent to
// the claude CLI, plus advisor-mode bookkeeping and D22 interrupt state.
type claudeSubSync struct {
	entries      []claudeSubEntry
	systemPrompt string
	started      bool

	// advisorEntries is the parent-snapshot prefix already sent to the advisor.
	advisorEntries []claudeSubEntry

	// interrupted is set by markInterrupted and cleared by the next commitSent.
	interrupted bool
	// interruptible is the index of the assistant entry produced by the call
	// that was just interrupted, or -1 when no such assistant exists. Only that
	// one entry may be subset-matched or omitted. markInterrupted derives it
	// from the record: the last assistant is interruptible only when no user
	// message was sent after it, otherwise it belongs to a completed call.
	interruptible int
}

// claudeSubSystemPrompt joins the system messages of a first request, in order,
// and appends the provider's fixed tool note describing the mcp__steiner__ tool
// prefix the claude CLI adds.
func claudeSubSystemPrompt(msgs []Message) string {
	var parts []string
	for _, m := range msgs {
		if m.Role != MessageRoleSystem {
			continue
		}
		if c := strings.TrimSpace(m.Content); c != "" {
			parts = append(parts, c)
		}
	}
	joined := strings.Join(parts, "\n\n")
	if joined == "" {
		return claudeSubToolNote
	}
	return joined + "\n\n" + claudeSubToolNote
}

// claudeSubMessages returns the request's non-system messages in order. System
// messages after the first request are ignored: the CLI snapshots the system
// prompt per conversation.
func claudeSubMessages(msgs []Message) []Message {
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == MessageRoleSystem {
			continue
		}
		out = append(out, m)
	}
	return out
}

// plan returns the messages a normal-mode request adds to the CLI transcript.
// It enforces D8's append-only rule: an already-sent message may only lose
// images, and a fresh process may not start with assistant or tool history.
func (s *claudeSubSync) plan(req ChatRequest) (claudeSubDelta, error) {
	msgs := claudeSubMessages(req.Messages)
	if !s.started {
		for _, m := range msgs {
			if m.Role == MessageRoleAssistant || m.Role == MessageRoleTool {
				return claudeSubDelta{}, errClaudeSubHistoryChanged
			}
		}
		s.systemPrompt = claudeSubSystemPrompt(req.Messages)
		return claudeSubDelta{User: msgs}, nil
	}
	if !s.interrupted && len(msgs) < len(s.entries) {
		return claudeSubDelta{}, errClaudeSubHistoryChanged
	}

	out := make([]claudeSubEntry, 0, len(s.entries))
	ei, mi := 0, 0
	for ei < len(s.entries) {
		entry := s.entries[ei]
		if s.interrupted && ei == s.interruptible {
			if mi < len(msgs) && msgs[mi].Role == MessageRoleAssistant {
				// The interrupted assistant may drop tool calls: its request form
				// must be a multiset subset of the recorded ids.
				if !claudeSubSubsetIDs(claudeSubToolCallIDs(msgs[mi]), entry.ToolCallIDs) {
					return claudeSubDelta{}, errClaudeSubHistoryChanged
				}
				entry.ToolCallIDs = claudeSubToolCallIDs(msgs[mi])
				out = append(out, entry)
				ei++
				mi++
				continue
			}
			// The interrupted assistant may be absent from the request entirely.
			// Drop just the assistant entry; its recorded tool results stay in
			// the record and are still compared, so a same-id tool result can
			// never be rewritten into an appended "new" result (D22). A tool
			// result the request also dropped is tolerated.
			ei++
			for ei < len(s.entries) && s.entries[ei].Role == MessageRoleTool {
				e := s.entries[ei]
				if mi >= len(msgs) || msgs[mi].Role != MessageRoleTool {
					ei++
					continue
				}
				if !claudeSubEntryMatches(e, msgs[mi]) {
					return claudeSubDelta{}, errClaudeSubHistoryChanged
				}
				out = append(out, e)
				ei++
				mi++
			}
			continue
		}
		if mi >= len(msgs) || !claudeSubEntryMatches(entry, msgs[mi]) {
			return claudeSubDelta{}, errClaudeSubHistoryChanged
		}
		out = append(out, entry)
		ei++
		mi++
	}

	var delta claudeSubDelta
	var recorded []claudeSubEntry
	// A tool result whose call id already exists in the record must never be
	// appended again. This closes the reorder hole left by omitting an
	// interrupted assistant: dropping its recorded tool entries must not let a
	// later same-id tool result (unchanged or changed) be re-sent as a suffix
	// result. Recorded ids are read from s.entries, which still holds the
	// entries dropped by the omission above.
	recordedToolIDs := make(map[string]struct{})
	for _, e := range s.entries {
		if e.Role == MessageRoleTool && e.ToolCallID != "" {
			recordedToolIDs[e.ToolCallID] = struct{}{}
		}
	}
	for si, m := range msgs[mi:] {
		switch m.Role {
		case MessageRoleTool:
			if _, ok := recordedToolIDs[m.ToolCallID]; ok {
				return claudeSubDelta{}, errClaudeSubHistoryChanged
			}
			delta.ToolResults = append(delta.ToolResults, m)
		case MessageRoleUser:
			delta.User = append(delta.User, m)
		case MessageRoleAssistant:
			// D22: right after an interrupt, one text-only assistant message may
			// be steiner's kept partial text at the interrupt point. Record it,
			// never send it, and require it to carry no tool calls, no images
			// and no other non-text payload.
			if s.interrupted && si == 0 && claudeSubTextOnlyAssistant(m) {
				recorded = append(recorded, claudeSubEntryFromMessage(m))
				continue
			}
			return claudeSubDelta{}, errClaudeSubHistoryChanged
		default:
			return claudeSubDelta{}, errClaudeSubHistoryChanged
		}
	}
	s.entries = append(out, recorded...)
	return delta, nil
}

// commitSent records a delta as delivered. It is called right after the tool
// results and/or user line have been written to the CLI, not on call success,
// so a cancelled call never re-sends user content (D22).
func (s *claudeSubSync) commitSent(delta claudeSubDelta) {
	for _, m := range delta.ToolResults {
		s.entries = append(s.entries, claudeSubEntryFromMessage(m))
	}
	for _, m := range delta.User {
		s.entries = append(s.entries, claudeSubEntryFromMessage(m))
	}
	s.started = true
	s.interrupted = false
	s.interruptible = -1
}

// commitAssistant records the assistant message a successful call returned.
func (s *claudeSubSync) commitAssistant(m Message) {
	s.entries = append(s.entries, claudeSubEntryFromMessage(m))
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
		s.systemPrompt = claudeSubSystemPrompt(req.Messages)
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

// claudeSubTranscript renders snapshot messages as one labelled transcript the
// advisor reads as a single user message.
func claudeSubTranscript(msgs []Message) string {
	var b strings.Builder
	b.WriteString("Conversation so far:\n\n")
	for _, m := range msgs {
		if m.Role == MessageRoleUser {
			b.WriteString("[user]\n")
		} else {
			b.WriteString("[assistant]\n")
		}
		b.WriteString(m.Content)
		b.WriteString("\n\n")
	}
	return b.String()
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

// claudeSubEntryMatches reports whether a request message still matches the
// recorded entry for its position. Assistant text is not compared (steiner may
// normalise it); user and tool messages are compared by digest and their images
// must be a subset of the images recorded as sent.
func claudeSubEntryMatches(entry claudeSubEntry, m Message) bool {
	if entry.Role != m.Role {
		return false
	}
	if m.Role == MessageRoleAssistant {
		return claudeSubSameIDs(entry.ToolCallIDs, claudeSubToolCallIDs(m))
	}
	if m.Role == MessageRoleTool && entry.ToolCallID != m.ToolCallID {
		return false
	}
	return entry.Digest == claudeSubDigest(m) && claudeSubImagesSubset(claudeSubImageFingerprints(m), entry.Images)
}

func claudeSubEntryFromMessage(m Message) claudeSubEntry {
	return claudeSubEntry{
		Role:        m.Role,
		Digest:      claudeSubDigest(m),
		Images:      claudeSubImageFingerprints(m),
		ToolCallIDs: claudeSubToolCallIDs(m),
		ToolCallID:  m.ToolCallID,
	}
}

func claudeSubToolCallIDs(m Message) []string {
	if len(m.ToolCalls) == 0 {
		return nil
	}
	ids := make([]string, 0, len(m.ToolCalls))
	for _, c := range m.ToolCalls {
		ids = append(ids, c.ID)
	}
	return ids
}

// claudeSubTextOnlyAssistant reports whether an assistant message carries only
// text content. The one D22 partial assistant steiner keeps must not smuggle
// any other Message field into the recorded transcript; every field of Message
// other than Content must be empty, and the role must be assistant.
func claudeSubTextOnlyAssistant(m Message) bool {
	return m.Role == MessageRoleAssistant &&
		m.ReasoningContent == "" &&
		m.Name == "" &&
		m.ToolCallID == "" &&
		len(m.ToolCalls) == 0 &&
		len(m.Images) == 0 &&
		m.Turn == 0 &&
		m.ProviderMetadata == nil
}

// claudeSubEntrySnapshotEqual reports whether a recorded advisor-snapshot entry
// still matches the snapshot message at its position (role and content digest).
func claudeSubEntrySnapshotEqual(entry claudeSubEntry, m Message) bool {
	return entry.Role == m.Role && entry.Digest == claudeSubDigest(m)
}

// claudeSubSameIDs reports whether two tool-call id slices hold the same
// multiset: [c1,c2] and [c1,c1] must not compare equal.
func claudeSubSameIDs(a, b []string) bool {
	ca, cb := claudeSubIDCounts(a), claudeSubIDCounts(b)
	if len(ca) != len(cb) {
		return false
	}
	for id, n := range ca {
		if cb[id] != n {
			return false
		}
	}
	return true
}

// claudeSubSubsetIDs reports whether every id in sub is present in super at
// least as many times as in sub (multiset subset).
func claudeSubSubsetIDs(sub, super []string) bool {
	sc := claudeSubIDCounts(super)
	for id, n := range claudeSubIDCounts(sub) {
		if sc[id] < n {
			return false
		}
	}
	return true
}

// claudeSubImagesSubset reports whether cur is a multiset subset of recorded:
// removing sent images is tolerated, adding or replacing them is not.
func claudeSubImagesSubset(cur, recorded []string) bool {
	if len(cur) == 0 {
		return true
	}
	counts := claudeSubIDCounts(recorded)
	for _, f := range cur {
		if counts[f] == 0 {
			return false
		}
		counts[f]--
	}
	return true
}

func claudeSubIDCounts(ids []string) map[string]int {
	counts := make(map[string]int, len(ids))
	for _, id := range ids {
		counts[id]++
	}
	return counts
}
