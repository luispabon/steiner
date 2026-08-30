package delegation

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
)

func readJSONLLines(t *testing.T, path string) []toolCallTraceLine {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()

	var lines []toolCallTraceLine
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var line toolCallTraceLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("unmarshal line %q: %v", scanner.Text(), err)
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}
	return lines
}

func TestToolCallTraceWriter_RecordsStartedFinishedPairs(t *testing.T) {
	dir := t.TempDir()
	w := newToolCallTraceWriter(dir, "agent-1")
	if w == nil {
		t.Fatal("newToolCallTraceWriter returned nil")
	}
	defer w.close()

	sink := withToolCallTrace(nil, w)

	calls := []struct {
		tool   string
		callID string
		args   map[string]any
		result string
		errMsg string
	}{
		{"read", "call-1", map[string]any{"path": "a.go"}, "file contents", ""},
		{"bash", "call-2", map[string]any{"command": "ls"}, "listing", ""},
		{"mutate", "call-3", map[string]any{"operations": []any{"x"}}, "", "failed"},
	}

	for i, c := range calls {
		sink.Emit(output.NewToolCallStartedEvent(i, c.tool, c.callID, c.args))
		var err error
		if c.errMsg != "" {
			err = errString(c.errMsg)
		}
		sink.Emit(output.NewToolCallFinishedEvent(i, c.tool, c.callID, c.result, err))
	}

	path, total, failed, counts := w.snapshot()
	if total != len(calls) {
		t.Errorf("total = %d, want %d", total, len(calls))
	}
	if failed != 1 {
		t.Errorf("failed = %d, want 1", failed)
	}
	if counts["read"] != 1 || counts["bash"] != 1 || counts["mutate"] != 1 {
		t.Errorf("counts = %+v, want one each", counts)
	}

	lines := readJSONLLines(t, path)
	if len(lines) != len(calls) {
		t.Fatalf("len(lines) = %d, want %d", len(lines), len(calls))
	}
	for i, line := range lines {
		if line.Tool != calls[i].tool {
			t.Errorf("line[%d].Tool = %q, want %q", i, line.Tool, calls[i].tool)
		}
		wantOK := calls[i].errMsg == ""
		if line.OK != wantOK {
			t.Errorf("line[%d].OK = %v, want %v", i, line.OK, wantOK)
		}
		if line.ArgBytes == 0 {
			t.Errorf("line[%d].ArgBytes = 0, want > 0", i)
		}
	}
}

