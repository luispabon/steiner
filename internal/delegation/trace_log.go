package delegation

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/luispabon/steiner/internal/diagnostics"
)

// TraceLogger writes delegation trace records as JSON lines to a dedicated file.
// Safe for concurrent use.
type TraceLogger struct {
	mu   sync.Mutex
	file *os.File
	enc  *json.Encoder
	diag *diagnostics.Writer
}

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

// delegationTraceEvent discriminates delegation traces from the tool-call
// records that also live in the tool stream: tool.jsonl is not homogeneous.
const delegationTraceEvent = "delegation_trace"

// delegationTracePayload is the diagnostics payload for a delegation trace
// when body capture is enabled.
type delegationTracePayload struct {
	Event   string       `json:"event"`
	AgentID string       `json:"agent_id"`
	Task    string       `json:"task"`
	Entries []TraceEntry `json:"entries"`
}

// delegationTraceSummaryPayload keeps the default diagnostics payload bounded
// and free of delegation content.
type delegationTraceSummaryPayload struct {
	Event      string `json:"event"`
	EntryCount int    `json:"entry_count"`
}

// traceRecord is the top-level structure written to the delegation log file.
type traceRecord struct {
	AgentID string       `json:"agent_id"`
	Task    string       `json:"task"`
	Entries []TraceEntry `json:"entries"`
}

// NewTraceLogger creates a TraceLogger writing to path. Parent directories are
// created if needed. Returns nil without error when path is empty.
func NewTraceLogger(path string) (*TraceLogger, error) {
	return NewTraceLoggerWithDiagnostics(path, nil)
}

// NewTraceLoggerWithDiagnostics creates a TraceLogger that writes through diag
// when one is supplied, and falls back to its own file at path when it is not.
// The diagnostics writer subsumes the file: no file is opened when diag is
// non-nil, so traces land in the tool stream instead of a log path derived
// from the sensitive --log-file. Returns nil without error when there is
// neither a writer nor a path.
func NewTraceLoggerWithDiagnostics(path string, diag *diagnostics.Writer) (*TraceLogger, error) {
	if diag != nil {
		return &TraceLogger{diag: diag}, nil
	}
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create delegation log directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open delegation log: %w", err)
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("secure delegation log: %w", err)
	}
	return &TraceLogger{
		file: f,
		enc:  json.NewEncoder(f),
	}, nil
}

// WriteTrace appends a trace record to the log. No-op on nil receiver.
func (l *TraceLogger) WriteTrace(tc *traceCollector) {
	if l == nil || tc == nil {
		return
	}
	entries := tc.result()
	if len(entries) == 0 {
		return
	}
	record := traceRecord{
		AgentID: tc.agentID,
		Task:    truncateTaskPreview(tc.task, 200),
		Entries: entries,
	}
	if l.diag != nil {
		payload := any(delegationTraceSummaryPayload{
			Event:      delegationTraceEvent,
			EntryCount: len(record.Entries),
		})
		if l.diag.CaptureBodies() {
			payload = delegationTracePayload{
				Event:   delegationTraceEvent,
				AgentID: record.AgentID,
				Task:    record.Task,
				Entries: record.Entries,
			}
		}
		l.diag.Write(diagnostics.Record{
			Kind:    diagnostics.KindTool,
			Source:  diagnostics.SourceSubAgent,
			AgentID: record.AgentID,
			Payload: payload,
		})
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	// Best-effort; delegation logging must not break execution.
	_ = l.enc.Encode(record)
}

// Close flushes and closes the underlying file. No-op on nil receiver.
func (l *TraceLogger) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.file.Close()
}

// LogPath derives the delegation log path from the main session log path.
// Returns empty string when logPath is empty.
func LogPath(logPath string) string {
	logPath = strings.TrimSpace(logPath)
	if logPath == "" {
		return ""
	}
	ext := filepath.Ext(logPath)
	base := strings.TrimSuffix(logPath, ext)
	if ext == "" {
		ext = ".log"
	}
	return base + "-delegation" + ext
}
