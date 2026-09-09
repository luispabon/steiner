package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/diagnostics"
	"github.com/luispabon/steiner/internal/tool"
)

// mutateErrorCase drives a real mutate call that fails for a specific,
// known reason.
type mutateErrorCase struct {
	name string
	run  func(t *testing.T) *MutateResult
	want string
}

// mutateErrorCases builds real mutate failures — one per classifyMutateError
// reason reachable from mutate's own error space — used both to test the
// classifier and, in TestMutateFailedOps_Invariant, to check that every
// failing MutateResult carries FailedOps detail.
func mutateErrorCases() []mutateErrorCase {
	return []mutateErrorCase{
		{
			name: "no_match",
			run: func(t *testing.T) *MutateResult {
				root := t.TempDir()
				if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello world"), 0o644); err != nil {
					t.Fatalf("setup WriteFile: %v", err)
				}
				toolDef := newMutateTestTool(t, root)
				return runMutate(t, toolDef, map[string]any{
					"operations": []any{
						map[string]any{"type": "replace", "path": "a.txt", "old_string": "nope", "new_string": "x"},
					},
				})
			},
			want: ReasonNoMatch,
		},
		{
			name: "ambiguous_match",
			run: func(t *testing.T) *MutateResult {
				root := t.TempDir()
				if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("foo foo"), 0o644); err != nil {
					t.Fatalf("setup WriteFile: %v", err)
				}
				toolDef := newMutateTestTool(t, root)
				return runMutate(t, toolDef, map[string]any{
					"operations": []any{
						map[string]any{"type": "replace", "path": "a.txt", "old_string": "foo", "new_string": "bar"},
					},
				})
			},
			want: ReasonAmbiguousMatch,
		},
		{
			name: "missing_file",
			run: func(t *testing.T) *MutateResult {
				root := t.TempDir()
				toolDef := newMutateTestTool(t, root)
				return runMutate(t, toolDef, map[string]any{
					"operations": []any{
						map[string]any{"type": "delete_file", "path": "missing.txt"},
					},
				})
			},
			want: ReasonMissingFile,
		},
		{
			name: "path_policy",
			run: func(t *testing.T) *MutateResult {
				root := t.TempDir()
				toolDef := newMutateTestTool(t, root)
				return runMutate(t, toolDef, map[string]any{
					"operations": []any{
						map[string]any{"type": "create", "path": "/etc/steiner-stage4-test-outside-root.txt", "content": "x"},
					},
				})
			},
			want: ReasonPathPolicy,
		},
		{
			name: "stale_read",
			run: func(t *testing.T) *MutateResult {
				root := t.TempDir()
				if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello"), 0o644); err != nil {
					t.Fatalf("setup WriteFile: %v", err)
				}
				policy := tool.NewPathPolicy(root, config.PathsConfig{})
				toolDef := NewMutateTool(Env{WorkDir: root, PathPolicy: &policy})
				return runMutate(t, toolDef, map[string]any{
					"operations": []any{
						map[string]any{"type": "replace", "path": "a.txt", "old_string": "hello", "new_string": "bye"},
					},
				})
			},
			want: ReasonStaleRead,
		},
		{
			name: "io_error",
			run: func(t *testing.T) *MutateResult {
				if os.Geteuid() == 0 {
					t.Skip("test requires non-root")
				}
				root := t.TempDir()
				dir := filepath.Join(root, "sub")
				if err := os.Mkdir(dir, 0o755); err != nil {
					t.Fatalf("setup Mkdir: %v", err)
				}
				path := filepath.Join(dir, "a.txt")
				if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
					t.Fatalf("setup WriteFile: %v", err)
				}
				if err := os.Chmod(dir, 0o555); err != nil {
					t.Fatalf("setup Chmod: %v", err)
				}
				t.Cleanup(func() {
					if err := os.Chmod(dir, 0o755); err != nil {
						t.Errorf("cleanup Chmod: %v", err)
					}
				})
				toolDef := newMutateTestTool(t, root)
				return runMutate(t, toolDef, map[string]any{
					"operations": []any{
						map[string]any{"type": "write", "path": "sub/a.txt", "content": "modified"},
					},
				})
			},
			want: ReasonIOError,
		},
		{
			name: "other",
			run: func(t *testing.T) *MutateResult {
				root := t.TempDir()
				if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello"), 0o644); err != nil {
					t.Fatalf("setup WriteFile: %v", err)
				}
				toolDef := newMutateTestTool(t, root)
				return runMutate(t, toolDef, map[string]any{
					"operations": []any{
						map[string]any{"type": "replace", "path": "a.txt", "old_string": "", "new_string": "x"},
					},
				})
			},
			want: ReasonOther,
		},
	}
}

