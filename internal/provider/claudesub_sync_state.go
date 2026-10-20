package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

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

// claudeSubLaunchProfile versions the child launch flags and env that shape what
// the CLI injects into a session. Bump it when those change, so stored CLI
// transcripts from the old profile are reseeded instead of resumed: they hold
// CLI-injected memory and account context.
const claudeSubLaunchProfile = 1

// claudeSubFingerprint returns the SHA-256 of a process's system prompt, tool
// specs and launch profile. encoding/json sorts map keys, so the digest is
// stable.
func claudeSubFingerprint(systemPrompt string, tools []ToolSpec) (string, error) {
	raw, err := json.Marshal(struct {
		SystemPrompt  string     `json:"system_prompt"`
		Tools         []ToolSpec `json:"tools"`
		LaunchProfile int        `json:"launch_profile"`
	}{systemPrompt, tools, claudeSubLaunchProfile})
	if err != nil {
		return "", fmt.Errorf("encode prompt and tools fingerprint: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
