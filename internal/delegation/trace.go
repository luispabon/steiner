package delegation

import (
	"time"
)

// TraceEntry records a single lifecycle event during delegation execution.
type TraceEntry struct {
	Time    time.Time      `json:"time"`
	Phase   string         `json:"phase"`
	Message string         `json:"message"`
	Fields  map[string]any `json:"fields,omitempty"`
}

// traceCollector accumulates lifecycle entries during a single delegation.
type traceCollector struct {
	agentID string
	task    string
	entries []TraceEntry
}

func newTraceCollector(agentID, task string) *traceCollector {
	return &traceCollector{
		agentID: agentID,
		task:    task,
	}
}

func (t *traceCollector) add(phase, message string, fields map[string]any) {
	t.entries = append(t.entries, TraceEntry{
		Time:    time.Now(),
		Phase:   phase,
		Message: message,
		Fields:  fields,
	})
}

func (t *traceCollector) result() []TraceEntry {
	if len(t.entries) == 0 {
		return nil
	}
	out := make([]TraceEntry, len(t.entries))
	copy(out, t.entries)
	return out
}

// traceRecord is the top-level structure written to the delegation log file.
type traceRecord struct {
	AgentID string       `json:"agent_id"`
	Task    string       `json:"task"`
	Entries []TraceEntry `json:"entries"`
}
