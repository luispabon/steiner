package provider

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/luispabon/steiner/internal/diagnostics"
)

func newProviderDiagnostics(t *testing.T, dir string) *diagnostics.Writer {
	t.Helper()
	w, err := diagnostics.New(diagnostics.Options{Dir: dir, Streams: diagnostics.Streams{Provider: true}})
	if err != nil {
		t.Fatalf("diagnostics.New() error = %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w
}

func TestStreamErrorLoggerWritesThroughDiagnostics(t *testing.T) {
	diagDir := filepath.Join(t.TempDir(), "diag")
	logPath := filepath.Join(t.TempDir(), "steiner-stream-errors.log")

	logger, err := NewStreamErrorLoggerWithDiagnostics(logPath, newProviderDiagnostics(t, diagDir))
	if err != nil {
		t.Fatalf("NewStreamErrorLoggerWithDiagnostics() error = %v", err)
	}
	if logger == nil {
		t.Fatal("logger = nil, want a logger")
	}
	logger.Log(streamErrorRecord{Outcome: "retried", Attempts: 2})
	if err := logger.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Errorf("stat %s error = %v, want no fallback file when diagnostics is wired", logPath, err)
	}
	data, err := os.ReadFile(filepath.Join(diagDir, "provider.jsonl"))
	if err != nil {
		t.Fatalf("read provider.jsonl: %v", err)
	}
	var rec struct {
		Kind    string `json:"kind"`
		Payload struct {
			Outcome  string `json:"outcome"`
			Attempts int    `json:"attempts"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("unmarshal record: %v", err)
	}
	if rec.Kind != string(diagnostics.KindProvider) {
		t.Errorf("kind = %q, want provider", rec.Kind)
	}
	if rec.Payload.Outcome != "retried" || rec.Payload.Attempts != 2 {
		t.Errorf("payload = %+v, want the stream error record", rec.Payload)
	}
}

func TestStreamErrorLoggerFallsBackToFile(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "steiner-stream-errors.log")
	logger, err := NewStreamErrorLoggerWithDiagnostics(logPath, nil)
	if err != nil {
		t.Fatalf("NewStreamErrorLoggerWithDiagnostics() error = %v", err)
	}
	logger.Log(streamErrorRecord{Outcome: "exhausted"})
	if err := logger.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fallback log: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("fallback log is empty, want the record")
	}
}

func TestStreamErrorLoggerNoPathNoDiagnostics(t *testing.T) {
	logger, err := NewStreamErrorLoggerWithDiagnostics("", nil)
	if err != nil {
		t.Fatalf("NewStreamErrorLoggerWithDiagnostics() error = %v", err)
	}
	if logger != nil {
		t.Fatalf("logger = %#v, want nil with neither a path nor a writer", logger)
	}
}

func TestClientCarriesDiagnosticsWriter(t *testing.T) {
	writer := newProviderDiagnostics(t, t.TempDir())
	client, err := NewOpenAICompat(ClientConfig{
		BaseURL:     "http://localhost:11434/v1",
		Model:       "test-model",
		Diagnostics: writer,
	})
	if err != nil {
		t.Fatalf("NewOpenAICompat() error = %v", err)
	}
	if client.diagnostics != writer {
		t.Errorf("client.diagnostics = %v, want the configured writer", client.diagnostics)
	}
}
