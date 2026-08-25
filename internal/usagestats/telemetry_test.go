package usagestats

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestTelemetryRecordsOneLinePerObservation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telemetry.jsonl")
	t.Setenv(TelemetryEnvVar, path)
	t.Setenv(TelemetryRunEnvVar, "run-42")

	recorder := New(nil)
	recorder.Record(Observation{
		ProviderAlias:     "codex",
		ProviderType:      "codex",
		BackendModelID:    "gpt-5.6-luna",
		PromptTokens:      1000,
		CompletionTokens:  50,
		CacheReadTokens:   800,
		CacheCreateTokens: 100,
		Source:            SourceParent,
	})
	recorder.Record(Observation{
		ProviderAlias: "codex",
		PromptTokens:  10,
		Source:        SourceAdvisor,
	})

	lines := readTelemetryLines(t, path)
	if len(lines) != 2 {
		t.Fatalf("got %d telemetry lines, want 2", len(lines))
	}

	first := lines[0]
	if first.Kind != "usage" {
		t.Errorf("kind = %q, want %q", first.Kind, "usage")
	}
	if first.RunID != "run-42" {
		t.Errorf("run_id = %q, want %q", first.RunID, "run-42")
	}
	if first.Seq != 1 {
		t.Errorf("seq = %d, want 1", first.Seq)
	}
	if first.Source != "parent" {
		t.Errorf("source = %q, want %q", first.Source, "parent")
	}
	if first.CacheReadTokens != 800 {
		t.Errorf("cache_read_tokens = %d, want 800", first.CacheReadTokens)
	}
	if first.CacheCreateTokens != 100 {
		t.Errorf("cache_create_tokens = %d, want 100", first.CacheCreateTokens)
	}
	if first.PromptTokens != 1000 {
		t.Errorf("prompt_tokens = %d, want 1000", first.PromptTokens)
	}
	if first.BackendModelID != "gpt-5.6-luna" {
		t.Errorf("backend_model_id = %q, want %q", first.BackendModelID, "gpt-5.6-luna")
	}
	if first.Timestamp == "" {
		t.Error("ts is empty, want an RFC3339 timestamp")
	}

	if lines[1].Source != "advisor" {
		t.Errorf("second source = %q, want %q", lines[1].Source, "advisor")
	}
	if lines[1].Seq != 2 {
		t.Errorf("second seq = %d, want 2", lines[1].Seq)
	}
}

func TestTelemetryDisabledWhenEnvUnset(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(TelemetryEnvVar, "")

	recorder := New(nil)
	if recorder.telemetry != nil {
		t.Fatal("telemetry writer built with the env var unset, want nil")
	}
	recorder.Record(Observation{PromptTokens: 10})

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read temp dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("wrote %d files with telemetry disabled, want 0", len(entries))
	}
}

func TestTelemetryUnwritablePathDoesNotFailRecording(t *testing.T) {
	// A directory path can never be opened for writing, standing in for any
	// unopenable target: recording must degrade to a no-op, not panic.
	t.Setenv(TelemetryEnvVar, t.TempDir())

	recorder := New(nil)
	if recorder.telemetry != nil {
		t.Fatal("telemetry writer built for an unwritable path, want nil")
	}
	recorder.Record(Observation{PromptTokens: 10})
}

func TestSourceName(t *testing.T) {
	tests := []struct {
		name   string
		source Source
		want   string
	}{
		{name: "parent", source: SourceParent, want: "parent"},
		{name: "sub agent", source: SourceSubAgent, want: "sub_agent"},
		{name: "advisor", source: SourceAdvisor, want: "advisor"},
		{name: "unknown", source: Source(99), want: "unknown"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sourceName(tc.source); got != tc.want {
				t.Errorf("sourceName(%v) = %q, want %q", tc.source, got, tc.want)
			}
		})
	}
}

func readTelemetryLines(t *testing.T, path string) []telemetryLine {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read telemetry file: %v", err)
	}
	var lines []telemetryLine
	decoder := json.NewDecoder(bytes.NewReader(data))
	for decoder.More() {
		var line telemetryLine
		if err := decoder.Decode(&line); err != nil {
			t.Fatalf("decode telemetry line: %v", err)
		}
		lines = append(lines, line)
	}
	return lines
}
