package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// claudeSubBackendStateVersion is the schema version of claudeSubBackendState.
// Readers must reject any other version.
const claudeSubBackendStateVersion = 1

// claudeSubBackendState is the opaque state a Claude parent generation stores
// for resumption (D2). It holds identifiers and digests only, never message
// text, tool payloads or credentials.
type claudeSubBackendState struct {
	Provider               string `json:"provider"`
	Version                int    `json:"version"`
	CLISessionID           string `json:"cli_session_id"`
	TransportSession       string `json:"transport_session"`
	SyncEntries            int    `json:"sync_entries"`
	SyncChainDigest        string `json:"sync_chain_digest"`
	SyncPrefixDigest       string `json:"sync_prefix_digest"`
	FinalMessageDigest     string `json:"final_message_digest"`
	FinalUUID              string `json:"final_uuid"`
	PromptToolsFingerprint string `json:"prompt_tools_fingerprint"`
}

// checkpoint describes the record as of its last committed entry, owned by
// transportSession. ok is false when the process has no fingerprint (advisor
// sessions) or the CLI has reported no session ID, because such a state cannot
// resume anything.
func (s *claudeSubSync) checkpoint(transportSession string) (state claudeSubBackendState, ok bool, err error) {
	if s.fingerprint == "" || s.cliSessionID == "" || s.tainted {
		return claudeSubBackendState{}, false, nil
	}
	chain, err := claudeSubChainDigest(s.entries)
	if err != nil {
		return claudeSubBackendState{}, false, err
	}
	prefix, err := claudeSubPrefixDigest(s.entries)
	if err != nil {
		return claudeSubBackendState{}, false, err
	}
	state = claudeSubBackendState{
		Provider:               "claude_subscription",
		Version:                claudeSubBackendStateVersion,
		CLISessionID:           s.cliSessionID,
		TransportSession:       transportSession,
		SyncEntries:            len(s.entries),
		SyncChainDigest:        chain,
		SyncPrefixDigest:       prefix,
		PromptToolsFingerprint: s.fingerprint,
	}
	if n := len(s.entries); n > 0 {
		last := s.entries[n-1]
		state.FinalMessageDigest = last.Digest
		state.FinalUUID = last.UUID
	}
	return state, true, nil
}

// backendState encodes the checkpoint as the opaque state a final chunk hands
// back. It returns nil, with no error, when the checkpoint has no state.
func (s *claudeSubSync) backendState(transportSession string) (json.RawMessage, error) {
	state, ok, err := s.checkpoint(transportSession)
	if err != nil || !ok {
		return nil, err
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("encode claude backend state: %w", err)
	}
	return raw, nil
}

// claudeSubParseState decodes a stored backend state. It reports false for an
// absent, malformed or other-version state, so the caller reseeds instead.
func claudeSubParseState(raw json.RawMessage) (claudeSubBackendState, bool) {
	var st claudeSubBackendState
	if len(raw) == 0 || json.Unmarshal(raw, &st) != nil {
		return claudeSubBackendState{}, false
	}
	if st.Provider != "claude_subscription" || st.Version != claudeSubBackendStateVersion {
		return claudeSubBackendState{}, false
	}
	return st, true
}

// claudeSubStateMatches reports whether a live checkpoint equals a stored state.
// The prompt and tools fingerprint is not compared: a live process keeps the
// prompt it started with (D5).
func claudeSubStateMatches(live, stored claudeSubBackendState) bool {
	return live.CLISessionID == stored.CLISessionID &&
		live.TransportSession == stored.TransportSession &&
		live.SyncEntries == stored.SyncEntries &&
		live.SyncChainDigest == stored.SyncChainDigest &&
		live.FinalMessageDigest == stored.FinalMessageDigest &&
		live.FinalUUID == stored.FinalUUID
}

// claudeSubResumeEntries rebuilds the record a process resumed from stored
// holds: the first stored.SyncEntries request messages, the last of which must
// be the assistant message that carried stored.FinalUUID. It reports false when
// the request's own history does not reproduce the stored final message.
func claudeSubResumeEntries(msgs []Message, stored claudeSubBackendState) ([]claudeSubEntry, bool) {
	n := stored.SyncEntries
	if n <= 0 || n > len(msgs) || stored.FinalUUID == "" {
		return nil, false
	}
	entries := claudeSubEntriesFromMessages(msgs[:n])
	last := &entries[n-1]
	if last.Role != MessageRoleAssistant || last.Digest != stored.FinalMessageDigest {
		return nil, false
	}
	last.UUID = stored.FinalUUID
	return entries, true
}

