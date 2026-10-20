package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
	// UUID is the final CLI transcript uuid of an assistant entry. It is empty
	// for user and tool entries, which steiner writes itself.
	UUID string
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
// keep and keepInterruptible are the sync record a normal-mode request leaves
// behind; plan computes them and adopt applies them once delivery starts.
type claudeSubDelta struct {
	ToolResults []Message
	User        []Message

	keep              []claudeSubEntry
	keepInterruptible int
	// reseeded marks a transcript delivery: its User message is the rendered
	// history, so keep (not User) is the record and commitSent must not append.
	reseeded bool
}

// claudeSubSync is the per-process append-only record of what has been sent to
// the claude CLI, plus advisor-mode bookkeeping and D22 interrupt state.
type claudeSubSync struct {
	entries []claudeSubEntry
	started bool

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

	// cliSessionID is the CLI session ID from the latest envelope that carried
	// one. fingerprint is the SHA-256 of the system prompt and tools the process
	// started with, set once by its first normal parent call. Both feed the
	// backend state.
	cliSessionID string
	fingerprint  string
	// tainted is set once an in-place summary is sent: the CLI transcript then
	// holds steiner's summary request, which the record does not describe. A
	// tainted record plans and checkpoints nothing (see taint).
	tainted bool
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
// plan does not change the record: a request that fails before delivery leaves
// it exactly as it was, so a retry plans the same delta.
func (s *claudeSubSync) plan(req ChatRequest) (claudeSubDelta, error) {
	if s.tainted {
		return claudeSubDelta{}, errClaudeSubHistoryChanged
	}
	msgs := claudeSubMessages(req.Messages)
	if !s.started {
		for _, m := range msgs {
			if m.Role == MessageRoleAssistant || m.Role == MessageRoleTool {
				return claudeSubDelta{}, errClaudeSubHistoryChanged
			}
		}
		return claudeSubDelta{User: msgs, keep: s.entries, keepInterruptible: s.interruptible}, nil
	}
	if !s.interrupted && len(msgs) < len(s.entries) {
		return claudeSubDelta{}, errClaudeSubHistoryChanged
	}

	out := make([]claudeSubEntry, 0, len(s.entries))
	keepInterruptible := s.interruptible
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
			keepInterruptible = -1
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
	// Two suffix results for one call id would resolve the same pending call
	// twice, so the second must fail the whole request before anything applies.
	suffixToolIDs := make(map[string]struct{})
	for si, m := range msgs[mi:] {
		switch m.Role {
		case MessageRoleTool:
			if _, ok := recordedToolIDs[m.ToolCallID]; ok {
				return claudeSubDelta{}, errClaudeSubHistoryChanged
			}
			if _, ok := suffixToolIDs[m.ToolCallID]; ok {
				return claudeSubDelta{}, errClaudeSubHistoryChanged
			}
			suffixToolIDs[m.ToolCallID] = struct{}{}
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
	delta.keep = append(out, recorded...)
	delta.keepInterruptible = keepInterruptible
	return delta, nil
}

// adopt replaces the record with the state plan computed for a normal-mode
// request. Call it only after every check that can fail before delivery has
// passed.
func (s *claudeSubSync) adopt(delta claudeSubDelta) {
	s.entries = delta.keep
	s.interruptible = delta.keepInterruptible
}

// commitToolResult records one tool result as delivered to the CLI. It is
// called as each result is resolved, so a later failure in the same turn cannot
// leave a delivered result unrecorded and a retry never resolves it again (D22).
func (s *claudeSubSync) commitToolResult(m Message) {
	s.entries = append(s.entries, claudeSubEntryFromMessage(m))
}

// commitSent records a delta's user messages as delivered and clears the
// interrupt state. It is called right after the user line has been written to
// the CLI, not on call success, so a cancelled call never re-sends user content
// (D22). Tool results were already recorded by commitToolResult.
func (s *claudeSubSync) commitSent(delta claudeSubDelta) {
	if !delta.reseeded {
		for _, m := range delta.User {
			s.entries = append(s.entries, claudeSubEntryFromMessage(m))
		}
	}
	s.started = true
	s.interrupted = false
	s.interruptible = -1
}

// commitAssistant records the assistant message a successful call returned.
// uuid is that message's final CLI transcript uuid, or empty when the CLI
// reported none.
func (s *claudeSubSync) commitAssistant(m Message, uuid string) {
	entry := claudeSubEntryFromMessage(m)
	entry.UUID = uuid
	s.entries = append(s.entries, entry)
}