// TestClassifyMutateError drives classifyMutateError from the actual errors
// mutate returns for each supported operation, rather than fabricated
// strings, so a wording change in the planners breaks this test instead of
// silently drifting the classification.
func TestClassifyMutateError(t *testing.T) {
	for _, tt := range mutateErrorCases() {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.run(t)
			failures := got.FailedOps()
			if len(failures) != 1 {
				t.Fatalf("FailedOps() = %v, want exactly one entry", failures)
			}
			if failures[0].Reason != tt.want {
				t.Errorf("FailedOps()[0].Reason = %q, want %q (output: %s)", failures[0].Reason, tt.want, got.Output)
			}
		})
	}
}

// TestMutateFailedOps_Success asserts a fully successful mutate call carries
// no failure detail, so ops_failed derives to zero for the tool diagnostics
// stream.
func TestMutateFailedOps_Success(t *testing.T) {
	root := t.TempDir()
	toolDef := newMutateTestTool(t, root)
	got := runMutate(t, toolDef, map[string]any{
		"operations": []any{
			map[string]any{"type": "create", "path": "a.txt", "content": "hello"},
		},
	})
	if got.OperationsFailed != 0 {
		t.Fatalf("OperationsFailed = %d, want 0", got.OperationsFailed)
	}
	if failures := got.FailedOps(); len(failures) != 0 {
		t.Fatalf("FailedOps() = %v, want none", failures)
	}
}

// TestMutateFailedOps_PartialFailure asserts a batch with one succeeding and
// one failing operation records exactly one nested failure entry — for the
// failing operation only, never for the one that planned fine — and that no
// absolute path appears anywhere in the recorded detail.
func TestMutateFailedOps_PartialFailure(t *testing.T) {
	root := t.TempDir()
	toolDef := newMutateTestTool(t, root)
	got := runMutate(t, toolDef, map[string]any{
		"operations": []any{
			map[string]any{"type": "create", "path": "a.txt", "content": "hello"},
			map[string]any{"type": "delete_file", "path": "missing.txt"},
		},
	})

	failures := got.FailedOps()
	if len(failures) != 1 {
		t.Fatalf("FailedOps() = %v, want exactly one entry", failures)
	}
	f := failures[0]
	if f.Op != "delete_file" {
		t.Errorf("FailedOps()[0].Op = %q, want %q", f.Op, "delete_file")
	}
	if f.Reason != ReasonMissingFile {
		t.Errorf("FailedOps()[0].Reason = %q, want %q", f.Reason, ReasonMissingFile)
	}
	if strings.Contains(f.Path, root) {
		t.Errorf("FailedOps()[0].Path = %q contains the absolute root %q", f.Path, root)
	}
}

// TestMutateFailedOps_EmptyOperations asserts the "operations is required"
// failure — which never enters the per-operation loop — still records a
// failure entry, so it cannot silently surface as outcome "ok" upstream.
func TestMutateFailedOps_EmptyOperations(t *testing.T) {
	root := t.TempDir()
	toolDef := newMutateTestTool(t, root)
	got := runMutate(t, toolDef, map[string]any{"operations": []any{}})

	if got.OperationsFailed == 0 {
		t.Fatalf("OperationsFailed = 0, want nonzero")
	}
	if failures := got.FailedOps(); len(failures) == 0 {
		t.Fatalf("FailedOps() is empty on a failed call; a failed mutate would be recorded as outcome %q", "ok")
	}
}