// claudeSubPrefixDigest is the chain digest of entries with their CLI uuids
// cleared. Steiner can recompute it from request messages alone, so a stored
// state can prove the history prefix without the CLI transcript.
func claudeSubPrefixDigest(entries []claudeSubEntry) (string, error) {
	bare := make([]claudeSubEntry, len(entries))
	for i, e := range entries {
		e.UUID = ""
		bare[i] = e
	}
	return claudeSubChainDigest(bare)
}

// claudeSubRestoreFor reports whether a new process may resume the stored state
// for msgs, and the resume target. Every failed check reseeds instead: a missing
// or foreign state, a prompt and tools fingerprint that differs from the
// request's (D5), a missing final uuid, or a history whose prefix or final
// message the stored state does not reproduce. A state owned by another key
// forks (D3).
func claudeSubRestoreFor(msgs []Message, stored claudeSubBackendState, hasState bool, key, fingerprint string) (claudeSubResume, bool, error) {
	if !hasState || !claudeSubHasHistory(msgs) || stored.CLISessionID == "" || stored.TransportSession == "" || stored.PromptToolsFingerprint != fingerprint {
		return claudeSubResume{}, false, nil
	}
	entries, ok := claudeSubResumeEntries(msgs, stored)
	if !ok {
		return claudeSubResume{}, false, nil
	}
	prefix, err := claudeSubPrefixDigest(entries)
	if err != nil {
		return claudeSubResume{}, false, err
	}
	if prefix != stored.SyncPrefixDigest {
		return claudeSubResume{}, false, nil
	}
	dry := claudeSubSync{entries: entries, started: true, interruptible: -1}
	if _, err := dry.plan(ChatRequest{Messages: msgs}); err != nil {
		return claudeSubResume{}, false, nil
	}
	return claudeSubResume{SessionID: stored.CLISessionID, AtUUID: stored.FinalUUID, Fork: stored.TransportSession != key}, true, nil
}

// claudeSubHasHistory reports whether msgs carry assistant or tool messages,
// which a fresh process cannot take as appended input.
func claudeSubHasHistory(msgs []Message) bool {
	for _, m := range msgs {
		if m.Role == MessageRoleAssistant || m.Role == MessageRoleTool {
			return true
		}
	}
	return false
}

// claudeSubReseedDelta renders the whole non-system history as one transcript
// user request, carrying only the images still held in memory. The record
// becomes the history itself, so later appends compare against the same
// messages.
func claudeSubReseedDelta(msgs []Message) claudeSubDelta {
	transcript := Message{Role: MessageRoleUser, Content: claudeSubTranscript(msgs)}
	entries := claudeSubEntriesFromMessages(msgs)
	for _, m := range msgs {
		for _, img := range m.Images {
			if img.Data != "" {
				transcript.Images = append(transcript.Images, img)
			}
		}
	}
	return claudeSubDelta{User: []Message{transcript}, keep: entries, keepInterruptible: -1, reseeded: true}
}

// claudeSubChainDigest folds every sync entry, in order, into one SHA-256
// chain. Each link hashes the previous link with the entry's JSON, so the digest
// changes when an entry, its order or its final uuid changes.
func claudeSubChainDigest(entries []claudeSubEntry) (string, error) {
	chain := ""
	for _, entry := range entries {
		raw, err := json.Marshal(entry)
		if err != nil {
			return "", fmt.Errorf("encode sync entry: %w", err)
		}
		sum := sha256.Sum256([]byte(chain + "\x00" + string(raw)))
		chain = hex.EncodeToString(sum[:])
	}
	return chain, nil
}

// claudeSubFingerprint returns the SHA-256 of a process's system prompt and
// tool specs. encoding/json sorts map keys, so the digest is stable.
func claudeSubFingerprint(systemPrompt string, tools []ToolSpec) (string, error) {
	raw, err := json.Marshal(struct {
		SystemPrompt string     `json:"system_prompt"`
		Tools        []ToolSpec `json:"tools"`
	}{systemPrompt, tools})
	if err != nil {
		return "", fmt.Errorf("encode prompt and tools fingerprint: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
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

// claudeSubEntriesFromMessages returns the sync entry of each message in msgs.
func claudeSubEntriesFromMessages(msgs []Message) []claudeSubEntry {
	entries := make([]claudeSubEntry, 0, len(msgs))
	for _, m := range msgs {
		entries = append(entries, claudeSubEntryFromMessage(m))
	}
	return entries
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
