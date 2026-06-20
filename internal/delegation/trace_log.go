package delegation

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// TraceLogger writes delegation trace records as JSON lines to a dedicated file.
// Safe for concurrent use.
type TraceLogger struct {
	mu   sync.Mutex
	file *os.File
	enc  *json.Encoder
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

// traceRecord is the top-level structure written to the delegation log file.
type traceRecord struct {
	AgentID string       `json:"agent_id"`
	Task    string       `json:"task"`
	Entries []TraceEntry `json:"entries"`
}

// NewTraceLogger creates a TraceLogger writing to path. Parent directories are
// created if needed. Returns nil without error when path is empty.
func NewTraceLogger(path string) (*TraceLogger, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create delegation log directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open delegation log: %w", err)
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
	l.mu.Lock()
	defer l.mu.Unlock()
	// Best-effort; delegation logging must not break execution.
	_ = l.enc.Encode(record)
}

// Close flushes and closes the underlying file. No-op on nil receiver.
func (l *TraceLogger) Close() error {
	if l == nil {
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