// TestMutateFailedOps_Invariant asserts every failing MutateResult produced
// across this file's scenarios carries at least one FailedOps entry — the
// invariant tool.recordDiagnostics relies on to avoid reporting a failed
// mutate as outcome "ok".
func TestMutateFailedOps_Invariant(t *testing.T) {
	for _, tt := range mutateErrorCases() {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.run(t)
			if got.OperationsFailed != 0 && len(got.FailedOps()) == 0 {
				t.Fatalf("OperationsFailed = %d but FailedOps() is empty (output: %s)", got.OperationsFailed, got.Output)
			}
		})
	}
}

// TestMutateToolStream_EndToEnd exercises the real seam that
// tool.Executor.recordDiagnostics depends on: a genuine *MutateResult
// returned through tool.Executor.Execute, asserted via the emitted
// diagnostics JSON decoded generically (not through the writer's own
// payload struct), so a wrong json tag can't round-trip clean. This is the
// path executor_tool_stream_test.go's fakeMutateResult cannot cover, since
// that package cannot import builtin without cycling.
func TestMutateToolStream_EndToEnd(t *testing.T) {
	root := t.TempDir()
	diagDir := t.TempDir()

	writer, err := diagnostics.New(diagnostics.Options{Dir: diagDir, Streams: diagnostics.Streams{Tool: true}})
	if err != nil {
		t.Fatalf("diagnostics.New() error = %v", err)
	}
	t.Cleanup(func() { _ = writer.Close() })

	policy := tool.NewPathPolicy(root, config.PathsConfig{})
	toolDef := NewMutateTool(Env{WorkDir: root, PathPolicy: &policy, FileObserved: func(string) bool { return true }})
	registry := tool.NewRegistry(toolDef)
	executor := tool.NewExecutor(registry, config.Config{}, nil, root, "", tool.Unsandboxed{})
	executor.WithDiagnostics(writer)

	if _, err := executor.Execute(context.Background(), "mutate", "call-1", map[string]any{
		"operations": []any{
			map[string]any{"type": "create", "path": "a.txt", "content": "hello"},
			map[string]any{"type": "delete_file", "path": "missing.txt"},
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

	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 1 {
		t.Fatalf("tool.jsonl has %d lines, want 1:\n%s", len(lines), raw)
	}

	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("unmarshal record: %v", err)
	}
	payload, ok := rec["payload"].(map[string]any)
	if !ok {
		t.Fatalf("payload = %v, want an object", rec["payload"])
	}

	if got := payload["tool"]; got != "mutate" {
		t.Errorf("payload[tool] = %v, want %q", got, "mutate")
	}
	if got := payload["outcome"]; got != "error" {
		t.Errorf("payload[outcome] = %v, want %q", got, "error")
	}
	if got := payload["ops_total"]; got != float64(2) {
		t.Errorf("payload[ops_total] = %v, want 2", got)
	}
	if got := payload["ops_failed"]; got != float64(1) {
		t.Errorf("payload[ops_failed] = %v, want 1", got)
	}

	failures, ok := payload["failures"].([]any)
	if !ok || len(failures) != 1 {
		t.Fatalf("payload[failures] = %v, want exactly one entry", payload["failures"])
	}
	failure, ok := failures[0].(map[string]any)
	if !ok {
		t.Fatalf("failures[0] = %v, want an object", failures[0])
	}
	if got := failure["op"]; got != "delete_file" {
		t.Errorf("failures[0][op] = %v, want %q", got, "delete_file")
	}
	if got := failure["reason"]; got != ReasonMissingFile {
		t.Errorf("failures[0][reason] = %v, want %q", got, ReasonMissingFile)
	}
	if got := failure["path_ext"]; got != ".txt" {
		t.Errorf("failures[0][path_ext] = %v, want %q", got, ".txt")
	}
}
