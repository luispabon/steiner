package delegation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/diagnostics"
)

func TestTraceLoggerWritesDetailedTraceThroughDiagnosticsWhenCaptureEnabled(t *testing.T) {
	diagDir := filepath.Join(t.TempDir(), "diag")
	logPath := filepath.Join(t.TempDir(), "steiner-delegation.log")
	writer, err := diagnostics.New(diagnostics.Options{Dir: diagDir, Streams: diagnostics.Streams{Tool: true}, CaptureBodies: true})
	if err != nil {
		t.Fatalf("diagnostics.New() error = %v", err)
	}
	t.Cleanup(func() { _ = writer.Close() })

	logger, err := NewTraceLoggerWithDiagnostics(logPath, writer)
	if err != nil {
		t.Fatalf("NewTraceLoggerWithDiagnostics() error = %v", err)
	}
	collector := newTraceCollector("agent-7", "investigate the cache")
	collector.add("start", "child run started", map[string]any{"turn": 1, "detail": "captured"})
	logger.WriteTrace(collector)
	if err := logger.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Errorf("stat %s error = %v, want no fallback file when diagnostics is wired", logPath, err)
	}
	data, err := os.ReadFile(filepath.Join(diagDir, "tool.jsonl"))
	if err != nil {
		t.Fatalf("read tool.jsonl: %v", err)
	}
	var rec struct {
		Kind    string `json:"kind"`
		Source  string `json:"source"`
		AgentID string `json:"agent_id"`
		Payload struct {
			Event   string       `json:"event"`
			AgentID string       `json:"agent_id"`
			Task    string       `json:"task"`
			Entries []TraceEntry `json:"entries"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("unmarshal record: %v", err)
	}
	if rec.Kind != string(diagnostics.KindTool) || rec.Source != string(diagnostics.SourceSubAgent) {
		t.Errorf("envelope = %q/%q, want tool/sub_agent", rec.Kind, rec.Source)
	}
	if rec.AgentID != "agent-7" {
		t.Errorf("agent_id = %q, want agent-7", rec.AgentID)
	}
	if rec.Payload.Event != delegationTraceEvent {
		t.Errorf("payload event = %q, want %q", rec.Payload.Event, delegationTraceEvent)
	}
	if len(rec.Payload.Entries) != 1 || rec.Payload.Entries[0].Phase != "start" {
		t.Errorf("payload entries = %+v, want the collected entry", rec.Payload.Entries)
	}
	if rec.Payload.Task != "investigate the cache" || rec.Payload.Entries[0].Message != "child run started" {
		t.Errorf("payload detail = %+v, want captured task and message", rec.Payload)
	}
	if rec.Payload.Entries[0].Fields["detail"] != "captured" {
		t.Errorf("payload fields = %+v, want captured map value", rec.Payload.Entries[0].Fields)
	}
}

func TestTraceLoggerDiagnosticsOmitsBodiesWhenCaptureDisabled(t *testing.T) {
	diagDir := filepath.Join(t.TempDir(), "diag")
	logPath := filepath.Join(t.TempDir(), "private", "steiner-delegation.log")
	writer, err := diagnostics.New(diagnostics.Options{Dir: diagDir, Streams: diagnostics.Streams{Tool: true}})
	if err != nil {
		t.Fatalf("diagnostics.New() error = %v", err)
	}
	t.Cleanup(func() { _ = writer.Close() })

	logger, err := NewTraceLoggerWithDiagnostics(logPath, writer)
	if err != nil {
		t.Fatalf("NewTraceLoggerWithDiagnostics() error = %v", err)
	}
	collector := newTraceCollector("agent-privacy", "secret task text /absolute/task/path")
	collector.add("private-phase", "secret trace message", map[string]any{
		"secret_key": "secret map value",
		"path":       "/absolute/trace/path",
	})
	logger.WriteTrace(collector)

	data, err := os.ReadFile(filepath.Join(diagDir, "tool.jsonl"))
	if err != nil {
		t.Fatalf("read tool.jsonl: %v", err)
	}
	payload := string(data)
	for _, secret := range []string{
		"secret task text",
		"secret trace message",
		"secret map value",
		"/absolute/task/path",
		"/absolute/trace/path",
		"private-phase",
	} {
		if strings.Contains(payload, secret) {
			t.Errorf("diagnostics payload contains private value %q: %s", secret, payload)
		}
	}
	var rec struct {
		Payload map[string]any `json:"payload"`
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("unmarshal record: %v", err)
	}
	if rec.Payload["event"] != delegationTraceEvent {
		t.Errorf("payload event = %v, want %q", rec.Payload["event"], delegationTraceEvent)
	}
	if rec.Payload["entry_count"] != float64(1) {
		t.Errorf("entry_count = %v, want 1", rec.Payload["entry_count"])
	}
	if len(rec.Payload) != 2 {
		t.Errorf("payload fields = %v, want only scalar summary fields", rec.Payload)
	}
	for _, field := range []string{"agent_id", "task", "message", "fields", "entries", "path"} {
		if _, ok := rec.Payload[field]; ok {
			t.Errorf("payload contains forbidden field %q: %v", field, rec.Payload)
		}
	}
}

func TestTraceLoggerFallsBackToFile(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "steiner-delegation.log")
	logger, err := NewTraceLoggerWithDiagnostics(logPath, nil)
	if err != nil {
		t.Fatalf("NewTraceLoggerWithDiagnostics() error = %v", err)
	}
	collector := newTraceCollector("agent-1", "task")
	collector.add("start", "child run started", nil)
	logger.WriteTrace(collector)
	if err := logger.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fallback log: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("fallback log is empty, want the trace record")
	}
}

func TestTraceLoggerNoPathNoDiagnostics(t *testing.T) {
	logger, err := NewTraceLoggerWithDiagnostics("", nil)
	if err != nil {
		t.Fatalf("NewTraceLoggerWithDiagnostics() error = %v", err)
	}
	if logger != nil {
		t.Fatalf("logger = %#v, want nil with neither a path nor a writer", logger)
	}
}
