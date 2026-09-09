package tool

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/diagnostics"
)

// readToolRecords reads every record from dir's tool.jsonl and decodes each
// payload into toolRecordPayload.
func readToolRecords(t *testing.T, dir string) []toolRecordPayload {
	t.Helper()
	f, err := os.Open(filepath.Join(dir, "tool.jsonl"))
	if err != nil {
		t.Fatalf("open tool.jsonl: %v", err)
	}
	defer func() { _ = f.Close() }()

	var records []toolRecordPayload
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec diagnostics.Record
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("unmarshal record: %v", err)
		}
		payloadBytes, err := json.Marshal(rec.Payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		var payload toolRecordPayload
		if err := json.Unmarshal(payloadBytes, &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		records = append(records, payload)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan tool.jsonl: %v", err)
	}
	return records
}

func newToolStreamExecutor(t *testing.T, reg *Registry, root string) (*Executor, string) {
	t.Helper()
	diagDir := t.TempDir()
	writer, err := diagnostics.New(diagnostics.Options{Dir: diagDir, Streams: diagnostics.Streams{Tool: true}})
	if err != nil {
		t.Fatalf("diagnostics.New() error = %v", err)
	}
	t.Cleanup(func() { _ = writer.Close() })

	executor := NewExecutor(reg, config.Config{}, nil, root, "", Unsandboxed{})
	executor.WithDiagnostics(writer)
	return executor, diagDir
}

func TestExecuteEmitsToolRecord_Success(t *testing.T) {
	root := t.TempDir()
	reg := NewRegistry(ToolDef{
		Name:    "read",
		Handler: func(_ context.Context, _ map[string]any) (any, error) { return "ok", nil },
	})
	executor, diagDir := newToolStreamExecutor(t, reg, root)

	if _, err := executor.Execute(context.Background(), "read", "", map[string]any{}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	records := readToolRecords(t, diagDir)
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	got := records[0]
	if got.Tool != "read" || got.Outcome != "ok" || got.OpsTotal != 1 || got.OpsFailed != 0 {
		t.Errorf("record = %+v, want tool=read outcome=ok ops_total=1 ops_failed=0", got)
	}
	if len(got.Failures) != 0 {
		t.Errorf("Failures = %v, want none", got.Failures)
	}
}

func TestExecuteEmitsToolRecord_GenericError(t *testing.T) {
	root := t.TempDir()
	reg := NewRegistry(ToolDef{
		Name:    "bash",
		Handler: func(_ context.Context, _ map[string]any) (any, error) { return nil, errors.New("boom") },
	})
	executor, diagDir := newToolStreamExecutor(t, reg, root)

	if _, err := executor.Execute(context.Background(), "bash", "", map[string]any{}); err == nil {
		t.Fatal("Execute() error = nil, want error")
	}

	records := readToolRecords(t, diagDir)
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	got := records[0]
	if got.Outcome != "error" || got.OpsTotal != 1 || got.OpsFailed != 1 {
		t.Errorf("record = %+v, want outcome=error ops_total=1 ops_failed=1", got)
	}
	if len(got.Failures) != 1 || got.Failures[0].Op != "" {
		t.Errorf("Failures = %v, want one entry with no op (tool already carries the name)", got.Failures)
	}
}

func TestExecuteEmitsToolRecord_PolicyDenied(t *testing.T) {
	root := t.TempDir()
	reg := NewRegistry(ToolDef{
		Name:    "mutate",
		Handler: func(_ context.Context, _ map[string]any) (any, error) { return nil, nil },
	})
	executor, diagDir := newToolStreamExecutor(t, reg, root)

	_, err := executor.Execute(context.Background(), "mutate", "", map[string]any{
		"operations": []any{
			map[string]any{"type": "write", "path": "/etc/passwd", "content": "x"},
		},
	})
	if err == nil {
		t.Fatal("Execute() error = nil, want policy_denied")
	}

	records := readToolRecords(t, diagDir)
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	if got := records[0].Outcome; got != "denied" {
		t.Errorf("Outcome = %q, want %q", got, "denied")
	}
}

// TestExecuteEmitsToolRecord_NoAbsolutePath asserts that a tool call
// touching an absolute path never leaks that path into the diagnostics
// record — only the failing operation's extension may appear.
func TestExecuteEmitsToolRecord_NoAbsolutePath(t *testing.T) {
	root := t.TempDir()
	reg := NewRegistry(ToolDef{
		Name: "mutate",
		Handler: func(_ context.Context, _ map[string]any) (any, error) {
			return &fakeMutateResult{
				failures: []OpFailure{{Op: "delete_file", Reason: "missing_file", Path: filepath.Join(root, "secret.go")}},
			}, nil
		},
	})
	executor, diagDir := newToolStreamExecutor(t, reg, root)

	if _, err := executor.Execute(context.Background(), "mutate", "", map[string]any{
		"operations": []any{
			map[string]any{"type": "delete_file", "path": "secret.go"},
		},
	}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(diagDir, "tool.jsonl"))
	if err != nil {
		t.Fatalf("read tool.jsonl: %v", err)
	}
	if strings.Contains(string(raw), root) {
		t.Fatalf("tool.jsonl contains the absolute root %q:\n%s", root, raw)
	}
	if strings.Contains(string(raw), "secret.go") {
		t.Fatalf("tool.jsonl contains the bare filename %q:\n%s", "secret.go", raw)
	}

	records := readToolRecords(t, diagDir)
	if len(records) != 1 || len(records[0].Failures) != 1 {
		t.Fatalf("records = %+v, want one record with one failure", records)
	}
	if got := records[0].Failures[0].PathExt; got != ".go" {
		t.Errorf("PathExt = %q, want %q", got, ".go")
	}
}

// TestPathExt asserts a dotfile's basename is not returned as its
// "extension" — filepath.Ext("/a/.env") is ".env", which would otherwise
// leak the whole filename into the diagnostics stream.
func TestPathExt(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{path: "/home/user/project/main.go", want: ".go"},
		{path: "main.go", want: ".go"},
		{path: "/home/user/.env", want: ""},
		{path: ".env", want: ""},
		{path: "/home/user/Makefile", want: ""},
		{path: "", want: ""},
	}
	for _, tt := range tests {
		if got := pathExt(tt.path); got != tt.want {
			t.Errorf("pathExt(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

// fakeMutateResult is a minimal DiagnosticsDetail implementation for testing
// the executor's redaction boundary without depending on internal/tool/builtin.
type fakeMutateResult struct {
	failures []OpFailure
}

func (r *fakeMutateResult) FailedOps() []OpFailure { return r.failures }
