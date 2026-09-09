package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestClientEmitsObservableProviderDiagnostics(t *testing.T) {
	diagDir := t.TempDir()
	writer := newProviderDiagnostics(t, diagDir)
	logger, err := NewStreamErrorLoggerWithDiagnostics("", writer)
	if err != nil {
		t.Fatalf("NewStreamErrorLoggerWithDiagnostics() error = %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer server.Close()

	client, err := NewOpenAICompat(ClientConfig{BaseURL: server.URL, Model: "test-model", StreamErrorLog: logger})
	if err != nil {
		t.Fatalf("NewOpenAICompat() error = %v", err)
	}
	if _, err := client.ChatCompletion(context.Background(), ChatRequest{}); err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(diagDir, "provider.jsonl"))
	if err != nil {
		t.Fatalf("read provider diagnostics: %v", err)
	}
	var record struct {
		Kind    string `json:"kind"`
		Payload struct {
			Model   string `json:"model"`
			Outcome string `json:"outcome"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("unmarshal provider diagnostic: %v", err)
	}
	if record.Kind != string(diagnostics.KindProvider) || record.Payload.Model != "test-model" || record.Payload.Outcome != "ok" {
		t.Errorf("record = %+v, want provider test-model ok record", record)
	}
}
