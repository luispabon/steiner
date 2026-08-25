package provider

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/luispabon/steiner/internal/usagestats"
)

func TestRecordWSTelemetryWritesConnectionEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telemetry.jsonl")
	t.Setenv(usagestats.TelemetryEnvVar, path)
	t.Setenv(usagestats.TelemetryRunEnvVar, "run-7")

	recordWSTelemetry(wsTelemetryEventDial, "", "cache-key-1")
	recordWSTelemetry(wsTelemetryEventReconnect, "request: read response: EOF", "cache-key-1")

	lines := readWSTelemetryLines(t, path)
	if len(lines) != 2 {
		t.Fatalf("got %d telemetry lines, want 2", len(lines))
	}

	if lines[0].Kind != "ws" {
		t.Errorf("kind = %q, want %q", lines[0].Kind, "ws")
	}
	if lines[0].Event != wsTelemetryEventDial {
		t.Errorf("event = %q, want %q", lines[0].Event, wsTelemetryEventDial)
	}
	if lines[0].RunID != "run-7" {
		t.Errorf("run_id = %q, want %q", lines[0].RunID, "run-7")
	}
	if lines[0].CacheKey != "cache-key-1" {
		t.Errorf("cache_key = %q, want %q", lines[0].CacheKey, "cache-key-1")
	}
	if lines[0].Timestamp == "" {
		t.Error("ts is empty, want an RFC3339 timestamp")
	}

	if lines[1].Event != wsTelemetryEventReconnect {
		t.Errorf("second event = %q, want %q", lines[1].Event, wsTelemetryEventReconnect)
	}
	if lines[1].Reason != "request: read response: EOF" {
		t.Errorf("second reason = %q, want the recorded read error", lines[1].Reason)
	}
}

func TestRecordWSTelemetryDisabledWhenEnvUnset(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(usagestats.TelemetryEnvVar, "")

	recordWSTelemetry(wsTelemetryEventDial, "", "cache-key-1")

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read temp dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("wrote %d files with telemetry disabled, want 0", len(entries))
	}
}

func TestRecordWSTelemetryFollowsPathChange(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.jsonl")
	second := filepath.Join(dir, "second.jsonl")

	t.Setenv(usagestats.TelemetryEnvVar, first)
	recordWSTelemetry(wsTelemetryEventDial, "", "key-a")

	t.Setenv(usagestats.TelemetryEnvVar, second)
	recordWSTelemetry(wsTelemetryEventReconnect, "boom", "key-b")

	if got := len(readWSTelemetryLines(t, first)); got != 1 {
		t.Errorf("first file has %d lines, want 1", got)
	}
	secondLines := readWSTelemetryLines(t, second)
	if len(secondLines) != 1 {
		t.Fatalf("second file has %d lines, want 1", len(secondLines))
	}
	if secondLines[0].Event != wsTelemetryEventReconnect {
		t.Errorf("event = %q, want %q", secondLines[0].Event, wsTelemetryEventReconnect)
	}
}

func readWSTelemetryLines(t *testing.T, path string) []wsTelemetryLine {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read telemetry file: %v", err)
	}
	var lines []wsTelemetryLine
	decoder := json.NewDecoder(bytes.NewReader(data))
	for decoder.More() {
		var line wsTelemetryLine
		if err := decoder.Decode(&line); err != nil {
			t.Fatalf("decode telemetry line: %v", err)
		}
		lines = append(lines, line)
	}
	return lines
}
