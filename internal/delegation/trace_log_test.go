package delegation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewTraceLogger_EmptyPath(t *testing.T) {
	logger, err := NewTraceLogger("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if logger != nil {
		t.Error("expected nil logger for empty path")
	}
}

func TestNewTraceLogger_WhitespacePath(t *testing.T) {
	logger, err := NewTraceLogger("   ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if logger != nil {
		t.Error("expected nil logger for whitespace path")
	}
}

func TestTraceLogger_WriteAndClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "delegation.log")

	logger, err := NewTraceLogger(path)
	if err != nil {
		t.Fatalf("NewTraceLogger: %v", err)
	}

	tc := newTraceCollector("test-agent", "do something")
	tc.add("start", "started", map[string]any{"turns": 10})
	tc.add("done", "finished", nil)

	logger.WriteTrace(tc)
	if err := logger.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var record traceRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("Unmarshal: %v (data: %s)", err, string(data))
	}
	if record.AgentID != "test-agent" {
		t.Errorf("AgentID = %q, want %q", record.AgentID, "test-agent")
	}
	if record.Task != "do something" {
		t.Errorf("Task = %q, want %q", record.Task, "do something")
	}
	if len(record.Entries) != 2 {
		t.Errorf("len(Entries) = %d, want 2", len(record.Entries))
	}
}

func TestTraceLogger_MultipleWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "delegation.log")

	logger, err := NewTraceLogger(path)
	if err != nil {
		t.Fatalf("NewTraceLogger: %v", err)
	}

	for i := range 3 {
		tc := newTraceCollector("agent", "task")
		tc.add("step", "entry", map[string]any{"i": i})
		logger.WriteTrace(tc)
	}
	if err := logger.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Errorf("expected 3 JSON lines, got %d", len(lines))
	}
}

func TestTraceLogger_NilReceiver(t *testing.T) {
	var logger *TraceLogger
	tc := newTraceCollector("agent", "task")
	tc.add("start", "msg", nil)
	logger.WriteTrace(tc)

	if err := logger.Close(); err != nil {
		t.Errorf("nil Close should return nil, got %v", err)
	}
}

func TestTraceLogger_EmptyCollector(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "delegation.log")

	logger, err := NewTraceLogger(path)
	if err != nil {
		t.Fatalf("NewTraceLogger: %v", err)
	}

	tc := newTraceCollector("agent", "task")
	logger.WriteTrace(tc)
	if err := logger.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(strings.TrimSpace(string(data))) != 0 {
		t.Errorf("expected empty file for empty collector, got %q", string(data))
	}
}

func TestTraceLogger_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "dir", "delegation.log")

	logger, err := NewTraceLogger(path)
	if err != nil {
		t.Fatalf("NewTraceLogger: %v", err)
	}
	if err := logger.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("expected log file to be created")
	}
}

func TestLogPath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"   ", ""},
		{"/var/log/steiner.log", "/var/log/steiner-delegation.log"},
		{"/var/log/steiner.jsonl", "/var/log/steiner-delegation.jsonl"},
		{"/var/log/steiner", "/var/log/steiner-delegation.log"},
		{"session.log", "session-delegation.log"},
	}
	for _, tt := range tests {
		got := LogPath(tt.input)
		if got != tt.want {
			t.Errorf("LogPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
