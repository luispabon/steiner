package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
)

const decisionsMaxBytes = 2000
const decisionsMaxLines = 24

// ScratchpadManager owns scratchpad state, scaffold inference, and failure tracking.
type ScratchpadManager struct {
	mode                    config.ScratchpadMode
	scratchpad              Scratchpad
	failures                int
	lastScaffoldFingerprint string
	events                  output.EventSink
}

// OnTurnComplete tracks whether the scratchpad tool was called and emits a
// warning when three consecutive turns have been missed in hybrid mode.
func (m *ScratchpadManager) OnTurnComplete(turnIndex int, scratchpadCalled bool) {
	if m.mode != config.ScratchpadModeHybrid {
		return
	}
	if scratchpadCalled {
		m.failures = 0
		return
	}
	m.failures++
	if m.failures >= 3 {
		note := "scratchpad tool not called in 3+ consecutive turns"
		emitEvent(m.events, output.NewScratchpadEvent(turnIndex, false, m.scratchpad.Render(), m.failures, note))
	}
}

// IngestToolResult processes a scratchpad tool result, updating internal state
// and emitting events. Returns the compact ack string.
func (m *ScratchpadManager) IngestToolResult(turn int, content string) string {
	next, ok := parseScratchpadToolResult(content, m.scratchpad)
	if ok {
		m.scratchpad = next
		m.scratchpad.LastAction = "scratchpad updated"
		m.failures = 0
		emitEvent(m.events, output.NewScratchpadEvent(turn, true, m.scratchpad.Render(), 0, ""))
		emitEvent(m.events, output.NewScratchpadUpdatedEvent(output.ScratchpadUpdatedEvent{
			Intent:    m.scratchpad.Intent,
			Decisions: m.scratchpad.Decisions,
			Open:      m.scratchpad.Open,
			Next:      m.scratchpad.Next,
		}))
	}
	return `{"ok":true}`
}

// SyncState updates the scratchpad session state and populates RecentToolCalls
// in the provided ContextState.
func (m *ScratchpadManager) SyncState(state RunState, next *ContextState) {
	if next == nil {
		return
	}
	m.scratchpad.SessionState = compactSessionState(next.TurnCount, next.CompactionCount)
	next.RecentToolCalls = summarizeRecentToolCalls(state.Lineage.FullMessages(), 3)
}

// AppendDecisionFact appends a single decision fact to the scratchpad.
func (m *ScratchpadManager) AppendDecisionFact(fact string) {
	m.scratchpad.Decisions = appendDecisionFactText(m.scratchpad.Decisions, fact)
}

// AppendDecisionFacts appends multiple decision facts.
func (m *ScratchpadManager) AppendDecisionFacts(facts []string) {
	for _, fact := range facts {
		m.AppendDecisionFact(fact)
	}
}

// SetWorkingFile updates the scratchpad working file and last action.
func (m *ScratchpadManager) SetWorkingFile(path, lastAction string) {
	if path != "" {
		m.scratchpad.WorkingFile = path
	}
	if lastAction != "" {
		m.scratchpad.LastAction = lastAction
	}
}

// ShouldRunScaffoldInference reports whether scaffold inference should run for
// the current state and compaction count.
func (m *ScratchpadManager) ShouldRunScaffoldInference(state RunState, compactionCount int) bool {
	if m.mode != config.ScratchpadModeScaffoldOnly {
		return false
	}
	fingerprint := m.scaffoldFingerprint(state, compactionCount)
	if fingerprint == m.lastScaffoldFingerprint {
		return false
	}
	m.lastScaffoldFingerprint = fingerprint
	return true
}

// ApplyScaffoldInference parses scaffold inference output and updates the scratchpad.
func (m *ScratchpadManager) ApplyScaffoldInference(turn int, content string) (bool, string) {
	next, parsed, note := parseScaffoldInferenceResult(content, m.scratchpad)
	if !parsed {
		emitEvent(m.events, output.NewScratchpadEvent(turn, false, m.scratchpad.Render(), 0, note))
		return false, note
	}
	m.scratchpad = next
	emitEvent(m.events, output.NewScratchpadEvent(turn, true, m.scratchpad.Render(), 0, ""))
	emitEvent(m.events, output.NewScratchpadUpdatedEvent(output.ScratchpadUpdatedEvent{
		Intent:    m.scratchpad.Intent,
		Decisions: m.scratchpad.Decisions,
		Open:      m.scratchpad.Open,
		Next:      m.scratchpad.Next,
	}))
	return true, ""
}

