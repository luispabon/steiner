package delegation

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/luispabon/steiner/internal/output"
)

const toolCallTraceRetention = 7 * 24 * time.Hour

var (
	processTraceSessionID     string
	processTraceSessionIDOnce sync.Once
)

// processTraceSession returns a random 12-hex-character ID generated once per
// process and reused for every child spawned by this run. This is a fallback
// for callers with no real interactive-session or oneshot run ID threaded
// through. It groups this run's trace files together when no better session ID
// is available.
func processTraceSession() string {
	processTraceSessionIDOnce.Do(func() {
		buf := make([]byte, 6)
		if _, err := rand.Read(buf); err != nil {
			processTraceSessionID = "unknown"
			return
		}
		processTraceSessionID = hex.EncodeToString(buf)
	})
	return processTraceSessionID
}

// toolCallTraceLine is one JSONL record describing a single tool call made by
// a delegated child. It records sizes only, never argument or result bodies.
type toolCallTraceLine struct {
	Time        time.Time `json:"time"`
	Turn        int       `json:"turn"`
	Tool        string    `json:"tool"`
	ArgBytes    int       `json:"arg_bytes"`
	ResultBytes int       `json:"result_bytes"`
	OK          bool      `json:"ok"`
	DurationMs  int64     `json:"duration_ms"`
	FailClass   string    `json:"fail_class,omitempty"`
}

type pendingToolCall struct {
	start    time.Time
	turn     int
	tool     string
	argBytes int
}

// toolCallTraceWriter records one JSONL line per tool call for a single
// delegated child, to .steiner/traces/<session-id>/<agent-id>.jsonl. It is a
// separate mechanism from TraceLogger/traceCollector, which record coarse
// lifecycle events for the debug log.
type toolCallTraceWriter struct {
	mu      sync.Mutex
	file    *os.File
	enc     *json.Encoder
	path    string
	pending map[string]pendingToolCall

	toolCallsTotal  int
	toolCallsFailed int
	toolCounts      map[string]int
}

// traceSessionID returns sessionID if non-empty, otherwise the process-scoped
// random fallback used when no real session ID was threaded through.
func traceSessionID(sessionID string) string {
	if sessionID != "" {
		return sessionID
	}
	return processTraceSession()
}

// newToolCallTraceWriter creates a trace writer for agentID under workDir.
// Returns nil when the trace file cannot be created; tracing is best-effort
// instrumentation and must never break delegation.
func newToolCallTraceWriter(workDir, agentID, sessionID string) *toolCallTraceWriter {
	if workDir == "" || agentID == "" {
		return nil
	}
	dir := filepath.Join(workDir, ".steiner", "traces", traceSessionID(sessionID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil
	}
	pruneOldToolCallTraces(filepath.Dir(dir))

	path := filepath.Join(dir, agentID+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil
	}
	return &toolCallTraceWriter{
		file:       f,
		enc:        json.NewEncoder(f),
		path:       path,
		pending:    make(map[string]pendingToolCall),
		toolCounts: make(map[string]int),
	}
}

// pruneOldToolCallTraces removes session directories under tracesDir whose
// modification time is older than the retention window. Best-effort; a
// pruning error must not break execution.
func pruneOldToolCallTraces(tracesDir string) {
	entries, err := os.ReadDir(tracesDir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-toolCallTraceRetention)
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.RemoveAll(filepath.Join(tracesDir, entry.Name()))
		}
	}
}

