package delegation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/luispabon/steiner/internal/diagnostics"
)

func TestTraceLoggerWritesThroughDiagnostics(t *testing.T) {
	diagDir := filepath.Join(t.TempDir(), "diag")
	logPath := filepath.Join(t.TempDir(), "steiner-delegation.log")
	writer, err := diagnostics.New(diagnostics.Options{Dir: diagDir, Streams: diagnostics.Streams{Tool: true}})
	if err != nil {
		t.Fatalf("diagnostics.New() error = %v", err)
	}
	t.Cleanup(func() { _ = writer.Close() })

	logger, err := NewTraceLoggerWithDiagnostics(logPath, writer)
	if err != nil {
		t.Fatalf("NewTraceLoggerWithDiagnostics() error = %v", err)
	}
	collector := newTraceCollector("agent-7", "investigate the cache")
	collector.add("start", "child run started", map[string]any{"turn": 1})
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