// ScaffoldPromptState returns the rendered scratchpad state for scaffold inference.
func (m *ScratchpadManager) ScaffoldPromptState() string {
	return m.scratchpad.Render()
}

// SetEventSink installs the event sink for scratchpad diagnostic events.
func (m *ScratchpadManager) SetEventSink(sink output.EventSink) {
	m.events = sink
}

// Render returns the rendered scratchpad as a plain-text block.
func (m *ScratchpadManager) Render() string {
	return m.scratchpad.Render()
}

// Reset clears all scratchpad task state (intent, decisions, open, next, working
// file, last action, and failure count). Called when the user issues a task-reset
// directive.
func (m *ScratchpadManager) Reset() {
	m.scratchpad.Intent = ""
	m.scratchpad.Decisions = ""
	m.scratchpad.Open = ""
	m.scratchpad.Next = ""
	m.scratchpad.WorkingFile = ""
	m.scratchpad.LastAction = ""
	m.failures = 0
}

func (m *ScratchpadManager) scaffoldFingerprint(state RunState, compactionCount int) string {
	toolSummary := ""
	if recent := summarizeRecentToolCalls(state.Lineage.FullMessages(), 1); len(recent) > 0 {
		toolSummary = recent[0]
	}
	workingFile := strings.TrimSpace(m.scratchpad.WorkingFile)
	return fmt.Sprintf("compaction=%d|working=%s|tool=%s", compactionCount, workingFile, toolSummary)
}

func parseScratchpadToolResult(content string, previous Scratchpad) (Scratchpad, bool) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return previous, false
	}
	next := previous
	if v, ok := decodeScratchpadString(raw, "intent"); ok {
		next.Intent = v
	} else {
		return previous, false
	}
	if v, ok := decodeScratchpadString(raw, "decisions"); ok {
		next.Decisions = appendDecisionFactText(previous.Decisions, v)
	} else {
		return previous, false
	}
	if v, ok := decodeScratchpadString(raw, "open"); ok {
		next.Open = v
	} else {
		return previous, false
	}
	if v, ok := decodeScratchpadString(raw, "next"); ok {
		next.Next = v
	} else {
		return previous, false
	}
	return next, true
}

func parseScaffoldInferenceResult(content string, previous Scratchpad) (Scratchpad, bool, string) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return previous, false, "invalid scaffold inference JSON: " + err.Error()
	}
	next := previous
	if v, ok := decodeScratchpadString(raw, "intent"); ok {
		next.Intent = v
	}
	if v, ok := decodeScratchpadString(raw, "next"); ok {
		next.Next = v
	}
	return next, true, ""
}

func decodeScratchpadString(raw map[string]json.RawMessage, key string) (string, bool) {
	value, ok := raw[key]
	if !ok {
		return "", false
	}
	var text string
	if err := json.Unmarshal(value, &text); err != nil {
		return "", false
	}
	return text, true
}

func appendDecisionFactText(existing, fact string) string {
	fact = strings.TrimSpace(fact)
	if fact == "" || strings.EqualFold(fact, "none") {
		return strings.TrimSpace(existing)
	}
	lines := splitDecisionFacts(existing)
	lines = append(lines, fact)
	lines = boundDecisionFacts(lines)
	return strings.Join(lines, "\n")
}

func splitDecisionFacts(existing string) []string {
	existing = strings.TrimSpace(existing)
	if existing == "" {
		return nil
	}
	parts := strings.Split(existing, "\n")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func boundDecisionFacts(lines []string) []string {
	if len(lines) == 0 {
		return nil
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	for len(out) > decisionsMaxLines {
		out = out[1:]
	}
	for len(out) > 0 && len(strings.Join(out, "\n")) > decisionsMaxBytes {
		if len(out) == 1 {
			line := out[0]
			if len(line) > decisionsMaxBytes {
				out[0] = line[len(line)-decisionsMaxBytes:]
			}
			break
		}
		out = out[1:]
	}
	return out
}