func (w *toolCallTraceWriter) started(ts time.Time, turn int, tool, callID string, arguments map[string]any) {
	if w == nil {
		return
	}
	argBytes := 0
	if len(arguments) > 0 {
		if data, err := json.Marshal(arguments); err == nil {
			argBytes = len(data)
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending[callID] = pendingToolCall{
		start:    ts,
		turn:     turn,
		tool:     tool,
		argBytes: argBytes,
	}
}

func (w *toolCallTraceWriter) finished(ts time.Time, turn int, tool, callID, result, errMsg string) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	pc, found := w.pending[callID]
	if found {
		delete(w.pending, callID)
	} else {
		// No matching start event: duration cannot be derived, report zero
		// rather than a garbage delta.
		pc = pendingToolCall{start: ts, turn: turn, tool: tool}
	}

	ok := errMsg == ""
	line := toolCallTraceLine{
		Time:        ts,
		Turn:        turn,
		Tool:        tool,
		ArgBytes:    pc.argBytes,
		ResultBytes: len(result),
		OK:          ok,
		DurationMs:  ts.Sub(pc.start).Milliseconds(),
	}
	if !ok && tool == "mutate" {
		failText := result
		if failText == "" {
			failText = errMsg
		}
		line.FailClass = classifyMutateFailure(failText)
	}

	w.toolCallsTotal++
	if !ok {
		w.toolCallsFailed++
	}
	w.toolCounts[tool]++

	// Best-effort; delegation tracing must not break execution.
	_ = w.enc.Encode(line)
}

// snapshot returns a copy of the accumulated counters for host-side (never
// model-facing) diagnostics.
func (w *toolCallTraceWriter) snapshot() (path string, total, failed int, counts map[string]int) {
	if w == nil {
		return "", 0, 0, nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	countsCopy := make(map[string]int, len(w.toolCounts))
	for k, v := range w.toolCounts {
		countsCopy[k] = v
	}
	return w.path, w.toolCallsTotal, w.toolCallsFailed, countsCopy
}

func removeAndCloseToolCallTraceWriter(agentID string) {
	w := takeToolCallTraceWriter(agentID)
	if w != nil {
		w.close()
	}
}

func (w *toolCallTraceWriter) close() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	// Best-effort; delegation tracing must not break execution.
	_ = w.file.Close()
}

// mutateFailClasses mirrors scripts/mutate-session-stats.mjs's FAIL_CLASSES,
// in the same match order. Keep both lists in sync.
var mutateFailClasses = []struct {
	name  string
	match func(string) bool
}{
	{"no_match_whitespace_variant_exists", func(s string) bool {
		return strings.Contains(s, "no match for old_string") && strings.Contains(s, "normalized whitespace match exists")
	}},
	{"no_match_other", func(s string) bool { return strings.Contains(s, "no match for old_string") }},
	{"ambiguous_match", func(s string) bool { return strings.Contains(s, "ambiguous match") }},
	{"line_replace_guard_mismatch", func(s string) bool {
		return strings.Contains(s, "contains old_string") || strings.Contains(s, "want exactly 1")
	}},
	{"line_replace_newline_in_old_string", func(s string) bool { return strings.Contains(s, "contains newline characters") }},
	{"line_replace_missing_old_string", func(s string) bool { return strings.Contains(s, "requires old_string for safety") }},
	{"empty_old_string", func(s string) bool { return strings.Contains(s, "old_string is empty") }},
	{"wrong_field_for_op", func(s string) bool { return strings.Contains(s, "is not valid for this operation type") }},
	{"unknown_op_type", func(s string) bool { return strings.Contains(s, "unsupported type") }},
	{"line_out_of_range", func(s string) bool {
		return strings.Contains(s, "is outside file with") || strings.Contains(s, "exceeds file length")
	}},
	{"bad_line_number", func(s string) bool { return strings.Contains(s, "line must be >=") }},
	{"file_hash_stale", func(s string) bool { return strings.Contains(s, "file_hash") }},
	{"parent_dir_missing", func(s string) bool { return strings.Contains(s, "parent directory") }},
	{"assertion_failed", func(s string) bool {
		return strings.Contains(s, "assert_present failed") || strings.Contains(s, "assert_absent failed")
	}},
	{"target_exists", func(s string) bool { return strings.Contains(s, "already exists") }},
	{"target_missing", func(s string) bool { return strings.Contains(s, "does not exist") }},
	{"empty_content_guard", func(s string) bool { return strings.Contains(s, "content is empty but") }},
}

// classifyMutateFailure classifies a failed mutate call's failure text using
// the same taxonomy and match order as scripts/mutate-session-stats.mjs's
// classify function. Falls back to "other" when nothing matches.
func classifyMutateFailure(text string) string {
	s := strings.ToLower(text)
	for _, class := range mutateFailClasses {
		if class.match(s) {
			return class.name
		}
	}
	return "other"
}

// toolCallTraceRegistry maps agent ID to its trace writer for the lifetime of
// a delegation, so SpawnDelegate (in task.go) can read counters built up
// during the child run without threading a new value through agent.RunRequest.
var (
	toolCallTraceRegistryMu sync.Mutex
	toolCallTraceRegistry   = make(map[string]*toolCallTraceWriter)
)

// registerToolCallTraceWriter registers w for agentID so SpawnDelegate can
// later retrieve it via toolCallTraceFields.
func registerToolCallTraceWriter(agentID string, w *toolCallTraceWriter) {
	if w == nil || agentID == "" {
		return
	}
	toolCallTraceRegistryMu.Lock()
	defer toolCallTraceRegistryMu.Unlock()
	toolCallTraceRegistry[agentID] = w
}

// takeToolCallTraceWriter removes and returns the trace writer registered for
// agentID, if any. Calling this closes the caller's responsibility to also
// call close() on the returned writer.
func takeToolCallTraceWriter(agentID string) *toolCallTraceWriter {
	toolCallTraceRegistryMu.Lock()
	defer toolCallTraceRegistryMu.Unlock()
	w := toolCallTraceRegistry[agentID]
	delete(toolCallTraceRegistry, agentID)
	return w
}

// scopedToolCallTraceSink decorates an EventSink, feeding tool-call started
// and finished events to a toolCallTraceWriter before forwarding to inner.
type scopedToolCallTraceSink struct {
	inner  output.EventSink
	writer *toolCallTraceWriter
}

// withToolCallTrace wraps inner so tool-call events are also recorded by
// writer. Returns inner unchanged when writer is nil.
func withToolCallTrace(inner output.EventSink, writer *toolCallTraceWriter) output.EventSink {
	if writer == nil {
		return inner
	}
	return scopedToolCallTraceSink{inner: inner, writer: writer}
}

func (s scopedToolCallTraceSink) Emit(event output.Event) {
	switch payload := event.Payload.(type) {
	case output.ToolCallStartedEvent:
		s.writer.started(event.Timestamp, payload.Turn, payload.Tool, payload.CallID, payload.Arguments)
	case output.ToolCallFinishedEvent:
		s.writer.finished(event.Timestamp, payload.Turn, payload.Tool, payload.CallID, payload.Result, payload.Error)
	}
	if s.inner != nil {
		s.inner.Emit(event)
	}
}

// toolCallTraceFields builds the host-side (never model-facing) trace-log
// fields describing this child's tool-call activity, for the debug log
// entry appended by SpawnDelegate/failedDelegateExecution.
func toolCallTraceFields(agentID string) map[string]any {
	w := takeToolCallTraceWriter(agentID)
	if w == nil {
		return nil
	}
	defer w.close()
	path, total, failed, counts := w.snapshot()
	return map[string]any{
		"trace_file":        path,
		"tool_calls_total":  total,
		"tool_calls_failed": failed,
		"tool_counts":       counts,
	}
}
