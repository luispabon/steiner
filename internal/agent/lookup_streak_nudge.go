package agent

import (
	"encoding/json"
	"strings"
)

// lookupStreakThreshold is the number of consecutive lookup tool results that
// triggers the async wait note.
const lookupStreakThreshold = 3

// lookupStreakNote is appended to a lookup tool result while sub-agents are
// still running or queued. It steers the orchestrator to end its turn instead
// of filling the wait with serial lookups.
const lookupStreakNote = "Sub-agents are still running. If this lookup is in their area, end your turn; they will report it."

// lookupNoteField is the JSON field name carrying lookupStreakNote inside an
// otherwise unchanged tool result object.
const lookupNoteField = "harness_note"

// isLookupTool reports whether name is a read-only lookup tool whose result can
// be the orchestrator waiting on a running sub-agent.
func isLookupTool(name string) bool {
	switch name {
	case "read", "grep", "glob":
		return true
	}
	return strings.HasPrefix(name, "lsp_")
}

// resetLookupStreak clears the lookup streak and the once-per-streak nudge
// latch. A delegation call, a non-lookup tool call, or a mid-run delivery
// restarts counting.
func (p *turnProgressor) resetLookupStreak() {
	p.lookupStreak = 0
	p.lookupStreakNudged = false
}

// applyLookupStreakNudge appends lookupStreakNote to a tool result once per
// lookup streak, on the lookup that reaches lookupStreakThreshold, when the
// run is an async orchestrator with a running or queued sub-agent. It never
// blocks anything: the tool result is returned unchanged in every other case.
// Child runs and runs without a pending-sub-agent source (exec, oneshot) are
// left alone and keep no streak.
func (p *turnProgressor) applyLookupStreakNudge(name, content string) string {
	if !p.request.Prompt.AsyncSubAgents || p.request.AgentID != "" || p.request.PendingSubAgents == nil {
		return content
	}
	if p.isDelegationCall(name) || !isLookupTool(name) {
		p.resetLookupStreak()
		return content
	}
	p.lookupStreak++
	if p.lookupStreak < lookupStreakThreshold || p.lookupStreakNudged {
		return content
	}
	p.lookupStreakNudged = true
	if !pendingSubAgentsActive(p.request.PendingSubAgents()) {
		return content
	}
	return withLookupNote(content)
}

// pendingSubAgentsActive reports whether any pending sub-agent is still
// running or queued. Finished entries have results to deliver, not work the
// orchestrator should wait on.
func pendingSubAgentsActive(pending []PendingSubAgent) bool {
	for _, agent := range pending {
		if agent.State == SubAgentRunning || agent.State == SubAgentQueued {
			return true
		}
	}
	return false
}

// withLookupNote appends lookupStreakNote to content. A JSON object keeps the
// field inside the object so JSON consumers keep parsing it; every other
// content gets the note as a trailing line.
func withLookupNote(content string) string {
	if merged, ok := insertLookupNoteField(content); ok {
		return merged
	}
	return content + "\n\n" + lookupStreakNote
}

// insertLookupNoteField inserts the harness_note field before the closing brace
// of a JSON object without re-marshalling, so every original byte survives. It
// reports false when content is not a JSON object or the insertion does not
// produce valid JSON.
func insertLookupNoteField(content string) (string, bool) {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "{") || !json.Valid([]byte(trimmed)) {
		return "", false
	}
	end := strings.LastIndexByte(content, '}')
	start := strings.IndexByte(content, '{')
	if start < 0 || end < start {
		return "", false
	}
	encoded, err := json.Marshal(lookupStreakNote)
	if err != nil {
		return "", false
	}
	separator := ","
	if strings.TrimSpace(content[start+1:end]) == "" {
		separator = ""
	}
	merged := content[:end] + separator + `"` + lookupNoteField + `":` + string(encoded) + content[end:]
	if !json.Valid([]byte(merged)) {
		return "", false
	}
	return merged, true
}