func TestToolCallTraceWriter_DurationMsFromEventTimestamps(t *testing.T) {
	dir := t.TempDir()
	w := newToolCallTraceWriter(dir, "agent-duration")
	defer w.close()

	sink := withToolCallTrace(nil, w)

	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	started := output.NewToolCallStartedEvent(0, "read", "call-1", nil)
	started.Timestamp = base
	sink.Emit(started)

	finished := output.NewToolCallFinishedEvent(0, "read", "call-1", "ok", nil)
	finished.Timestamp = base.Add(250 * time.Millisecond)
	sink.Emit(finished)

	path, _, _, _ := w.snapshot()
	lines := readJSONLLines(t, path)
	if len(lines) != 1 {
		t.Fatalf("len(lines) = %d, want 1", len(lines))
	}
	if lines[0].DurationMs != 250 {
		t.Errorf("DurationMs = %d, want 250", lines[0].DurationMs)
	}
	if !lines[0].Time.Equal(finished.Timestamp) {
		t.Errorf("Time = %v, want %v", lines[0].Time, finished.Timestamp)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestToolCallTraceWriter_MutateFailClassTaxonomy(t *testing.T) {
	tests := []struct {
		name    string
		message string
		want    string
	}{
		{"no match other", "mutate: no match for old_string in file.go", "no_match_other"},
		{"ambiguous match", "mutate: ambiguous match for old_string, 3 occurrences", "ambiguous_match"},
		{"whitespace variant", "mutate: no match for old_string; a normalized whitespace match exists", "no_match_whitespace_variant_exists"},
		{"target exists", "mutate: create failed, file already exists", "target_exists"},
		{"unrecognized", "mutate: something completely different went wrong", "other"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			w := newToolCallTraceWriter(dir, "agent-mutate")
			defer w.close()

			sink := withToolCallTrace(nil, w)
			sink.Emit(output.NewToolCallStartedEvent(0, "mutate", "call-1", map[string]any{"operations": []any{"x"}}))
			sink.Emit(output.NewToolCallFinishedEvent(0, "mutate", "call-1", tt.message, errString(tt.message)))

			path, _, _, _ := w.snapshot()
			lines := readJSONLLines(t, path)
			if len(lines) != 1 {
				t.Fatalf("len(lines) = %d, want 1", len(lines))
			}
			if lines[0].FailClass != tt.want {
				t.Errorf("FailClass = %q, want %q", lines[0].FailClass, tt.want)
			}
		})
	}
}

func TestToolCallTraceWriter_NonMutateFailureHasNoFailClass(t *testing.T) {
	dir := t.TempDir()
	w := newToolCallTraceWriter(dir, "agent-read")
	defer w.close()

	sink := withToolCallTrace(nil, w)
	sink.Emit(output.NewToolCallStartedEvent(0, "read", "call-1", map[string]any{"path": "a.go"}))
	sink.Emit(output.NewToolCallFinishedEvent(0, "read", "call-1", "no such file", errString("no such file")))

	path, _, _, _ := w.snapshot()
	lines := readJSONLLines(t, path)
	if len(lines) != 1 {
		t.Fatalf("len(lines) = %d, want 1", len(lines))
	}
	if lines[0].FailClass != "" {
		t.Errorf("FailClass = %q, want empty for non-mutate failure", lines[0].FailClass)
	}
}

func TestToolCallTraceWriter_ForwardsToInnerSink(t *testing.T) {
	dir := t.TempDir()
	w := newToolCallTraceWriter(dir, "agent-forward")
	defer w.close()

	var received []output.Event
	inner := output.SinkFunc(func(e output.Event) { received = append(received, e) })
	sink := withToolCallTrace(inner, w)

	sink.Emit(output.NewToolCallStartedEvent(0, "read", "call-1", nil))
	sink.Emit(output.NewToolCallFinishedEvent(0, "read", "call-1", "ok", nil))

	if len(received) != 2 {
		t.Fatalf("len(received) = %d, want 2", len(received))
	}
}

func TestWithToolCallTrace_NilWriterReturnsInnerUnchanged(t *testing.T) {
	inner := output.SinkFunc(func(output.Event) {})
	got := withToolCallTrace(inner, nil)
	if _, ok := got.(scopedToolCallTraceSink); ok {
		t.Error("withToolCallTrace should not wrap when writer is nil")
	}
}

func TestNewToolCallTraceWriter_EmptyWorkDirOrAgentID(t *testing.T) {
	if w := newToolCallTraceWriter("", "agent-1"); w != nil {
		t.Error("expected nil writer for empty workDir")
	}
	if w := newToolCallTraceWriter(t.TempDir(), ""); w != nil {
		t.Error("expected nil writer for empty agentID")
	}
}

func TestToolCallTraceRegistry_RegisterAndTake(t *testing.T) {
	dir := t.TempDir()
	w := newToolCallTraceWriter(dir, "agent-registry")
	registerToolCallTraceWriter("agent-registry", w)

	fields := toolCallTraceFields("agent-registry")
	if fields == nil {
		t.Fatal("toolCallTraceFields returned nil, want populated fields")
	}
	if _, ok := fields["trace_file"]; !ok {
		t.Error("missing trace_file field")
	}

	if got := toolCallTraceFields("agent-registry"); got != nil {
		t.Error("second lookup should return nil; the writer was already taken")
	}
}

func TestPruneOldToolCallTraces(t *testing.T) {
	tracesDir := t.TempDir()

	staleDir := filepath.Join(tracesDir, "stale-session")
	if err := os.MkdirAll(staleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staleDir, "agent.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	staleTime := time.Now().Add(-10 * 24 * time.Hour)
	if err := os.Chtimes(staleDir, staleTime, staleTime); err != nil {
		t.Fatal(err)
	}

	freshDir := filepath.Join(tracesDir, "fresh-session")
	if err := os.MkdirAll(freshDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(freshDir, "agent.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	pruneOldToolCallTraces(tracesDir)

	if _, err := os.Stat(staleDir); !os.IsNotExist(err) {
		t.Errorf("stale session dir still exists: %v", err)
	}
	if _, err := os.Stat(freshDir); err != nil {
		t.Errorf("fresh session dir was removed: %v", err)
	}
}

func TestClassifyMutateFailure_MatchOrder(t *testing.T) {
	// A message matching both "no match for old_string" and the whitespace
	// variant marker must classify as the whitespace variant since it's
	// checked first.
	got := classifyMutateFailure("no match for old_string, but a normalized whitespace match exists")
	if got != "no_match_whitespace_variant_exists" {
		t.Errorf("classifyMutateFailure = %q, want %q", got, "no_match_whitespace_variant_exists")
	}
}

func TestProcessTraceSession_StableWithinProcess(t *testing.T) {
	first := processTraceSession()
	second := processTraceSession()
	if first != second {
		t.Errorf("processTraceSession() changed within the same process: %q vs %q", first, second)
	}
	if len(first) != 12 {
		t.Errorf("processTraceSession() length = %d, want 12", len(first))
	}
	if strings.ContainsAny(first, "ghijklmnopqrstuvwxyzGHIJKLMNOPQRSTUVWXYZ") {
		t.Errorf("processTraceSession() = %q, want hex-only characters", first)
	}
}
